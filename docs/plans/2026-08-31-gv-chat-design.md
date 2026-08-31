# `gv chat` — orchestrator chats from a phone

**Status:** DRAFT 2026-08-31, not yet design-reviewed.
**Goal:** read, continue, and start orchestrator chats in every registered
workspace from a phone browser, over Tailscale, with no terminal.

The operator has Termius + Tailscale today and finds it clunky: attaching
to a chat means ssh → tmux → a 45-column pane rendering a TUI built for
90. The chats themselves are fine; the *access* is the problem.

## Decision: first-party, in-repo, no JS toolchain

This is a **first-party surface**, like the TUI cockpit — not a plugin.
The 2026-07-13 one-repo-per-plugin rule (docs/plugins.md) governs
*sidecars that consume the contract*; it does not govern grove's own UIs.
Two facts decided it:

- The separate-repo model has shipped nothing in seven weeks:
  gv-remarkable (#76) was never finished, gv-xteink is still a DRAFT.
  A surface nobody can install is a surface nobody uses.
- grove has **zero JS toolchain** today (the only `package.json` files are
  `internal/probe/testdata` fixtures) and 7 lean direct deps. `site/` is
  hand-written `index.html`. Adding Next.js/shadcn would import an entire
  second ecosystem — npm, lockfiles, node on the host, a second release
  cadence — to render three screens.

So: **one Go HTTP server, UI embedded via `embed.FS`, no build step.**
`orchestrator/embed.go` is the in-repo precedent. Cost is +0 Go
dependencies and one vendored `marked.min.js`. Install is
`gv update` — which is the "anyone can set this up themselves" property
a separate repo cannot offer.

The framing that keeps this honest: **the `gv chat` verbs are the
contract; the server is the first client, shipped in the box.** An
external shadcn client stays possible against the same JSON, without
grove changing.

## What already exists (verified 2026-08-31 on v0.1.21)

Almost all of it. Nothing below is new work.

| Need | Primitive | Shipped |
|---|---|---|
| a chat is its own tmux session | `grove-chat-<label>-<n>`, detached, outside the cockpit | #200 (grove-198) |
| enumerate a workspace's chats | `tmux.ChatSessions(label, CockpitCheck)` → session, pane pid, `pane_current_command`, attached, created | #210 (grove-203) |
| next free slot | `tmux.NextChatSession(label)` | #200 |
| spawn one (locally / on a host) | `gv orchestrator new [--workspace L] [--profile P] [--op-id] [--as]`, `--host` relay | #200 |
| reap one | `gv park --chats` | #210 |
| projects | `gv workspaces --json` → `{root, label, scope}` | shipped |
| chat history | `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`, parsed by `transcript.ListSessions` → `{ID, CWD, FirstPrompt, ModTime, GitBranch}` | shipped |
| deliver a message to a pane | `tmux.PasteText` — `load-buffer -b gv-relay` + bracketed `paste-buffer -p` | shipped |

`grove-chat-<label>-<n>` sessions being *outside* the shared
`grove-<label>` cockpit was chosen (#198) so an attaching ssh client
can't resize the cockpit's windows and a chat outlives an ssh drop.
That decision is exactly what makes a second, concurrent web client safe.

## The three states a chat can be in

The app must not pretend these are uniform. They are not.

**A. Live chat session** — `grove-chat-<label>-<n>` with a claude
process. **Read + write.** The clean case, and what `+ New chat`
produces.

**B. The cockpit's own orchestrator pane** — `grove-<label>:0.1`.
Mechanically identical (a claude process in the same orchestrator dir),
but `ParseChatSessions` deliberately excludes cockpit names — a nil
`CockpitCheck` yields *nothing* rather than everything, so `gv park
--chats` can never kill a dashboard. Writing here also means pasting
into a pane the operator may be typing in at the desk.
**Read-only in v1.** Listed, history rendered, input disabled with a
reason.

**C. Transcript with no live pane** — every past chat. **Read-only until
revived**; see §5.

On 2026-08-31 groveremote had *zero* type-A sessions: both live chats
were type B (cockpit panes, grove-repo and unbrewed) and everything else
was type C. That is because the host's binary predated #198. Day one
after the update, the app shows three projects, full history, a working
`+ New chat`, and two read-only cockpit chats.

## Design

### 1. Pane identity: stamp the session id (the one real gap)

`ChatSessions` gives a tmux session; the transcript gives session ids in
a project dir. Nothing joins them, and "newest `.jsonl` in the dir" is
ambiguous the moment a workspace has two chats — they share one project
dir.

Fix: when `spawnWorkspaceChat` creates a chat, stamp the pane with a
**tmux pane user option**:

```
tmux set-option -p -t <%pane> @grove_chat_session <session-id>
```

Pane user options are durable — a foreground program cannot clobber them
(unlike pane titles, which Claude Code overwrites on boot with
`✳ Claude Code`). This is the same mechanism the tmux-discipline skill
prescribes for durable per-pane tags, and #204 already introduced pane
identity for armed spawns.

Wrinkle: the id is not known at spawn time — claude mints it on boot. So
stamp lazily: on first `gv chat ls` the resolver picks the newest
unclaimed `.jsonl` in the workspace's orchestrator project dir whose
`cwd` matches, stamps it, and never re-derives. A chat whose id cannot be
resolved yet reports `session_id: null` and renders as "starting…".

### 2. `gv chat ls --json`

Per the plugin contract: `{schema_version, chats: [...]}`.

```json
{"session":"grove-chat-unbrewed-1","workspace":"unbrewed","n":1,
 "kind":"chat","session_id":"eeeb…","label":"triage the artgen backlog",
 "command":"claude","busy":true,"attached":false,
 "created":"2026-08-31T01:00:00Z","writable":true}
```

- `kind`: `chat` (A) | `cockpit` (B) | `archived` (C).
- `label`: `transcript.Session.FirstPrompt` (already an 80-char
  truncation built for list labels).
- `busy`: from `pane_current_command` (`claude`/`node` = working).
- `writable`: false for B and C — the UI disables input off this field,
  never off its own guesswork.
- No `--workspace` flag → every registered workspace, each row carrying
  `workspace`, matching grove-191's workspace-transparent global layer.

### 3. `gv chat tail <session> [--follow] [--since <n>]`

Emits transcript entries as JSONL: `{seq, role, kind, text, tool, ts}`.
`role` ∈ user/assistant; `kind` ∈ text/tool_use/tool_result/thinking.
`--follow` streams appends.

**Read from the transcript, never from the pane.** Pane scraping gives
ANSI soup that hard-wraps at pane width, and the house rule is that
pane-scraping is liveness garnish while the transcript/hooks are truth.
The JSONL is append-only, so following it is a file tail — no polling of
tmux, no parsing of chrome that has changed under us twice.

### 4. `gv chat send <session> "<text>"`

Reuses the relay path wholesale. The rules are non-negotiable and already
paid for in blood (grove-144):

1. `load-buffer -b gv-relay` (server-global buffer; never a generic name).
2. bracketed `paste-buffer -p` — never `send-keys` for prose, which is
   single-line and interprets `Enter`/`Space` lookalikes inside the text.
3. settle ~250ms.
4. separate `send-keys Enter`.
5. **verify** by scraping the whole visible pane for the input box; retry
   Enter once, then fail loudly. Delivered is not submitted.

Refuses `kind != chat` with a clear message. Targets the immutable `%N`
pane id resolved via `tmux.ClaudePaneTarget`, never a stored name —
window/session name targets prefix-match and silently resolve to a
sibling (grove-116/78).

**Modal states.** A plain text box cannot drive permission prompts,
option pickers, or slash-command menus. The relay rule's own exception —
a single character aimed at a picker goes through raw, un-Enter-wrapped —
becomes a UI affordance: when a scrape detects a picker, the app shows a
raw-key row (1/2/3, y/n, Esc). Anything beyond that is out of scope; the
answer there is ssh.

### 5. `gv orchestrator new --resume <session-id>`

Revives a type-C chat: allocates the next `grove-chat-<label>-<n>` and
launches `<orchestrator launch> --resume <id>` in it. `--resume` is
verified to work ≥6 days after the pane died, and re-fires SessionStart
with the **same** session_id, so identity survives.

This is `gv adopt`'s pattern applied to chats, and it is what makes the
app feel right — "pick up yesterday's grove chat from the couch". Note
`--resume` opens idle awaiting input; it does not auto-continue.

### 6. `gv chat serve [--port 3000] [--bind 127.0.0.1]`

One `net/http` server. grove uses `net/http` as a *client* today
(update, openrouter, linear, kimi, hooks); this is its first listener.

```
GET  /                       embedded index.html
GET  /api/chats              → gv chat ls --json
GET  /api/chats/<s>/events   SSE, from gv chat tail --follow
POST /api/chats/<s>/send     → gv chat send
POST /api/chats/<s>/keys     raw keys (pickers)
POST /api/workspaces/<l>/new → gv orchestrator new --workspace
POST /api/chats/<s>/resume   → orchestrator new --resume
```

UI: one embedded `index.html` (~400 lines, hand-written, in the spirit of
`site/`) plus a vendored `marked.min.js`. Three screens — projects →
chats in a project → the chat. Assistant text through marked; tool_use
collapsed to one line, expandable. Service worker caches the shell so a
disconnected tailnet reads "not connected" instead of failing blank.

### 7. Bind safety (this ships to every gv user)

An endpoint that spawns Claude sessions and pastes into panes is the
highest-consequence thing grove would ever listen on.

- **Default `--bind 127.0.0.1`.** Any other bind requires the explicit
  flag and prints a warning naming what an attacker on that network could
  do.
- **Off unless invoked.** No daemon, no autostart, not wired into
  `gv` or the cockpit.
- **No auth of its own.** `tailscale serve` is the sanctioned exposure
  and the entire auth story; the docs must say so, and must say
  `tailscale funnel` (public) is never correct here.
- The verbs it exposes are read + relay + spawn. It must never reach
  `done`, `untrack --rm`, or any backend mutation — the propose-then-
  dispose rule applies to a phone even more than to a desk.

### 8. Deployment (groveremote, verified)

```
gv chat serve --port 3000 &          # systemd unit for reboot-safety
tailscale serve --bg 3000
```

→ `https://groveremote.tail504e3.ts.net`, tailnet-only, real cert, no
inbound ports, the panel firewall's DROP-all untouched (Tailscale is
outbound WireGuard). MagicDNS name is stable; `--bg` persists the serve
config in tailscaled state, and tailscaled is systemd-enabled.

Prerequisites, both operator-side: tailnet **HTTPS enabled** (done
2026-08-31 — it was off, `CertDomains: None`; a PWA install and service
worker need a secure origin), and **key expiry disabled** for
groveremote (currently expires 2027-02-22, which would silently drop the
host off the tailnet along with ssh and `gv --host`).

Note HTTPS publishes `groveremote.tail504e3.ts.net` to public
Certificate Transparency logs — a name leak, not access.

## Scope boundary (write it down now)

**`gv chat serve` serves orchestrator chats only.** Anything
fleet-shaped — task rows, cost charts, audit, sweep — stays in the TUI
or goes to an external plugin against `--json`. This line exists because
in-repo surfaces accrete, and grove's value is being a lean CLI.

## Testing

- Pure halves table-tested as usual: the id↔session resolver, the
  transcript→message projection, `writable` classification.
- HTTP handlers via `httptest`; no live tmux.
- Extend the existing `e2e/chat.sh` (grove-203) with the dummy-data
  pattern — scratch `HOME`, state-dir override, repo `claude:` set to
  `echo` — covering spawn → ls → send → tail → park. Add to `e2e/all.sh`.
- `GROVE_E2E_TMUX_CONF=hostile` must pass: never a literal `.0`/`.1`
  pane target, since `pane-base-index 1` is a real operator config.

## Risks / open questions

1. **Lazy id resolution races** a chat that boots slowly. Mitigation:
   `session_id: null` renders "starting…" and resolves on the next `ls`.
   Needs a test with two chats spawned back-to-back in one workspace.
2. **Type-B write-through** is deliberately deferred. If the operator
   wants it, it needs a "someone may be typing here" interlock, not just
   a flag.
3. **Modal detection is a scrape**, therefore garnish. If a picker is
   missed the user sees a stalled chat; the escape hatch is ssh, and the
   UI should say so rather than pretend.
4. **Multiple browser clients** on one chat: harmless (they all tail the
   same file), but two people submitting at once interleaves into one
   input box. Single-operator tool; noted, not solved.
5. `marked.min.js` is a vendored third-party file in a repo with no JS
   supply chain. Pin the version, record the SHA in the commit message,
   never auto-update.
