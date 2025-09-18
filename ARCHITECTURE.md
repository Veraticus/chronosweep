# Architecture document

## 1. Overview

**chronosweep** is a small suite of Go CLIs for keeping Gmail clean while staying safe and reversible:

* **`chronosweep-sweep`** — Hourly “moving window” archiver. Finds mail older than a grace period (default 48h) that you haven’t touched; marks read, archives, and applies a safety label. Supports per-label shortened grace (e.g., calendar RSVP 2h; monitoring alerts 4h).
* **`chronosweep-audit`** — Read-only analyzer. Scans the last N days, tallies noisy senders/List-Id headers, and emits **candidate gmailctl Jsonnet snippet rules** (e.g., bulk → markRead+archive). Optionally integrates with gmailctl’s compiled filters to identify **dead rules** and **conflicts**.
* **`chronosweep-lint`** — CI guard. Runs audits + static checks; fails when there are dead rules, missing labels, or bad smells (e.g., a rule stars a message that another rule archives).

**Design priorities**

* **Safety:** no deletions by default (sweeper writes `auto-archived/expired`), fail-open on ambiguity, idempotent operations, and small surface area.
* **Clarity:** small interfaces, strong types; no `any`/`interface{}` in public surfaces.
* **Ergonomics:** reuse **gmailctl** auth store (`localcred`), one binary per job, easy Nix packaging.
* **Performance:** headers-only reads, request rate limiting, batchModify, exponential backoff on 429s.

---

## 2. Repo layout

```
chronosweep/
  cmd/
    chronosweep-sweep/    # main.go only, wires flags → services
    chronosweep-audit/
    chronosweep-lint/
  internal/
    gmail/                # small types + Client interface (mockable)
    runtime/              # adapters: gmailctl auth, google api client, logging, rate limiter
    sweep/                # sweep engine (queries, batching, label ensure)
    audit/                # analyzer + rule suggestor
    lint/                 # lint runner (wraps audit + gmailctl compiled-export)
    gmailctl/             # (optional later) helpers to call `gmailctl compile/export` safely
  go.mod
  flake.nix
  README.md
```

**Dependencies**

* `google.golang.org/api/gmail/v1` (official Gmail API client)
* `github.com/mbrt/gmailctl/cmd/gmailctl/localcred` (auth reuse)
* stdlib + `log/slog`
  No other heavy deps; keep mocks hand-written; table tests only.

---

## 3. Shared components

### 3.1 `internal/gmail` (types + small interface)

* Strong types: `MessageID`, `LabelID`.
* `MessageMeta` carries **headers only** (fast) and any labels if requested.
* `Client` interface defines only the calls we need: `List`, `GetMetadata`, `BatchModify`, `ListLabels`, `EnsureLabel`.

### 3.2 `internal/runtime`

* `NewGmailClient(ctx, cfgDir, scope)` returns a `gmail.Client` based on gmailctl’s local creds and requested scope (`gmail.readonly` for audit/lint, `gmail.modify` for sweep).
* `DefaultLogger()` returns a `slog` logger with sane defaults.
* Google API adapter to our interface with:

  * **Rate limiting** left to caller (we inject a small Limiter).
  * **BatchModify** chunks of ≤1000 IDs.
  * Label ensure: list first, then create.

### 3.3 Rate limiting/backoff

* Token bucket limiter (`rps` flag).
* Backoff is handled implicitly via limiter. If needed, wrap Gmail calls with a simple exponential backoff (cap at \~3 retries, jitter).

---

## 4. Binary: `chronosweep-sweep`

**Goal**: Maintain a bounded inbox via an **hourly** moving window.

**Algorithm**

1. Build query:

   ```
   [optional label:"X"] in:inbox is:unread before:<epochSeconds> -is:starred -is:important [ -label:"protected"... ]
   ```

   Use **epoch** in `before:` to avoid midnight TZ semantics.
2. List all matching message IDs (page size 500; keep pulling until done).
3. Ensure `auto-archived/expired` label exists.
4. `BatchModify` in chunks: remove `UNREAD` and `INBOX`, add expired label.
5. Log counts. Dry-run mode prints only.

**Flags**

* `-config`: path to gmailctl auth dir (usually `~/.gmailctl` or account-specific).
* `-grace`: default delay (e.g., `48h`).
* `-grace-map`: per-label overrides (`calendar/rsvps=2h,monitoring/alerts=4h`).
* `-exclude-labels`: protected labels (never sweep).
* `-expired-label`: name of archive marker label.
* `-page-size`: up to 500.
* `-rps`: request rate limit.
* `-dry-run`
* `-pause-weekends`

**Safety**

* Never deletes. Only archives + marks read + labels.
* If `EnsureLabel` fails, abort run; don’t sweep unlabeled messages.
* If Gmail API errors mid-run, exit non-zero so systemd can retry; idempotent.

**Testing**

* Table tests for query construction given spec.
* Mock client for `List`/`BatchModify` to verify chunking, labels, and that protected labels are respected.
* Edge: empty results, single-page, multi-page, 999/1000/1001 IDs.

---

## 5. Binary: `chronosweep-audit`

**Goal**: generate **actionable suggestions** to tighten gmailctl filters.

**Inputs**

* Lookback window (e.g., 60 days).
* Read-only scope.
* Optionally path to gmailctl compiled filters to detect dead/broken rules (phase 2).

**Outputs**

* Human report:

  * Top senders (by domain), with counts and sample subjects.
  * Top lists (`List-Id`) with counts and sample subjects.
  * Coverage: % by key labels if you pull label IDs for each message.
  * Suggestions (text + Jsonnet snippets).
* JSON report (for use by lint/CI):

  * `top_senders`, `top_lists`, `suggestions.archive_rules[]`, `dead_rules[]`, `conflicts[]`.

**Phase 1 (headers-only)**

* Query: `newer_than:<Nd>`.
* For each message: collect `From`, `Subject`, `List-Id`, `Auto-Submitted`, `Precedence`.
* Rank and produce **Jsonnet** suggestions, e.g.:

  ```jsonnet
  {
    filter: { or: [ { list: "list.example.com" }, { from: "*@example.com" } ] },
    actions: { labels: ['bulk'], archive: true, markRead: true },
  }
  ```
* Heuristics to escalate: if domain contains finance/legal keywords → propose keeping in inbox.

**Phase 2 (optional gmailctl integration)**

* Shell out: `gmailctl compile` (or `export`) to obtain concrete filters (XML).
* Parse to an internal filter struct the subset we care about (from/to/subject/query, actions).
* **Replay** recent messages against predicates we can simulate (from/to/subject/list, not full Gmail search semantics).
* Mark any filter with **0 matches** as **dead**.
* Detect **smells/conflicts**:

  * A rule `archive:true` always co-matches with another rule that `star:true` for same messages.
  * Rules that contain `older_than:` (non-functional at delivery time).
  * Labels that are referenced by rules but do not exist in Gmail.
* Emit removal suggestions + notes.

**Testing**

* Golden tests on report rendering (human + json).
* Simulated message sets to verify ranking and snippet generation.
* When filter replay is added: construct a small synthetic rule set + headers and ensure expected dead/conflict results.

---

## 6. Binary: `chronosweep-lint`

**Goal**: CI guardrail. Run in GitHub Actions (or locally) to prevent drift.

**Behavior**

* Calls `audit.RunLint` (which wraps Phase 2 above).
* Prints a summary table and returns **non-zero** if conditions matched `-fail-on`, e.g.:

  * `dead` — at least one rule had zero matches in last `N` days.
  * `missing-label` — a rule references a label not present.
  * `conflict` — archive/star contradictions detected.

**Flags**

* `-config`, `-days`, `-fail-on=dead,conflict,missing-label`.

**Testing**

* Unit tests feed synthetic audit data and assert exit behavior.

---

## 7. Nix flake integration

**In your dotfiles flake**

```nix
# flake.nix (dotfiles)
{
  inputs.chronosweep.url = "github:yourname/chronosweep";
  # ...
  outputs = { self, nixpkgs, chronosweep, ... }@inputs:
  let
    system = "x86_64-linux"; # or builtins.currentSystem
    pkgs = import nixpkgs { inherit system; };
  in {
    # Home Manager or NixOS config where you create timers:
    home.packages = [
      chronosweep.packages.${system}.chronosweep-sweep
      chronosweep.packages.${system}.chronosweep-audit
      chronosweep.packages.${system}.chronosweep-lint
    ];

    systemd.user.services.chronosweep-sweep = {
      Unit.Description = "chronosweep moving-window sweep";
      Service = {
        Type = "oneshot";
        ExecStart = "${chronosweep.packages.${system}.chronosweep-sweep}/bin/chronosweep-sweep \
          -config ${config.home.homeDirectory}/.gmailctl-work \
          -grace 48h -grace-map 'calendar/rsvps=2h,monitoring/alerts=4h' \
          -exclude-labels 'üö®-critical,üë•-team,üì®-direct,üí∞-finance,‚öñÔ∏è-legal,üé´-support,üì¶-shipping,docs/comments,compliance/drata'";
      };
      Install.WantedBy = [ "default.target" ];
    };

    systemd.user.timers.chronosweep-sweep = {
      Unit.Description = "Run chronosweep hourly";
      Timer = { OnCalendar = "hourly"; Persistent = true; };
      Install.WantedBy = [ "timers.target" ];
    };

    # Optional: weekly audit
    systemd.user.services.chronosweep-audit = {
      Unit.Description = "chronosweep audit last 60d";
      Service.Type = "oneshot";
      Service.ExecStart = "${chronosweep.packages.${system}.chronosweep-audit}/bin/chronosweep-audit \
        -config ${config.home.homeDirectory}/.gmailctl-work -days 60";
      Install.WantedBy = [ "default.target" ];
    };
    systemd.user.timers.chronosweep-audit = {
      Unit.Description = "Weekly chronosweep audit";
      Timer.OnCalendar = "Sun *-*-* 09:00:00";
      Timer.Persistent = true;
      Install.WantedBy = [ "timers.target" ];
    };
  };
}
```

---

## 8. Coding guidelines (enforced)

* **DI via small interfaces** (`gmail.Client`, `Limiter`), constructors that accept dependencies.
* **Strong typing** for IDs; **no `any`** in exported surfaces.
* Use `context.Context` everywhere; no global state.
* **Errors**: wrap with `%w`, define **sentinel** errors only when callers need to branch.
* **Logging**: structured, `slog`, include counts and labels; no PII leakage by default.
* **Table-driven tests** for query building, chunking, and suggestion generation.
* **Concurrency**: externally rate-limited; only add goroutines where latency matters (e.g., parallel metadata fetch with a small worker pool).
* **CLI UX**: flags only (no config file by default); output deterministic.
* **Docs**: each cmd has `README` section with examples; the top README covers scopes and first-run prompts.

---

## 9. Security & scopes

* **Auth reuse**: `localcred.Provider` in gmailctl directory.
* **Scopes**:

  * `chronosweep-sweep`: `gmail.modify` (mark read, archive, labels).
  * `chronosweep-audit`/`-lint`: `gmail.readonly`.
* **First run** prompts once; subsequent runs reuse tokens.
* **No message bodies** in audit (metadata-only) unless a lab flag explicitly requests it for advanced heuristics.

---

## 10. Roadmap (future PRs)

* Gmail History API cursor (process only deltas).
* `gmailctl` integration module: run `gmailctl compile` and parse; add dead-rule detection in `-lint`.
* Worker pool + bounded parallelism in audit for faster metadata fetch.
* Optional: HTML digest email for what `-sweep` archived in last hour.

---

If you give me the green light on **chronosweep**, I can flesh out `audit.RunLint`’s phase-2 (gmailctl replay) and add table tests + a basic GitHub Actions workflow (`go build`, `go test`, `golangci-lint`).

