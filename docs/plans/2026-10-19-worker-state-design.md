# Worker state: one resolved state, cheaper than today — design

Status: draft, 2026-10-19. Not yet design-reviewed.

## Goal

A worker's row should say what the worker is actually doing — `setup`,
`working`, `waiting (permission)`, `idle ✓`, `errored (rate limit)`,
`dead` — and stay right through an Esc, a crash, a failed setup, an
API error or a paste that didn't land. Today it often doesn't.

**Overriding constraint: grove's footprint goes down, not up.** The
operator already runs the machine at its RAM limit
([[cockpit-ram-reserved-for-workers]]). Every change below is costed
against the cockpit's existing 1s beat. A design that adds a goroutine,
a watcher, a resident cache or a per-tick exec is rejected, however
much more accurate it would be. The success criterion is better state
with **fewer** process spawns per second than today.

## Why state is sloppy today

Three state tracks exist and nothing reconciles them:

| Track | Source | Where it shows |
|---|---|---|
| `agent` + `sentinel` → `Task.Label()` | 4 hooks (SessionStart, Stop, Notification, SessionEnd), folded last-event-wins (`internal/state/state.go` `fold`) | the row label, `gv ls` |
| `liveness` | pane scraping in the supervise engine, debounced (`internal/supervise/transitions.go`) | footer flash, ntfy, `gv watch`, `gv ls --json` — **never the row** |
| LIVE column | stateless `capture-pane` classification per tick (`internal/detect/live.go` `classifyPaneOutput`) | beside the label, unreconciled; reads a running turn as idle on about half its frames (grove-300) |

The concrete failures:

| Symptom | Cause |
|---|---|
| `working` forever after Esc | No hook fires on interrupt; UserPromptSubmit isn't installed |
| `working` forever after a crash or kill | No hook fires; `worker_vanished` never touches the label |
| `setup` forever | A failed `gv run-setup` never starts claude; liveness skips `setup` rows on purpose |
| `working` while sitting at a permission prompt | The `notification` event is appended but never folded |
| `working` after an API error or 429 | `StopFailure` isn't installed; only the pane scrape sees it |
| `working` after a `gv answer` whose paste didn't land | `answered` flips to `working` optimistically; the "scraper corrects" comment at `state.go` `EvAnswered` is false — no code does |
| `idle`/`stalled?` while the operator is typing into it | `session_started` doesn't promote from `idle`; no prompt-submit signal |

The scraper has also been broken by Claude Code UI changes three
times (LEARNINGS.md), and it costs one `capture-pane` exec per active
worker per second.

## What other harnesses do (research, 2026-10-19)

- **herdr** owns every PTY (embedded ghostty) and matches the screen
  against hot-reloaded per-agent rule manifests. Accurate, but needs a
  PTY grove doesn't own, and the rules are what keeps breaking for us.
- **agent-deck** is the closest model: hooks write state
  (`UserPromptSubmit`/`PreToolUse` → running, `Stop`/`PermissionRequest`
  → waiting, `SessionEnd` → dead). A hook's state is trusted for 2 min;
  a permission wait for 5s, then the pane decides (Esc on a dialog fires
  nothing). A pane is only captured when `#{window_activity}` changed.
- **claux / workmux / Sculptor**: hooks only, near-zero idle cost.
- **vibe-kanban / Nimbalyst / Crystal**: `claude -p --output-format
  stream-json` or the Agent SDK — exact, but no interactive terminal.
  Not for grove.
- **claude-squad / uzi**: hash the pane every 500ms. Least reliable.

**And the finding that shapes this design:** Claude Code itself now
writes `<config dir>/sessions/<pid>.json` for every live session,
interactive ones included. Verified on this machine (v2.1.303):

```
{"pid": 11700, "kind": "interactive", "status": "busy",
 "statusUpdatedAt": 1792426873483, "tmux": "grove-grove-repo:@43.%166",
 "sessionId": "62d49153-…", "procStart": "Mon Oct 19 16:13:14 2026", …}
```

`status` is `busy | idle | waiting`, with `waitingFor` (`permission
prompt | input needed | dialog open | …`) when waiting. Claude Code
maintains it itself; it names the tmux **pane id**, which the tmux
snapshot grove already reads every tick carries too. 16 live sessions
here, ~700 bytes each, no dead-pid files at the time of checking.
`claude agents --json` is the supported reader of the same data — but
it starts a Claude Code process (a running one sits at ~400 MB RSS
here), so grove never calls it on a beat.

## Design

### 1. One pure resolver, owned by `internal/state`

```go
// Resolve folds everything known about one worker into the one state
// shown everywhere. Pure: no I/O, no clock reads, no allocation beyond
// the returned value.
func Resolve(t *Task, s Signals, now time.Time) Resolved

// Signals, not Observation: supervise.Observation already exists and
// will carry one of these.
type Signals struct {
    WindowExists bool
    PaneCommand  string        // from the existing tmux snapshot
    Claude       *ClaudeStatus // nil = no session file for this pane
    PaneError    string        // ErrorMarker kind, only when a fallback capture ran
}

type ClaudeStatus struct {
    Status     string // busy | idle | waiting
    WaitingFor string
    UpdatedAt  time.Time
    SessionID  string
}

type Resolved struct {
    State  string // setup | setup_failed | working | waiting | idle | errored | dead | paused | done
    Detail string // "permission prompt", "rate_limit", "done ✓", "stalled?", …
    Source string // hook | claude | pane | timeout — why we believe it
}
```

Rules, in priority order:

1. `Done` / `Paused` / handed-off → as today.
2. Window gone, or a pane whose command is no longer claude after
   `session_started` → `dead` (no 60s wait needed for the label; the
   `worker_vanished` push keeps its debounce).
3. `setup` with a `setup_failed` event → `setup_failed`. `setup` older
   than `setupTimeout` (10 min) with no claude in the pane →
   `setup_failed` with detail `stuck?`, source `timeout`.
4. A `stop_failed` event newer than the last prompt → `errored` with its
   reason.
5. A session file whose `sessionId` matches the task's → it wins for
   busy/idle/waiting: `busy` → `working`, `waiting` → `waiting` with
   `WaitingFor`, `idle` → the hook's `idle` + sentinel (`idle ✓`,
   `done?`, `stalled?`), or `idle` with detail `interrupted` when the
   last hook said `working` (the Esc case).
6. No session file → today's hook fold, plus the pane-capture fallback.

`Task.Label()` becomes a thin wrapper over `Resolve` for callers that
have no observation, so nothing that reads the label breaks.

**The resolver does not append events.** `events.jsonl` stays a history
of what hooks and verbs reported. The resolved state is a view, computed
per beat and discarded — no new persisted state, no `tasks.json` churn.
The supervise engine feeds the same `Signals` into its existing
liveness rules, so the row, the push and `gv watch` finally agree.

### 2. The session-file read replaces the per-worker capture

On the existing 1s beat, inside `refreshCmd` (`internal/tui/tui.go`):

- One `os.ReadDir(<config dir>/sessions)` per Claude config dir in use
  (normally one; ccwork-profiled workers add theirs).
- For each `*.json` entry, read and decode a 6-field struct; keep it
  only if its pane id matches a worker pane in the snapshot already
  taken this beat. Everything else is dropped on the spot.
- `capture-pane` runs **only** for a worker with no matching session
  file (older Claude Code, a foreign harness, a non-default profile
  whose dir we can't find). With a current Claude Code that's zero
  captures per beat.

Liveness check: the pane id (`%N`) is unique for a tmux server's life
and the snapshot already has `pane_current_command`, so "the file names
this pane and the pane is still running claude" needs no `kill -0`, no
`ps`, and no `procStart` parsing. A stale file from a crash names a pane
that's gone or now running a shell; rule 2 handles it.

**Cost, per beat, N active workers, S sessions:**

| | Today | After |
|---|---|---|
| tmux execs | 2·S + N (`capture-pane` per worker) | 2·S (+ fallbacks only) |
| other syscalls | — | 1 `readdir` + ~16 small reads (~11 KB) |
| bytes allocated per worker | ~30 lines of pane text, kept in `LiveInfo.PaneContent` | one 6-field struct |
| resident state | unchanged | unchanged — nothing outlives the beat |

At 6 workers that's 6 fewer forks per second. A fork+exec of the tmux
client costs far more than reading 16 small files.

**Explicitly rejected, for footprint:**
- An fsnotify watcher on `sessions/`: a goroutine plus fds, resident
  for the cockpit's life. The 1s readdir is cheaper than the watcher it
  would replace.
- `claude agents --json` on a beat: a Claude Code process start per
  second.
- A cache of decoded session files keyed by mtime: a cache the RAM rule
  forbids, to save reading 11 KB.
- Keeping the hysteresis `Memory` on disk: not needed once the session
  file — not the pane — drives waiting.

### 3. Two more hooks, chosen by how rarely they fire

A hook costs one short-lived `gv hook` process per firing. Only events
that fire about once per turn or less, and close a real gap, are added:

| Hook | Event appended | Why | Frequency |
|---|---|---|---|
| `StopFailure` | `stop_failed` {error, details} | API error / rate limit / auth ends a turn without `Stop` | rare |
| `UserPromptSubmit` | `prompt_submitted` | Confirms a `gv answer`/nudge landed; un-sticks `idle` when the operator types into the pane | once per prompt — rare for autonomous workers |

Plus one fold change with no new hook: `notification` with
`notification_type: permission_prompt|elicitation_dialog` sets
`waiting`, and the next `prompt_submitted` or `agent_status` clears it.

**Not added:** `PreToolUse`, `PostToolUse`, `PermissionRequest`. The
first two fire on every tool call — dozens of process spawns per turn,
per worker. The session file's `waiting` + `waitingFor` already covers
what `PermissionRequest` would tell us, without a spawn.

`answered` stays an optimistic flip, but the resolver now corrects it:
no `prompt_submitted` within 15s and a session file still `idle` →
detail `answer not delivered?`.

### 4. Setup becomes a real phase

`gv run-setup` appends `setup_failed` {exit code, last stderr line ≤200
runes} on a non-zero exit. That's one event in a path that already
runs; no polling. Together with rule 3's timeout, a broken setup reads
as `setup_failed` instead of `◌ setup` forever. Liveness keeps skipping
setup rows; the label carries it.

### 5. Surfaces

- **Cockpit:** the row shows `Resolved.State` + `Detail`; the LIVE
  column is removed (it was the unreconciled one). Removing a column
  also removes its per-frame formatting.
- **`gv ls`:** switches to `SnapshotSession` + the same resolver, which
  also retires its leftover per-task `DetectLive` path (several tmux
  execs per task).
- **`gv ls --json`:** additive fields `state`, `state_detail`,
  `state_source`. `agent`, `sentinel`, `liveness` stay exactly as they
  are (plugin contract; `e2e/plugin.sh` is the tripwire).
- **`gv watch`:** new event types `stop_failed`, `prompt_submitted`,
  `setup_failed` are additive. Document them in docs/plugins.md.

## Plan shape (feature train `worker-state`)

0. **Spike — verify the session file before building on it.** In a
   dummy-data sandbox, record `status`/`waitingFor`/`statusUpdatedAt`
   for: a running turn, Esc mid-turn, a permission prompt, an
   AskUserQuestion menu, a clean exit, `kill -9`. Answer: where the dir
   lives under a non-default `CLAUDE_CONFIG_DIR`; whether writes are
   atomic (rename) or can be read half-written; update latency. Land
   the findings in claude-code-facts. **Go/no-go gate:** if Esc doesn't
   reach `idle` or writes can tear, rules 5 and §2 shrink to "session
   file as a hint, capture to confirm", and the rest still ships.
1. `state.Resolve` + table tests, wired through `Task.Label()` with no
   behaviour change (no observation → today's answers).
2. Session-file reader (`internal/claudestate`, pure parse + one
   readdir function) + the cockpit beat switch; capture-pane only as
   fallback. Extend `TestViewAllocsFlatUnderSupervision` and add a test
   that counts execs per refresh drops by N.
3. Hooks: `StopFailure`, `UserPromptSubmit`; fold `notification`;
   `gv doctor` checks they're installed.
4. `setup_failed` from `gv run-setup` + the setup timeout rule.
5. Supervise consumes `Resolve`; `gv ls` moves to snapshot + resolver;
   `--json` fields; LIVE column removed; docs/plugins.md.

Each car is independently shippable and each one lowers or holds the
per-beat cost. Car 2 is the one that actually cuts spawns; cars 1 and 3
are where most of the accuracy comes from.

## Risks

- **The session file isn't a documented interface.** It could change
  shape or move in any Claude Code release. Mitigations: one parser with
  a fixture test; any decode failure falls back to capture-pane for
  that worker, never to a wrong state; `gv doctor` reports "claude
  session state unavailable, using pane fallback". The cost of losing it
  is today's behaviour, not worse.
- **Remote workers.** Each host's own cockpit or `gv supervise` reads
  its own sessions dir; nothing crosses ssh. A host on an old Claude
  Code just uses the fallback.
- **Esc semantics unverified** — the spike's job.

## Open questions

- Should `waiting (permission)` from the session file trigger
  `worker_waiting` immediately, dropping the 10s hysteresis? It's
  Claude's own state, not a scrape, so flapping should be rare; the
  spike's latency numbers decide.
- Is it worth stamping the claude pid on `session_started` (from the
  hook's parent) so the reader opens one file per worker instead of
  reading the dir? Only if the spike shows the dir growing large.
