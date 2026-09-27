# gv-keys 04: `gv keys exec NAME[,NAME…] -- <cmd>` — use a key without holding it

## Scope

Design §10. One verb in `cmd/gv/keys.go` backed by a small
`internal/secrets` helper `ExecEnv(label string, names []string, base
[]string) ([]string, error)` that returns a copy of the environment with
exactly the named secrets added. The verb resolves the ambient workspace
label, calls `ExecEnv`, then `syscall.Exec`s the command (argv as a
slice — no shell, no quoting, no `eval`; `exec.LookPath` for the
binary). stdout/stderr are the child's own descriptors. A missing name
fails before exec naming it and the namespaces searched. No TTY refusal
(the verb never prints a value). Help text carries the §10 wrong/right
`sh -c '…'` example, and the verb warns to stderr — without refusing —
when any argument after `--` is the empty string (the signature of a
`$NAME` expanded by the caller's shell). Not relayed over `--host`.
Orchestrator brain wording is ticket 09; this ticket only adds the
verb's own help line.

## Acceptance criteria

- Gate green.
- Unit: `ExecEnv` adds only the named secrets, inherits the rest of
  `base`, overrides an existing same-name var, and errors on a missing
  name without touching the others; a name list with a duplicate is
  deduplicated; an invalid name is rejected before any decrypt.
- `gv keys exec A -- sh -c 'printf %s "$A"'` prints the value; `gv keys
  exec A -- printenv B` prints nothing about A; `gv keys exec A -- true`
  exits 0 and `-- false` exits 1 (exit code is the child's).
- `gv keys exec A -- sh -c 'echo ""'` — no warning (empty arg is inside
  the child's script). `gv keys exec A -- curl -H ""` — stderr warning
  with the right-shape example, child still runs.
- `gv keys exec` with no `--` is a usage error; with an unknown name it
  exits non-zero before running anything (assert a `touch` after `--`
  did not fire).
- `e2e/keys.sh` gains the §9 line: `gv keys exec DUMMY_KEY -- sh -c
  'printf %s "$DUMMY_KEY" > seen-exec'` → `hunter2`, and the existing
  state/pane/`gv ls --json` greps still find nothing.
- TASKS.md row.

## Dependency

Depends on gv-keys-02 merged into `feature/gv-keys-secrets`. Can run in
parallel with 03 and 05 (different files: `keys.go` verb + one new
`secrets` helper; coordinate the `keys.go` dispatch switch on rebase).

## Suggested model

`claude-opus-5-5` — a new security-relevant primitive (env assembly +
exec) with a subtle UX trap (caller-shell expansion) whose error message
is the feature's most-read line.

## Feature-train kickoff (goes verbatim in `--brief`)

FEATURE TRAIN feature/gv-keys-secrets — this ticket does NOT target main.
1. Before ANY work, in your worktree run:
     git fetch origin feature/gv-keys-secrets && git reset --hard origin/feature/gv-keys-secrets
   (safe: your branch has zero commits at this point). Build on that tip.
2. Open your PR with:
     gh pr create --base feature/gv-keys-secrets ...
   NEVER against main. If you must catch up, rebase on origin/feature/gv-keys-secrets, never on main.
3. Read docs/plans/2026-09-27-secure-keys-design.md first. This is ticket gv-keys-04 of the train; its scope is docs/plans/tickets/gv-keys-04-keys-exec.md — build only what it lists.
4. Gate before the PR: go build ./... && go vet ./... && go test ./... green and gofmt -l . empty — run bare, never piped. Also run e2e/keys.sh under the tmux-discipline isolation rules.
