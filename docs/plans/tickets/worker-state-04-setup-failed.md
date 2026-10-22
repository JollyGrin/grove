# worker-state 04: `setup_failed` from `gv run-setup`

Design: docs/plans/2026-10-19-worker-state-design.md §4. Feature train
`worker-state`. **Depends on 01** (fold case + `SetupFailed` field).

## Scope

- `cmd/gv/main.go` launch string (~1926) passes the ticket to run-setup:
  `gv run-setup <repo> <root> <ticket>`. `cmdRunSetup` (~2398) accepts
  2 or 3 args after the repo so an already-running old launch line still
  works (no ticket → behave exactly as today, append nothing).
- On a non-zero exit of the setup command (not the lock or config
  errors), append `setup_failed` {`exit`, `tail`} to the workspace's
  `events.jsonl` via `state.Append`, where `tail` is the last non-empty
  stderr line, ≤200 runes. Capture it with an `io.MultiWriter` to
  `os.Stderr` plus a fixed-size tail buffer (bounded — never buffer the
  whole output; installs can print megabytes).
- Return the original error so the `&& claude` in the launch string still
  short-circuits as today.
- The resolver's timeout rule (`setupTimeout`) already covers a setup
  that hangs; nothing else here.
- docs/plugins.md: document `setup_failed` (additive); ntfy table →
  high · `warning`, desktop yes.

## Acceptance criteria

- Gate green.
- Test: `setup: "echo boom >&2; exit 3"` with a ticket arg appends one
  `setup_failed` with `exit: "3"`, `tail: "boom"`; `setup: "true"`
  appends nothing; no ticket arg appends nothing; a 10 MB stderr setup
  keeps the tail buffer at its fixed size.
- `e2e/dummy.sh` green, plus a dummy grab with a failing `setup:` that
  shows `setup_failed` in `gv ls --json` (once 05 lands the field; until
  then assert the event in events.jsonl).
- TASKS.md row.
