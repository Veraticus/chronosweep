// cmd/chronosweepaudit/main.go (skeleton)
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/yourorg/chronosweep/internal/audit"
	"github.com/yourorg/chronosweep/internal/runtime"
)

func main() {
	var (
		cfgDir  = flag.String("config", os.ExpandEnv("$HOME/.gmailctl"), "gmailctl config dir for auth reuse")
		days    = flag.Int("days", 60, "lookback window")
		topN    = flag.Int("top", 30, "how many top senders/lists to show")
		jsonOut = flag.String("json", "", "write machine-readable JSON report to file (optional)")
	)
	flag.Parse()
	logger := runtime.DefaultLogger()
	ctx := context.Background()
	client, err := runtime.NewGmailClient(ctx, *cfgDir, runtime.ScopeReadonly) // gmail.readonly
	runtime.Must(err)

	rep, err := audit.Run(ctx, client, time.Duration(*days)*24*time.Hour, *topN)
	runtime.Must(err)

	audit.PrintHuman(rep, logger)
	if *jsonOut != "" {
		runtime.Must(audit.WriteJSON(rep, *jsonOut))
	}
}
