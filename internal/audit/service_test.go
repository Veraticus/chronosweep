package audit

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/joshsymonds/chronosweep/internal/gmail"
	"github.com/joshsymonds/chronosweep/internal/gmailctl"
)

type fakeAuditClient struct {
	pages        []gmail.ListPage
	metas        map[gmail.MessageID]gmail.MessageMeta
	labelsByName map[string]gmail.LabelID
	labelsByID   map[gmail.LabelID]string
}

func (f *fakeAuditClient) List(
	ctx context.Context,
	q gmail.Query,
	pageToken string,
	pageSize int,
) (gmail.ListPage, error) {
	_ = ctx
	_ = q
	_ = pageToken
	_ = pageSize
	if len(f.pages) == 0 {
		return gmail.ListPage{}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func (f *fakeAuditClient) GetMetadata(
	ctx context.Context,
	id gmail.MessageID,
	headers []string,
) (gmail.MessageMeta, error) {
	_ = ctx
	_ = headers
	return f.metas[id], nil
}

func (f *fakeAuditClient) BatchModify(
	ctx context.Context,
	ids []gmail.MessageID,
	ops gmail.ModifyOps,
) error {
	_ = ctx
	_ = ids
	_ = ops
	return nil
}

func (f *fakeAuditClient) ListLabels(
	ctx context.Context,
) (map[string]gmail.LabelID, map[gmail.LabelID]string, error) {
	_ = ctx
	return f.labelsByName, f.labelsByID, nil
}

func (f *fakeAuditClient) EnsureLabel(ctx context.Context, name string) (gmail.LabelID, error) {
	_ = ctx
	_ = name
	return "", nil
}

type stubLoader struct {
	export gmailctl.Export
	err    error
}

func (s stubLoader) ExportFilters(ctx context.Context) (gmailctl.Export, error) {
	_ = ctx
	return s.export, s.err
}

func TestServiceRunBasic(t *testing.T) {
	client := &fakeAuditClient{
		pages: []gmail.ListPage{{IDs: []gmail.MessageID{"1", "2", "3"}}},
		metas: map[gmail.MessageID]gmail.MessageMeta{
			"1": {
				ID: "1",
				Headers: map[string]string{
					"From":    "alerts@example.com",
					"Subject": "Alert 1",
					"List-Id": "<alerts.example.com>",
				},
				LabelIDs: []gmail.LabelID{"Label_bulk"},
			},
			"2": {
				ID: "2",
				Headers: map[string]string{
					"From":    "updates@example.com",
					"Subject": "Update",
					"List-Id": "<alerts.example.com>",
				},
				LabelIDs: []gmail.LabelID{"Label_bulk"},
			},
			"3": {
				ID: "3",
				Headers: map[string]string{
					"From":    "news@another.com",
					"Subject": "News",
					"List-Id": "<newsletters.example.net>",
				},
				LabelIDs: []gmail.LabelID{"Label_news"},
			},
		},
		labelsByName: map[string]gmail.LabelID{"bulk": "Label_bulk", "newsletters": "Label_news"},
		labelsByID:   map[gmail.LabelID]string{"Label_bulk": "bulk", "Label_news": "newsletters"},
	}

	svc := NewService(client, nil, slogDiscard(), nil)
	svc.Clock = func() time.Time { return time.Unix(1700000000, 0) }

	rep, err := svc.Run(
		context.Background(),
		Options{Window: 48 * time.Hour, TopN: 5, PageSize: 50},
	)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if rep.Total != 3 {
		t.Fatalf("expected 3 messages, got %d", rep.Total)
	}
	if len(rep.TopSenders) == 0 || rep.TopSenders[0].Domain != "example.com" {
		t.Fatalf("unexpected top sender: %+v", rep.TopSenders)
	}
	if len(rep.TopLists) == 0 || rep.TopLists[0].ListID != "alerts.example.com" {
		t.Fatalf("unexpected top list: %+v", rep.TopLists)
	}
	if rep.Coverage["bulk"] != 2 {
		t.Fatalf("coverage mismatch: %+v", rep.Coverage)
	}
	if len(rep.Suggestions.ArchiveRules) == 0 {
		t.Fatalf("expected archive suggestions")
	}
}

func TestServiceRunGmailctlFindings(t *testing.T) {
	client := &fakeAuditClient{
		pages: []gmail.ListPage{{IDs: []gmail.MessageID{"1"}}},
		metas: map[gmail.MessageID]gmail.MessageMeta{
			"1": {
				ID: "1",
				Headers: map[string]string{
					"From":    "alerts@example.com",
					"Subject": "Alert",
					"List-Id": "<alerts.example.com>",
				},
				LabelIDs: []gmail.LabelID{"Label_bulk"},
			},
		},
		labelsByName: map[string]gmail.LabelID{"bulk": "Label_bulk"},
		labelsByID:   map[gmail.LabelID]string{"Label_bulk": "bulk"},
	}

	export := gmailctl.Export{
		Filters: []gmailctl.Filter{
			{
				Name:     "ArchiveAlerts",
				Criteria: gmailctl.FilterCriteria{List: "alerts.example.com"},
				Action: gmailctl.FilterAction{
					RemoveLabelIDs: []string{"INBOX"},
					AddLabelIDs:    []string{"Label_bulk"},
				},
			},
			{
				Name:     "StarAlerts",
				Criteria: gmailctl.FilterCriteria{List: "alerts.example.com"},
				Action:   gmailctl.FilterAction{AddLabelIDs: []string{"STARRED"}},
			},
			{
				Name:     "DeadRule",
				Criteria: gmailctl.FilterCriteria{List: "unused.example.com"},
				Action:   gmailctl.FilterAction{RemoveLabelIDs: []string{"INBOX"}},
			},
			{
				Name:     "MissingLabelRule",
				Criteria: gmailctl.FilterCriteria{List: "alerts.example.com"},
				Action:   gmailctl.FilterAction{AddLabelIDs: []string{"Label_missing"}},
			},
		},
		Labels: []gmailctl.Label{
			{ID: "Label_bulk", Name: "bulk"},
			{ID: "Label_missing", Name: "missing"},
		},
	}

	svc := NewService(client, nil, slogDiscard(), stubLoader{export: export})
	svc.Clock = func() time.Time { return time.Unix(1700000000, 0) }

	rep, err := svc.Run(
		context.Background(),
		Options{Window: 24 * time.Hour, TopN: 5, PageSize: 10},
	)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(rep.Findings.DeadRules) != 1 || rep.Findings.DeadRules[0].Name != "DeadRule" {
		t.Fatalf("dead rules mismatch: %+v", rep.Findings.DeadRules)
	}
	if len(rep.Findings.Conflicts) == 0 {
		t.Fatalf("expected conflict findings")
	}
	if len(rep.Findings.MissingLabels) != 1 || rep.Findings.MissingLabels[0] != "missing" {
		t.Fatalf("missing labels mismatch: %+v", rep.Findings.MissingLabels)
	}
}

func TestParseFailOn(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name:  "values",
			input: "dead, conflict ,missing-label",
			want:  []string{"dead", "conflict", "missing-label"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseFailOn(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("unexpected length: got %d want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("token mismatch: got %q want %q", got[i], tt.want[i])
				}
			}
		})
	}
}

func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
