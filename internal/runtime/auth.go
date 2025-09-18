// internal/runtime/auth.go
package runtime

import (
	"context"
	"log/slog"
	"os"

	"github.com/mbrt/gmailctl/cmd/gmailctl/localcred"
	"google.golang.org/api/gmail/v1"

	gc "github.com/yourorg/chronosweep/internal/gmail"
)

type Scope int

const (
	ScopeReadonly Scope = iota
	ScopeModify
)

func NewGmailClient(ctx context.Context, cfgDir string, scope Scope) (gc.Client, error) {
	var svc *gmail.Service
	var err error
	// localcred chooses scopes based on what the binary requests on first run
	switch scope {
	case ScopeReadonly:
		svc, err = (localcred.Provider{}).ServiceWithScopes(ctx, cfgDir, gmail.GmailReadonlyScope)
	case ScopeModify:
		svc, err = (localcred.Provider{}).ServiceWithScopes(ctx, cfgDir, gmail.GmailModifyScope)
	default:
		panic("unknown scope")
	}
	if err != nil {
		return nil, err
	}
	return NewGoogleAPIClient(svc), nil
}

func DefaultLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
