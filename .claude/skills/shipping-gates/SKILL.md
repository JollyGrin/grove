---
name: shipping-gates
description: Use before running the build/test gate, writing shell assertions or e2e scripts, merging anything that touches the task lifecycle, or handing a change to the operator for manual testing. Covers the piped-gate trap, the dummy-data e2e pattern, throwaway builds, and merge verification.
---

# Shipping gates

How changes in this repo get verified and handed over. War stories:
[LEARNINGS.md](../../../LEARNINGS.md) §"Go / CLI" and §"Field notes".

## The gate

```sh
go build ./... && go vet ./... && go test ./...   # must be green
gofmt -l .                                        # must be empty
```

- **Never pipe the gate.** `go test ./... | tail` reports the PIPE's exit
  status, not the tests' — two red runs merged to main that way in one
  evening. Run bare and check `$?`; filter a saved log afterwards, never
  inline.
- The same trap in reverse: `cmd | grep -q` flakes under
  `set -o pipefail` (grep exits at first match, the producer SIGPIPEs).
  E2E assertions capture to a file first, then grep the file.
- **Write e2e shell for BSD userland too — the operator runs it on a Mac.**
  No GNU-only flags (`touch -d @<epoch>` is GNU; BSD needs
  `-t YYYYMMDDhhmm.SS`), and resolve the scratch root with
  `SCRATCH="$(cd "$(mktemp -d /tmp/…)" && pwd -P)"` — on macOS `/tmp` is a
  symlink, so a tmux pane reports its cwd as `/private/tmp/…` and any
  assertion against a bare `$SCRATCH` path fails on that alone. Both bit
  `e2e/chat.sh`, which read green on Linux while its whole `chat ls` half
  had never executed on the Mac (grove-228).

## E2E: the dummy-data pattern

Anything touching the task lifecycle (grab/ls/hooks/untrack/done) must
pass `e2e/dummy.sh` before merge. It runs the full loop against scratch
everything: scratch `HOME` (config), `GROVE_STATE_DIR` override (state),
and the repo's `claude:` command set to `echo` (worker). Other suites:
`wizard.sh`, `workspace.sh`, `github.sh` (stub `gh`), `cockpit.sh`,
`plugin.sh`. **`e2e/all.sh` runs all six** — no CI covers them, so run it
before merging anything that touches the TUI or the task lifecycle
(grove-79: three TUI PRs merged while `cockpit.sh` + `workspace.sh` were
red, because nothing ran them; the panic had shipped in a fourth a day
earlier). Details: docs/seed-manifest.md §Dummy-data E2E. Scripted-tmux
suites must also follow the
[tmux-discipline](../tmux-discipline/SKILL.md) isolation rules —
including capturing panes with `-S` so a panic keeps its reason line.

TUI render code: the first frame always renders with ZERO events (they
load async), so any `[:budget]` slice must clamp to `len(data)`, and
render tests sweep small heights with empty models, not just narrow
widths — grove-79's panic lived only at heights the tests never visited.

## Handing a change to the operator

- **Never `go install` from an unmerged branch.** Hooks and live sessions
  reference `~/go/bin/gv` by absolute path; replacing it mid-flight
  changes the behavior of every running worker. The DEFAULT handoff for
  "try it yourself" testing is a throwaway build:

  ```sh
  go build -o /tmp/gv-<ticket> ./cmd/gv
  ```

  Hand over that path. `go install ./cmd/gv` happens from main, after
  merge.

## Merging and cleanup

- Verify merges via `gh pr view --json state,mergedAt`, **never git
  ancestry** — squash-merge breaks ancestry, so `git branch -d` refuses
  every time (use `-D` + remote delete once `gh` confirms the merge).
- Docs go direct to main; code goes through a short-lived branch in a
  worktree (`~/git/.worktrees/grove/<slug>`), commit per plan task,
  `git merge --ff-only` to main.

## Non-negotiables inherited from ovs

- The binary never mutates a task backend's terminal state, and never
  deletes worktrees/branches it didn't create.
- `events.jsonl` is append-only (O_APPEND + flock); `tasks.json` is a
  derived view — never writable state.
- `~/git/thegrid/overstory-tui` is frozen. Never edit it; note backports
  instead.
