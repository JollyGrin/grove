# `gv account` — N Claude subscriptions, one `~/.claude`

**Status:** REVISED 2026-10-07 after design review (3 blockers, 7 majors,
3 minors, all folded in; see §Review log), then re-grounded in the
published docs (§Mechanism, §Sources) in place of the two spikes. The
operator agreed the shape in the grove orchestrator chat.
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

## The mechanism (verified against the docs, 2026-10-07)

The operator has no second subscription to test with, so spike A (#468)
was answered from Anthropic's published docs instead. Every load-bearing
claim below has its source in §Sources.

- **Minting a token** [S1]: `claude setup-token` runs "the same browser
  authorization flow as `/login`" and prints "a one-year OAuth token". "It
  does not save the token anywhere." It "authenticates with your Claude
  subscription and requires a Pro, Max, Team, or Enterprise plan."
- **Precedence** [S1 §Authentication precedence]: `CLAUDE_CODE_OAUTH_TOKEN`
  is source **5**, and "Subscription OAuth credentials from `/login`" is
  source **7**. So the env token beats the stored login. Sources 1–4 are
  cloud-provider vars, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY` and
  `apiKeyHelper`; any of them would beat the token. That is the formal
  reason accounts and non-Anthropic profiles exclude each other
  (Decision 1). The env-var reference agrees [S2]: "Takes precedence over
  keychain credentials."
- **One config dir means one shared history** [S1 §Log in with multiple
  accounts]: "Each directory has its own settings, session history, and
  claude.ai login or API key." Transcripts are keyed by config dir, not by
  account [S3: `~/.claude/projects/<project>/<session-id>.jsonl`, moved
  only by `CLAUDE_CONFIG_DIR`]. So env-token launches against one
  `~/.claude` share conversations, `--resume`, memory and settings.
- **Why not the documented multi-account recipe.** The docs' answer to
  multiple accounts is one `CLAUDE_CONFIG_DIR` per account [S1]. That
  recipe is for staying signed in to separate worlds (work vs personal),
  and it splits session history, which is the thing this feature exists to
  keep whole. Grove still uses it for thegrid (Decision 2).
- `~/.claude/.credentials.json` (the interactive login) is never touched.
  Swapping that file was rejected: running sessions refresh their token and
  write it back, so a swap races with every live worker.

**Documented costs of a token account.** Each one is shown in `gv account
ls` and the docs:

1. **No claude.ai connectors and no Remote Control** [S1, S4]: the token
   "can only make model requests, so it can't establish Remote Control
   sessions or fetch claude.ai connectors. MCP servers you configure
   locally still work." Connectors (Gmail, Drive, Claude Docs, …) are
   available only on `login`. Workers rarely need them; a cockpit chat
   that does should stay on `login`.
2. **`--bare` ignores it** [S1]: "Bare mode does not read
   `CLAUDE_CODE_OAUTH_TOKEN`." So `gv sub --agentic` stays excluded, as
   designed.
3. **`/login` inside a token session switches that session** [S1]: "If you
   run `/login` while the variable is set, Claude Code switches the current
   session to the new login." The seed and docs say: never `/login` in an
   account-pinned session.
4. **Tokens expire after one year** [S1]: doctor warns from day 330.

**Still unverified** (by design, nothing gates on it; observe at first
real use):
- Whether a token session rewrites `~/.claude.json` `oauthAccount`. The
  docs only say `setup-token` saves nothing. Car 02's `ls` reads the
  login's email from there, so if it does get rewritten, `ls` shows the
  wrong email: cosmetic, and flagged in the first-use checklist.
- Whether statusline `rate_limits` populates under the env token
  (Decision 8). The docs say it appears "for claude.ai Pro and Max
  subscribers", which the token authenticates as. If it's absent, the
  gauge reads "no data" rather than lying.
- Whether `setup-token` output can be captured without a TTY. Not needed:
  `add` reads the token from stdin only.

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
( t="$(cat '<tokenPath>')" && [ -n "$t" ] && export CLAUDE_CODE_OAUTH_TOKEN="$t" GROVE_ACCOUNT='<name>' && exec <cmd> )
```

`GROVE_ACCOUNT` (a name, never a secret) lets the statusline shim
(Decision 8) attribute what it sees to an account. A launch without it
counts as `login`.

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
gv account add <name>        # token from stdin only (paste what `claude
                             #   setup-token` printed); the token's mint
                             #   date is recorded for the expiry row
gv account ls [--json]       # name · active ● · pinned tasks/chats · token
     [--usage]               #   age · "no connectors" note on token rows;
                             #   --usage adds the last-seen 5h/7d (car 06)
gv account use <name|login>  # set the active account
gv account rm <name>         # refuses while any non-done task or live chat
     [--force]               #   names it; shell sessions are invisible to this
gv account shell-init
gv account token-path        # active account's path; empty for login
```

Doctor rows are conditional and silent when fine: token not 0600; active
names a missing account; a task pins a missing account; shell-init absent
while accounts exist; a token 330+ days old (it expires at one year,
[S1]). There's no live validity probe: grove makes no calls to Anthropic
on its own behalf.

## Decision 8: limits, the `$` → CLAUDE tab

**Source: the documented statusline input** [S5]. Claude Code passes
every statusline command a JSON blob on stdin carrying
`rate_limits.five_hour.{used_percentage,resets_at}` and
`rate_limits.seven_day.{…}`. These "appear only for claude.ai Pro and Max
subscribers … and only after the first API response in the session",
and "Claude Code drops a window once its `resets_at` time passes".

| Source | Verdict |
|---|---|
| statusline stdin `rate_limits` | **chosen**: documented, free (no extra request), refreshed by every live session |
| `GET /api/oauth/usage` (what `/usage` renders; found only in the binary) | **rejected**: undocumented. Per house rule, model-facing machinery rests on published guidance (memory: ground-recommendations-in-published-guidance). Revisit only if Anthropic documents it. |
| `anthropic-ratelimit-unified-*` response headers | **rejected**: undocumented, and reading them costs a request |

**`gv statusline`, a pass-through shim** (car 05). The operator already
has a statusline command (`bash ~/.claude/statusline-command.sh`).
`gv statusline install` records that command in grove config
(`statusline.wrap`) and points `statusLine.command` at `gv statusline`.
On each call the shim:

1. reads stdin once and runs the recorded command with the same bytes,
   printing its output unchanged. Its exit status and latency are the
   wrapped command's plus a few ms;
2. if `rate_limits` is present, writes
   `<state>/accounts/usage/<GROVE_ACCOUNT or login>.json`
   (`{five_hour, seven_day, seen_at}`), atomically (temp + rename), and
   only when a value changed.

`gv statusline uninstall` restores the recorded command byte-for-byte.
The shim installs into `~/.claude/settings.json` only (accounts never
apply elsewhere, Decision 2) and preserves every other key, the same
discipline `gv hooks install` follows.

**What the numbers mean.** They're the last values a session on that
account saw, stamped `seen_at`. An account with no session since its
window reset shows the window as reset (its `resets_at` has passed) or
"no data". The tab shows the age ("as of 4m") and never extrapolates.

**The CLAUDE tab** is the third on `$` (SPEND / ACCOUNT / CLAUDE). It
shows one row per account: name, active ●, pinned count, a 5h gauge with
reset time, a 7d gauge with reset time, and the age of the data. It reads
local files only, on tab open and `r` (the cockpit RAM rule,
account.go:26-29), and reuses the `kimi.Window` gauge rendering.
`gv account ls --usage --json` exposes the same numbers. That replaces
model-lanes Step 1's "Claude sub — no API" for any account with a recent
session.

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
| 01 (#468) | ~~Spike A~~: **resolved from the docs** (§Mechanism), closed | — |
| 02 (#469) | `internal/account` store + `gv account add/ls/use/rm/token-path/shell-init` + doctor rows | — |
| 03 (#470) | launch I: resolver (Decisions 1–3), `WrapAccount` + preflight + `GROVE_ACCOUNT`, grab/adopt, state + plugin contract, PreToolUse deny, dummy e2e | 02 |
| 04 (#471) | launch II: cockpit pane, orchestrator new/brief, chat + web chat, chat event, `rm` in-use over chats, handoff verify prints the account | 03 |
| 05 (#472) | `gv statusline` shim: install/uninstall, pass-through, `rate_limits` capture per account | 02 |
| 06 (#473) | usage reader + `gv account ls --usage [--json]` | 05 |
| 07 (#474) | `$` → CLAUDE tab | 06 |
| 08 (#475) | brains: orchestrator seed duty 3, model-lanes Step 1, CLAUDE.md layout line, token-account caveats, gv-keys 07 note | 04, 06 |

Two independent lanes after 02: launch (03 → 04) and usage (05 → 06 →
07). 05 attributes sessions to `login` until 03 adds `GROVE_ACCOUNT`, so
it's useful even before then.

`e2e/all.sh` must pass before 03, 04 and 07 merge.

## Out of scope

- **Remote hosts.** A `--host` grab resolves against the remote's own
  store. `handoff` doesn't carry a pin: the remote adopt resolves its own
  active account. Car 04 makes handoff's verify step print the account
  so the drop is visible. Pushing tokens to hosts is gv-keys 08.
- **Auto-routing** between accounts (memory: no-model-routing-without-ask).
- **Work (thegrid) accounts**: guarded by Decision 2.

## Risks and open questions

- The statusline only sees accounts that have a live session. An idle
  account's numbers age, and the tab says so ("as of …").
- Moving a conversation to another account re-caches its whole context
  once. Caches are isolated per organization [S6: "Different
  organizations never share caches"], and each subscription is its own
  organization. Document it; don't engineer around it.
- Name clash with the OpenRouter ACCOUNT tab: rename it to KEYS when
  gv-keys 06 touches it.
- **Terms** [S7, read 2026-10-07]. The consumer terms do not mention
  holding multiple accounts. They forbid sharing credentials ("You may not
  share your Account login information … or make your Account available
  to anyone else"), and they forbid automated or non-human access "except
  when you are accessing our Services via an Anthropic API Key or where
  we otherwise explicitly permit it". `setup-token` is Anthropic's own
  documented route for scripts and CI [S1]. Nothing found speaks to
  switching between your own subscriptions to extend limits, either way.
  That remains the operator's judgement. Grove takes no position and
  never switches accounts on its own (Decision 9).

## Sources

Fetched 2026-10-07. Quotes are verbatim.

- [S1] Claude Code docs, *Authentication*: https://code.claude.com/docs/en/authentication
  (§Log in with multiple accounts, §Authentication precedence, §Generate a
  long-lived token, §Credential management)
- [S2] Claude Code docs, *Environment variables*: https://code.claude.com/docs/en/env-vars
  (`CLAUDE_CODE_OAUTH_TOKEN`)
- [S3] Claude Code docs, *Sessions*: https://code.claude.com/docs/en/sessions
  (transcript location, `CLAUDE_CONFIG_DIR`)
- [S4] Claude Code docs, *MCP*: https://code.claude.com/docs/en/mcp
  ("Connectors from claude.ai are fetched only when your active
  authentication method is a claude.ai subscription login … They aren't
  loaded … when `CLAUDE_CODE_OAUTH_TOKEN` holds a token from `claude
  setup-token`")
- [S5] Claude Code docs, *Status line*: https://code.claude.com/docs/en/statusline
  (`rate_limits` fields and availability)
- [S6] Claude API docs, *Prompt caching*: https://platform.claude.com/docs/en/build-with-claude/prompt-caching
- [S7] Anthropic, *Consumer Terms*: https://www.anthropic.com/legal/consumer-terms

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
- M6, unverified mechanism → answered from the docs (§Mechanism); spike A closed.
- M7, child-process inheritance → honest limit + PreToolUse deny.
- m1 contract in the adding car; m2 split 02/04 → 02/03/04 and 06/07;
  m3 handoff drop made visible.
