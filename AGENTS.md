# Repository Guidelines

## Project Structure & Module Organization
- CLI entrypoints live in `cmd/`; today `chronosweep-audit` and `chronosweep-lint` wire flags into services under `internal/...`.
- Shared packages reside in `internal/`: `gmail` (API types), `runtime` (auth + Gmail adapters), `sweep` (archiver engine), `audit` (analyzer), and `rate` (token bucket limiter). Keep new modules private and cohesive.
- Nix packaging and developer tooling live in `flake.nix`; update `subPackages` there when adding binaries like `chronosweep-sweep`.
- Place reference docs alongside `ARCHITECTURE.md`; create `docs/` for longer guides or fixtures when needed.

## Build, Test, and Development Commands
- `nix develop` opens a shell with Go, `golangci-lint`, and `staticcheck` preinstalled.
- `make build` compiles every CLI in `cmd/...`; use it before tagging releases to ensure wiring stays green.
- `make lint` runs `gofmt`, `golangci-lint`, and `deadcode`; expect it to rewrite files, so run on a clean tree.
- `make test` executes `gotestsum` with `-race` and produces `coverage.out` plus `coverage.html` for quick inspection.
- `nix build .#chronosweep-audit` mirrors CI packaging and catches missing dependencies or module metadata.

## Coding Style & Naming Conventions
- Target Go 1.21+ and run `gofmt` (or `go fmt ./...`) before committing; enforce `golangci-lint run ./...` to keep interfaces tight.
- Exported identifiers should be readable at call sites (`sweep.Service`, `gmail.Client`); avoid stutter (`audit.AuditResult`).
- Prefer table-driven tests, explicit structs over `map[string]any`, and wrap errors with `%w` for context.
- CLI flags stay kebab-case (`-grace-map`); environment variables use upper snake case.

## Testing Guidelines
- Co-locate tests as `_test.go` files; name cases `Test<Service>_<Scenario>` for clarity.
- Use fakes for `gmail.Client` and `rate.Limiter` to isolate logic from APIs and timers.
- Cover pagination edges (0, 1, 1000+ IDs), dry-run behavior, and label enforcement.
- Store golden outputs under `testdata/` for audit report snapshots; keep them deterministic and update `coverage.html` artifacts when behavior shifts.

## Commit & Pull Request Guidelines
- Start commit subjects with an imperative verb under 72 characters (e.g., `Add limiter token bucket`); detail rationale in the body.
- Separate functional and refactor changes; update `ARCHITECTURE.md` or `flake.nix` alongside code when behavior shifts.
- PR descriptions should list intent, manual/automated testing (`make test`, `make lint`), and call out follow-up work.
- Link related issues or roadmap items; request review from an owner when touching exported APIs or CLI UX.
