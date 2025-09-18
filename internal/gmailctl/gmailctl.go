package gmailctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Export mirrors the JSON payload produced by `gmailctl compile --format=json`.
type Export struct {
	Filters []Filter `json:"filters"`
	Labels  []Label  `json:"labels"`
}

// Filter represents a single Gmail filter definition.
type Filter struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name,omitempty"`
	Criteria FilterCriteria `json:"criteria"`
	Action   FilterAction   `json:"action"`
}

// FilterCriteria captures the subset of Gmail search predicates we replay.
type FilterCriteria struct {
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Subject string `json:"subject,omitempty"`
	Query   string `json:"query,omitempty"`
	List    string `json:"list,omitempty"`
}

// FilterAction describes the Gmail actions for a filter.
type FilterAction struct {
	AddLabelIDs    []string `json:"addLabelIds,omitempty"`
	RemoveLabelIDs []string `json:"removeLabelIds,omitempty"`
	Forward        string   `json:"forward,omitempty"`
}

// Label mirrors Gmail label metadata in the compile output.
type Label struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Runner shells out to the gmailctl binary to obtain compiled filters.
type Runner struct {
	Binary    string
	ConfigDir string
}

// ExportFilters invokes gmailctl and parses the resulting JSON export.
func (r Runner) ExportFilters(ctx context.Context) (Export, error) {
	bin := r.Binary
	if bin == "" {
		bin = "gmailctl"
	}
	args := []string{"compile", "--format=json"}
	if strings.TrimSpace(r.ConfigDir) != "" {
		args = append(args, "--config", r.ConfigDir)
	}
	cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 - binary determined by user input
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Export{}, fmt.Errorf(
			"run gmailctl: %w (output: %s)",
			err,
			strings.TrimSpace(string(out)),
		)
	}
	var export Export
	if decodeErr := json.Unmarshal(out, &export); decodeErr != nil {
		return Export{}, fmt.Errorf("decode gmailctl output: %w", decodeErr)
	}
	if len(export.Filters) == 0 && len(export.Labels) == 0 {
		return Export{}, errors.New("gmailctl returned no filters or labels")
	}
	return export, nil
}
