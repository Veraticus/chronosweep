package sweep

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/joshsymonds/chronosweep/internal/gmail"
)

// Limiter is the minimal rate limiter interface the service needs.
type Limiter interface {
	Wait(ctx context.Context) error
}

const (
	maxPageSize         = 500
	batchSize           = 1000
	splitPairSeparator  = 2
	defaultExpiredLabel = "auto-archived/expired"
)

// Spec configures a single sweep pass.
type Spec struct {
	Label          string
	Grace          time.Duration
	DryRun         bool
	PauseWeekends  bool
	GraceOverrides map[string]time.Duration
	ExcludeLabels  []string
	ExpiredLabel   string
	PageSize       int
}

// Service sweeps stale messages out of the inbox while labeling them for safety.
type Service struct {
	Client  gmail.Client
	Limiter Limiter
	Logger  *slog.Logger
	Clock   func() time.Time
}

// NewService constructs a sweeper with injected dependencies.
func NewService(client gmail.Client, limiter Limiter, logger *slog.Logger) *Service {
	return &Service{
		Client:  client,
		Limiter: limiter,
		Logger:  logger,
		Clock:   time.Now,
	}
}

// Run executes the sweep according to spec.
func (s *Service) Run(ctx context.Context, spec Spec) error {
	if err := validateSpec(spec); err != nil {
		return err
	}

	logger := s.Logger
	if spec.DryRun {
		logger.InfoContext(
			ctx,
			"sweep dry-run configured",
			slog.String("label", spec.Label),
			slog.Duration("grace", spec.Grace),
		)
	}

	if spec.PauseWeekends && s.shouldPauseForWeekend() {
		logger.InfoContext(
			ctx,
			"weekend pause enabled; skipping sweep",
			slog.String("label", spec.Label),
		)
		return nil
	}

	grace := s.effectiveGrace(spec)
	pageSize := normalizePageSize(spec.PageSize)
	query := gmail.Query{
		Raw: strings.Join(
			buildQueryParts(spec.Label, spec.ExcludeLabels, s.Clock().Add(-grace).Unix()),
			" ",
		),
	}

	ids, err := s.collectMessageIDs(ctx, query, pageSize)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		logger.InfoContext(
			ctx,
			"no stale messages",
			slog.String("label", spec.Label),
			slog.Int("count", 0),
		)
		return nil
	}

	if spec.DryRun {
		logger.InfoContext(
			ctx,
			"dry-run sweep",
			slog.String("label", spec.Label),
			slog.Int("count", len(ids)),
			slog.Duration("grace", grace),
		)
		return nil
	}

	expiredLabel := spec.ExpiredLabel
	if expiredLabel == "" {
		expiredLabel = defaultExpiredLabel
	}
	labelID, err := s.Client.EnsureLabel(ctx, expiredLabel)
	if err != nil {
		return fmt.Errorf("ensure expired label %q: %w", expiredLabel, err)
	}

	ops := gmail.ModifyOps{
		AddLabels: []gmail.LabelID{labelID},
		MarkRead:  true,
		Archive:   true,
	}
	if applyErr := s.applyBatches(ctx, ids, ops); applyErr != nil {
		return applyErr
	}

	logger.InfoContext(
		ctx,
		"sweep complete",
		slog.String("label", spec.Label),
		slog.Int("count", len(ids)),
		slog.Duration("grace", grace),
	)
	return nil
}

func (s *Service) collectMessageIDs(
	ctx context.Context,
	query gmail.Query,
	pageSize int,
) ([]gmail.MessageID, error) {
	var (
		ids   []gmail.MessageID
		token string
		page  int
	)
	for {
		page++
		if err := s.wait(ctx, "rate limit list messages"); err != nil {
			return nil, err
		}
		resp, err := s.Client.List(ctx, query, token, pageSize)
		if err != nil {
			return nil, fmt.Errorf("list page %d: %w", page, err)
		}
		ids = append(ids, resp.IDs...)
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	return ids, nil
}

func (s *Service) applyBatches(
	ctx context.Context,
	ids []gmail.MessageID,
	ops gmail.ModifyOps,
) error {
	for start := 0; start < len(ids); start += batchSize {
		end := start + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		if err := s.wait(ctx, "rate limit batch modify"); err != nil {
			return err
		}
		if err := s.Client.BatchModify(ctx, ids[start:end], ops); err != nil {
			return fmt.Errorf("batch modify %d-%d: %w", start, end, err)
		}
	}
	return nil
}

// ParseGraceMap converts CLI input into per-label durations.
func ParseGraceMap(input string) (map[string]time.Duration, error) {
	if strings.TrimSpace(input) == "" {
		return map[string]time.Duration{}, nil
	}
	items := strings.Split(input, ",")
	result := make(map[string]time.Duration, len(items))
	saw := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, "=", splitPairSeparator)
		if len(parts) != splitPairSeparator {
			return nil, fmt.Errorf("invalid grace map entry %q", item)
		}
		label := strings.TrimSpace(parts[0])
		dur, err := time.ParseDuration(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("parse duration for %q: %w", label, err)
		}
		if dur <= 0 {
			return nil, fmt.Errorf("duration for %q must be positive", label)
		}
		if _, exists := saw[label]; exists {
			return nil, fmt.Errorf("duplicate grace map entry for %q", label)
		}
		saw[label] = struct{}{}
		result[label] = dur
	}
	return result, nil
}

func buildQueryParts(label string, exclude []string, before int64) []string {
	parts := []string{
		"in:inbox",
		"is:unread",
		fmt.Sprintf("before:%d", before),
		"-is:starred",
		"-is:important",
	}
	if label != "" {
		parts = append([]string{fmt.Sprintf(`label:"%s"`, label)}, parts...)
	}
	sorted := append([]string(nil), exclude...)
	sort.Strings(sorted)
	for _, ex := range sorted {
		ex = strings.TrimSpace(ex)
		if ex == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf(`-label:"%s"`, ex))
	}
	return parts
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

func (s *Service) shouldPauseForWeekend() bool {
	weekday := s.Clock().In(time.Local).Weekday()
	return weekday == time.Saturday || weekday == time.Sunday
}

func (s *Service) effectiveGrace(spec Spec) time.Duration {
	if spec.Label == "" || spec.GraceOverrides == nil {
		return spec.Grace
	}
	if override, ok := spec.GraceOverrides[spec.Label]; ok && override > 0 {
		return override
	}
	return spec.Grace
}

func validateSpec(spec Spec) error {
	if spec.Grace <= 0 {
		return fmt.Errorf("grace must be positive")
	}
	return nil
}

func normalizePageSize(size int) int {
	if size <= 0 || size > maxPageSize {
		return maxPageSize
	}
	return size
}
