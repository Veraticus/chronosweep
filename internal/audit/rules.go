package audit

import (
	"sort"
	"strings"

	"github.com/joshsymonds/chronosweep/internal/gmail"
	"github.com/joshsymonds/chronosweep/internal/gmailctl"
)

type matcherKind int

const (
	matcherFrom matcherKind = iota
	matcherTo
	matcherSubject
	matcherList
)

type matcher struct {
	kind   matcherKind
	values []string
}

func (m matcher) matches(meta gmail.MessageMeta) bool {
	switch m.kind {
	case matcherFrom:
		return containsAny(meta.Headers["From"], m.values)
	case matcherTo:
		return containsAny(meta.Headers["To"], m.values)
	case matcherSubject:
		return containsAny(meta.Headers["Subject"], m.values)
	case matcherList:
		return matchListID(meta.Headers["List-Id"], m.values)
	default:
		return false
	}
}

type ruleActions struct {
	Archive  bool
	MarkRead bool
	Star     bool
	Labels   []string
}

type compiledRule struct {
	Name      string
	Matchers  []matcher
	Actions   ruleActions
	Labels    []string
	Evaluable bool
}

func compileRules(export gmailctl.Export, labelsByID map[gmail.LabelID]string) []compiledRule {
	labelNames := make(map[string]string, len(labelsByID)+len(export.Labels))
	for id, name := range labelsByID {
		labelNames[string(id)] = name
	}
	for _, lbl := range export.Labels {
		if lbl.ID != "" && lbl.Name != "" {
			labelNames[lbl.ID] = lbl.Name
		}
	}
	compiled := make([]compiledRule, 0, len(export.Filters))
	for _, filt := range export.Filters {
		matchers, evaluable := buildMatchers(filt.Criteria)
		actions := mapActions(filt.Action, labelNames)
		ruleName := strings.TrimSpace(filt.Name)
		if ruleName == "" {
			ruleName = strings.TrimSpace(filt.ID)
		}
		if ruleName == "" {
			ruleName = describeCriteria(filt.Criteria)
		}
		compiled = append(compiled, compiledRule{
			Name:      ruleName,
			Matchers:  matchers,
			Actions:   actions,
			Labels:    actions.Labels,
			Evaluable: evaluable,
		})
	}
	return compiled
}

func buildMatchers(c gmailctl.FilterCriteria) ([]matcher, bool) {
	var matchers []matcher
	if strings.TrimSpace(c.From) != "" {
		matchers = append(matchers, matcher{kind: matcherFrom, values: splitCandidates(c.From)})
	}
	if strings.TrimSpace(c.To) != "" {
		matchers = append(matchers, matcher{kind: matcherTo, values: splitCandidates(c.To)})
	}
	if strings.TrimSpace(c.Subject) != "" {
		matchers = append(
			matchers,
			matcher{kind: matcherSubject, values: splitCandidates(c.Subject)},
		)
	}
	if strings.TrimSpace(c.List) != "" {
		matchers = append(
			matchers,
			matcher{kind: matcherList, values: []string{normalizeListID(c.List)}},
		)
	}
	if strings.TrimSpace(c.Query) != "" {
		qm, ok := parseQueryMatchers(c.Query)
		if !ok {
			return nil, false
		}
		matchers = append(matchers, qm...)
	}
	if len(matchers) == 0 {
		return nil, false
	}
	return matchers, true
}

func parseQueryMatchers(query string) ([]matcher, bool) {
	tokens := strings.Fields(query)
	matchers := make([]matcher, 0, len(tokens))
	for _, raw := range tokens {
		tok := normalizeQueryToken(raw)
		if tok.skip {
			continue
		}
		if tok.invalid {
			return nil, false
		}
		m, ok := matcherFromToken(tok.value)
		if !ok {
			return nil, false
		}
		matchers = append(matchers, m)
	}
	if len(matchers) == 0 {
		return nil, false
	}
	return matchers, true
}

func splitCandidates(raw string) []string {
	replacer := strings.NewReplacer(",", " ", ";", " ", "|", " ")
	raw = replacer.Replace(raw)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Fields(raw)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.Trim(part, "\"'()"))
		if part == "" || strings.EqualFold(part, "OR") {
			continue
		}
		out = append(out, part)
	}
	return out
}

func containsAny(header string, values []string) bool {
	header = strings.ToLower(header)
	for _, val := range values {
		if strings.Contains(header, val) {
			return true
		}
	}
	return false
}

func matchListID(raw string, values []string) bool {
	listID := normalizeListID(raw)
	for _, val := range values {
		if listID == val || strings.Contains(listID, val) {
			return true
		}
	}
	return false
}

type queryToken struct {
	value   string
	skip    bool
	invalid bool
}

func normalizeQueryToken(raw string) queryToken {
	trimmed := strings.Trim(raw, "()\"'")
	if trimmed == "" || strings.EqualFold(trimmed, "OR") {
		return queryToken{skip: true}
	}
	if strings.HasPrefix(trimmed, "-") {
		return queryToken{invalid: true}
	}
	return queryToken{value: trimmed}
}

func matcherFromToken(token string) (matcher, bool) {
	lower := strings.ToLower(token)
	switch {
	case strings.HasPrefix(lower, "list:"):
		val := normalizeListID(token[len("list:"):])
		if val == "" {
			return matcher{}, false
		}
		return matcher{kind: matcherList, values: []string{val}}, true
	case strings.HasPrefix(lower, "from:"):
		vals := splitCandidates(token[len("from:"):])
		if len(vals) == 0 {
			return matcher{}, false
		}
		return matcher{kind: matcherFrom, values: vals}, true
	case strings.HasPrefix(lower, "subject:"):
		vals := splitCandidates(token[len("subject:"):])
		if len(vals) == 0 {
			return matcher{}, false
		}
		return matcher{kind: matcherSubject, values: vals}, true
	case strings.HasPrefix(lower, "to:"):
		vals := splitCandidates(token[len("to:"):])
		if len(vals) == 0 {
			return matcher{}, false
		}
		return matcher{kind: matcherTo, values: vals}, true
	default:
		return matcher{}, false
	}
}

func mapActions(action gmailctl.FilterAction, labelNames map[string]string) ruleActions {
	result := ruleActions{}
	if len(action.RemoveLabelIDs) > 0 {
		for _, id := range action.RemoveLabelIDs {
			if id == "INBOX" {
				result.Archive = true
			}
			if id == "UNREAD" {
				result.MarkRead = true
			}
		}
	}
	if len(action.AddLabelIDs) > 0 {
		for _, id := range action.AddLabelIDs {
			if id == "STARRED" {
				result.Star = true
				continue
			}
			if name, ok := labelNames[id]; ok && name != "" {
				result.Labels = appendIfMissing(result.Labels, name)
			}
		}
	}
	sort.Strings(result.Labels)
	return result
}

func describeCriteria(c gmailctl.FilterCriteria) string {
	if c.From != "" {
		return "from:" + strings.TrimSpace(c.From)
	}
	if c.List != "" {
		return "list:" + strings.TrimSpace(c.List)
	}
	if c.Subject != "" {
		return "subject:" + strings.TrimSpace(c.Subject)
	}
	if c.Query != "" {
		return strings.TrimSpace(c.Query)
	}
	return "gmailctl-rule"
}

func evaluateRules(rules []compiledRule, metas []gmail.MessageMeta) map[string][]gmail.MessageID {
	matches := make(map[string][]gmail.MessageID, len(rules))
	for _, rule := range rules {
		if !rule.Evaluable {
			continue
		}
		for _, meta := range metas {
			if rule.matches(meta) {
				matches[rule.Name] = append(matches[rule.Name], meta.ID)
			}
		}
	}
	return matches
}

func (r compiledRule) matches(meta gmail.MessageMeta) bool {
	for _, m := range r.Matchers {
		if !m.matches(meta) {
			return false
		}
	}
	return true
}

type ruleSummary struct {
	Name    string
	Actions ruleActions
}

func detectConflicts(rules []compiledRule, matches map[string][]gmail.MessageID) []Conflict {
	byMessage := collectRuleSummaries(rules, matches)
	seen := map[string]struct{}{}
	conflicts := make([]Conflict, 0, len(byMessage))
	for _, summaries := range byMessage {
		archiveRules, starRules := classifySummaries(summaries)
		if len(archiveRules) == 0 || len(starRules) == 0 {
			continue
		}
		combined := mergeRuleSets(archiveRules, starRules)
		key := strings.Join(combined, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		conflicts = append(conflicts, Conflict{
			Rules:       combined,
			Description: "archive and star rules overlap",
		})
	}
	sort.Slice(conflicts, func(i, j int) bool {
		return strings.Join(conflicts[i].Rules, "|") < strings.Join(conflicts[j].Rules, "|")
	})
	return conflicts
}

func collectRuleSummaries(
	rules []compiledRule,
	matches map[string][]gmail.MessageID,
) map[gmail.MessageID][]ruleSummary {
	byMessage := make(map[gmail.MessageID][]ruleSummary)
	for _, rule := range rules {
		ids := matches[rule.Name]
		if len(ids) == 0 {
			continue
		}
		summary := ruleSummary{Name: rule.Name, Actions: rule.Actions}
		for _, id := range ids {
			byMessage[id] = append(byMessage[id], summary)
		}
	}
	return byMessage
}

func classifySummaries(summaries []ruleSummary) ([]string, []string) {
	archiveRules := make([]string, 0, len(summaries))
	starRules := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		if summary.Actions.Archive {
			archiveRules = appendIfMissing(archiveRules, summary.Name)
		}
		if summary.Actions.Star {
			starRules = appendIfMissing(starRules, summary.Name)
		}
	}
	return archiveRules, starRules
}

func mergeRuleSets(a, b []string) []string {
	combined := append([]string{}, a...)
	for _, name := range b {
		combined = appendIfMissing(combined, name)
	}
	sort.Strings(combined)
	return combined
}
