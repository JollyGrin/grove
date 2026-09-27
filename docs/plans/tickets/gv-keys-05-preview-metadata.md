# gv-keys 05: masked preview + type metadata sidecar (`set --type --reveal`, `ls` columns)

## Scope

Design §11. A plaintext sidecar `NAME.meta.yaml` next to `NAME.age`
holding `type` (free text), `preview`, `reveal`, `set_at` — and nothing
derived from the value except the preview (no length, no hash). In
`internal/secrets`: `Meta` type, `WriteMeta`/`ReadMeta`, `Remove` also
deletes the sidecar, `List` entries carry `Type` and `Preview`, and a
pure `PreviewFor(value []byte, reveal int) (string, error)` implementing
the rule exactly: first `reveal` chars + `***` + last 4; refused (error,
secret still saved without a preview) when `reveal > 16`, `len(value) <
24`, `len(value) − reveal − 4 < 16`, or fewer than 8 distinct bytes.
In `cmd/gv/keys.go`: `set --type "…" --reveal N`; `ls` gains type and
preview columns (`***` when absent); `ls --json` gains `type` and
`preview` fields (additive). `ls` still never decrypts. Pushing sidecars
is ticket 08; the ACCOUNT tab rendering is ticket 06; `import --reveal`
is ticket 07 (it must call `PreviewFor`).

## Acceptance criteria

- Gate green.
- Table tests for `PreviewFor`: a 40-char high-entropy value with
  `reveal 12` → `<12>***<4>`; `reveal 17` refused; a 23-char value
  refused at any reveal; a 40-char value of `aaaa…` refused; `reveal 0`
  or absent → no preview, no error; the refusal error names the rule
  that fired.
- `printf <40 random chars> | gv keys set X --type "Stripe restricted
  key" --reveal 12` writes both files; `gv keys ls` shows the type and
  preview; `--json` carries both; `gv keys rm X` removes both files.
- A `set` whose `--reveal` is refused still stores the secret, prints
  the reason, exits 0, and `ls` shows `***`.
- Sidecar round-trips through yaml.v3 with unknown fields ignored.
- A unit test asserts the sidecar never contains the full value for a
  value that passes the rule (string-search the file).
- TASKS.md row.

## Dependency

Depends on gv-keys-02 merged into `feature/gv-keys-secrets`. Can run in
parallel with 03 and 04.

## Suggested model

`claude-sonnet-5` — the rule is fully enumerated above and executable;
mostly a small pure function plus flag/column plumbing with precedents.

## Feature-train kickoff (goes verbatim in `--brief`)

FEATURE TRAIN feature/gv-keys-secrets — this ticket does NOT target main.
1. Before ANY work, in your worktree run:
     git fetch origin feature/gv-keys-secrets && git reset --hard origin/feature/gv-keys-secrets
   (safe: your branch has zero commits at this point). Build on that tip.
2. Open your PR with:
     gh pr create --base feature/gv-keys-secrets ...
   NEVER against main. If you must catch up, rebase on origin/feature/gv-keys-secrets, never on main.
3. Read docs/plans/2026-09-27-secure-keys-design.md first. This is ticket gv-keys-05 of the train; its scope is docs/plans/tickets/gv-keys-05-preview-metadata.md — build only what it lists.
4. Gate before the PR: go build ./... && go vet ./... && go test ./... green and gofmt -l . empty — run bare, never piped.
