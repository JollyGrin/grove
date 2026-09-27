# gv-keys 09: docs + brains — CLAUDE.md, orchestrator seed, config.example.yaml, plugin contract

## Scope

Documentation only; no Go behavior changes except the embedded seed
bytes. Once 03/04/07/08 are on the branch the feature is usable, and
every place that today says `.env` must say the new thing:

- Root `CLAUDE.md`: a short "Secrets (`gv keys`)" subsection under
  "Running the binary": store location, identity location and the
  never-copy rule, `secret:NAME` in profiles, `gv keys exec` for ad-hoc
  use, `push --host` for remotes.
- Orchestrator seed `orchestrator/CLAUDE.md` (embedded via
  `orchestrator/embed.go`): `gv keys ls --json` added to the tools block
  as read-only; a new short section stating `set`, `env`, `push`, `exec`
  are operator-confirmed (design §6, §10) — a chat proposes the exact
  `gv keys exec …` line and never runs it unheard; the cost-analysis and
  unstick duties may READ `ls --json`. Keep the seed's stamp/refresh
  contract intact (`seed_test.go`, `release_test.go`, `gv brains` must
  stay green — read the memory note on the seed stamp before editing).
  Then refresh this repo's own `.grove/orchestrator/CLAUDE.md` the
  sanctioned way (`gv init --only orchestrator-md`, diff the `.new`).
- `config.example.yaml`: the `model_profiles` block shows `secret:NAME`
  in `env:` and a comment that `auth_token_env` names a stored secret;
  the `.env` mention goes.
- `docs/plugins.md`: the additive `gv keys ls --json` envelope row
  (`{schema_version, keys: [{name, namespace, recipients, shadowed,
  type, preview}]}`), names/previews only.
- `LEARNINGS.md`: nothing beyond what 03/07 already appended, unless the
  seed refresh surprised you.
- `HANDOFF.md`/`TASKS.md`: TASKS.md gets the train's summary row.

## Acceptance criteria

- `go test ./...` green (seed tests), `gv brains` reports the seed
  current for this repo after the refresh.
- `grep -rn '\.env' CLAUDE.md orchestrator/CLAUDE.md config.example.yaml
  docs/plugins.md` returns only the ticket-07 migration sentence (or
  nothing).
- `e2e/plugin.sh` green (contract doc and envelope agree).
- `e2e/brains.sh` green.
- Every ticket/PR number mentioned in the new text carries its 3–5 word
  label (brain guardrail).

## Dependency

Depends on gv-keys-08 merged into `feature/gv-keys-secrets` (and
transitively 03/04/07).

## Suggested model

`claude-sonnet-5` — prose and one embedded-file refresh with a tested
contract; the only trap (seed stamp) is documented in memory and guarded
by tests.

## Feature-train kickoff (goes verbatim in `--brief`)

FEATURE TRAIN feature/gv-keys-secrets — this ticket does NOT target main.
1. Before ANY work, in your worktree run:
     git fetch origin feature/gv-keys-secrets && git reset --hard origin/feature/gv-keys-secrets
   (safe: your branch has zero commits at this point). Build on that tip.
2. Open your PR with:
     gh pr create --base feature/gv-keys-secrets ...
   NEVER against main. If you must catch up, rebase on origin/feature/gv-keys-secrets, never on main.
3. Read docs/plans/2026-09-27-secure-keys-design.md first. This is ticket gv-keys-09 of the train; its scope is docs/plans/tickets/gv-keys-09-docs-brain.md — build only what it lists.
4. Gate before the PR: go build ./... && go vet ./... && go test ./... green and gofmt -l . empty — run bare, never piped. Also run e2e/plugin.sh and e2e/brains.sh.
