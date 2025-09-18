package audit

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// LintReport captures gmailctl findings for CI enforcement.
type LintReport struct {
	Window   time.Duration
	Total    int
	Findings GmailctlFindings
}

// RunLint reuses the regular audit analysis but returns a lean report.
func (s *Service) RunLint(ctx context.Context, opts Options) (LintReport, error) {
	rep, err := s.Run(ctx, opts)
	if err != nil {
		return LintReport{}, err
	}
	return LintReport{Window: opts.Window, Total: rep.Total, Findings: rep.Findings}, nil
}

// ShouldFail reports whether any of the requested conditions are present.
func (lr LintReport) ShouldFail(failOn []string) bool {
	flags := map[string]bool{
		"dead":          len(lr.Findings.DeadRules) > 0,
		"missing-label": len(lr.Findings.MissingLabels) > 0,
		"conflict":      len(lr.Findings.Conflicts) > 0,
	}
	for _, cond := range failOn {
		cond = strings.TrimSpace(strings.ToLower(cond))
		if cond == "" {
			continue
		}
		if flags[cond] {
			return true
		}
	}
	return false
}

// HumanSummary renders a concise CLI summary.
func (lr LintReport) HumanSummary() string {
	builder := &strings.Builder{}
	fmt.Fprintf(builder, "chronosweep lint — window %s (%d messages checked)\n", lr.Window, lr.Total)
	if len(lr.Findings.DeadRules) == 0 && len(lr.Findings.MissingLabels) == 0 && len(lr.Findings.Conflicts) == 0 {
		builder.WriteString("no findings\n")
		return builder.String()
	}
	if len(lr.Findings.DeadRules) > 0 {
		builder.WriteString("dead rules:\n")
		sorted := append([]RuleFinding(nil), lr.Findings.DeadRules...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
		for _, fr := range sorted {
			fmt.Fprintf(builder, "  %s — %s\n", fr.Name, fr.Reason)
		}
	}
	if len(lr.Findings.MissingLabels) > 0 {
		builder.WriteString("missing labels:\n")
		labels := append([]string(nil), lr.Findings.MissingLabels...)
		sort.Strings(labels)
		for _, lbl := range labels {
			fmt.Fprintf(builder, "  %s\n", lbl)
		}
	}
	if len(lr.Findings.Conflicts) > 0 {
		builder.WriteString("conflicts:\n")
		for _, cf := range lr.Findings.Conflicts {
			fmt.Fprintf(builder, "  %s — %s\n", strings.Join(cf.Rules, ", "), cf.Description)
		}
	}
	return builder.String()
}

// ParseFailOn splits a comma separated list into canonical tokens.
func ParseFailOn(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	parts := strings.Split(input, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
