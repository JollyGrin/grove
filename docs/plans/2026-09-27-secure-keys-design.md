# `gv keys` — encrypted, per-workspace, host-portable secrets

**Status:** DRAFT 2026-09-27, revised the same day after operator review
(single store with namespaces, `gv keys exec`, masked previews, feature
train — Decisions 2, 10, 11, 12). Not yet design-reviewed.
**Goal:** replace the plaintext `~/.config/grove/.env` with an age-encrypted
store that a worker can *use* without the value ever passing through
anything an agent reads or writes, that scopes secrets per workspace the
way the rest of grove's config does — without a single byte of ciphertext
ever landing in a target repo — and that ships to a `hosts:` remote with
one explicit command over the ssh plumbing that already exists.

## The problem

Every model-profile credential grove has today lives in one plaintext
file, `~/.config/grove/.env` (`config.SecretsPath`, config.go:385-390),
written by the cockpit's ACCOUNT tab (`openrouter.SaveKey`,
openrouter.go:94-118, mode 0600) and read four ways:

| Reader | Where | How |
|---|---|---|
| worker / orchestrator launch | `config.WrapProfile`, config.go:461-485 | the pane's shell **sources the file** (`. <path> && export … && exec claude`) |
| ACCOUNT tab key rows | account.go:161-166 | `openrouter.Key` (env first, then a tolerant dotenv parse) |
| `gv sub` lanes | sub/lane.go:124-131, cmd/gv/sub.go:135, 297-303 | same |
| `gv doctor` | connections/core.go:181-186 | same grammar, re-implemented (`envFileHasVar`) |

The grove-36 design (docs/plans/2026-07-08-openrouter-model-profiles-design.md
§2.1) already did the hard part right: the launch line sent through
`tmux send-keys` carries only `ANTHROPIC_AUTH_TOKEN="$OPENROUTER_API_KEY"` —
a bare `$NAME`, so the value is never in argv, `capture-pane`, or shell
history. That invariant is preserved here, not reinvented.

What is wrong is everything around it:

1. **At rest it is plaintext**, in the directory the operator copies most
   (there are nine `config.yaml.bak-*` siblings next to it today). A
   `scp -r ~/.config/grove` to a new box, a dotfiles sync, an agent that
   greps `~/.config` — each is a full disclosure.
2. **The `env:` map is a second, leaky path.** `WrapProfile` renders every
   `env:` value as a shell-quoted *literal* into the send-keys line
   (config.go:474: `extra.WriteString(k + "=" + shellQuote(p.Env[k]) + " ")`).
   Anything an operator puts there — a Kimi key, a Muse Spark lane token —
   lands in the pane scrollback, the pane shell's history, and in
   `config.yaml` itself. Nothing stops them: `auth_token_env` is
   documented as "the VAR NAME, never the key" (config.go:36) but `env:`
   has no such rule.
3. **Per-workspace does not exist.** `SecretsPath()` is `Dir()/.env`, global
   only. A workspace that needs its own lane token (the 2026-09-09 contrib
   lane, declared per-workspace on the Mac precisely so other workspaces
   cannot inherit it) has nowhere to put the key except the global file.
4. **Remote hosts get nothing.** `gv grab --host H` relays the verb over
   ssh (remote.Argv, remote.go:166-173) and H runs its own grab against its
   own `.env`. Keeping the two files in sync is a manual `scp` of plaintext
   the operator has to remember.

## The invariant

> A secret value exists in exactly two places: encrypted on disk, and in
> the process environment of the child that was spawned to use it.

Concretely it must never appear in: `events.jsonl` or `tasks.json`
(state.go:141-181 — the Task struct has no command or env field, and the
grab event's data map at main.go:1711-1719 carries names only, which stays
true), any `--json` output, the kickoff prompt file, the send-keys line
(hence scrollback and shell history), any grove log line, or a ticket/PR
body. Names are public; values are not.

**The honest limit, stated up front.** A worker that holds
`ANTHROPIC_AUTH_TOKEN` in its environment — which is how Claude Code
authenticates, so it must — can run `printenv` and read it. No design
that lets the worker *use* the key can prevent a worker that *decides* to
read it. What this design guarantees is that it never happens by
accident: not by grove's own output, not by a state file, not by the
prompt, not by copying a directory. The deliberate case is the job of the
PreToolUse guard the operator already runs
(`~/.config/grove/hooks/no-secrets-on-contrib.sh`, wired into
`~/.claude/settings.json`; it denies any tool payload mentioning
`.env`/secrets/keys, and both `permissions.deny` and a PreToolUse deny are
honored under `--dangerously-skip-permissions`). §9 extends it.

## Decision 1: embed `filippo.io/age`, no external binary

Researched 2026-09-27 against pkg.go.dev and the FiloSottile/age releases:

- **Latest: v1.3.2** (2026-08-29), BSD-3, `go 1.25.0` minimum — grove is on
  1.26.1. Pure Go.
- **The whole API this design needs is six calls:** `GenerateX25519Identity()`,
  `ParseX25519Identity(s)`, `ParseRecipients(io.Reader)` (reads the standard
  recipients-file grammar — one `age1…` per line, `#` comments — and
  transparently accepts the v1.3 post-quantum `age1pq1…` and hardware
  `age1tag1…` forms), `Encrypt(dst, recipients...)` → `io.WriteCloser`,
  `Decrypt(src, identities...)` → `io.Reader`, plus `armor.NewWriter/NewReader`
  from the same module for the ASCII form.
- **Dependency cost:** `filippo.io/age`, `filippo.io/edwards25519`,
  `filippo.io/hpke`, `filippo.io/nistec`, `golang.org/x/crypto`. `x/sys` and
  `x/term` are already in go.mod. Five modules, all from the same author or
  the Go project, no transitive sprawl. grove has seven direct deps today;
  this is the first crypto one and it is the audited reference
  implementation of the format, not a wrapper around one.
- **Errors are typed:** a decrypt with the wrong identity returns
  `*age.NoIdentityMatchError`, which maps to one friendly message ("this
  host's identity is not a recipient of `NAME` — run `gv keys push --host
  <this host>` from a host that is").

Why not shell out to the `age` CLI:

- It is an **external binary grove does not otherwise depend on**. `gh`,
  `tmux`, `ssh`, `git` are all pre-installed on any box that runs grove;
  `age` is not, and it would have to be installed — and kept at a
  compatible version — on *every* `hosts:` remote, outside `gv update`.
- Every operation becomes argv + stdin plumbing to a subprocess, which is
  exactly the surface (`ps`, quoting, exit codes) this design is trying to
  shrink. The library call takes an `io.Reader`.
- `go test ./...` would need the binary or skip. Embedded, the whole
  package is table-tested with scratch identities and the e2e suite needs
  nothing installed.
- The CLI is a thin wrapper over the same library. There is no capability
  it has that the package lacks.

Why not sops: heavier (YAML/JSON partial-encryption, cloud-KMS backends
grove will never use), an external binary, and underneath it *is* age. The
one thing sops adds — encrypting values inside a structured file while
leaving keys readable — is exactly what per-secret files (§3) give for
free.

Why X25519 and not the v1.3 hybrid post-quantum recipient by default: a
PQ recipient string is ~2000 characters, which is unusable in a
`recipients.txt` comment, a `gv keys recipient` paste, or a chat message,
and the threat it addresses (harvest-now-decrypt-later) does not apply to
short-lived bearer tokens that rotate. Because `ParseRecipients` accepts
`age1pq1…` lines, an operator who wants it later adds one to
`recipients.txt` and runs `gv keys reseal` — a config edit, not a code
change.

Interop is a real benefit of using the standard format unchanged: an
operator can `age -d -i ~/.local/state/grove/age/identity.txt NAME.age`
with the stock CLI to check what grove wrote, and `age -R recipients.txt`
to encrypt something for it. grove owns the layout, not the format.

## Decision 2: storage layout

```
~/.local/state/grove/age/identity.txt        # THIS host's private key. 0700 dir, 0600 file.
                                             # Never copied, never pushed, never in config.
~/.config/grove/secrets/                     # THE store — one physical location, always
    global/                                  # namespace shared by every workspace
        recipients.txt                       # who may read this namespace: one age1… per line,
                                             # `# host: <name>` label above each
        OPENROUTER_API_KEY.age               # one armored ciphertext per secret
        OPENROUTER_API_KEY.meta.yaml         # optional sidecar: type label + masked preview (§11)
        KIMI_CODE_API_KEY.age
    thegrid/                                 # namespace = a workspace LABEL, identical shape
        recipients.txt
        MUSE_CONTRIB_KEY.age
<root>/.grove/config.yaml                    # a workspace carries NAME REFERENCES only
                                             # (`auth_token_env: X`, `env: {K: "secret:X"}`)
```

**One physical store; workspaces are namespaces inside it.** An earlier
draft put a second store at `<root>/.grove/secrets/` inside each
workspace's repo. The operator rejected it, rightly: a target repo must
never be one `.gitignore` mistake away from carrying ciphertext in its
history, and ciphertext in history is recoverable forever by anyone who
later obtains any recipient identity. So the workspace layer is a
*directory named by the workspace label* under the one store, and the
thing a workspace's committable `.grove/config.yaml` holds is names.
Labels are already host-independent identifiers (the cockpit session is
`grove-<label>`, the registry indexes by label), so `thegrid/` means the
same namespace on every host that has that workspace. The namespace
`global` is reserved: `parse` rejects a workspace labeled `global`.

**Identity in the state dir, not the config dir.** This is the decisive
placement rule of the design. `~/.config/grove/` is what gets copied,
backed up, and synced — and the ciphertext store *should* travel with it
(a `scp -r ~/.config/grove` to a fresh box is a legitimate onboarding
move, and every namespace arrives unreadable until that box's identity is
added as a recipient). The identity must never ride along with that copy.
`~/.local/state/grove/` is per-machine by construction, `--host` and
`handoff` never sync it (remote.go:2-5: "nothing syncs"), and it already
has the `GROVE_STATE_DIR` override (config.go:210-218) that e2e uses to
land everything in scratch — so a scratch identity for tests comes free.
The identity always resolves through the *global* `StateDir()`, never
`StateDirAt(root)` (merge.go:126-134): an identity is a property of the
host, not of a workspace.

**Per-secret files, not one store file.** `gv keys ls` and the ACCOUNT
tab can list names without decrypting or holding an identity; a push
can be verified by filename; doctor checks are per-name; and a `set`
rewrites one small file instead of decrypt-modify-reencrypt of
everything. The cost — every file in a namespace must be resealed when
its `recipients.txt` changes — is one loop (`gv keys reseal`, §6), and it
is the same loop push runs anyway. `recipients.txt` is per namespace so
a host can be given one workspace's secrets and not another's.

**Armored.** `-----BEGIN AGE ENCRYPTED FILE-----` is self-describing when
someone finds the file, survives every transport (ssh stdin, a paste,
JSON), and costs ~35% size on values that are ~100 bytes.

**Names are `envKeyPattern`** (config.go:400, `^[A-Za-z_][A-Za-z0-9_]*$`).
A name is a filename and an environment variable, and it must be safe
as both; reuse the existing rule rather than write a second one. The
value is the plaintext byte-for-byte, with one trailing newline
stripped on `set` (what `printf x | gv keys set` and an interactive
prompt both produce).

**Resolution order for a name**, in `secrets.Resolve(label, name)`:

1. process environment (kept — this is how e2e stubs keys, how an
   operator overrides one for a session, and what `openrouter.Key`
   does today at openrouter.go:56-63; no behavior change)
2. this workspace's namespace `~/.config/grove/secrets/<label>/<NAME>.age`
3. the global namespace `~/.config/grove/secrets/global/<NAME>.age`
4. legacy `~/.config/grove/.env` — **migration window only** (§7), with a
   doctor warning while any name still resolves from here

Workspace over global, per name, exactly the shape `LoadAt` gives
config (merge.go:26-64). `Resolve` takes the workspace label the caller
already has (`workspace.Find(cwd).Label`, workspace.go:58-107, resolved
before config is loaded); label == "" means global only, the same
legacy fallback `LoadAt("")` has.

## Decision 3: identity lifecycle

- **`gv keys init`** generates `identity.txt` if absent (0700/0600,
  `age.GenerateX25519Identity`), writes nothing else, and prints the
  **public** recipient. Idempotent: an existing identity is never
  overwritten; there is no `--force` in v1 (rotation is §10). It becomes a
  wizard step (`wizard.Build`, wizard.go:99-207 — a `KindConfirm` row
  "generate this host's age identity for encrypted secrets", kept in the
  parent-scope subset) so a fresh `gv init` produces a host that can
  receive a push.
- **`gv keys recipient`** prints the public key only. It is the one thing
  another host needs to know about this one, and it is safe to paste
  anywhere.
- **The private key has no read path.** No verb prints it, no `--json`
  carries it, push sends ciphertext only. Copying an identity between
  hosts is explicitly *not* a feature: every host has its own, and each
  secret is encrypted to all of them. This is what "never synced through
  anything that isn't a deliberate, explicit copy step" means in practice —
  there is no copy step at all, deliberate or otherwise.
- **`gv doctor`** gains rows (in `internal/connections`, the same
  `Connection` list the existing sub-lane check sits in, core.go:90-98):
  `keys:identity` (present, mode 0600, dir 0700), `keys:legacy-env`
  (warn while `.env` still holds names not in the store — with the
  `gv keys import --rm` fix line), `keys:unresolved` (a configured
  profile's `auth_token_env` or `secret:NAME` that no namespace holds). The remote-host probe
  (connections.go:249-266) gains a sibling `keys:host:<name>` row that
  runs `<gv> keys ls --json` over the same BatchMode ssh and reports which
  locally-stored names the host lacks.
- **Losing an identity** loses that host's access, not the secrets: any
  other recipient host can `push` back to it after a fresh `init`, and if
  there is none, every value in the store is a re-issuable API key. Say
  this in the `init` output so nobody treats `identity.txt` as something
  to back up into the config dir.

## Decision 4: the reference syntax and where it resolves

A model profile references secrets by **name**, and only by name:

```yaml
model_profiles:
  openrouter-glm:
    base_url: https://openrouter.ai/api
    auth_token_env: OPENROUTER_API_KEY        # unchanged: names the secret
    env:
      GROVE_DATA_LANE: "private"             # literal, unchanged
      OPENROUTER_PROVISIONING_KEY: "secret:OPENROUTER_PROVISIONING_KEY"
```

- `auth_token_env` keeps its meaning and its spelling. It has always been
  "the env VAR NAME, never the key itself" (config.go:36); the store is
  simply where that name now resolves.
- An `env:` value of the form **`secret:NAME`** exports that key from
  secret `NAME`. Everything else stays a literal. `parse` validates `NAME`
  against `envKeyPattern` at load (config.go:280-289 already loops the
  map for the key check) so a typo is a config error, not an empty
  variable in a worker. No existing profile has a literal starting with
  `secret:` (grepped the live global and workspace configs 2026-09-27); a
  future one escapes with
  `literal:secret:…`, documented in config.example.yaml.

**Where it lands, and the only code that changes to make it land there.**
`WrapProfile` (config.go:461-485) today renders

```
( . '<~/.config/grove/.env>' && export K='literal' … ANTHROPIC_AUTH_TOKEN="$OPENROUTER_API_KEY" … && exec <claude …> )
```

It becomes

```
( eval "$(<abs gv> keys env --profile openrouter-glm)" && export K='literal' OPENROUTER_PROVISIONING_KEY="$OPENROUTER_PROVISIONING_KEY" … ANTHROPIC_AUTH_TOKEN="$OPENROUTER_API_KEY" … && exec <claude …> )
```

Two edits: the leading `. <path>` becomes the `eval` of an internal
verb's output, and a `secret:` value renders as `K="$NAME"` (bare
expansion, byte-identical to how the auth token has always been rendered
at config.go:478) instead of `shellQuote(literal)` at config.go:474. The
signature changes from `WrapProfile(cmd, p, secretsPath)` to
`WrapProfile(cmd, p, source)` where `source` is the shell snippet that
populates the environment; the four call sites (main.go:1055 orchestrator,
1702 grab, 3376/3379 adopt fresh/resume) pass it from one helper. The
absolute `gv` path comes from `os.Executable()` exactly like the
`run-setup` prefix on the same line (main.go:1704) — a fresh pane has no
PATH guarantees, the same reason `Host.GV` is absolute (config.go:50-52).
Everything grove-36 verified about this line survives untouched: the
value is never in argv (`eval` is a builtin; `$(…)` output is not echoed
and is not history), the wrap still ends in `exec <cmd> )` so the
2026-08-31 "compose bare, wrap LAST" rule (LEARNINGS.md) and its goldens
hold, and `p == nil` still returns the command unchanged.

**`gv keys env --profile <name>` (internal)** is the decrypting half.
It resolves exactly the set `{p.AuthTokenEnv} ∪ {NAME : "secret:NAME" ∈
p.Env}` — a worker gets the names its profile references and nothing
else — and prints `export NAME='value'` lines through the same
`shellQuote` (config.go:381-383) that quotes every other value in the
wrap. It is listed under "(internal)" in `gv help` next to `run-setup`
(main.go:151), and it refuses when stdout is a terminal ("refusing to
print secrets to a terminal; this verb exists for `eval` inside a worker
launch"). That guard is not security against an agent (nothing in-band
is — see The invariant); it is what stops an operator or an orchestrator
chat from ever seeing a value in a pane by running the verb out of
curiosity because it appeared in `--help`.

Why `eval` of a pipe and not `gv keys exec -- <cmd>` (Go `syscall.Exec`
with the env set): identical protection — in both, the value lives in a
grove process briefly and then in the child's env — but the exec form
would re-implement the six built-in exports, the `env:` ordering rule
(config.go:458-460), and the `resume || fresh` two-limb shape
(main.go:3368-3381) in Go, touching a wrap whose exact bytes are pinned
by tests and one production incident. The eval form is a two-token
change to a function that already exists.

**In-process readers** (`gv sub` — sub/lane.go:126, cmd/gv/sub.go:297-303;
the ACCOUNT tab — account.go:166, 176, 193; doctor — core.go:181-186)
call `secrets.Resolve(label, name)` in place of `openrouter.Key(path, name)`.
Same shape, same env-first precedence, same "" = unset contract. These
are the only places grove itself needs a value in memory, and each
already holds it only for one HTTP call.

## Decision 5: remote hosts — one push, ciphertext only, existing ssh

Nothing about `--host` dispatch changes. `gv grab grove-N --host H` still
becomes `ssh <H.ssh> -- <H.gv> grab grove-N …` (remote.Argv,
remote.go:166-173) and **H runs its own grab, its own `WrapProfile`, its
own `gv keys env`, against its own identity and its own copy of the
store**. The only new requirement is that the store exists on H. That is
what `push` does:

```
gv keys push --host H [--workspace <label>]
```

1. `remote.Run(cfg, "H", "keys", ["recipient"])` → H's public key. Adds
   `keys` to `remote.Supported` (remote.go:29-33) with sub-verb gating the
   way `orchestrator` and `chat` are gated — only `recipient` and
   `receive` relay; anything else is the same friendly refusal.
2. Add the recipient to the namespace's `recipients.txt` under `# host:
   H` if absent, then **reseal**: re-encrypt every `NAME.age` in that
   namespace to the full recipient list. (An operator who wants this
   host to read only *some* names puts those names in a workspace
   namespace and pushes that; per-name recipient lists are deliberately
   not a v1 feature.)
3. Stream the namespace to H: `remote.RunWithInput(cfg, "H", "keys",
   ["receive"], stdin)` where stdin is one JSON object
   `{"namespace": "global", "recipients": "<file>", "secrets": {"NAME":
   "<armored>"}, "meta": {"NAME": "<sidecar>"}}` — a one-line exported
   wrapper over the unexported `run` at remote.go:210, which already
   takes an `io.Reader`. Ciphertext and sidecars only; ssh is the
   transport, not the security.
4. H's `gv keys receive` writes the files atomically (temp + rename, 0700
   dir, 0600 files) into `~/.config/grove/secrets/<namespace>/` on H —
   the same path on every host, no registry lookup, no guessing at
   repo roots. Prints the count of names written; nothing else.
   Idempotent: pushing twice is a no-op diff.

Local output: `✓ 3 secrets → H (age1…q7x2, namespace global)`.

**Default namespace is global, and that matters on a remote:** the
`--host` relay has no `cd` (remote.Argv), so H's gv runs at the login
dir, finds no ambient `.grove/`, and resolves the *global* namespace
(memory of 2026-09-09: this is why the contrib lane had to be global on
groveremote). A workspace-namespace push lands on H regardless of
whether H has that workspace registered; it is simply unused until it
does.

**Push is explicit, never automatic.** `gv keys set` does not fan out.
It prints a hint when `recipients.txt` names hosts other than this one
("2 other recipients — `gv keys push --host …` to sync"), and stops.
Fanning secrets out to machines is outward-facing; propose, then dispose.

**`gv handoff`** needs nothing: the remote adopt runs H's `WrapProfile`.
If a profile's secret is missing there, H's `gv keys env` fails *before*
`exec`, with a message naming the secret and the push command, and the
pane drops to a shell — visible, recoverable, no silent fallback to the
operator's own Claude sub (the exact failure grove-36 T3 killed).

## Decision 6: CLI surface

```
gv keys init                          # generate this host's identity (idempotent); prints recipient
gv keys recipient                     # this host's public key, nothing else
gv keys set NAME                      # value from stdin if piped, else a hidden prompt (x/term.ReadPassword)
    [--global | --workspace [L]]      #   default: this workspace's namespace when inside one, else global
    [--type "Stripe restricted key"]  #   optional display label (§11)
    [--reveal N]                      #   opt-in masked preview: first N chars + last 4 (§11)
gv keys ls [--json]                   # name · namespace · type · preview (or ***) · recipients. NEVER values.
gv keys rm NAME [--global | --workspace [L]]
gv keys exec NAME[,NAME2…] -- <cmd…>  # run <cmd> with those secrets in ITS env only; output passes through (§10)
gv keys push --host H [--workspace L] # §5
gv keys reseal [--global | --workspace [L]]   # re-encrypt a namespace to its current recipients.txt
gv keys import [PATH] [--rm]          # legacy .env → global namespace; --rm deletes the file after (§7)
gv keys env --profile P               # (internal) the eval'd half of a launch; refuses a TTY
gv keys receive                       # (internal) the remote half of push; ciphertext on stdin
```

Rules that shape it:

- **A value is never an argument.** `set NAME VALUE` does not exist —
  argv is `ps`-visible and shell-history-visible. stdin or a hidden
  prompt, the same two channels the ACCOUNT tab's paste flow collapses to.
- **`ls --json` is a new, additive contract envelope** for plugins
  (`{schema_version, keys: [{name, layer, recipients}]}`), names only. No
  existing `--json` field changes; `e2e/plugin.sh` stays green by
  construction.
- **No events.** `events.jsonl` is task history, append-only; a secrets
  operation is configuration, like editing `config.yaml`. Nothing here
  appends, folds, or reads it — which is also how the "never in
  events.jsonl" half of the invariant is proven: there is no code path
  from a value to `state.Append`.
- **The orchestrator brain** lists `gv keys ls --json` as a read-only
  tool and names `set`, `env`, `push`, `exec` as operator-confirmed — a
  chat that needs a key present on a host proposes the push command, a
  chat that needs to *call* something with a key proposes the exact
  `gv keys exec` line; neither runs unheard (§10).

## Decision 7: migration

Cut over in stages, each shippable on its own; the plaintext file stops
being *written* on day one and stops being *read* one release later.

1. **Write side first.** `pasteKeyCmd` (account.go:218-241) calls
   `secrets.Set` instead of `openrouter.SaveKey`; the connect prompt's
   copy at account.go:483-485 ("set OPENROUTER_API_KEY in
   ~/.config/grove/.env") becomes "`gv keys set OPENROUTER_API_KEY`, or p
   to paste". The key rows (`accountKeyRows`, account.go:87-120) gain a
   dim namespace tag (`global` / `<label>`) so the operator can see where
   a name resolves from, and show the §11 preview (or `set · <type>`)
   instead of `openrouter.Mask`. From this point no grove code writes
   `.env`.
2. **`gv keys import [--rm]`** encrypts every assignment in the legacy
   file into the global layer using the tolerant grammar
   `openrouter.keyFromFile` already implements (openrouter.go:65-91 —
   `export`/bare, quoted/unquoted, last wins), lists the names, and
   deletes the file only with `--rm`. Wired as a wizard step that
   appears only when `.env` exists. Never silent: an operator who ran
   `set -a; . ~/.config/grove/.env` in a shell rc (the grove-36 §M4 note)
   has a line to remove, and the output says so.
3. **Read side with fallback.** `secrets.Resolve` step 4 keeps reading
   `.env` for one release, and doctor warns per name. `gv sub`, the tab,
   and the doctor check switch to `Resolve` in this step.
4. **Cut.** Delete the fallback, `openrouter.Key`, `openrouter.SaveKey`,
   `config.SecretsPath`, and `envFileHasVar`; the `.env` reference in
   config.example.yaml, the orchestrator seed, and `LEARNINGS.md`'s
   2026-09-05 note ("`.env` holds provider API keys only") get their
   successor lines.

Nothing about `notify.ntfy` moves: it is config, not a secret in this
sense, and it is read by hook receivers through `NotifySettingsFrom`
without `LoadAt` (LEARNINGS.md 2026-09-05) — a path this design must not
touch.

## Decision 8: hard rules, unchanged — and how each is shown

| Rule | Status |
|---|---|
| binary never mutates a task backend's terminal state | not touched; no backend call anywhere in `internal/secrets` |
| never deletes worktrees/branches it didn't create | not touched |
| `events.jsonl` append-only; `tasks.json` derived | not touched; secrets emit no events, and the launch command is not persisted (main.go:1711-1719 stores names) |
| merge checks via `gh` | not touched |
| propose, then dispose | `push` (outward) and `exec` (runs a command with a live key) are operator-confirmed in the brain; `set` never fans out |
| `ovs` frozen | untouched; no backport (ovs never had profiles) |
| `--json` contract additive-only | one new envelope (`keys ls`), zero changed fields |
| cockpit RAM rule | the tab reads on open/`r`/after-save as today (account.go:159-160); no cache, no goroutine, no per-frame work |
| tmux-discipline | no new tmux calls; the send-keys line shrinks by one path |

## Decision 9: testability (the dummy-data pattern)

**Unit, `internal/secrets`** (table tests, scratch dirs, two generated
identities): round trip; a file sealed to two recipients decrypts with
either identity and fails with `*age.NoIdentityMatchError` on a third
(mapped to the friendly message); name validation rejects `../x` and
`FOO-BAR`; namespace precedence (env > `<label>/` > `global/` > legacy); reseal
after adding a recipient makes the new identity work and the old one
still work; `receive` is atomic (a payload with one bad name writes
nothing); `env` output quoting survives a value containing `'`, `$`,
backtick and a newline; the TTY refusal (isTerminal injected);
`exec` env assembly (only the named secrets are added, the parent env is
otherwise inherited, a missing name fails before exec); the preview rule
(§11) refuses short values and never reveals more than the cap.

**Unit, `internal/config`:** `WrapProfile` goldens updated for the
`eval` source; a `secret:` value renders `K="$NAME"`; a profile with no
`secret:` values renders byte-identically to today apart from the source
token; `parse` rejects `secret:not a name`.

**`e2e/keys.sh`**, the dummy-data pattern from `e2e/dummy.sh:1-80`
verbatim — scratch `HOME`, scratch `GROVE_STATE_DIR` (so the identity is
scratch too), isolated tmux server (`unset TMUX`, scratch `TMUX_TMPDIR`),
the live-state canaries snapshot before and after — plus:

- `gv keys init`; `printf hunter2 | gv keys set DUMMY_KEY` (from inside
  the scratch workspace, so it lands in the `dummy/` namespace; a second
  `--global` name proves precedence).
- `gv keys exec DUMMY_KEY -- sh -c 'printf %s "$DUMMY_KEY" > seen-exec'`
  → `hunter2`; the same command via `gv ls --json`/state greps below finds
  nothing.
- A profile `p` with `auth_token_env: DUMMY_KEY` and
  `env: {DUMMY_EXTRA: "secret:DUMMY_KEY"}`; the repo's `claude:` set to a
  scratch script that writes `"$ANTHROPIC_AUTH_TOKEN|$DUMMY_EXTRA"` to
  `$SCRATCH/seen` and exits.
- `gv grab task-001 --repo dummy --profile p`; assert `seen` reads
  `hunter2|hunter2` — the value reached the child's environment.
- Assert `hunter2` appears **nowhere else**: `capture-pane -S -` of the
  worker pane (tmux-discipline: capture with `-S` so the whole scrollback
  is checked), every file under `$GROVE_STATE_DIR`, the kickoff prompt
  file, `gv ls --json`, the scratch `HISTFILE`, and `$HOME/.config/grove`
  (only ciphertext there). These greps are the invariant, executable.
- A fake `ssh` on PATH (the `e2e/handoff.sh:48-91` pattern) that re-runs
  the command with `GROVE_STATE_DIR=$SCRATCH/state-remote
  HOME=$SCRATCH/home-remote`; `gv keys push --host pc`; then a grab run
  against the remote env sees the key, and a third scratch identity does
  not. Capture to a file, then grep — never `| grep -q` under pipefail
  (shipping-gates).

Added to `e2e/all.sh`. No real API key, no real host, no age binary.

## Decision 10: `gv keys exec` — use a key without ever holding it

```
gv keys exec NAME[,NAME2,…] -- <command> [args…]
```

Resolves each named secret (same order as §2), sets `NAME=value` in a
copy of the current environment, and **`syscall.Exec`s the command with
that environment** — no shell, no quoting, no `eval`, argv passed as a
slice. stdout/stderr are the child's own file descriptors, untouched.
The value exists in the grove process for the microseconds between
decrypt and exec, then only in the child. `gv keys env` (§4) stays as
the launch-time half because the worker wrap is a shell line that must
end in `exec <claude> )`; `exec` is the general primitive for
everything else.

What it is for: an orchestrator chat that wants to check an OpenRouter
balance, a worker that must call a provider API from a script, an
operator who wants to run a one-off `curl`. The reference must be
expanded *inside the child*, never by the caller's shell:

```
# wrong — the caller's shell expands $OPENROUTER_API_KEY (unset there) to ""
gv keys exec OPENROUTER_API_KEY -- curl -s -H "Authorization: Bearer $OPENROUTER_API_KEY" https://openrouter.ai/api/v1/credits

# right — single quotes hand the reference to the child's sh
gv keys exec OPENROUTER_API_KEY -- sh -c 'curl -s -H "Authorization: Bearer $OPENROUTER_API_KEY" https://openrouter.ai/api/v1/credits'
```

or a script that reads the variable itself. The verb's help text, and
its error when the received argv contains an *empty* argument where a
`$NAME` reference was clearly meant, must say exactly this; that one
message will save more steering turns than any other line in the
feature.

Rules:

- **Operator-confirmed in the orchestrator brain, like `set` and
  `push`.** The value stays hidden, but the *command* is the
  consequential part — a `curl -X POST` with a live key is an
  outward-facing action, and propose-then-dispose applies to the action,
  not to whether the chat can see the key. A chat proposes the exact
  line; the operator says yes.
- **Only the named secrets are added.** `NAME,NAME2` is the whole
  grant; nothing else from the store enters the child. A missing name
  fails before exec, naming it and the namespace searched.
- **No TTY refusal** — unlike `env`, this verb never prints a value; its
  output is the child's. And no `shellQuote` — there is no shell between
  grove and the child. The lead's brief asked for both disciplines; the
  *reason* for each is absent here, and adding a quoting layer to a
  no-shell exec would be the injection surface, not the protection. The
  discipline it inherits is the invariant itself: the value lives in the
  child's environment and nowhere grove writes.
- **Inherent limit, restated:** `gv keys exec K -- printenv K` prints the
  key. That is the operator's right and the agent's temptation; the
  brain rule above and the PreToolUse guard (§9 risk 1) are the answer,
  not a denylist of commands inside `exec` (which any `sh -c` defeats).
- Not relayed over `--host`: an operator who wants to run something on H
  with H's key runs `gv keys exec … --host H`? No — that would put the
  command line through `remote.Argv` quoting and ssh; the composable form
  is `ssh H -- gv keys exec …` by hand, and `--host` support is a later
  additive ticket if it earns its keep.

## Decision 11: masked preview and type metadata

Today's ACCOUNT tab already shows a mask — `openrouter.Mask`
(openrouter.go:121-131) renders 12 head + 3 tail characters of every
key, unconditionally. The new default is stricter: **nothing is
revealed unless the operator opts in per secret.**

A per-secret plaintext sidecar `NAME.meta.yaml` next to `NAME.age`:

```yaml
type: Stripe restricted key       # free text, optional, display only
preview: rk_live_8723***6AE2      # computed at `set` time; absent = no preview
reveal: 12                        # the --reveal N that produced it (audit trail)
set_at: 2026-09-27T14:26:00Z
```

- **The preview is computed once, at `set`, and stored.** `ls` therefore
  never decrypts and never needs an identity — it reads sidecars and
  filenames only, which is what keeps `gv keys ls` safe for an
  orchestrator chat to run.
- **`--reveal N`** reveals the first N characters and the last 4. It is
  refused (the secret is still saved, with no preview, and the refusal
  says why) when the value has **fewer than 16 hidden characters** —
  `len(value) − N − 4 < 16` — or when the value is fewer than 24
  characters at all, or when the value has fewer than 8 distinct bytes
  (a low-entropy string where a prefix is most of the information). N is
  capped at 16. There is no `--reveal` that shows the whole value.
- **No `--reveal` means no preview:** `ls` shows `***` and the type
  label. `import` (§7) takes an optional `--reveal N` applied to every
  imported name so an operator who liked today's mask can keep it for
  the keys they migrate — an explicit choice, not a default.
- The sidecar holds *nothing derived from the value except the preview*
  — no length, no hash (a hash of a 40-byte key is a crackable oracle).
- Sidecars ride along in `push` (§5) so `ls` reads the same on every
  host, and `rm` deletes both files.
- The ACCOUNT tab's key rows (account.go:414-418) render the preview when
  present and `set · <type>` otherwise, replacing `openrouter.Mask` on
  that path.

The tradeoff is stated in Risks: a preview is a *deliberate small
disclosure* — the same one the tab makes today, made opt-in and bounded.

## Decision 12: built as an isolated feature train

Every ticket in this feature lands on a long-lived integration branch,
`feature/gv-keys-secrets` (pushed to origin at the current main tip),
and **nothing reaches main until the whole feature is validated end to
end**. A half-migrated secrets path is the case the ticket-writing skill
reserves feature branches for: ticket 3 changes what every profiled
launch sources, ticket 6 changes what every in-process reader reads, and
main with only one of them is a fleet that cannot start a worker.

Mechanics, chosen so that no grove code and no global config changes:

- `repos.grove.base` in config stays `main`. Editing it would redirect
  every concurrent grab on this host, not just this train's.
- Each ticket's kickoff (`gv grab … --brief`, main.go:1464 — appended as
  the prompt's final "Operator brief" section) tells the worker to run,
  before any work, `git fetch origin feature/gv-keys-secrets && git
  reset --hard origin/feature/gv-keys-secrets` in its worktree (safe: the
  freshly-cut branch has zero commits), to open its PR with `gh pr create
  --base feature/gv-keys-secrets`, and to rebase on that branch, never
  main. The verbatim text lives in each ticket file under
  `docs/plans/tickets/`.
- grove's PR detection is head-branch-based and base-agnostic (`gh pr
  view` by branch; merge state via `state,mergedAt`), so `gv watch`,
  `pr_merged`, sweep's merged→done all work unchanged against the feature
  branch.
- The taxes the skill names, accepted: the branch drifts from main and
  the operator eats the rebases (plan one after ticket 5 and one before
  the final merge); PRs review against a moving target; **`gv diff
  grove-N` diffs against `repo.Base`, so it will show the whole train's
  delta, not the ticket's** — review with `gh pr diff` instead.
- The last ticket **proposes** the `feature/gv-keys-secrets → main`
  merge after `e2e/all.sh` is green on the branch; it never runs it.
  The design doc itself must be committed to the feature branch before
  ticket 1 is grabbed, or the workers cannot read it.

## Alternatives considered and rejected

- **An in-repo `<root>/.grove/secrets/` workspace layer** (this doc's
  first draft) — puts ciphertext one `.gitignore` away from a client
  repo's history, forever. Replaced by label namespaces under the one
  store. §2.
- **sops** — external binary, cloud-KMS-shaped, and age underneath. §1.
- **age CLI via `os/exec`** — new external dependency on every host,
  untestable without it, no capability gain. §1.
- **OS keychains** (macOS Keychain, libsecret) — not portable to the
  Linux VPS that is grove's overflow host, need an unlocked session, and
  `security find-generic-password` is just as callable by an agent.
- **`pass` / gpg** — gpg-agent, pinentry, key servers; the wrong weight
  class, and an external binary again.
- **1Password / Vault / Doppler / any service** — the ask is no SaaS, no
  account, no running server.
- **One store file** instead of per-secret files — `ls` would need the
  identity; every `set` rewrites everything; no per-name doctor/push
  verification. §3.
- **Passphrase (scrypt) recipient** — no unattended decrypt, which is
  the whole job. Available later as protection *of the identity file*
  (`age -p` on it), not of the store; §10.
- **Reusing the host's ssh key as the age identity** (`agessh`) —
  tempting because every `hosts:` remote already has one, but it welds
  secret access to the ssh key's lifecycle, the ed25519 conversion is
  documented as best-effort, and a compromised ssh key would then also
  decrypt the store. A possible `gv keys init --from-ssh` convenience
  later; not the default.
- **Copying the private identity to remotes** — one identity everywhere
  is what makes every other design leak. Rejected on principle; §3.
- **Claude Code's `apiKeyHelper`** — would keep the key out of the
  worker's *environment* by having Claude Code call a helper. Unverified
  whether it applies to the `ANTHROPIC_AUTH_TOKEN` bearer path
  third-party base URLs use, and it only moves the read from `printenv`
  to running the helper, which the agent can also do. Not pursued.
- **A `secret:` lookup implied for every `env:` value** whose name
  matches a stored secret — magic; a stored name silently changing a
  literal's meaning is exactly the ambiguity `auth_token_env` avoids by
  being explicit.

## Risks and open questions

1. **The limit is the limit.** An agent inside a worker that holds the
   key can read it. Say it in the docs, scope exports to the names a
   profile references, and extend the PreToolUse guard to deny
   `gv keys env`, `identity.txt`, and `.config/grove/secrets/` in every lane, not
   only `GROVE_DATA_LANE=contributor` — via `gv hooks install`, which
   already merges into the shared `settings.json` files with respect for
   others' entries.
2. **`eval` of generated shell.** A quoting bug turns a secret value into
   command injection in the worker's own shell. Mitigation: the one
   `shellQuote` every other wrap value goes through, and the hostile-value
   unit test. There is no second quoting function.
3. **Version skew on push.** A remote gv predating this feature answers
   `gv keys recipient` with "unknown command"; push must surface that as
   "H's gv is too old — `gv update --yes` there", not as a parse error.
4. **Two namespaces, one name.** A workspace `OPENROUTER_API_KEY`
   silently shadows the global one. That is the intended semantics (it
   is how `LoadAt` works for every other key), but `gv keys ls` must show
   both rows with the shadowed one marked, and the ACCOUNT tab's
   namespace tag exists for the same reason.
5. **A preview is a disclosure.** `--reveal 12` on a 40-character key
   hands anyone who can read `~/.config/grove/secrets/` (or a `gv keys
   ls` transcript) 16 of its characters. That is what today's tab
   discloses to anyone who can see the screen, so this is not a
   regression — but it is now on disk in plaintext and travels with
   `push`. The bounds in §11 (≥16 hidden, cap 16, no short/low-entropy
   values) are the whole mitigation; an operator who wants zero
   disclosure sets no `--reveal`, and that is the default.
6. **`exec` makes a key usable from a chat.** That is the feature; it is
   also the first time an orchestrator has a sanctioned way to spend a
   key. The brain rule (operator-confirmed, always) is enforced by the
   brain, i.e. by prompt — the same enforcement `grab` and `nudge` have.
   If a chat runs one unheard it is a brain bug, and the PreToolUse
   guard extension should deny `gv keys exec` in chat lanes outright if
   that happens once.
7. **Feature-branch taxes.** Two rebases the operator eats, `gv diff`
   showing the train instead of the ticket, and a window where main
   still writes plaintext while the branch does not. Bounded by keeping
   the train short (ten small tickets) and by ticket 10's gate.
8. **Binary and module growth.** Five modules; expect a low-single-digit
   MB increase in the release binary. Measure in the first ticket and
   record it in LEARNINGS.md if it surprises.
9. **Rotation of an identity** (compromised host): remove its line from
   `recipients.txt`, `gv keys reseal`, then re-issue every value the host
   could read — because it could have read them. v1 ships the primitives
   (`reseal`, editable `recipients.txt`); a `gv keys revoke --host H` that
   does the first two and prints the third is a follow-up.
10. **Non-profile secrets.** `LINEAR_API_KEY` (config.go:498-505,
    `c.APIKey()` reads the process env) and `GH_TOKEN` are not
    model-profile keys and stay where they are in v1. `Resolve` is
    general enough to take them later — and `gv keys exec LINEAR_API_KEY
    -- …` already covers the ad-hoc case — but scoping them into the
    launch path now would widen the first ticket for no worker-facing
    gain.
