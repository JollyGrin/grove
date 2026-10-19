# worker-state 00: spike — verify Claude Code's session status file

Design: docs/plans/2026-10-19-worker-state-design.md (§"What other
harnesses do", Plan shape step 0). Feature train `worker-state`.

## Goal

Before anything is built on `<config dir>/sessions/<pid>.json`, find out
how it actually behaves. This is a research ticket: **no production
code changes**. Output is facts in the claude-code-facts skill plus a
go/no-go line.

## Scope

In a dummy-data sandbox (scratch `HOME`, a throwaway tmux socket per
the tmux-discipline skill — never the operator's server), run a real
interactive `claude` and record the file's `status`, `waitingFor`,
`statusUpdatedAt` and mtime for each scenario:

1. A running turn (long tool call), then its normal end.
2. Esc mid-turn (the interrupt case — today grove sees nothing).
3. A permission prompt (Bash in default permission mode).
4. An AskUserQuestion menu.
5. Clean exit (`/exit`) — is the file removed?
6. `kill -9` of the claude pid — is the file left behind?
7. A non-default `CLAUDE_CONFIG_DIR` — where does `sessions/` live?
8. Write atomicity: is the file replaced by rename, or written in place
   (could a 1s reader see a torn/partial JSON)? Check with `fs_usage`
   / `dtruss`-free means if needed (e.g. read it in a tight loop for a
   minute during busy turns and count decode failures).
9. Update latency: busy→idle lag after a turn ends.

Also confirm the `tmux` field always carries the `%pane` id, and what it
holds when claude runs outside tmux.

## Acceptance criteria

- `.claude/skills/claude-code-facts/SKILL.md` gains a "Session status
  file" section with every finding above, each marked verified + the
  Claude Code version tested.
- A fixture file `internal/claudestate/testdata/session-<version>.json`
  (a real file, sessionId/cwd scrubbed) is committed for ticket 02.
- The PR body ends with one line: `GO`, `GO-WITH-FALLBACK` (Esc doesn't
  reach idle, or writes can tear — the resolver treats the file as a
  hint and captures to confirm), or `NO-GO`, with the reason.
- `scripts/log-append.py learnings` entry for anything surprising.
