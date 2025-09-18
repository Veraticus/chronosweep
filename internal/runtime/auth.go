package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/mbrt/gmailctl/cmd/gmailctl/localcred"
)

// Scope controls which Gmail OAuth scope is requested from gmailctl's local credentials store.
type Scope int

const (
	// ScopeReadonly grants access only to read message metadata.
	ScopeReadonly Scope = iota
	// ScopeModify grants access to modify labels and mark messages read.
	ScopeModify
)

// NewGmailClient constructs a gmail.Client backed by the Google API Go client using gmailctl's credential store.
func NewGmailClient(ctx context.Context, cfgDir string, scope Scope) (*ClientAdapter, error) {
	switch scope {
	case ScopeReadonly, ScopeModify:
	default:
		return nil, fmt.Errorf("unsupported scope %d", scope)
	}
	provider := localcred.Provider{}
	svc, err := provider.Service(ctx, cfgDir)
	if err != nil {
		return nil, fmt.Errorf("create gmail service: %w", err)
	}
	return NewGoogleAPIClient(svc), nil
}

// DefaultLogger returns a slog.Logger configured for structured CLI output.
func DefaultLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
