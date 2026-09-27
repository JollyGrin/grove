# gv-keys 08: `gv keys push --host H` / `receive` / `reseal` — ciphertext to a remote over existing ssh

## Scope

Design §5. No new transport: `internal/remote` gains `keys` in
`Supported` (remote.go:29-33) gated to the `recipient` and `receive`
sub-verbs exactly the way `orchestrator new` / `chat close` are gated,
and an exported `RunWithInput(cfg, host, verb, args, stdin io.Reader,
stdout, stderr)` — a one-line wrapper over the unexported `run`
(remote.go:210). Verbs in `cmd/gv/keys.go`, logic in a new
`internal/secrets/push.go`:

- `reseal [--global | --workspace [L]]` — re-encrypt every `NAME.age`
  in a namespace to its current `recipients.txt` (ticket 01's `Reseal`,
  exposed).
- `push --host H [--workspace L]` — (1) `remote.Run(cfg, H, "keys",
  ["recipient"])` → `age1…`; an "unknown command" from an old remote gv
  becomes "H's gv predates gv keys — run `gv update --yes` there"; (2)
  `AddRecipient(ns, "host: H", key)` if absent, then `Reseal(ns)`; (3)
  build the payload `{"namespace", "recipients", "secrets": {NAME:
  armored}, "meta": {NAME: sidecar}}` and `RunWithInput(cfg, H, "keys",
  ["receive"], payload)`; (4) print `✓ N secrets → H (age1…xxxx,
  namespace <ns>)`. Never automatic — `set` only prints a hint when
  other recipients exist.
- `receive` (internal) — reads the payload from stdin, validates every
  name and the namespace, writes `~/.config/grove/secrets/<ns>/` files
  atomically (temp + rename, 0700/0600), all-or-nothing, prints the
  count.
- Doctor: `keys:host:<H>` row beside the remote-host probe
  (connections.go:249-266) running `<gv> keys ls --json` over the same
  BatchMode ssh and reporting locally-stored names the host lacks.
- `e2e/keys.sh` fake-ssh half.

## Acceptance criteria

- Gate green.
- Unit: payload encode/decode round trip; `receive` with one invalid
  name writes nothing; `receive` is idempotent (second run, same bytes,
  no mtime-based diff needed); `Reseal` after `AddRecipient` decrypts
  with both identities; the old-gv error mapping; `Supported` gating
  refuses `gv keys set --host H` with the friendly shape.
- `e2e/keys.sh`: a fake `ssh` on PATH (copy the `e2e/handoff.sh:48-91`
  pattern) that re-runs the command with `GROVE_STATE_DIR=$SCRATCH/
  state-remote HOME=$SCRATCH/home-remote`; the remote side runs
  `gv keys init` first; `gv keys push --host pc` succeeds; a grab run
  under the remote env (same profile `p`) writes `hunter2|hunter2|plain`
  to its own `seen`; a THIRD scratch identity cannot decrypt the pushed
  file (`ErrNotRecipient`); the payload passed through the fake ssh
  (captured to a file) does not contain `hunter2`. Capture then grep.
- `e2e/handoff.sh` and `e2e/relay.sh` still green (the `Supported`
  edit touches their dispatcher).
- TASKS.md row.

## Dependency

Depends on gv-keys-05 merged into `feature/gv-keys-secrets` (sidecars
ride along). 03/04/06/07 need not be merged; rebase on the feature
branch as usual.

## Suggested model

`claude-opus-5-5` — new protocol across hosts (recipient fetch, reseal,
atomic receive) plus edits to the relay dispatcher two e2e suites guard.

## Feature-train kickoff (goes verbatim in `--brief`)

FEATURE TRAIN feature/gv-keys-secrets — this ticket does NOT target main.
1. Before ANY work, in your worktree run:
     git fetch origin feature/gv-keys-secrets && git reset --hard origin/feature/gv-keys-secrets
   (safe: your branch has zero commits at this point). Build on that tip.
2. Open your PR with:
     gh pr create --base feature/gv-keys-secrets ...
   NEVER against main. If you must catch up, rebase on origin/feature/gv-keys-secrets, never on main.
3. Read docs/plans/2026-09-27-secure-keys-design.md first. This is ticket gv-keys-08 of the train; its scope is docs/plans/tickets/gv-keys-08-push-remote.md — build only what it lists.
4. Gate before the PR: go build ./... && go vet ./... && go test ./... green and gofmt -l . empty — run bare, never piped. Also run e2e/keys.sh, e2e/handoff.sh and e2e/relay.sh under the tmux-discipline isolation rules.
