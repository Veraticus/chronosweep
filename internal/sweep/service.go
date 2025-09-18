// internal/sweep/service.go
package sweep

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	gc "github.com/yourorg/chronosweep/internal/gmail"
)

type Spec struct {
	Label  string        // optional: restrict sweep to this label
	Grace  time.Duration // how long to wait before sweeping
	DryRun bool
}

type Service struct {
	Client       gc.Client
	Log          *slog.Logger
	Rate         interface{ Wait(context.Context) error } // small interface
	ExpiredLabel string
	Exclude      []string
	PageSize     int
}

func ParseGraceMap(s string) map[string]time.Duration {
	out := map[string]time.Duration{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if d, err := time.ParseDuration(strings.TrimSpace(kv[1])); err == nil {
			out[strings.TrimSpace(kv[0])] = d
		}
	}
	return out
}

func (s *Service) Run(ctx context.Context, spec Spec) error {
	th := time.Now().Add(-spec.Grace).Unix()
	parts := []string{"in:inbox", "is:unread", fmt.Sprintf("before:%d", th), "-is:starred", "-is:important"}
	for _, l := range s.Exclude {
		parts = append(parts, fmt.Sprintf(`-label:"%s"`, l))
	}
	if spec.Label != "" {
		parts = append([]string{fmt.Sprintf(`label:"%s"`, spec.Label)}, parts...)
	}
	q := gc.Query{Raw: strings.Join(parts, " ")}

	var all []gc.MessageID
	pageToken := ""
	for {
		if err := s.Rate.Wait(ctx); err != nil {
			return err
		}
		ids, next, err := s.Client.List(ctx, q, s.PageSize)
		if err != nil {
			return err
		}
		all = append(all, ids...)
		if next == "" {
			break
		}
		pageToken = next // for completeness; List can close over next token
		_ = pageToken
	}
	if len(all) == 0 {
		s.Log.Info("no messages to sweep", "label", spec.Label, "grace", spec.Grace)
		return nil
	}
	if spec.DryRun {
		s.Log.Info("dry-run", "label", spec.Label, "grace", spec.Grace, "count", len(all))
		return nil
	}
	lid, err := s.Client.EnsureLabel(ctx, s.ExpiredLabel)
	if err != nil {
		return err
	}

	ops := gc.ModifyOps{
		AddLabels:    []gc.LabelID{lid},
		RemoveLabels: []gc.LabelID{"UNREAD", "INBOX"},
		MarkRead:     true, // explicit for clarity
		Archive:      true,
	}
	// Chunk conservatively (Gmail API allows 1000/message batch modify)
	const chunk = 1000
	for i := 0; i < len(all); i += chunk {
		j := i + chunk
		if j > len(all) {
			j = len(all)
		}
		if err := s.Client.BatchModify(ctx, all[i:j], ops); err != nil {
			return err
		}
	}
	s.Log.Info("swept", "label", spec.Label, "grace", spec.Grace, "count", len(all))
	return nil
}
