// cmd/chronosweep-lint/main.go (skeleton)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/yourorg/chronosweep/internal/audit"
	"github.com/yourorg/chronosweep/internal/runtime"
)

func main() {
	var (
		cfgDir = flag.String("config", os.ExpandEnv("$HOME/.gmailctl"), "gmailctl config dir")
		days   = flag.Int("days", 30, "lookback window")
		failOn = flag.String("fail-on", "dead,conflict,missing-label", "comma list of conditions to fail CI on")
	)
	flag.Parse()
	logger := runtime.DefaultLogger()
	ctx := context.Background()
	client, err := runtime.NewGmailClient(ctx, *cfgDir, runtime.ScopeReadonly)
	runtime.Must(err)

	rep, err := audit.RunLint(ctx, client, time.Duration(*days)*24*time.Hour)
	runtime.Must(err)

	exit := 0
	out := rep.HumanSummary()
	fmt.Println(out)
	if rep.ShouldFail(*failOn) {
		exit = 1
	}
	os.Exit(exit)
}
