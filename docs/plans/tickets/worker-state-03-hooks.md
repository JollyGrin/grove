# worker-state 03: install `StopFailure` + `UserPromptSubmit` hooks

Design: docs/plans/2026-10-19-worker-state-design.md §3. Feature train
`worker-state`. **Depends on 01** (fold cases and Task fields exist).

## Scope

- `internal/hooks/hooks.go` `hookEvents` (~335): add
  `"StopFailure": "stop-failure"` and
  `"UserPromptSubmit": "prompt-submit"`.
- Handlers, same attribution path as `Stop` (cwd → task via
  `ReadTasks`, `foreignSession` gate, subagent payloads — `agent_id`
  set — dropped):
  - stop-failure → append `stop_failed` {`error`, `details` ≤200 runes}.
  - prompt-submit → append `prompt_submitted` {} — **no prompt text**
    (it can be large and is not ours to log).
- Do **not** add PreToolUse, PostToolUse or PermissionRequest (design
  §3: they fire per tool call; each firing is a `gv` process spawn).
- `internal/connections/connections.go` `checkHooksAt` (333): the
  hard-coded `wired == 4` / `%d/4` becomes `len(hookEvents)`.
- `gv hooks install` preserves other tools' entries in the shared
  settings file exactly as today; re-running it adds the two new events.
- docs/plugins.md §"React: `gv watch`": document `stop_failed` and
  `prompt_submitted` (additive); ntfy table: `stop_failed` → high ·
  `warning`, desktop yes; `prompt_submitted` → none.

## Acceptance criteria

- Gate green.
- hooks tests: a StopFailure payload (shape from the claude-code-facts
  skill / hooks docs, `error: "rate_limit"`) appends `stop_failed` with
  the reason; a UserPromptSubmit appends `prompt_submitted` with no
  text; a subagent payload appends nothing; a foreign session appends
  nothing.
- Install test: an existing settings.json with a foreign hook keeps it;
  all six grove events present after install.
- Doctor row shows `6/6` / OK on a fresh install.
- `e2e/dummy.sh` and `e2e/plugin.sh` green (hook + contract paths).
- claude-code-facts skill: `StopFailure` row updated from "not installed
  by grove" to installed.
- TASKS.md row.
