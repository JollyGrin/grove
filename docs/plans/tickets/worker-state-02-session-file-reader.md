# worker-state 02: read Claude's session file on the beat; capture-pane only as fallback

Design: docs/plans/2026-10-19-worker-state-design.md §2. Feature train
`worker-state`. **Depends on 00 (GO or GO-WITH-FALLBACK) and 01.**

## Scope

- New package `internal/claudestate`:
  - `Parse([]byte) (state.ClaudeStatus, pane string, err)` — decodes
    only `status`, `waitingFor`, `statusUpdatedAt`, `sessionId`, `tmux`
    (pane id = the `%N` after the last `.`); unknown fields ignored.
  - `Read(dirs []string, want map[string]bool) map[string]state.ClaudeStatus`
    — one `os.ReadDir` per dir, reads `*.json` only (skip `*.key`),
    returns entries whose pane id is in `want`. Decode failure on a file
    = that file is skipped, never an error for the batch.
  - Dirs: `~/.claude/sessions` plus `<claude_config_dir>/sessions` for
    each configured profile (whatever 00 found for
    `CLAUDE_CONFIG_DIR`).
- `internal/tmux`: expose the worker pane id from the existing snapshot
  (`snapPane.id`; `ClaudePane` at `internal/detect/live.go:146` already
  finds the pane) — **no new tmux exec**.
- `internal/tui/tui.go` `refreshCmd` / `liveStates` (~437-505): build
  the wanted pane-id set from the snapshot, call `claudestate.Read`
  once, and put the result on the per-task info. Call
  `detect.DetectLiveFrom`'s capture **only** for a task with no matching
  session file (the fallback). When a file matches, `LiveInfo.PaneContent`
  stays empty.
- Rows render `Resolve(t, signals, now)`; keep the LIVE column for now
  (05 removes it).
- If 00 said GO-WITH-FALLBACK: a file reading `idle` while the last hook
  said `working` still triggers one capture to confirm; otherwise none.

**Memory rule:** no goroutine, no fsnotify, no cache across beats —
the map from `Read` lives for one refresh and is dropped. No call to
`claude agents --json` anywhere on a beat.

## Acceptance criteria

- Gate green.
- `internal/claudestate` tests against ticket 00's fixture plus: torn
  JSON skipped, `.key` files ignored, pane not wanted → not returned,
  missing dir → empty map, no error.
- A refresh test with injected exec counter (pattern: the `snapFor` /
  `detectFrom` injection `liveStates` already uses) proving: N workers
  all with session files → **0** capture-pane execs; one without → 1.
- `TestViewAllocsFlatUnderSupervision` (`internal/tui/supervise_test.go:243`)
  still passes with its current bound.
- `e2e/all.sh` green (touches the TUI) — note the known macOS
  `/private` red-base failures if they appear, and confirm they also
  fail on main.
- TASKS.md row.
