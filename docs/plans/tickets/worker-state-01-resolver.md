# worker-state 01: `state.Resolve` — one pure resolver behind `Task.Label()`

Design: docs/plans/2026-10-19-worker-state-design.md §1. Feature train
`worker-state`. Independent of 00 (the session-file input is just a
nil-able struct here).

## Scope

`internal/state` only, plus call-site-neutral wiring:

- New `internal/state/resolve.go`: `Resolve(t *Task, s Signals, now
  time.Time) Resolved` and the `Signals`, `ClaudeStatus`, `Resolved`
  types exactly as the design's §1 shows. Named `Signals` because
  `supervise.Observation` (`internal/supervise/transitions.go:31`)
  already exists and will carry one in ticket 05.
- Implement design rules 1–6 in that priority order. Rules that depend
  on events added by later tickets (`stop_failed`, `prompt_submitted`,
  `setup_failed`) read new `Task` fields set by `fold` — add the fields
  (`StopFailedAt`, `StopError`, `PromptAt`, `SetupFailed`, …) and their
  fold cases in `internal/state/state.go` `fold` now, so 03/04 only
  have to append the events. Event-type constants go next to
  `EvNotification` (state.go:26).
- Fold `notification`: `notification_type` `permission_prompt` or
  `elicitation_dialog` → a `WaitingSince` field; cleared by the next
  `agent_status` or `prompt_submitted`. (The hook already writes the
  event; check its data carries `notification_type` — add it in
  `internal/hooks/hooks.go` if not.)
- `Task.Label()` (state.go:533) becomes `Resolve(t, Signals{}, now)`
  rendered to today's strings: **with empty Signals every existing
  label is byte-identical** to today.
- Constants: `setupTimeout = 10 * time.Minute`,
  `answerDeliveryGrace = 15 * time.Second`.

**Memory rule** (`cockpit-ram-reserved-for-workers`): `Resolve` does no
I/O, reads no clock (takes `now`), keeps no state, and allocates only
its return value. New `Task` fields are scalars/short strings; the
`stop_failed` details are capped (≤200 runes) the way the Stop message
is (hooks.go 2000-rune cap).

## Acceptance criteria

- Gate green: `go build ./... && go vet ./... && go test ./...`,
  `gofmt -l .` empty.
- Table test in `internal/state/resolve_test.go` covering every rule,
  including: window gone → dead; pane command not claude after
  session_started → dead; setup + setup_failed → setup_failed; setup
  older than 10 min with no claude → setup_failed/`stuck?`; stop_failed
  newer than last prompt → errored with reason; session file busy →
  working; waiting → waiting + WaitingFor; idle after hook `working` →
  idle/`interrupted`; idle + done sentinel → `idle ✓`; nil ClaudeStatus
  → today's fold answer; answered with no prompt_submitted after 15s +
  file idle → `answer not delivered?`.
- Existing `Label()` tests pass unmodified.
- `testing.AllocsPerRun` on `Resolve` ≤ 1.
- TASKS.md row via `scripts/log-append.py tasks`.
