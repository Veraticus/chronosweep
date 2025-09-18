package sweep

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/chronosweep/internal/gmail"
)

type fakeClient struct {
	listPages        []gmail.ListPage
	listQueries      []string
	batchBatches     [][]gmail.MessageID
	ensuredLabel     string
	ensureLabelErr   error
	listLabelsByName map[string]gmail.LabelID
	listLabelsByID   map[gmail.LabelID]string
}

func (f *fakeClient) List(ctx context.Context, q gmail.Query, pageToken string, pageSize int) (gmail.ListPage, error) {
	_ = ctx
	_ = q
	_ = pageToken
	_ = pageSize
	f.listQueries = append(f.listQueries, q.Raw)
	if len(f.listPages) == 0 {
		return gmail.ListPage{}, nil
	}
	page := f.listPages[0]
	f.listPages = f.listPages[1:]
	return page, nil
}

func (f *fakeClient) GetMetadata(ctx context.Context, id gmail.MessageID, headers []string) (gmail.MessageMeta, error) {
	_ = ctx
	_ = id
	_ = headers
	return gmail.MessageMeta{}, nil
}

func (f *fakeClient) BatchModify(ctx context.Context, ids []gmail.MessageID, ops gmail.ModifyOps) error {
	_ = ctx
	_ = ops
	copyIDs := append([]gmail.MessageID(nil), ids...)
	f.batchBatches = append(f.batchBatches, copyIDs)
	return nil
}

func (f *fakeClient) ListLabels(ctx context.Context) (map[string]gmail.LabelID, map[gmail.LabelID]string, error) {
	_ = ctx
	if f.listLabelsByName == nil {
		f.listLabelsByName = map[string]gmail.LabelID{"auto-archived/expired": "Label123"}
		f.listLabelsByID = map[gmail.LabelID]string{"Label123": "auto-archived/expired"}
	}
	return f.listLabelsByName, f.listLabelsByID, nil
}

func (f *fakeClient) EnsureLabel(ctx context.Context, name string) (gmail.LabelID, error) {
	_ = ctx
	_ = name
	f.ensuredLabel = name
	if f.ensureLabelErr != nil {
		return "", f.ensureLabelErr
	}
	return "Label123", nil
}

type noLimiter struct{}

func (noLimiter) Wait(ctx context.Context) error {
	_ = ctx
	return nil
}

func TestParseGraceMap(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]time.Duration
		wantErr bool
	}{
		{
			name:  "empty",
			input: "",
			want:  map[string]time.Duration{},
		},
		{
			name:  "single",
			input: "alerts=2h",
			want:  map[string]time.Duration{"alerts": 2 * time.Hour},
		},
		{
			name:  "multiple",
			input: "alerts=2h,build=4h",
			want: map[string]time.Duration{
				"alerts": 2 * time.Hour,
				"build":  4 * time.Hour,
			},
		},
		{
			name:    "bad-duration",
			input:   "alerts=abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseGraceMap(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("unexpected map size: got %d want %d", len(got), len(tc.want))
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("duration mismatch for %s: got %v want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestRunBuildsQuery(t *testing.T) {
	fake := &fakeClient{}
	svc := NewService(fake, noLimiter{}, slogDiscard())
	svc.Clock = func() time.Time { return time.Unix(1700000000, 0) }

	spec := Spec{
		Label:         "news",
		Grace:         48 * time.Hour,
		DryRun:        true,
		ExcludeLabels: []string{"protected", "finance"},
		ExpiredLabel:  "auto-archived/expired",
	}

	if err := svc.Run(context.Background(), spec); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(fake.listQueries) != 1 {
		t.Fatalf("expected 1 list call, got %d", len(fake.listQueries))
	}
	query := fake.listQueries[0]
	wantParts := []string{
		"label:\"news\"",
		"-label:\"finance\"",
		"-label:\"protected\"",
		"in:inbox",
		"before:",
	}
	for _, part := range wantParts {
		if !strings.Contains(query, part) {
			t.Fatalf("query %q missing segment %q", query, part)
		}
	}
}

func TestRunChunking(t *testing.T) {
	fake := &fakeClient{}
	ids := make([]gmail.MessageID, 1200)
	for i := range ids {
		ids[i] = gmail.MessageID(fmt.Sprintf("id-%04d", i))
	}
	fake.listPages = []gmail.ListPage{{IDs: ids}}
	svc := NewService(fake, noLimiter{}, slogDiscard())
	svc.Clock = func() time.Time { return time.Unix(1700000000, 0) }

	spec := Spec{Grace: 24 * time.Hour, ExpiredLabel: "auto-archived/expired"}
	if err := svc.Run(context.Background(), spec); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(fake.batchBatches) != 2 {
		t.Fatalf("expected 2 batch calls, got %d", len(fake.batchBatches))
	}
	if len(fake.batchBatches[0]) != 1000 {
		t.Fatalf("first batch size %d", len(fake.batchBatches[0]))
	}
	if len(fake.batchBatches[1]) != 200 {
		t.Fatalf("second batch size %d", len(fake.batchBatches[1]))
	}
}

func TestRunDryRunSkipsMutations(t *testing.T) {
	fake := &fakeClient{}
	fake.listPages = []gmail.ListPage{{IDs: []gmail.MessageID{"a", "b"}}}
	svc := NewService(fake, noLimiter{}, slogDiscard())
	svc.Clock = func() time.Time { return time.Unix(1700000000, 0) }

	spec := Spec{Grace: 24 * time.Hour, DryRun: true}
	if err := svc.Run(context.Background(), spec); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if fake.ensuredLabel != "" {
		t.Fatalf("expected no label creation in dry-run, got %q", fake.ensuredLabel)
	}
	if len(fake.batchBatches) != 0 {
		t.Fatalf("expected no batch modify calls, got %d", len(fake.batchBatches))
	}
}

func TestRunPauseWeekends(t *testing.T) {
	fake := &fakeClient{}
	svc := NewService(fake, noLimiter{}, slogDiscard())
	svc.Clock = func() time.Time { return time.Date(2024, time.March, 9, 10, 0, 0, 0, time.UTC) } // Saturday

	spec := Spec{Grace: 24 * time.Hour, PauseWeekends: true}
	if err := svc.Run(context.Background(), spec); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(fake.listQueries) != 0 {
		t.Fatalf("expected no list calls when paused, got %d", len(fake.listQueries))
	}
}

func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
