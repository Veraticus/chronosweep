package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joshsymonds/chronosweep/internal/rate"
	"github.com/joshsymonds/chronosweep/internal/runtime"
	"github.com/joshsymonds/chronosweep/internal/sweep"
)

type sweepConfig struct {
	cfgDir        string
	label         string
	grace         time.Duration
	graceMap      string
	exclude       string
	expiredLabel  string
	pageSize      int
	rps           int
	dryRun        bool
	pauseWeekends bool
}

func main() {
	cfg := parseSweepFlags()
	if err := run(cfg); err != nil {
		runtime.DefaultLogger().Error("chronosweep-sweep failed", "error", err)
		os.Exit(1)
	}
}

func parseSweepFlags() sweepConfig {
	cfgDir := flag.String("config", os.ExpandEnv("$HOME/.gmailctl"), "gmailctl auth directory")
	label := flag.String("label", "", "limit sweep to this label")
	grace := flag.Duration("grace", 48*time.Hour, "default grace period")
	graceMapFlag := flag.String("grace-map", "", "comma separated label=duration overrides")
	excludeFlag := flag.String("exclude-labels", "", "comma separated labels to protect")
	expiredLabel := flag.String("expired-label", "auto-archived/expired", "label applied to swept mail")
	pageSize := flag.Int("page-size", 500, "Gmail list page size (<=500)")
	rps := flag.Int("rps", 4, "max requests per second")
	dryRun := flag.Bool("dry-run", false, "log only; skip modifications")
	pauseWeekends := flag.Bool("pause-weekends", false, "skip runs on Saturday/Sunday")
	flag.Parse()

	return sweepConfig{
		cfgDir:        *cfgDir,
		label:         *label,
		grace:         *grace,
		graceMap:      *graceMapFlag,
		exclude:       *excludeFlag,
		expiredLabel:  *expiredLabel,
		pageSize:      *pageSize,
		rps:           *rps,
		dryRun:        *dryRun,
		pauseWeekends: *pauseWeekends,
	}
}

func run(cfg sweepConfig) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	overrides, err := sweep.ParseGraceMap(cfg.graceMap)
	if err != nil {
		return fmt.Errorf("parse grace map: %w", err)
	}
	exclude := splitList(cfg.exclude)

	client, err := runtime.NewGmailClient(ctx, cfg.cfgDir, runtime.ScopeModify)
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

	svc := sweep.NewService(client, limiter, runtime.DefaultLogger())
	svc.Clock = time.Now

	spec := sweep.Spec{
		Label:          cfg.label,
		Grace:          cfg.grace,
		DryRun:         cfg.dryRun,
		PauseWeekends:  cfg.pauseWeekends,
		GraceOverrides: overrides,
		ExcludeLabels:  exclude,
		ExpiredLabel:   cfg.expiredLabel,
		PageSize:       cfg.pageSize,
	}

	if runErr := svc.Run(ctx, spec); runErr != nil {
		return fmt.Errorf("run sweep: %w", runErr)
	}

	if cfg.label != "" {
		return nil
	}

	for lbl, dur := range overrides {
		overrideSpec := sweep.Spec{
			Label:          lbl,
			Grace:          dur,
			DryRun:         cfg.dryRun,
			PauseWeekends:  cfg.pauseWeekends,
			GraceOverrides: overrides,
			ExcludeLabels:  exclude,
			ExpiredLabel:   cfg.expiredLabel,
			PageSize:       cfg.pageSize,
		}
		if runErr := svc.Run(ctx, overrideSpec); runErr != nil {
			return fmt.Errorf("run sweep override for %s: %w", lbl, runErr)
		}
	}
	return nil
}

func splitList(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	parts := strings.Split(input, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
