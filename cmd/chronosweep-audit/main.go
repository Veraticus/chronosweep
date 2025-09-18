package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joshsymonds/chronosweep/internal/audit"
	"github.com/joshsymonds/chronosweep/internal/gmailctl"
	"github.com/joshsymonds/chronosweep/internal/rate"
	"github.com/joshsymonds/chronosweep/internal/runtime"
)

const hoursPerDay = 24

type auditConfig struct {
	cfgDir         string
	days           int
	topN           int
	jsonOut        string
	pageSize       int
	rps            int
	gmailctlCfg    string
	gmailctlBinary string
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		runtime.DefaultLogger().Error("chronosweep-audit failed", "error", err)
		os.Exit(1)
	}
}

func parseFlags() auditConfig {
	cfgDir := flag.String("config", os.ExpandEnv("$HOME/.gmailctl"), "gmailctl auth directory")
	days := flag.Int("days", 60, "lookback window in days")
	topN := flag.Int("top", 30, "number of top senders/lists to display")
	jsonOut := flag.String("json", "", "write JSON report to path")
	pageSize := flag.Int("page-size", 500, "Gmail list page size (<=500)")
	rps := flag.Int("rps", 4, "max requests per second")
	gmailctlConfig := flag.String("gmailctl-config", "", "path to gmailctl config (optional)")
	gmailctlBin := flag.String("gmailctl-binary", "gmailctl", "gmailctl binary to invoke")
	flag.Parse()

	return auditConfig{
		cfgDir:         *cfgDir,
		days:           *days,
		topN:           *topN,
		jsonOut:        *jsonOut,
		pageSize:       *pageSize,
		rps:            *rps,
		gmailctlCfg:    *gmailctlConfig,
		gmailctlBinary: *gmailctlBin,
	}
}

func run(cfg auditConfig) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := runtime.DefaultLogger()
	client, err := runtime.NewGmailClient(ctx, cfg.cfgDir, runtime.ScopeReadonly)
	if err != nil {
		return fmt.Errorf("create gmail client: %w", err)
	}

	var (
		limiter rate.Limiter
		bucket  *rate.TokenBucket
	)
	if cfg.rps > 0 {
		bucket = rate.NewTokenBucket(cfg.rps)
		limiter = bucket
		defer bucket.Stop()
	}

	cfgPath := cfg.gmailctlCfg
	if cfgPath == "" {
		cfgPath = cfg.cfgDir
	}
	var loader audit.GmailctlLoader
	if cfgPath != "" {
		loader = gmailctl.Runner{Binary: cfg.gmailctlBinary, ConfigDir: cfgPath}
	}

	svc := audit.NewService(client, limiter, logger, loader)
	window := time.Duration(cfg.days) * hoursPerDay * time.Hour
	rep, err := svc.Run(ctx, audit.Options{Window: window, TopN: cfg.topN, PageSize: cfg.pageSize})
	if err != nil {
		return fmt.Errorf("run audit: %w", err)
	}

	if printErr := audit.PrintHuman(rep, os.Stdout); printErr != nil {
		return fmt.Errorf("print report: %w", printErr)
	}
	if cfg.jsonOut == "" {
		return nil
	}
	if writeErr := audit.WriteJSON(rep, cfg.jsonOut); writeErr != nil {
		return fmt.Errorf("write json: %w", writeErr)
	}
	return nil
}
