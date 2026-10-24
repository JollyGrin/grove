# worker-state 05: supervise, `gv ls` and `--json` on the resolver; drop LIVE

Design: docs/plans/2026-10-19-worker-state-design.md §5. Feature train
`worker-state`. **Depends on 02, 03, 04.**

## Scope

- `internal/supervise/transitions.go`: `Observation` (31) gains
  `Signals state.Signals`; liveness rules read it. A session file saying
  `waiting` is Claude's own state: decide in this ticket whether
  `worker_waiting` keeps the 10s hysteresis (design open question —
  use 00's latency numbers; write the decision in the PR body).
  `worker_vanished` keeps its debounce. Supervise memory stays
  in-process, never persisted.
- `cmd/gv/supervise.go`: build Signals the same way the cockpit does
  (`claudestate.Read` once per pass, capture fallback only).
- `cmd/gv/main.go` `gv ls` (~2461-2555): replace the per-task
  `detect.DetectLive` + `ResolveWindowName` (~2542) with one
  `SnapshotSession` per session + `claudestate.Read` + `Resolve`.
- `gv ls --json`: add `state`, `state_detail`, `state_source` per task.
  `agent`, `sentinel`, `liveness` unchanged (additive-only contract;
  `e2e/plugin.sh` is the tripwire).
- Cockpit: remove the LIVE column (`internal/tui/view.go:186` header and
  its cell); the label column shows `Resolved.State` + `Detail`.
- Docs: docs/plugins.md (`--json` fields), CLAUDE.md layout paragraph
  only if it mentions LIVE, the orchestrator seed's `gv ls --json`
  description if it lists fields.

## Acceptance criteria

- Gate green; `e2e/all.sh` green (TUI + lifecycle) apart from the known
  macOS `/private` red-base failures, confirmed also red on main.
- `gv ls` exec-count test: per-task tmux execs gone (snapshot per
  session only, capture only for workers without a session file).
- `gv ls --json` golden test: old fields byte-identical, three new
  fields present.
- Supervise test: a session file `waiting` + `waitingFor: "permission
  prompt"` produces `worker_waiting` per the decided hysteresis; pane
  gone produces `worker_vanished` after its debounce as today.
- View alloc test still flat.
- TASKS.md row; LEARNINGS.md entry summarising before/after execs per
  beat with the measured numbers.
