# gv-keys 07: `gv keys import`, legacy `.env` fallback, doctor migration checks, wizard import step

## Scope

Design §7 steps 2–3 and §3's `keys:legacy-env` row. The migration
verb and the one-release compatibility window:

- `gv keys import [PATH] [--rm] [--reveal N]` — PATH defaults to
  `~/.config/grove/.env`; parses every assignment with the tolerant
  grammar `openrouter.keyFromFile` implements (openrouter.go:65-91:
  `export`/bare, quoted/unquoted, last wins — move that parser into
  `internal/secrets` as `ParseDotenv` so 10 can delete the openrouter
  copy); encrypts each into the `global` namespace; `--reveal N` applies
  `PreviewFor` to every imported name (refusals reported per name, not
  fatal); prints the names; deletes the file only with `--rm`, and
  prints the reminder about a `set -a; . ~/.config/grove/.env` line in
  a shell rc. Existing names in the store are NOT overwritten unless
  `--force`.
- `secrets.Resolve` step 4: legacy `.env` fallback, reading
  `config.SecretsPath()` with `ParseDotenv`. Marked in code as removed
  by ticket 10.
- Doctor (`internal/connections`): `keys:legacy-env` — warn while `.env`
  exists and holds any name not in the store, listing the names and the
  fix `gv keys import --rm`; OK when the file is absent; the row is
  where `gv audit` users get pointed (audit classifies tasks against
  reality and has no config rows — this belongs in doctor, do not add
  it to audit).
- Wizard: a `KindConfirm` step "import ~/.config/grove/.env into the
  encrypted store (the file is kept; delete it with `gv keys import
  --rm` once workers run clean)" shown only when the file exists.

## Acceptance criteria

- Gate green.
- `import` tests with a scratch `.env` covering all four line shapes and
  a `#` comment; `--rm` deletes; without `--rm` the file is untouched
  byte-for-byte; an already-stored name is skipped with a message and
  imported with `--force`.
- `Resolve` test: a name only in `.env` resolves; a name in the store
  AND `.env` resolves from the store.
- Doctor table tests: file present with an unimported name → warn
  naming it; all imported → OK; absent → OK.
- Wizard `Build` test: step present only when the file exists; `--yes`
  imports without deleting.
- `e2e/wizard.sh` green.
- TASKS.md row; LEARNINGS.md: append the successor to the 2026-09-05
  note ("`.env` holds provider API keys only") saying where they live now.

## Dependency

Depends on gv-keys-06 merged into `feature/gv-keys-secrets`.

## Suggested model

`claude-sonnet-5` — a parser move, a loop over it, and doctor/wizard
rows with direct precedents; every rule is enumerated.

## Feature-train kickoff (goes verbatim in `--brief`)

FEATURE TRAIN feature/gv-keys-secrets — this ticket does NOT target main.
1. Before ANY work, in your worktree run:
     git fetch origin feature/gv-keys-secrets && git reset --hard origin/feature/gv-keys-secrets
   (safe: your branch has zero commits at this point). Build on that tip.
2. Open your PR with:
     gh pr create --base feature/gv-keys-secrets ...
   NEVER against main. If you must catch up, rebase on origin/feature/gv-keys-secrets, never on main.
3. Read docs/plans/2026-09-27-secure-keys-design.md first. This is ticket gv-keys-07 of the train; its scope is docs/plans/tickets/gv-keys-07-import-legacy-doctor.md — build only what it lists.
4. Gate before the PR: go build ./... && go vet ./... && go test ./... green and gofmt -l . empty — run bare, never piped. Also run e2e/wizard.sh.
