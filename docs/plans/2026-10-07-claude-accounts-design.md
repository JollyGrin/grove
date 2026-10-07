# `gv account` — N Claude subscriptions, one `~/.claude`

**Status:** REVISED 2026-10-07 after design review (3 blockers, 7 majors,
3 minors, all folded in; see §Review log). The operator agreed the shape
in the grove orchestrator chat.
**Feature train:** `claude-accounts` (`feature/claude-accounts`, label
`claude-accounts`). The operator chose a feature branch over a merge
train.
**Goal:** let the operator hold any number of personal Claude
subscriptions and spend from whichever one has headroom (by swapping the
default, or by pinning single launches) without losing a conversation, a
memory note, a hook, a plugin, or a resume. With no accounts configured,
every launch stays byte-identical to today's.

## The problem

One subscription's 5h and weekly windows now cap the fleet. A second
subscription only helps if moving between them is free. The obvious tool,
a second `CLAUDE_CONFIG_DIR`, makes the move expensive. A config dir holds
transcripts (`<dir>/projects/<encoded cwd>/`), auto memory, settings,
hooks, plugins and MCP auth (claude-code-facts §Profiles: "separate
worlds"). A conversation started under one dir can't be `--resume`d under
another.

## The mechanism

- `claude setup-token` prints a long-lived OAuth token bound to the
  subscription that signed in.
- `CLAUDE_CODE_OAUTH_TOKEN=<token> claude …` authenticates as that
  subscription and reads and writes the **same** `~/.claude`. So switching
  accounts is just which token a launch gets. Transcripts, memory and
  resume are unaffected.
- `~/.claude/.credentials.json` (the interactive login) is never touched.
  Swapping that file was rejected: running sessions refresh their token and
  write it back, so a swap races with every live worker.

**Unverified, and gating everything (car 01, spike A):**
1. the env token beats the stored login on 2.1.292 (`/status`);
2. a token-authenticated session does not rewrite `.credentials.json` or
   `~/.claude.json` `oauthAccount`;
3. `setup-token` works when its stdout is captured rather than a TTY, or
   else `add` takes the token on stdin;
4. claude.ai MCP connectors still load under an inference-scoped token.

## Decision 1: an account is its own axis, not a model profile

A model profile answers "which model and which endpoint". An account
answers "whose quota pays". They vary independently. `--account <name>`
is a new flag beside `--model`, `--effort` and `--profile`.

**Accounts and non-Anthropic profiles are mutually exclusive at the
resolver.** When a profile resolves, the active account is skipped, and
an explicit `--account` is a hard error. `WrapAccount` is never applied
to `WrapProfile` output: both end in `exec <cmd> )`, so nesting them
would render `exec ( … )`, a syntax error. A unit test asserts this.

## Decision 2: who an account may touch

**An account applies only to a launch on the personal config dir:** the
workspace's `claude_config_dir` is empty (config.go:96-104) and the
resolved claude command doesn't set `CLAUDE_CONFIG_DIR`. Anywhere else
(thegrid runs `ccwork` → `~/.cc-work`, grove-227), the active account is
skipped silently and an explicit `--account` is a hard error. This is
the guard that keeps a personal token off the work subscription.

## Decision 3: one resolution order, two ways in

Every launch in scope (Decision 5) resolves its account like this:

1. `--account <name>` on that launch: an error if Decision 1 or 2
   forbids it
2. for `adopt`: the account **recorded** on the task. A task records an
   account only when `--account` was given explicitly. If the recorded
   account no longer exists, adopt errors; it never falls back to login.
   `--account login` strips it, mirroring `--profile anthropic`
   (main.go:3676).
3. the **active** account (`gv account use <name>`), unless Decision 1 or
   2 skips it
4. none: the interactive login, with no env added (today's path)

That gives the operator two ways to work:

- **Swap the main.** `gv account use alt`. Every new launch, every adopt
  of an unpinned task, and every shell `claude` gets alt. Live sessions
  keep their account until restarted, so nothing switches mid-turn.
- **Run both.** Keep main active and pin overflow with
  `gv grab X --account alt`. The pin survives adopt.

The interactive login is always present as the implicit account `login`
(shown with its email from `~/.claude.json` `oauthAccount`).

## Decision 4: storage, interim, then gv keys

Secure keys (docs/plans/2026-09-27-secure-keys-design.md) is where tokens
belong, but only car 01 has merged (on `feature/gv-keys-secrets`) and
that train is parked. Accounts don't wait for it:

- **Interim:** `~/.local/state/grove/accounts/<name>.token` (file 0600,
  dir 0700) and `…/accounts/active` holding a bare name. That's the
  state dir, deliberately not `~/.config/grove/`, because secure keys §2
  treats the config dir as the one that gets copied and synced.
  `GROVE_STATE_DIR` relocates it with everything else. Account names
  match `^[a-z][a-z0-9-]{0,31}$`, and `login` is reserved.
- **When gv-keys lands:** its import car (**gv-keys 07**) moves each token
  to `CLAUDE_ACCOUNT_<NAME>` (upper-cased, `-`→`_`, in the `global`
  namespace; it passes `envKeyPattern`, config.go:449). The store hides
  storage behind one seam, `account.TokenSource(name)`. gv-keys 03 also
  changes `WrapProfile`'s call sites, so whichever train lands second
  rebases.

**The invariant.** The token value never appears in a send-keys line,
argv, `events.jsonl`, `tasks.json`, any `--json` output, a log line, or a
prompt file. Names are public; values are not.

**The honest limit** (as in secure keys): the claude process holds
`CLAUDE_CODE_OAUTH_TOKEN` in its environment, and its children inherit
it (Bash tool calls, hooks, MCP servers). A worker that runs `printenv`
puts it in its transcript. Grove guarantees no accidental exposure, and
car 03 adds `CLAUDE_CODE_OAUTH_TOKEN` to the PreToolUse guard's deny
list (`~/.config/grove/hooks/no-secrets-on-contrib.sh`).

## Decision 5: the launch wrap and its sites

Go-side preflight comes first: the token file exists, is non-empty and
is 0600, or the launch refuses. Never a silent fallback to login, which
is the same reason `WrapProfile` refuses (main.go:3988-3994). Then
`config.WrapAccount(cmd, tokenPath)`:

```
( t="$(cat '<tokenPath>')" && [ -n "$t" ] && export CLAUDE_CODE_OAUTH_TOKEN="$t" && exec <cmd> )
```

The path is `shellQuote`d and the value is read by the pane's shell, so
it never appears in the send-keys line, argv (`cat` only sees the path)
or `capture-pane`. An empty path returns `cmd` unchanged. In every `||`
chain, each limb is wrapped separately, the same way adopt does it
(main.go:3988).

| Site | Where | Car |
|---|---|---|
| grab | main.go:1806 (`p == nil` branch) | 03 |
| adopt fresh / resume | main.go:3996, 3999 | 03 |
| cockpit first pane | `orchestratorCmd`, main.go:1163-1166 (both limbs) | 04 |
| `orchestrator new` / brief | main.go:1174, `spawnOrchestratorBrief` (main.go:1386 `p == nil` hand-off) | 04 |
| chat spawn, incl. web chat | `chatSpawnPlan` chat.go:369 (unprofiled branch), `spawnWorkspaceChat` | 04 |
| `gv sub --agentic` | sub.go | **excluded**: a `--bare` lane run, not a subscription session |

`account` joins `grabValueFlags` (main.go:542).

**State.** The grab event, `task_adopted` (beside `model_profile`,
main.go:3964) and the chat spawn event carry `account` (name only, and
only when pinned). `Task` gains `Account string json:"account,omitempty"`,
which is additive: pre-field events fold to `""`. The field goes into
docs/plugins.md and `e2e/plugin.sh` **in the car that adds it**, as with
grove-435's `effort` (plugin.sh:60,171).

## Decision 6: the shell path

`gv account shell-init` prints a bash/zsh function for `~/.bashrc`, which
the operator adds with `eval "$(gv account shell-init)"`:

```bash
claude() {
  if [ -n "$CLAUDE_CONFIG_DIR" ] && [ "$CLAUDE_CONFIG_DIR" != "$HOME/.claude" ]; then
    command claude "$@"; return; fi              # work/other dirs: never touch
  local p; p=$(gv account token-path 2>/dev/null)
  if [ -z "$p" ]; then env -u CLAUDE_CODE_OAUTH_TOKEN claude "$@"; return; fi  # login
  local t; t=$(cat "$p") && [ -n "$t" ] || { echo "gv account: token unreadable" >&2; return 1; }
  CLAUDE_CODE_OAUTH_TOKEN="$t" command claude "$@"
}
```

It doesn't recurse (`command`/`env` bypass the function), it passes
through when gv is absent, and it fails closed on an empty token. Only
interactive shells get it; non-interactive shells always use the login.
`gv doctor` reports whether it's installed.

## Decision 7: CLI surface

```
gv account add <name>        # token from stdin, or runs `claude setup-token`
                             #   if spike A shows capture works
gv account ls [--json]       # name · active ● · pinned tasks/chats · token age
     [--usage]               #   --usage adds 5h/7d (HTTP; car 06)
gv account use <name|login>  # set the active account
gv account rm <name>         # refuses while any non-done task or live chat
     [--force]               #   names it; shell sessions are invisible to this
gv account shell-init
gv account token-path        # active account's path; empty for login
```

Doctor rows are conditional and silent when fine: token not 0600; active
names a missing account; a task pins a missing account; shell-init absent
while accounts exist. Token validity is checked only on `--usage`, never
in the background.

## Decision 8: limits, the `$` → CLAUDE tab

Sources found in the 2.1.292 binary:

| Source | What | Verdict |
|---|---|---|
| `GET <api>/api/oauth/usage`, Bearer OAuth token | what `/usage` renders: `five_hour`, `seven_day`, `seven_day_opus`, … utilization + reset | **primary** if spike B passes. Undocumented, so a tolerant parser shows a dash on any surprise. |
| statusline stdin `rate_limits.five_hour.used_percentage` | per live session, push-only | fallback |
| `anthropic-ratelimit-unified-{5h,7d}-*` response headers | on inference responses | rejected: reading them costs a request |

**Spike B (car 05) gates cars 06–07.** Does `/api/oauth/usage` accept a
`setup-token` token (it may be inference-scoped only)? If not, what's the
fallback? The verdict goes to LEARNINGS.md and the claude-code-facts
skill.

The CLAUDE tab is the third on `$` (SPEND / ACCOUNT / CLAUDE). It shows
one row per account: name, active ●, pinned count, a 5h gauge and reset
time, a 7d gauge and reset time, and Opus-weekly when present. It fetches
on tab open and `r` only (the cockpit RAM rule, account.go:26-29) and
reuses the `kimi.Window` gauge rendering. `gv account ls --usage --json`
exposes the same numbers. That replaces model-lanes Step 1's "Claude
sub — no API".

## Decision 9: what the orchestrator may do

It may **propose** `--account` in a grab line with the reason ("main 5h
at 92%, alt at 10%"), same as `--profile`. It never picks an account on
its own and never runs `gv account use`. Swapping the default is the
operator's act.

## Shape: feature train `claude-accounts`

Cars PR into `feature/claude-accounts`. The operator merges the train to
main.

| # | Car | Depends on |
|---|---|---|
| 01 | Spike A: env-token precedence + side effects + capture + connectors (operator supplies a token) | — |
| 02 | `internal/account` store + `gv account add/ls/use/rm/token-path/shell-init` + doctor rows | 01 |
| 03 | launch I: resolver (Decisions 1–3), `WrapAccount` + preflight, grab/adopt, state + plugin contract, PreToolUse deny, dummy e2e | 02 |
| 04 | launch II: cockpit pane, orchestrator new/brief, chat + web chat, chat event, `rm` in-use over chats, handoff verify prints the account | 03 |
| 05 | Spike B: usage source | 01 |
| 06 | `internal/claudeusage` + `gv account ls --usage --json` | 02, 05 |
| 07 | `$` → CLAUDE tab | 06 |
| 08 | brains: orchestrator seed duty 3, model-lanes Step 1, CLAUDE.md layout line, gv-keys 07 note | 04, 06 |

`e2e/all.sh` must pass before 03, 04 and 07 merge.

## Out of scope

- **Remote hosts.** A `--host` grab resolves against the remote's own
  store. `handoff` doesn't carry a pin: the remote adopt resolves its own
  active account. Car 04 makes handoff's verify step print the account
  so the drop is visible. Pushing tokens to hosts is gv-keys 08.
- **Auto-routing** between accounts (memory: no-model-routing-without-ask).
- **Work (thegrid) accounts**: guarded by Decision 2.

## Risks and open questions

- `/api/oauth/usage` is undocumented and can change. The tolerant parser
  and dashes keep the failure cosmetic.
- Moving a conversation to another account re-caches its whole context
  once (prompt caches are per account). Document it; don't engineer
  around it.
- Name clash with the OpenRouter ACCOUNT tab: rename it to KEYS when
  gv-keys 06 touches it.
- Anthropic's consumer terms on one person holding several subscriptions:
  the operator's call. Grove takes no position.

## Review log (2026-10-07)

- B1, active account leaking into ccwork workspaces → Decision 2.
- B2, empty `cat` silently falls back to login → preflight + `[ -n "$t" ]`.
- B3, `WrapAccount`/`WrapProfile` can't nest → Decision 1 exclusivity.
- M1, missed launch sites → Decision 5 table.
- M2, adopt pin vs swap contradiction → record only explicit pins.
- M3, plaintext in the synced config dir + bad secret name → state dir,
  `CLAUDE_ACCOUNT_<NAME>`, gv-keys 07.
- M4, `rm` in-use not derivable → events carry `account`, `--force`.
- M5, shell-init vs other config dirs → passes through on a foreign
  `CLAUDE_CONFIG_DIR`, fails closed.
- M6, unverified mechanism → spike A gates 02+.
- M7, child-process inheritance → honest limit + PreToolUse deny.
- m1 contract in the adding car; m2 split 02/04 → 02/03/04 and 06/07;
  m3 handoff drop made visible.
