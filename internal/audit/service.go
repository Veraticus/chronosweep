package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/joshsymonds/chronosweep/internal/gmail"
	"github.com/joshsymonds/chronosweep/internal/gmailctl"
	"github.com/joshsymonds/chronosweep/internal/rate"
)

const previewSubjectDisplayLimit = 60

func defaultHeaders() []string {
	return []string{"From", "To", "Subject", "List-Id", "Auto-Submitted", "Precedence"}
}

// Options controls the behavior of the audit analyzer.
type Options struct {
	Window   time.Duration
	TopN     int
	PageSize int
	Headers  []string
}

// GmailctlLoader loads compiled gmailctl filters for replay.
type GmailctlLoader interface {
	ExportFilters(ctx context.Context) (gmailctl.Export, error)
}

// Service executes audit analyses against Gmail metadata.
type Service struct {
	Client  gmail.Client
	Limiter rate.Limiter
	Logger  *slog.Logger
	Clock   func() time.Time
	Loader  GmailctlLoader
}

// NewService constructs a Service with sane defaults.
func NewService(
	client gmail.Client,
	limiter rate.Limiter,
	logger *slog.Logger,
	loader GmailctlLoader,
) *Service {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Service{
		Client:  client,
		Limiter: limiter,
		Logger:  logger,
		Clock:   time.Now,
		Loader:  loader,
	}
}

// Report summarizes recent inbox activity and suggestions.
type Report struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Window      time.Duration    `json:"window"`
	Total       int              `json:"total"`
	TopSenders  []SenderStat     `json:"top_senders"`
	TopLists    []ListStat       `json:"top_lists"`
	Coverage    map[string]int   `json:"coverage"`
	Suggestions Suggestions      `json:"suggestions"`
	Findings    GmailctlFindings `json:"findings"`
}

// SenderStat ranks noisy sender domains.
type SenderStat struct {
	Domain         string `json:"domain"`
	Count          int    `json:"count"`
	PreviewSubject string `json:"preview_subject"`
}

// ListStat ranks noisy List-Id sources.
type ListStat struct {
	ListID         string `json:"list_id"`
	Count          int    `json:"count"`
	PreviewSubject string `json:"preview_subject"`
}

// Suggestions includes proposed gmailctl snippets and clean-ups.
type Suggestions struct {
	ArchiveRules []string      `json:"archive_rules"`
	RemoveRules  []RuleFinding `json:"remove_rules"`
	Smells       []Conflict    `json:"smells"`
}

// GmailctlFindings feeds chronosweep-lint.
type GmailctlFindings struct {
	DeadRules     []RuleFinding `json:"dead_rules"`
	MissingLabels []string      `json:"missing_labels"`
	Conflicts     []Conflict    `json:"conflicts"`
}

// RuleFinding identifies a problematic rule.
type RuleFinding struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Conflict represents conflicting actions between rules for the same messages.
type Conflict struct {
	Rules       []string `json:"rules"`
	Description string   `json:"description"`
}

// Run produces a full audit report.
func (s *Service) Run(ctx context.Context, opts Options) (Report, error) {
	if opts.Window <= 0 {
		return Report{}, fmt.Errorf("window must be positive")
	}
	topN := opts.TopN
	if topN <= 0 {
		topN = 20
	}
	headers := opts.Headers
	if len(headers) == 0 {
		headers = defaultHeaders()
	}
	pageSize := opts.PageSize
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 500
	}

	logger := s.Logger
	logger.InfoContext(ctx, "running audit", slog.Duration("window", opts.Window))

	labelsByName, labelsByID, err := s.Client.ListLabels(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("list labels: %w", err)
	}
	existingLabels := make(map[string]struct{}, len(labelsByName))
	for name := range labelsByName {
		existingLabels[name] = struct{}{}
	}

	metas, err := s.fetchMetadata(ctx, opts.Window, headers, pageSize)
	if err != nil {
		return Report{}, err
	}

	rep := Report{
		GeneratedAt: s.Clock(),
		Window:      opts.Window,
		Total:       len(metas),
		Coverage:    map[string]int{},
	}

	if len(metas) == 0 {
		return rep, nil
	}

	rep.TopSenders, rep.TopLists = buildRankings(metas, topN)
	rep.Suggestions.ArchiveRules = buildArchiveRules(rep.TopLists, rep.TopSenders)
	rep.Coverage = buildCoverage(metas, labelsByID)

	findings, err := s.analyseGmailctl(ctx, metas, labelsByID, existingLabels)
	if err != nil {
		return Report{}, err
	}
	rep.Findings = findings
	rep.Suggestions.RemoveRules = findings.DeadRules
	rep.Suggestions.Smells = findings.Conflicts

	return rep, nil
}

func (s *Service) fetchMetadata(
	ctx context.Context,
	window time.Duration,
	headers []string,
	pageSize int,
) ([]gmail.MessageMeta, error) {
	days := daysFromDuration(window)
	query := gmail.Query{Raw: fmt.Sprintf("newer_than:%dd", days)}
	var (
		metas []gmail.MessageMeta
		token string
	)
	for {
		page, err := s.listMessages(ctx, query, token, pageSize)
		if err != nil {
			return nil, err
		}
		if len(page.IDs) == 0 {
			if page.NextPageToken == "" {
				break
			}
			token = page.NextPageToken
			continue
		}

		chunk, err := s.messageMetadata(ctx, page.IDs, headers)
		if err != nil {
			return nil, err
		}
		metas = append(metas, chunk...)

		if page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}
	return metas, nil
}

func (s *Service) analyseGmailctl(
	ctx context.Context,
	metas []gmail.MessageMeta,
	labelsByID map[gmail.LabelID]string,
	existingLabels map[string]struct{},
) (GmailctlFindings, error) {
	if s.Loader == nil {
		return GmailctlFindings{}, nil
	}
	export, err := s.Loader.ExportFilters(ctx)
	if err != nil {
		return GmailctlFindings{}, fmt.Errorf("load gmailctl filters: %w", err)
	}
	compiled := compileRules(export, labelsByID)
	if len(compiled) == 0 {
		return GmailctlFindings{}, nil
	}
	matches := evaluateRules(compiled, metas)
	findings := GmailctlFindings{}

	for _, rule := range compiled {
		if len(matches[rule.Name]) == 0 && rule.Evaluable {
			findings.DeadRules = append(
				findings.DeadRules,
				RuleFinding{Name: rule.Name, Reason: "no messages matched in lookback"},
			)
		}
		for _, lbl := range rule.Labels {
			if _, ok := existingLabels[lbl]; !ok {
				findings.MissingLabels = appendIfMissing(findings.MissingLabels, lbl)
			}
		}
	}

	findings.Conflicts = detectConflicts(compiled, matches)
	return findings, nil
}

// PrintHuman writes a readable report to the provided writer.
func PrintHuman(rep Report, w io.Writer) error {
	if w == nil {
		w = os.Stdout
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "chronosweep audit — window %s (%d messages)\n", rep.Window, rep.Total)
	if len(rep.TopSenders) > 0 {
		builder.WriteString("\nTop senders:\n")
		for _, s := range rep.TopSenders {
			fmt.Fprintf(
				&builder,
				"  %-30s %4d %s\n",
				s.Domain,
				s.Count,
				truncate(s.PreviewSubject, previewSubjectDisplayLimit),
			)
		}
	}
	if len(rep.TopLists) > 0 {
		builder.WriteString("\nTop lists:\n")
		for _, l := range rep.TopLists {
			fmt.Fprintf(
				&builder,
				"  %-30s %4d %s\n",
				l.ListID,
				l.Count,
				truncate(l.PreviewSubject, previewSubjectDisplayLimit),
			)
		}
	}
	if len(rep.Suggestions.ArchiveRules) > 0 {
		builder.WriteString("\nSuggested gmailctl snippets:\n")
		for _, snip := range rep.Suggestions.ArchiveRules {
			fmt.Fprintf(&builder, "%s\n\n", snip)
		}
	}
	if len(rep.Findings.DeadRules) > 0 || len(rep.Findings.MissingLabels) > 0 ||
		len(rep.Findings.Conflicts) > 0 {
		builder.WriteString("\nLint findings:\n")
		for _, fr := range rep.Findings.DeadRules {
			fmt.Fprintf(&builder, "  dead rule: %s — %s\n", fr.Name, fr.Reason)
		}
		for _, lbl := range rep.Findings.MissingLabels {
			fmt.Fprintf(&builder, "  missing label: %s\n", lbl)
		}
		for _, cf := range rep.Findings.Conflicts {
			fmt.Fprintf(
				&builder,
				"  conflict: %s (%s)\n",
				strings.Join(cf.Rules, ", "),
				cf.Description,
			)
		}
	}
	if _, err := io.WriteString(w, builder.String()); err != nil {
		return fmt.Errorf("write human report: %w", err)
	}
	return nil
}

// WriteJSON serializes the report to disk.
func WriteJSON(rep Report, path string) error {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return fmt.Errorf("path must not be empty")
	}
	clean = filepath.Clean(clean)
	if filepath.IsAbs(clean) {
		return fmt.Errorf("output path must be relative, got %s", clean)
	}
	if strings.HasPrefix(clean, "..") {
		return fmt.Errorf("output path %s escapes working directory", clean)
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine working directory: %w", err)
	}
	abs := filepath.Join(wd, clean)
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304
	if err != nil {
		return fmt.Errorf("create %s: %w", abs, err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if encodeErr := enc.Encode(rep); encodeErr != nil {
		return fmt.Errorf("encode report: %w", encodeErr)
	}
	return nil
}

func buildRankings(metas []gmail.MessageMeta, topN int) ([]SenderStat, []ListStat) {
	senders := map[string]*SenderStat{}
	lists := map[string]*ListStat{}
	for _, meta := range metas {
		from := meta.Headers["From"]
		if domain := domainOf(from); domain != "" {
			st := senders[domain]
			if st == nil {
				st = &SenderStat{Domain: domain}
				senders[domain] = st
			}
			st.Count++
			if st.PreviewSubject == "" {
				st.PreviewSubject = meta.Headers["Subject"]
			}
		}
		if lid := normalizeListID(meta.Headers["List-Id"]); lid != "" {
			ls := lists[lid]
			if ls == nil {
				ls = &ListStat{ListID: lid}
				lists[lid] = ls
			}
			ls.Count++
			if ls.PreviewSubject == "" {
				ls.PreviewSubject = meta.Headers["Subject"]
			}
		}
	}
	return rankSenders(senders, topN), rankLists(lists, topN)
}

func buildCoverage(metas []gmail.MessageMeta, labelsByID map[gmail.LabelID]string) map[string]int {
	coverage := make(map[string]int)
	for _, meta := range metas {
		for _, lid := range meta.LabelIDs {
			if name, ok := labelsByID[lid]; ok {
				coverage[name]++
			}
		}
	}
	return coverage
}

func rankSenders(m map[string]*SenderStat, topN int) []SenderStat {
	slice := make([]SenderStat, 0, len(m))
	for _, st := range m {
		slice = append(slice, *st)
	}
	sort.Slice(slice, func(i, j int) bool {
		if slice[i].Count == slice[j].Count {
			return slice[i].Domain < slice[j].Domain
		}
		return slice[i].Count > slice[j].Count
	})
	if topN < len(slice) {
		slice = slice[:topN]
	}
	return slice
}

func rankLists(m map[string]*ListStat, topN int) []ListStat {
	slice := make([]ListStat, 0, len(m))
	for _, st := range m {
		slice = append(slice, *st)
	}
	sort.Slice(slice, func(i, j int) bool {
		if slice[i].Count == slice[j].Count {
			return slice[i].ListID < slice[j].ListID
		}
		return slice[i].Count > slice[j].Count
	})
	if topN < len(slice) {
		slice = slice[:topN]
	}
	return slice
}

func buildArchiveRules(lists []ListStat, senders []SenderStat) []string {
	const maxRules = 10
	estimate := len(lists) + len(senders)
	if estimate > maxRules {
		estimate = maxRules
	}
	snippets := make([]string, 0, estimate)
	for _, ls := range lists {
		snippets = append(snippets, fmt.Sprintf(`{
  filter: { list: "%s" },
  actions: { archive: true, markRead: true },
}`, ls.ListID))
		if len(snippets) >= maxRules {
			return snippets
		}
	}
	for _, sd := range senders {
		snippets = append(snippets, fmt.Sprintf(`{
  filter: { from: "*@%s" },
  actions: { archive: true, markRead: true },
}`, sd.Domain))
		if len(snippets) >= maxRules {
			break
		}
	}
	return snippets
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func appendIfMissing(slice []string, val string) []string {
	for _, existing := range slice {
		if existing == val {
			return slice
		}
	}
	return append(slice, val)
}

func (s *Service) listMessages(
	ctx context.Context,
	query gmail.Query,
	pageToken string,
	pageSize int,
) (gmail.ListPage, error) {
	if err := s.wait(ctx, "rate limit messages"); err != nil {
		return gmail.ListPage{}, err
	}
	page, err := s.Client.List(ctx, query, pageToken, pageSize)
	if err != nil {
		return gmail.ListPage{}, fmt.Errorf("list messages: %w", err)
	}
	return page, nil
}

func (s *Service) messageMetadata(
	ctx context.Context,
	ids []gmail.MessageID,
	headers []string,
) ([]gmail.MessageMeta, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	metas := make([]gmail.MessageMeta, 0, len(ids))
	for _, id := range ids {
		if err := s.wait(ctx, "rate limit metadata"); err != nil {
			return nil, err
		}
		meta, err := s.Client.GetMetadata(ctx, id, headers)
		if err != nil {
			return nil, fmt.Errorf("get metadata %s: %w", id, err)
		}
		metas = append(metas, meta)
	}
	return metas, nil
}

func (s *Service) wait(ctx context.Context, operation string) error {
	if s.Limiter == nil {
		return nil
	}
	if err := s.Limiter.Wait(ctx); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func daysFromDuration(window time.Duration) int {
	const day = 24 * time.Hour
	if window <= 0 {
		return 1
	}
	days := int(window / day)
	if window%day != 0 {
		days++
	}
	if days <= 0 {
		days = 1
	}
	return days
}
