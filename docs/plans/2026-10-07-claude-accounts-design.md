# `gv account` — N Claude subscriptions, one `~/.claude`

**Status:** DRAFT 2026-10-07, shape agreed with the operator in the grove
orchestrator chat. Not yet design-reviewed.
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
another, and every worker profile would need its own hooks and plugins
installed.

## The mechanism (verified building blocks)

- `claude setup-token` prints a long-lived OAuth token bound to the
  subscription that signed in.
- `CLAUDE_CODE_OAUTH_TOKEN=<token> claude …` authenticates as that
  subscription and reads and writes the **same** `~/.claude`. So switching
  accounts is just which token a launch gets. Transcripts, memory, and
  resume are unaffected.
- `~/.claude/.credentials.json` (the interactive login) is never touched.
  Swapping that file was rejected: running sessions refresh their token and
  write it back, so a swap races with every live worker.

To verify in ticket 03: that the env token beats the stored login on
2.1.292 (`/status` shows the account).

## Decision 1: an account is its own axis, not a model profile

A model profile answers "which model and which endpoint". An account
answers "whose quota pays". They vary independently: Opus on main, Opus
on alt, and a z.ai lane at the same time. Accounts as profiles would need
the N × M cross product, and `--profile` couldn't combine with `--model`
and `--effort`. So:

- `--account <name>` is a new flag beside `--model`, `--effort` and
  `--profile`.
- `--account` together with a non-Anthropic `--profile` is a hard error.
  The profile sets `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN`, so the
  account would be silently ignored.

## Decision 2: one resolution order, two ways in

Every claude launch grove makes (worker grab, adopt resume, adopt fresh,
orchestrator / chat) resolves its account like this:

1. `--account <name>` on that launch
2. for `adopt`: the account the task was grabbed with (state), unless
   `--account` overrides it. `--account login` strips it, mirroring
   `--profile anthropic` (main.go:3676).
3. the **active** account (`gv account use <name>`)
4. none: the interactive login, with no env added (today's path)

That gives the operator two ways to work:

- **Swap the main.** `gv account use alt`: every new launch and every
  shell `claude` gets alt. Live sessions keep their account until they're
  restarted or adopted, so nothing switches mid-turn.
- **Run both.** Keep main active and pin overflow with
  `gv grab X --account alt`.

The interactive login is always present as the implicit account `login`
(shown with its email from `~/.claude.json` `oauthAccount`). `gv account
use login` means "no override".

## Decision 3: storage, interim, then gv keys

The secure-keys train (docs/plans/2026-09-27-secure-keys-design.md) is
where tokens belong, but only car 01 has merged (on
`feature/gv-keys-secrets`) and the train is parked. Accounts don't wait
for it:

- **Interim:** `~/.config/grove/accounts/<name>.token` (file 0600, dir
  0700), plus `~/.config/grove/accounts/active` holding a bare name.
  Plaintext at rest, exactly like today's `.env`, and no worse.
- **When gv-keys lands:** its readers-migration car (gv-keys 06) moves
  tokens to `secret:claude-account/<name>` and the launch path resolves
  through it. The account store hides storage behind one
  `Token(name) (path or ref)` seam so this is a one-file change. Add a line
  to gv-keys 06's body when this design is accepted.

The gv-keys invariant applies from day one: **the token value never
appears** in a send-keys line, argv, `events.jsonl`, `tasks.json`, any
`--json` output, a log line, or a prompt file. Names are public; values
are not.

## Decision 4: the launch wrap

`config.WrapAccount(cmd, tokenPath)` sits beside `WrapProfile` and follows
its rules (config.go:492-536):

```
( export CLAUDE_CODE_OAUTH_TOKEN="$(cat '<tokenPath>')" && exec <cmd> )
```

The pane's shell reads the file, so the value is never in the send-keys
line, scrollback, or history. An empty `tokenPath` returns `cmd` unchanged
(the byte-identical guarantee). It applies at all four `WrapProfile` call
sites (main.go:1174, 1806, 3996, 3999) and at the chat launch path.

State: the grab event's data gains `account` (name only). `Task` gains
`Account string json:"account,omitempty"`. Both additive, and pre-field
events fold to `""`, the same as `ModelProfile` (state.go:155-158).

## Decision 5: the shell path

`gv account shell-init` prints a function for `~/.bashrc`:

```bash
eval "$(gv account shell-init)"
# defines: claude() { local t; t=$(gv account token-path 2>/dev/null);
#   if [ -n "$t" ]; then CLAUDE_CODE_OAUTH_TOKEN="$(cat "$t")" command claude "$@";
#   else command claude "$@"; fi; }
```

With no active account it's a pass-through. `gv account use` and plain
`claude` in any repo then follow each other. The function is opt-in and
the doctor reports whether it is installed. thegrid is unaffected because
it has its own config dir and command.

## Decision 6: CLI surface

```
gv account add <name>        # runs `claude setup-token`, captures the token
                             #   (or --from-stdin), writes it 0600
gv account ls [--json]       # name, active marker, live workers on it,
     [--usage]               #   token age; --usage adds 5h/7d (HTTP, ticket 04)
gv account use <name|login>  # set the active account
gv account rm <name>         # refuses while a live worker uses it
gv account shell-init        # the bash function above
gv account token-path        # path for the active account, empty for login
```

`--json` fields are a contract: additive only, and listed in
docs/plugins.md. `gv doctor` rows (conditional, silent when fine): token
file mode not 0600, active names a missing account, token rejected
(only on `--usage`, never a background probe).

## Decision 7: limits, the `$` → CLAUDE tab

Three possible sources were found in the 2.1.292 binary:

| Source | What | Verdict |
|---|---|---|
| `GET <api>/api/oauth/usage` with `Authorization: Bearer <oauth token>` | what `/usage` renders: `five_hour`, `seven_day`, `seven_day_opus`, … each with utilization + reset | **primary**, but undocumented. Tolerant parser; dash on any surprise. |
| statusline stdin `rate_limits.five_hour.used_percentage` | per live session, push-only | fallback for accounts with a live session only |
| `anthropic-ratelimit-unified-{5h,7d}-*` response headers | on every inference response | rejected: reading them costs a request |

**Ticket 03 is a spike, gated before any UI.** Does `/api/oauth/usage`
accept a `setup-token` token? Those tokens may carry inference scope only.
If not, what's the fallback: the interactive login's token for `login`,
plus statusline capture for the others? Verdict goes to LEARNINGS.md and
the claude-code-facts skill.

The tab (third on `$`: SPEND / ACCOUNT / CLAUDE) shows one row per account:
name, active ●, live-worker count, a 5h gauge with reset time, a 7d gauge
with reset time, and the Opus-weekly gauge when present. Fetch on tab open
and `r` only, never polled. That's the cockpit RAM rule the ACCOUNT tab
already follows (account.go:26-29). Reuse the kimi fuel-gauge rendering
(`kimi.Window`) instead of drawing a new one.

`gv account ls --usage --json` exposes the same numbers. That replaces
model-lanes Step 1's "Claude sub — no API, ask the operator for a %".

## Decision 8: what the orchestrator may do

It may **propose** `--account` in a grab line with the reason ("main 5h
at 92%, alt at 10%"), same as `--profile`. It never picks an account on
its own and never runs `gv account use`. Swapping the default is the
operator's act. Seed text goes in `.grove/orchestrator/CLAUDE.md` duty 3
and in the model-lanes skill.

## Shape: merge train on main

| # | Car | Depends on |
|---|---|---|
| 01 | `internal/account` store + `gv account add/ls/use/rm/token-path/shell-init` + doctor rows | — |
| 02 | launch path: `WrapAccount`, `--account` on grab/adopt/orchestrator new/chat, state field, profile conflict error, e2e dummy coverage | 01 |
| 03 | spike: usage source + env-token precedence; LEARNINGS + claude-code-facts | — (parallel) |
| 04 | `internal/claudeusage` + `ls --usage --json` + `$` CLAUDE tab | 01, 03 |
| 05 | brains: orchestrator seed duty 3, model-lanes Step 1, plugins.md fields, CLAUDE.md layout line | 02, 04 |

Each car lands green on its own. 02 and 04 touch the lifecycle and TUI,
so `e2e/all.sh` must pass before they merge.

## Out of scope

- **Remote hosts.** A `--host` grab resolves `--account` against the
  remote's own store. Pushing tokens to hosts is gv-keys 08's job.
- **Auto-routing** between accounts, by memory rule
  (no-model-routing-without-ask).
- **Work (thegrid) accounts.** That subscription keeps its own config dir.
  Nothing here reads or writes it.

## Risks and open questions

- `/api/oauth/usage` is undocumented and can change without notice. The
  tolerant parser plus dashes keep the failure cosmetic.
- Switching an existing conversation to another account re-caches its
  whole context once (prompt caches are per account). Document it; don't
  engineer around it.
- Name clash: the existing ACCOUNT tab is OpenRouter keys. Rename it to
  KEYS when gv-keys 06 touches it anyway.
- Anthropic's consumer terms on one person holding several subscriptions:
  the operator's call to check. Grove takes no position.
