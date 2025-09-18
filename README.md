# chronosweep

chronosweep is a small suite of Go CLI tools that helps keep Gmail tidy without sacrificing safety. It reuses your gmailctl credentials, works only with message metadata by default, and focuses on deterministic, reversible automation.

## Binaries

| Command | Description |
|---------|-------------|
| `chronosweep-sweep` | Hourly moving-window archiver that marks stale messages read, removes them from the inbox, and applies a safety label. |
| `chronosweep-audit` | Read-only analyzer that ranks noisy senders/list IDs and proposes gmailctl Jsonnet snippets to tighten filters. |
| `chronosweep-lint` | CI-friendly linter that replays compiled gmailctl rules and fails when it finds dead rules, missing labels, or conflicts. |

### Common Flags

All binaries support the `-config` flag pointing at a gmailctl credential directory (defaults to `$HOME/.gmailctl`). Each command also exposes job-specific options:

`rps`
: Requests-per-second budget used by the internal token bucket limiter. Setting `-rps 4` allows four Gmail API calls per second; lower values slow the tool down but help avoid `429` responses. A value of `0` disables the limiter.

Each command’s flags are explained below.

#### chronosweep-sweep

The sweeper looks for messages in `in:inbox` that are unread, older than the calculated grace window, and not starred/important. It removes `INBOX` and `UNREAD`, applies the expired marker label, and logs the number of messages touched. Runs are idempotent because Gmail ignores duplicate label updates.

```
chronosweep-sweep \
  -config $HOME/.gmailctl \
  -grace 48h \
  -grace-map 'calendar/rsvps=2h,monitoring/alerts=4h' \
  -exclude-labels 'finance,legal,team' \
  -expired-label 'auto-archived/expired' \
  -page-size 500 \
  -rps 4
```

* `-label` – restricts the sweep to a specific label (useful for dry-run testing or running override sweeps one-by-one).
* `-grace` – default moving window; messages older than this duration are eligible. Accepts Go-style durations (`1h30m`, `48h`).
* `-grace-map` – comma-separated list of `label=duration` overrides. When a message carries that label, the override is used instead of the default grace. Whitespace is ignored.
* `-exclude-labels` – comma list of labels that should never be swept; the tool appends `-label:"name"` to the Gmail query for each.
* `-expired-label` – safety label applied to swept threads. Defaults to `auto-archived/expired`; the label is created if needed.
* `-page-size` – Gmail list page size (1–500). Higher values reduce API round trips; keep at 500 unless you’re debugging partial pages.
* `-dry-run` – build the query and report counts without modifying Gmail.
* `-pause-weekends` – skip the run entirely on Saturday/Sunday.

#### chronosweep-audit

`chronosweep-audit` fetches metadata-only headers for messages newer than `N` days, aggregates the noisier senders/list IDs, and emits both textual and JSON reports. When `-gmailctl-config` is set, it also runs `gmailctl compile` and simulates the rules against the sampled messages to detect dead rules or conflicts. The JSON output is the input for `chronosweep-lint`.

```
chronosweep-audit \
  -config $HOME/.gmailctl \
  -days 60 \
  -top 30 \
  -page-size 500 \
  -rps 4 \
  -json report.json \
  -gmailctl-config $HOME/.gmailctl \
  -gmailctl-binary gmailctl
```

* `-days` – lookback window in calendar days (converted to a Gmail `newer_than:` query). Values under 1 are coerced to 1 day.
* `-top` – number of senders/lists to include in the summary and snippet generation.
* `-json` – optional path that receives the structured `Report`. The file must reside inside the current working directory; relative paths are safest.
* `-gmailctl-config` – alternate gmailctl directory for reading compiled rules. Defaults to whatever `-config` points at.
* `-gmailctl-binary` – override the executable name if gmailctl isn’t on PATH or renamed.

#### chronosweep-lint

Runs the same metadata collection as `audit`, but focuses on replaying gmailctl rules and enforcing policy (dead rules, missing labels, archive/star conflicts). Intended for CI pipelines; the human-friendly summary is printed to stdout.

```
chronosweep-lint \
  -config $HOME/.gmailctl \
  -days 30 \
  -page-size 500 \
  -rps 4 \
  -fail-on dead,conflict,missing-label \
  -gmailctl-config $HOME/.gmailctl \
  -gmailctl-binary gmailctl
```

* `-fail-on` – comma list of findings that should cause a non-zero exit (`dead`, `conflict`, `missing-label`). Unknown values are ignored.
* `-days`, `-page-size`, `-rps`, `-gmailctl-*` – equivalent to the audit command.
* Exit codes: `0` means no failure conditions were hit; `1` signals at least one requested finding occurred or the command failed internally.

## Development

* `make build` – compile all commands under `cmd/...`.
* `make test` – run the full `gotestsum -race` suite and emit coverage artifacts.
* `make lint` – format with `gofmt`, run `golangci-lint`, and execute `deadcode`.
* `nix develop` – enter a dev shell with Go, `golangci-lint`, and `staticcheck`.
* `nix build .#chronosweep-<command>` – build a specific CLI with Nix.

## Authentication

chronosweep never handles OAuth flow directly; it relies on [gmailctl](https://github.com/mbrt/gmailctl) local credentials (`localcred.Provider`). To grant access:

1. Install gmailctl (e.g., `go install github.com/mbrt/gmailctl/cmd/gmailctl@latest`).
2. Initialize credentials for the target account:
   ```
   gmailctl init --config $HOME/.gmailctl
   ```
   * Accept the OAuth prompts in your browser.
   * gmailctl will persist `credentials.json` and `token.json` under the config directory.
3. Ensure the scopes cover the desired operations:
   * `chronosweep-audit` and `chronosweep-lint` need `https://www.googleapis.com/auth/gmail.readonly`.
   * `chronosweep-sweep` requires `https://www.googleapis.com/auth/gmail.modify`.
   If you initialized with gmailctl defaults you can rerun `gmailctl auth login --scope gmail.modify` to extend scopes. gmailctl stores tokens per config directory, so you can keep separate read-only and modify directories if you want to isolate risk.
4. Point chronosweep commands at the directory (default `$HOME/.gmailctl`, or use `-config` to override). For multi-account setups, keep separate gmailctl directories and pass the appropriate path per invocation.

To refresh or revoke access, use `gmailctl auth refresh` or `gmailctl auth logout` in the chosen config directory; chronosweep will pick up updated tokens automatically.

## Project Structure

```
cmd/
  chronosweep-sweep/   # CLI entrypoint wiring flags into the sweep service
  chronosweep-audit/
  chronosweep-lint/
internal/
  gmail/               # Strong Gmail types and the narrow Client interface
  runtime/             # gmailctl auth adapter + Google API implementation
  sweep/               # Moving-window sweep engine
  audit/               # Analyzer, report generation, gmailctl replay
  rate/                # Token bucket limiter
  gmailctl/            # Helpers for invoking gmailctl safely
```

All internal packages are designed for dependency injection and table-driven testing. See `ARCHITECTURE.md` for deeper rationale and roadmap.

## Contributing

* Run `make lint` and `make test` before sending patches.
* Keep interfaces small, avoid globals, and prefer explicit constructors.
* Update docs (README, ARCHITECTURE.md) when behavior changes.
