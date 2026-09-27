# gv-keys 01: `internal/secrets` — age identity + namespaced per-secret store

## Scope

New package `internal/secrets`, no CLI, no call sites. Embeds
`filippo.io/age` (v1.3.2 or newer; +`edwards25519`, `hpke`, `nistec`,
`x/crypto` — `x/sys`/`x/term` already present). Implements exactly the
store in the design doc §2: a per-host X25519 identity at
`<StateDir>/age/identity.txt` (0700 dir, 0600 file, `config.StateDir()` —
the GLOBAL state dir, never `StateDirAt`), and the one store at
`~/.config/grove/secrets/<namespace>/` with `recipients.txt` (age's
recipients-file grammar) and one armored `NAME.age` per secret.
Namespaces are `global` or a workspace label. Names validated by the
same pattern as `config.envKeyPattern` (config.go:400). Resolution steps
1–3 only (env → `<label>/` → `global/`); the legacy `.env` fallback is
ticket 07. Surface: `internal/secrets/*.go`, `go.mod`, `go.sum`.

Public API (names are a proposal; keep them small and tested):
`InitIdentity() (recipient string, created bool, err)`, `Recipient()`,
`Set(ns, name string, value []byte)`, `Resolve(label, name string) string`
(`""` = unset), `List() []Entry{Name, Namespace}`, `Remove(ns, name)`,
`Recipients(ns) []string`, `AddRecipient(ns, label, recipient)`,
`Reseal(ns)`, `ExportLines(label string, names []string) (string, error)`
(the `export NAME='v'` lines ticket 03 evals, quoted with the one
`shellQuote`), and a typed `ErrNotRecipient` wrapping
`*age.NoIdentityMatchError` with the friendly message from §1.

## Acceptance criteria

- `go build ./... && go vet ./... && go test ./...` green, `gofmt -l .` empty.
- Table tests, scratch dirs and generated identities only (no fixtures
  with real-looking keys): round trip; a file sealed to two recipients
  decrypts with either and returns `ErrNotRecipient` for a third;
  `Set` rejects `../x`, `FOO-BAR`, `` and a namespace named anything
  but `global` or `[a-z0-9][a-z0-9_-]*`; precedence env > `<label>/` >
  `global/`; `Reseal` after `AddRecipient` makes the new identity decrypt
  and the old one still decrypt; `ExportLines` survives a value with
  `'`, `$`, backtick and newline (assert by `sh -c "eval \"$lines\";
  printf %s \"$NAME\""` in the test); `InitIdentity` is idempotent and
  never overwrites; file modes asserted (0700/0600).
- `Resolve` honors `GROVE_STATE_DIR` for the identity (so e2e lands in
  scratch) and `HOME` for the store — proven by a test that sets both.
- No `state.Append`, no `os/exec`, no network anywhere in the package
  (`grep` in the test, or by review).
- Record the release-binary size delta in the PR body (build before /
  after); add a LEARNINGS.md row only if it exceeds 3 MB.
- TASKS.md row.

## Dependency

None — can start immediately (after the design doc is committed to
`feature/gv-keys-secrets`).

## Suggested model

`claude-opus-5-5` — crypto correctness and the package every later
ticket trusts; the API shape set here is hard to change once 02–08
build on it.

## Feature-train kickoff (goes verbatim in `--brief`)

FEATURE TRAIN feature/gv-keys-secrets — this ticket does NOT target main.
1. Before ANY work, in your worktree run:
     git fetch origin feature/gv-keys-secrets && git reset --hard origin/feature/gv-keys-secrets
   (safe: your branch has zero commits at this point). Build on that tip.
2. Open your PR with:
     gh pr create --base feature/gv-keys-secrets ...
   NEVER against main. If you must catch up, rebase on origin/feature/gv-keys-secrets, never on main.
3. Read docs/plans/2026-09-27-secure-keys-design.md first. This is ticket gv-keys-01 of the train; its scope is docs/plans/tickets/gv-keys-01-secrets-core.md — build only what it lists.
4. Gate before the PR: go build ./... && go vet ./... && go test ./... green and gofmt -l . empty — run bare, never piped.
