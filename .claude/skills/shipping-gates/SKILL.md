---
name: shipping-gates
description: Use before running the build/test gate, writing shell assertions or e2e scripts, merging anything that touches the task lifecycle or TUI, adding a verb/flag the orchestrator would use, or handing a change to the operator to try. The piped-gate trap, BSD-safe e2e, the dummy-data pattern, throwaway builds, merge verification.
---

# Shipping gates

How changes in this repo get verified and handed over. War stories:
LEARNINGS.md + `docs/archive/LEARNINGS-*.md` §"Go / CLI" and §"Field notes".

## The gate

```sh
go build ./... && go vet ./... && go test ./...   # must be green
gofmt -l .                                        # must be empty
```

- **Never pipe the gate.** `go test ./... | tail` reports the PIPE's exit
  status, not the tests' — two red runs merged to main that way in one
  evening. Run bare and check `$?`; filter a saved log afterwards.
- The same trap in reverse: `cmd | grep -q` flakes under
  `set -o pipefail` (grep exits at first match, the producer SIGPIPEs).
  E2E assertions capture to a file first, then grep the file.
- **Teardown: `tmux kill-server` returns before the panes are gone.** A
  pane still writing under `$SCRATCH` makes `rm -rf "$SCRATCH"` fail with
  "Directory not empty" after every assertion passed (grove-383). Wait for
  the isolated socket to disappear, then retry the rm once — copy
  `cleanup()` from `e2e/serve.sh` or `e2e/feature.sh`.
- **Write e2e shell for BSD userland too — the operator runs it on a Mac.**
  No GNU-only flags (`touch -d @<epoch>` is GNU; BSD needs
  `-t YYYYMMDDhhmm.SS`), and resolve the scratch root with
  `SCRATCH="$(cd "$(mktemp -d /tmp/…)" && pwd -P)"` — on macOS `/tmp` is a
  symlink, so a tmux pane reports its cwd as `/private/tmp/…` and any
  assertion against a bare `$SCRATCH` path fails on that alone (grove-228:
  `e2e/chat.sh` read green on Linux while half of it had never run on the
  Mac). macOS `script(1)` drops a piped answer that arrives before the
  child prompts (`printf 'y\n' | script …` read as a bare EOF, so "y"
  became "no" — grove-380): hold the answer back,
  `{ sleep 1; printf 'y\n'; sleep 1; } | script -q /dev/null …`.

## E2E: the dummy-data pattern

Anything touching the task lifecycle (grab/ls/hooks/untrack/done) must
pass `e2e/dummy.sh` before merge. It runs the full loop against scratch
everything: scratch `HOME` (config), `GROVE_STATE_DIR` override (state),
and the repo's `claude:` command set to `echo` (worker). Other suites:
`wizard.sh`, `workspace.sh`, `github.sh` (stub `gh`), `cockpit.sh`,
`plugin.sh`, `serve.sh`, `feature.sh` (the feature-train lifecycle), and
more — `ls e2e/`. **`e2e/all.sh` runs every one** (a new suite is not
covered until it is added to all.sh's list) — no CI covers them, so run it
before merging anything that touches the TUI or the task lifecycle
(grove-79: three TUI PRs merged while `cockpit.sh` + `workspace.sh` were
red, because nothing ran them; the panic had shipped in a fourth a day
earlier). Details: docs/seed-manifest.md §Dummy-data E2E. Scripted-tmux
suites must also follow the
[tmux-discipline](../tmux-discipline/SKILL.md) isolation rules —
including capturing panes with `-S -` so a panic keeps its reason line.

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

- **After a merge, the operator's binary is refreshed with
  `gv update --yes` — never `go install ./cmd/gv`.** A push to main
  auto-cuts a release within ~a minute. `go install` stamps the binary
  `dev`, which is precisely what `gv update` refuses (`ErrDevBuild`,
  internal/update/update.go) — every `go install` breaks the next update.
  A binary already stamped `dev` escapes once with
  `gv update --yes --force`, then plain `--yes` forever after.

## Verb surface → the orchestrator seed

A new verb, flag, or lane that an orchestrator would ever be expected to
use is not shipped until `orchestrator/CLAUDE.md` teaches it. The seed is
the only doc every workspace's brain is born from, and a workspace brain
can be perfectly in sync with the seed and still be wrong — sync is not
currency. `orchestrator/seed_test.go` enforces the `--host` verb set and
a few load-bearing phrases mechanically; everything else is on you. Grep
the seed for the flag you just added before you open the PR.

## Merging and cleanup

- Verify merges via `gh pr view --json state,mergedAt`, **never git
  ancestry** — squash-merge breaks ancestry, so `git branch -d` refuses
  every time (use `-D` + remote delete once `gh` confirms the merge).
- Docs go direct to main; code goes through a short-lived branch in a
  worktree (`~/git/.worktrees/grove/<slug>`), commit per plan task.
