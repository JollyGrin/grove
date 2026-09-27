# gv-keys 02: `gv keys` verbs — init / recipient / set / ls / rm, doctor rows, wizard step

## Scope

The operator-facing CLI over ticket 01, design §3 and §6. New
`cmd/gv/keys.go` with `case "keys"` dispatched from `main.go` (mirror
`cmdHooks`, main.go:490 / 3960-3980) — thin glue, all logic in
`internal/secrets`. Verbs: `init` (prints the recipient and the "losing
this identity loses this host's access, not the secrets" line),
`recipient`, `set NAME [--global | --workspace [L]]` (value from stdin
when piped, else `x/term.ReadPassword` hidden prompt; **never an argv
value**; one trailing newline stripped; default namespace = ambient
workspace label via `workspace.Find`, else `global`), `ls [--json]`
(name · namespace · recipient count, shadowed global rows marked; the
`--json` envelope `{schema_version, keys: [{name, namespace,
recipients, shadowed}]}` via the existing `emitJSON`), `rm`. Config:
`parse` rejects `workspace.label: global` (reserved namespace). Doctor
(`internal/connections`): rows `keys:identity` (present, 0600/0700) and
`keys:unresolved` (a profile's `auth_token_env` that no namespace nor
the process env holds — warn severity, fix line `gv keys set <NAME>`).
Wizard (`wizard.Build`, wizard.go:99-207): a `KindConfirm` step
"generate this host's age identity for encrypted secrets", kept in the
parent-scope subset, on = `secrets.InitIdentity`. `--type`/`--reveal`
on `set` are ticket 05; `env`/`exec`/`push` are 03/04/08.

## Acceptance criteria

- Gate green (build/vet/test bare, gofmt empty).
- `gv keys set X VALUE` (positional value) is a usage error naming
  stdin/prompt. `printf v | gv keys set X` from a workspace lands in
  `secrets/<label>/X.age`; `--global` lands in `secrets/global/`.
- `gv keys ls` and `ls --json` never contain a value — a unit test sets a
  value `ZZZSENTINEL` and asserts it is absent from both outputs.
- `gv keys recipient` prints one `age1…` line and nothing else; no verb
  prints the identity (grep the package for `identity.String()` → only
  the file write in ticket 01).
- `parse` test: `workspace: {label: global}` is a load error.
- Doctor: table tests for both rows (`Env` injected, as core.go does).
- Wizard: `Build` test shows the step; `--yes` generates an identity in a
  scratch `GROVE_STATE_DIR`; a second run is a no-op.
- `e2e/wizard.sh` still green (run it; it exercises `Build`).
- Help text lists the verbs; `docs/plugins.md` NOT yet touched (ticket 09).
- TASKS.md row.

## Dependency

Depends on gv-keys-01 merged into `feature/gv-keys-secrets`.

## Suggested model

`claude-sonnet-5` — glue over a tested package; the shapes (flags,
envelope, doctor rows, wizard step) are fully specified above and each
has a precedent in the codebase to copy.

## Feature-train kickoff (goes verbatim in `--brief`)

FEATURE TRAIN feature/gv-keys-secrets — this ticket does NOT target main.
1. Before ANY work, in your worktree run:
     git fetch origin feature/gv-keys-secrets && git reset --hard origin/feature/gv-keys-secrets
   (safe: your branch has zero commits at this point). Build on that tip.
2. Open your PR with:
     gh pr create --base feature/gv-keys-secrets ...
   NEVER against main. If you must catch up, rebase on origin/feature/gv-keys-secrets, never on main.
3. Read docs/plans/2026-09-27-secure-keys-design.md first. This is ticket gv-keys-02 of the train; its scope is docs/plans/tickets/gv-keys-02-keys-cli.md — build only what it lists.
4. Gate before the PR: go build ./... && go vet ./... && go test ./... green and gofmt -l . empty — run bare, never piped.
