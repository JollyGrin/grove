# gv-keys 03: launch path — `secret:NAME` references, `WrapProfile` source, `gv keys env`

## Scope

Design §4, the one place a value reaches a worker. Three edits and one
internal verb:

1. `internal/config`: an `env:` value of the form `secret:NAME` is a
   reference; `parse` validates `NAME` against `envKeyPattern`
   (extend the loop at config.go:280-289). `literal:` prefix escapes.
2. `config.WrapProfile(cmd, p, source string)` replaces the
   `secretsPath` parameter with the shell snippet that populates the
   environment. A `secret:NAME` value renders `K="$NAME"` (bare
   expansion, like `ANTHROPIC_AUTH_TOKEN="$%s"` at config.go:478)
   instead of `shellQuote(literal)` at config.go:474. Literal values and
   the six built-ins are byte-identical to today. `p == nil` unchanged.
3. `cmd/gv`: one helper builds the source snippet
   `eval "$(<os.Executable()> keys env --profile <name>)"` and the four
   call sites pass it (main.go:1055 orchestrator wrap, 1702 grab,
   3376/3379 adopt fresh + resume). The absolute path follows the
   `run-setup` precedent on the same line (main.go:1704).
4. `gv keys env --profile P` (internal; listed next to `run-setup` in
   help): resolves `{p.AuthTokenEnv} ∪ {NAME : secret:NAME ∈ p.Env}` for
   the ambient workspace label, prints `secrets.ExportLines`, **refuses
   when stdout is a terminal**, and on a missing name exits non-zero
   naming the secret, the namespaces searched, and `gv keys set NAME` /
   `gv keys push --host` — so a profiled launch fails *before* `exec`
   and drops to a shell, never silently to the operator's Claude sub.

Plus `e2e/keys.sh` (grab half only; the fake-ssh half is ticket 08),
added to `e2e/all.sh`.

## Acceptance criteria

- Gate green; existing `WrapProfile` tests updated so the ONLY diff in
  their goldens is the source token (`. '<path>'` → `eval "$(…)"`); the
  2026-08-31 "compose bare, wrap LAST" test (`--resume <id> )`) still
  passes untouched.
- `parse` tests: `secret:OPENROUTER_API_KEY` accepted; `secret:not a
  name` is a load error naming the profile and key; `literal:secret:x`
  renders the literal `secret:x`.
- `gv keys env` on a TTY exits non-zero with the refusal message; piped,
  it prints exactly the referenced names and no others (a profile with
  two refs + one literal → two `export` lines).
- `e2e/keys.sh` (dummy-data pattern per `e2e/dummy.sh:1-80`: scratch
  HOME + `GROVE_STATE_DIR` + isolated tmux server + live-state canaries):
  `gv keys init`; `printf hunter2 | gv keys set DUMMY_KEY` inside the
  scratch workspace; profile `p` with `auth_token_env: DUMMY_KEY`,
  `env: {DUMMY_EXTRA: "secret:DUMMY_KEY", LIT: "plain"}`; repo `claude:`
  = a scratch script writing `"$ANTHROPIC_AUTH_TOKEN|$DUMMY_EXTRA|$LIT"`
  to `$SCRATCH/seen`. After `gv grab task-001 --repo dummy --profile p`:
  `seen` == `hunter2|hunter2|plain`; `hunter2` is ABSENT from the worker
  pane (`capture-pane -S -`), every file under `$GROVE_STATE_DIR`, the
  kickoff prompt file, `gv ls --json`, the scratch `HISTFILE`, and
  `$HOME/.config/grove` (ciphertext only). Capture to files, then grep.
- A grab with a profile referencing an unset name leaves the pane at a
  shell with the naming error visible (assert on the capture) and no
  claude process.
- `e2e/dummy.sh` still green (unprofiled launches are byte-identical).
- TASKS.md row; LEARNINGS.md row if the wrap surprised you.

## Dependency

Depends on gv-keys-02 merged into `feature/gv-keys-secrets`.

## Suggested model

`claude-opus-5-5` — this edits the launch line whose exact bytes are
pinned by tests and by one production incident (grove-217), and it is
where a quoting mistake becomes command injection inside every worker.

## Feature-train kickoff (goes verbatim in `--brief`)

FEATURE TRAIN feature/gv-keys-secrets — this ticket does NOT target main.
1. Before ANY work, in your worktree run:
     git fetch origin feature/gv-keys-secrets && git reset --hard origin/feature/gv-keys-secrets
   (safe: your branch has zero commits at this point). Build on that tip.
2. Open your PR with:
     gh pr create --base feature/gv-keys-secrets ...
   NEVER against main. If you must catch up, rebase on origin/feature/gv-keys-secrets, never on main.
3. Read docs/plans/2026-09-27-secure-keys-design.md first. This is ticket gv-keys-03 of the train; its scope is docs/plans/tickets/gv-keys-03-launch-path.md — build only what it lists.
4. Gate before the PR: go build ./... && go vet ./... && go test ./... green and gofmt -l . empty — run bare, never piped. Also run e2e/keys.sh and e2e/dummy.sh under the tmux-discipline isolation rules.
