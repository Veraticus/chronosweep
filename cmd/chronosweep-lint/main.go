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

const (
	hoursPerDayLint = 24
)

type lintConfig struct {
	cfgDir         string
	days           int
	failOn         string
	pageSize       int
	rps            int
	gmailctlCfg    string
	gmailctlBinary string
}

func main() {
	cfg := parseLintFlags()
	if err := run(cfg); err != nil {
		runtime.DefaultLogger().Error("chronosweep-lint failed", "error", err)
		os.Exit(1)
	}
}

func parseLintFlags() lintConfig {
	cfgDir := flag.String("config", os.ExpandEnv("$HOME/.gmailctl"), "gmailctl auth directory")
	days := flag.Int("days", 30, "lookback window in days")
	failOn := flag.String("fail-on", "dead,conflict,missing-label", "comma separated lint failures")
	pageSize := flag.Int("page-size", 500, "Gmail list page size (<=500)")
	rps := flag.Int("rps", 4, "max requests per second")
	gmailctlConfig := flag.String("gmailctl-config", "", "path to gmailctl config (optional)")
	gmailctlBin := flag.String("gmailctl-binary", "gmailctl", "gmailctl binary to invoke")
	flag.Parse()

	return lintConfig{
		cfgDir:         *cfgDir,
		days:           *days,
		failOn:         *failOn,
		pageSize:       *pageSize,
		rps:            *rps,
		gmailctlCfg:    *gmailctlConfig,
		gmailctlBinary: *gmailctlBin,
	}
}

func run(cfg lintConfig) error {
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
	window := time.Duration(cfg.days) * hoursPerDayLint * time.Hour
	rep, err := svc.RunLint(ctx, audit.Options{Window: window, TopN: 0, PageSize: cfg.pageSize})
	if err != nil {
		return fmt.Errorf("run lint: %w", err)
	}

	summary := rep.HumanSummary()
	if summary != "" {
		if _, writeErr := os.Stdout.WriteString(summary); writeErr != nil {
			return fmt.Errorf("write summary: %w", writeErr)
		}
	}
	failTokens := audit.ParseFailOn(cfg.failOn)
	if rep.ShouldFail(failTokens) {
		return fmt.Errorf("lint failures matched: %s", cfg.failOn)
	}
	return nil
}
