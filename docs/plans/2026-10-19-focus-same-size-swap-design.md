# Focus layout: same-size swap — design

Status: DRAFT (2026-10-19, orchestrator). Follows the focus-mode train
(#502), #510 (stable chat names), #521/PR #523 (scoped lookup + rail
redraw on signal), #522/PR #524 (`cockpit.focus_dash_width`).

## Problem

A focus swap (rail click, CHATS row click/`enter`, `gv chat focus`) feels
sluggish next to tmux's own status bar, which switches windows in ~10ms.
Three costs stack up:

| cost | today | after PR #523 | this design |
|---|---|---|---|
| `gv` start + find the target | ~0.6s | ~0.1–0.2s | same as #523 |
| tmux moves | new-session, break-pane, kill-window, join-pane, 2× main-vertical retile (~20ms) | same | one `swap-pane` + `rename-session` (~5–10ms) |
| Claude Code repaint after its pane changes size | ~270ms, on screen | same | **none** |
| rail catches up | 0–2s (tick) | ~ms (SIGUSR1) | same as #523 |

The repaint is the part no lookup tuning can touch: a hidden chat lives in
a `grove-chat-<label>-<n>` window created at tmux's default size (80×24,
`HideChatPane` grove.go:1543, `CreateChatSession` grove.go:921), so
joining it into the ~60%-wide slot sends SIGWINCH and Claude redraws its
whole screen while the operator watches.

## Spike (2026-10-19, isolated `-L` server, tmux 3.6a, 3 real Claude sessions)

Cockpit 200×50, dash 60 cols, slot 139×50.

- **Today's path** (break-pane shown out, join-pane an 80×24 hidden pane in,
  retile): tmux commands 21ms; the joined Claude pane settled **272ms**
  after the start, 1 repaint.
- **Same-size swap** (hidden window pre-sized to exactly 139×50,
  `swap-pane -d -s <hidden> -t <shown>`): tmux 4–5ms, **0 repaints**, the
  swapped-in screen byte-identical to what the chat drew while hidden.
  Stable over 4 repeats.
- A Claude session in a detached window with `window-size manual` renders
  and completes turns normally.
- **`set -g window-size manual` kills the tmux server** on the next
  `new-session`/`new-window` (reproduced 2×). Per-window `set -w` is safe.
  LEARNINGS.md §tmux, 2026-10-19. Hard rule for everything below.

Scripts: kept out of the repo (orchestrator scratchpad); the measurement
is easy to rebuild as an e2e leg (see ticket 04).

## Design

### 1. Hidden chats are kept at the slot's size (while in focus)

- **The slot** = the shown chat pane's live `#{pane_width}x#{pane_height}`
  after `RetileFocus`. Never computed from `main-pane-width`: the rail
  height, borders and #524's configurable width all fall out of reading
  the real pane.
- **`syncHiddenSizes(cockpit)`**: for every local `grove-chat-<label>-*`
  session of the workspace (focus-minimized AND hand-hidden — a hand-hidden
  chat can be focused too), `set -w -t <@win> window-size manual` +
  `resize-window -t <@win> -x W -y H`. Per window, never `-g`. One
  `list-panes -a` read, then two execs per hidden chat whose size differs
  (no-op when already equal).
- **Triggers** — no new poll, no goroutine (cockpit RAM rule):
  - end of `RetileFocus` (covers enter focus, `L`, spawn-in-focus,
    #524's width change, rail height);
  - the cockpit window's existing `window-resized` hook
    (`keepRailHeight`, rail.go:161) gains a second command:
    `run-shell -b '<abs gv> chat sync-sizes <session>'` (absolute
    `os.Executable()` path — tmux-discipline §3);
  - `HideChatPane` while the cockpit is in focus (the new hidden window is
    born at slot size: break-pane, then size).
- A resize of a hidden window repaints that chat **off screen**, where
  nobody waits for it.
- **Leaving focus** (`applyCockpitLayout` → horizontal): `set -wu
  window-size` on each hidden window before `RestoreMinimized`, so the
  horizontal layout and any later attach behave exactly as today.

### 2. A swap is one `swap-pane`, plus a session-name handoff

`focusChat` in focus, local target, target window already slot-sized:

1. `swap-pane -d -s <target %pane> -t <shown %pane>`. `%N`, pid and every
   `@grove_…` pane option travel with the pane (tmux-discipline §3), so
   #510's `@grove_chat_name` needs nothing.
2. The hidden session that held the target now holds the previously shown
   chat. **Rename it** so session names stay attached to chats, not to slots:
   - every chat pane carries `@grove_chat_home` = its own
     `grove-chat-<label>-<n>` (stamped the first time it is hidden;
     a cockpit-born chat gets `NextChatSession` then);
   - after the swap: `rename-session -t <old> <shown chat's home>`.
   This also ends today's name recycling (the emptied session vanishes on
   every show, grove.go:1664) — a chat keeps one session name for life.
3. `@grove_focus_hid`: swap the two names; one `set-option`.
4. **No retile.** The slot did not change size. `select-pane` on the new
   chat only when the swap came from the dash/rail with focus intent.
5. Events: `chat_hidden` + `chat_shown` with `reason: "focus"` exactly as
   today (plugin contract — additive only, same keys).
6. `railWriteNow` (signal-driven after #523).

**Fallback to today's path** (break-pane/join-pane + retile) whenever the
fast path's preconditions fail: target window not slot-sized (e.g. it was
hidden in horizontal and no sync has run yet), hidden session has ≠ 1 pane
(`ChatShowable`), no chat currently shown, remote chat. Correct first,
fast when possible.

### 3. Unchanged paths

Spawn-in-focus (a brand-new chat paints once anyway), enter focus's first
minimize, leave focus's restore, horizontal show/hide, remote chats
(`@grove_hidden_remote` — no local pane exists), `gv park --chats`.

## Edge cases / open questions

1. **Direct attach to a hidden chat** (`tmux attach -t =grove-chat-…`, the
   hint text at chat.go:690, grove.go:1610/1631): with `window-size
   manual` the window will not follow the attaching terminal. Proposed: a
   per-window `client-attached` hook (or the attach helper itself) that
   `set -wu window-size`s it, and the next `syncHiddenSizes` re-pins it once
   the client leaves. Needs a spike leg.
2. **Phone captures** (`chatweb`, `CapturePane`) wrap at the pane's width:
   hidden panes go from 80 to ~140 columns. Check the phone's turn
   classifier and render on wide captures (grove-317's chrome fixtures).
3. **Operator's own tmux config** (`aggressive-resize`, `window-size`
   set globally by the user): per-window `set -w` overrides both; the
   hostile-conf e2e mode (`GROVE_E2E_TMUX_CONF=hostile`) must cover it.
4. **Two cockpits / clients of different sizes on one workspace session**
   (desk + laptop): the slot is read from the cockpit window as tmux sized
   it; `window-size` of the COCKPIT window is untouched by this design.
5. **Cost of many hidden chats on resize**: each resize is a background
   repaint per chat. With ≤10 chats this is noise; note it, don't solve it.
6. Does `swap-pane` across sessions keep `pane_last`/active-pane semantics
   `PlanEnter` relies on? Verify in ticket 02's tests.

## Tickets (merge train, in order; each ≤ one package-ish surface)

- **00 — land PR #523 first** (scoped live lookup + SIGUSR1 rail). It does
  not touch the pane moves and is half of the speed-up regardless of this
  design. Then #524.
- **01 — hidden windows track the slot size.** `syncHiddenSizes`, the three
  triggers, unset on leave-focus, `gv chat sync-sizes` (hidden verb or
  internal). Tests: unit (plan of resize commands from a pane list),
  e2e/focus.sh asserts every hidden window == slot dims after enter,
  after `resize-window` of the cockpit, after a width change; hostile conf
  green. Never `-g`: a grep test in the package fails on `"-g",
  "window-size"`.
- **02 — swap-pane fast path + session-name handoff.** `@grove_chat_home`,
  rename on swap, `@grove_focus_hid` bookkeeping, fallback matrix, events
  unchanged. Tests: table tests for the fallback decision; e2e leg asserting
  the swapped-in pane's capture is byte-identical before/after (no repaint)
  and session names stay with their chats across 6 swaps.
- **03 — attach + phone edges** (open questions 1–2), after a short spike.
- **04 — measured before/after** on the dummy-data fixture (start →
  target visible, start → rail redrawn), plus docs: CLAUDE.md §Layout one
  line, docs/plugins.md if any event detail moves, the tmux-discipline
  skill gains the global-`window-size` rule.

## Out of scope

Keeping a persistent process in the dash to avoid `gv` start-up (would
violate the cockpit RAM rule), changing the horizontal layout, remote
chat swaps.
