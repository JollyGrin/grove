# gv-keys 06: in-process readers + ACCOUNT tab onto `secrets.Resolve` / `secrets.Set`

## Scope

Design §7 steps 1 and 3 (reads), §11's tab rendering. Mechanical
call-site migration, no new behavior beyond what is listed:

- `internal/tui/account.go`: `pasteKeyCmd` (218-241) saves via
  `secrets.Set(ns, varName, key)` — global namespace by default (keys
  are billing accounts) — instead of `openrouter.SaveKey`;
  `accountKeyRows` (87-120) resolves through `secrets.Resolve(label,
  v)` and each row gains a dim namespace tag (`global` / `<label>`) and
  renders the sidecar preview when present, else `set · <type>` (or
  `set`), replacing `openrouter.Mask` on that path; the kimi fuel fetch
  (176) and OpenRouter fetch (193) read via `Resolve`; the connect
  prompt text (483-485) becomes "`gv keys set OPENROUTER_API_KEY`, or p
  to paste". The workspace label reaches the model the way `cfg` does
  (the cockpit already knows its workspace).
- `internal/sub/lane.go:124-131` `Lane.Key` and `cmd/gv/sub.go:135,
  297-303` read via `Resolve`; the error text names `gv keys set`.
- `internal/connections/core.go:96` fix text and `checkSubLane` /
  the worker-key check (176-186) resolve via a `Resolve` func injected
  on `Env` (keep `envFileHasVar` in place until ticket 10 — the legacy
  fallback of ticket 07 needs it).
- `openrouter.Key`/`SaveKey`/`Mask` and `config.SecretsPath` are NOT
  deleted here (ticket 10); they simply lose these callers. `Mask` keeps
  its one remaining use (footer `key …` line, account.go:575-577) until
  10.

## Acceptance criteria

- Gate green; `internal/tui` tests updated, including a render test that
  a row with a preview shows it and a row without shows `set · <type>`;
  a set value never renders (existing "stars only" contract).
- `pasteKeyCmd` test (pbpaste stubbed as today) writes
  `secrets/global/<VAR>.age` and no `.env` line; a `keySavedMsg` still
  fires with the preview/`set` text.
- `gv sub lanes --json` `key_present` true for a name only in the store.
- Doctor `sub:lane` row OK for a name only in the store.
- `grep -rn 'SaveKey(' internal cmd` → only its definition.
- `e2e/cockpit.sh` and `e2e/sub.sh` green.
- TASKS.md row.

## Dependency

Depends on gv-keys-05 merged into `feature/gv-keys-secrets` (needs
`List` entries with `Type`/`Preview`). 03 need not be merged.

## Suggested model

`claude-sonnet-5` — call-site swaps with every target line enumerated;
the only judgment is the row rendering, which §11 spells out.

## Feature-train kickoff (goes verbatim in `--brief`)

FEATURE TRAIN feature/gv-keys-secrets — this ticket does NOT target main.
1. Before ANY work, in your worktree run:
     git fetch origin feature/gv-keys-secrets && git reset --hard origin/feature/gv-keys-secrets
   (safe: your branch has zero commits at this point). Build on that tip.
2. Open your PR with:
     gh pr create --base feature/gv-keys-secrets ...
   NEVER against main. If you must catch up, rebase on origin/feature/gv-keys-secrets, never on main.
3. Read docs/plans/2026-09-27-secure-keys-design.md first. This is ticket gv-keys-06 of the train; its scope is docs/plans/tickets/gv-keys-06-readers-migration.md — build only what it lists.
4. Gate before the PR: go build ./... && go vet ./... && go test ./... green and gofmt -l . empty — run bare, never piped. Also run e2e/cockpit.sh and e2e/sub.sh under the tmux-discipline isolation rules.
