// internal/audit/audit.go
package audit

import (
	"context"
	"sort"
	"time"

	gc "github.com/yourorg/chronosweep/internal/gmail"
)

type SenderStat struct {
	Domain        string
	Count         int
	SampleSubject string
}
type ListStat struct {
	ListID        string
	Count         int
	SampleSubject string
}

type Suggestions struct {
	ArchiveRules []string // Jsonnet snippets for bulk/list senders
	RemoveRules  []string // names/IDs of rules that never matched (requires gmailctl export integration)
	Smells       []string // text notes about conflicts (archive+star etc.)
}

type Report struct {
	Window     time.Duration
	TopSenders []SenderStat
	TopLists   []ListStat
	Suggest    Suggestions
	Coverage   map[string]int // label categories seen, if you fetch labels per message
	Total      int
}

func Run(ctx context.Context, c gc.Client, window time.Duration, topN int) (Report, error) {
	// 1) Gather recent message IDs
	q := gc.Query{Raw: "newer_than:" + days(window) + "d"}
	ids, _, err := c.List(ctx, q, 500)
	if err != nil {
		return Report{}, err
	}

	// 2) Pull metadata (headers-only)
	hdrs := []string{"From", "Subject", "List-Id", "Auto-Submitted", "Precedence", "Date"}
	var senders = map[string]SenderStat{}
	var lists = map[string]ListStat{}
	for _, id := range ids {
		m, err := c.GetMetadata(ctx, id, hdrs)
		if err != nil {
			continue
		}
		dom := domainOf(m.Headers["From"])
		if dom != "" {
			st := senders[dom]
			st.Domain, st.Count = dom, st.Count+1
			if st.SampleSubject == "" {
				st.SampleSubject = m.Headers["Subject"]
			}
			senders[dom] = st
		}
		if lid := normalizeListID(m.Headers["List-Id"]); lid != "" {
			ls := lists[lid]
			ls.ListID, ls.Count = lid, ls.Count+1
			if ls.SampleSubject == "" {
				ls.SampleSubject = m.Headers["Subject"]
			}
			lists[lid] = ls
		}
	}

	// 3) Rank
	topSenders := rankSenders(senders, topN)
	topLists := rankLists(lists, topN)

	// 4) Generate candidate Jsonnet snippets
	sugs := Suggestions{
		ArchiveRules: buildArchiveRules(topLists, topSenders),
		// RemoveRules + Smells require optional gmailctl export + simulation; see design notes below.
	}

	return Report{
		Window: window, TopSenders: topSenders, TopLists: topLists, Suggest: sugs, Total: len(ids),
	}, nil
}

// helper funcs (domainOf, normalizeListID, rank, buildArchiveRules, days) omitted for brevity
