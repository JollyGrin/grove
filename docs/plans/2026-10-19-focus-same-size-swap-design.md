# Focus layout: same-size swap — design

Status: IMPLEMENTED on `feature/focus-swap` (2026-10-19; cars 01, 02, 03,
05, 04). Follows the focus-mode train (#502), #510 (stable chat names),
#521/PR #523 (scoped lookup + rail redraw on signal), #522/PR #524
(`cockpit.focus_dash_width`). The spike and design sections below are kept
as written; the corrections the cars found are in the paragraphs here.

- **Car 01 (#525) — hidden windows track the slot (§1).**
  `internal/tmux/slot.go` (`SyncHiddenSizes`/`ResetHiddenSizes`, pure
  `PlanHiddenSizes`) pins every hidden chat window per window, never `-g`.
  It is synced at the end of `RetileFocus`, by the cockpit window's
  `window-resized` hook (`gv chat sync-sizes`, internal), and at
  `HideChatPane` in focus, and is unpinned when focus is left.
- **Car 02 (#526) — a swap is one `swap-pane` (§2).** `internal/tmux/swap.go`
  (pure `DecideSwap`/`PickHome`/`SwapFocusHid`). `FocusSwap` is one tmux exec
  of swap-pane + rename-session + `@grove_focus_hid`. `@grove_chat_home` is
  stamped by `HideChatPane` (lazily on show for older chats), so a chat
  keeps one session name for life. `focusChat` falls back to break/join on
  any `DecideSwap` reason. Correction to §1/§2: the precondition and the
  sync compare the hidden PANE with the slot, not its window, because under
  a global `pane-border-status` a lone pane is a row shorter than its
  window (LEARNINGS 2026-10-19).
- **Car 05 (#528) — CHATS box order.** The CHATS box sorts by stable name
  with the rail's rule (one shared `tui.ChatLess`), so a swap, hide or show
  never moves a row, and the cursor follows its chat across a refresh.
- **Car 03 (#531) — attach + phone edges.** §Edge cases 1–4 were spiked and
  recorded below. Hidden chat sessions carry their own attach/detach hooks,
  so a direct attach follows its client and a detach re-pins it.
  `SwapAttached` keeps the fast swap off an attached session, and
  `internal/chatweb/width_test.go` pins 80- vs slot-width captures of 15
  live Claude states. Four pre-existing, width-independent chatweb
  capture bugs are follow-ups.
- **Car 04 (#533) — measured before/after + docs.** The table below.
  CLAUDE.md §Layout and the tmux-discipline skill carry the rules; no
  event or `--json` field changed; the orchestrator seed is unchanged
  (`gv chat sync-sizes` is internal).

### Measured: `main` vs the train tip (grove-533)

`e2e/focus-bench.sh` (not in `all.sh`; re-run as is, `BENCH_RUNS` /
`BENCH_BASE` to vary). Each build gets a fresh fixture per pass: scratch
HOME + `GROVE_STATE_DIR` + `GV_CLAUDE_CONFIG_DIR`, an isolated tmux server
(3.6a), 10 workspaces, and 200 archived transcripts in the cockpit's
project dir. The cockpit is 200x50 in focus with 3 chats running real
Claude Code 2.1.304 (`claude --resume` of a 300–900k transcript under a
scratch config dir, not logged in, no API calls), so each chat has a full
screen that repaints on SIGWINCH. Each figure is the median of 9 runs per
entry point, after one warm-up. Times are ms from the action, sampled
after each poll; one tmux exec per poll gives ~3–6ms resolution.
"Settled" is the target pane's capture in the slot staying unchanged for
300ms; the moment counted is its last change. "Rail redrawn" is the rail
pane's last capture change, under the same 300ms rule. tmux execs and
their summed time come from a separate pass with a logging shim on PATH,
counting the acting process (the `gv` CLI, the rail, the dash) over start
→ settled; the shim's own cost never touches the timing pass. Base
`origin/main` 6d2c912, tip a8a2657, macOS, 2026-10-19.

| entry | build | wall | → in slot | → settled | → rail redrawn | tmux execs | tmux exec time |
|---|---|---|---|---|---|---|---|
| `gv chat focus` | main | 347 | 234 | 480 | 351 | 52 | 211 |
| `gv chat focus` | tip | 182 | 103 | 107 | 188 | 10 | 43 |
| rail click | main | — | 246 | 488 | 366 | 55 | 219 |
| rail click | tip | — | 90 | 90 | 166 | 11 | 48 |
| CHATS `enter` | main | — | 229 | 472 | 366 | 62 | 244 |
| CHATS `enter` | tip | — | 112 | 112 | 190 | 10 | 44 |

On main every swap repaints on screen: the pane lands at ~230ms and
Claude's redraw settles it ~245ms later. On the tip the pane is settled
the moment it lands (26 of 27 runs; one CLI run took a later change at
348ms). Wall time is the CLI process's exit; a click or `enter` runs
inside the long-lived rail/dash process, so it has none. The `chat_hidden`
/ `chat_shown` events the swaps wrote have identical key sets on both
builds (the bench diffs them).

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

## Edge cases — spike (grove-531, car 03)

2026-10-19, tmux 3.6a, Claude Code 2.1.303, isolated `-L` servers; the
attach/two-client legs are now `e2e/focus.sh` (default and hostile conf).

1. **Direct attach** (`tmux attach -t '=grove-chat-…'`, the hints at
   chat.go:690, grove.go:1610/1631; `gv`'s own attach is a `switch-client`
   from inside tmux). Observed: a 100x30 pty client attached to a hidden
   window pinned at 139x50 stays at 139x50 — the client sees a slot-sized
   Claude clipped to its screen. `set -wu window-size` makes it follow the
   client at once (100x29 under a status line). **Fix:** every pinned
   hidden session carries its OWN hooks (session hooks, never global):
   `client-attached` and `client-session-changed` (fires on attach AND on
   a `switch-client` into the session) unset the window's `window-size`;
   `client-detached` runs `gv chat sync-sizes --session <cockpit>`, which
   re-pins it to the slot (by then `#{session_attached}` reads 0). The
   planner leaves a window with an attached client to that client and
   installs the hooks where they are missing (`#{m:*sync-sizes*,
   #{client-detached}}` tells grove's hook from an operator's global one).
   Leaving focus unsets them with the pin. Verified: attach → 100x29,
   detach → back at the slot, and the next swap into it is the fast path
   (no select-layout, byte-identical capture). Known limits: no hook fires
   on the session a client `switch-client`s AWAY from, so that window stays
   at the client's size until the next sync (a cockpit resize, a re-tile)
   — a swap into it meanwhile takes the slow path (one repaint), never a
   wrong one; a session-level hook shadows an operator's own global hook of
   the same name for these hidden sessions only.
   **What wins when the chat is shown while attached elsewhere: the
   cockpit.** `DecideSwap` never swaps into a session with a client
   (`SwapAttached` — the swap would leave that client watching the
   previously shown chat); the fallback's join takes the pane, the emptied
   session ends and its client is detached.
2. **Phone captures** (`chatweb`, via `tmux.CapturePane`): 15 live states,
   each captured at 80x24 and at 139x50 (`internal/chatweb/testdata/
   cc2.1.303-*-w{80,139}.txt`, `width_test.go`). `Waiting`, `SendRefusal`,
   `ClassifyTurn` and `DetectPicker`+`WithReview` give the same reading at
   both sizes, and the correct one at slot size, with these recorded
   exceptions — none a regression of the slot width:
   - unsent input wrapping to 3+ lines at 80 reads `unknown` (the ❯ leaves
     `classifyPaneOutput`'s bottom-5 window), and typed words "esc to
     interrupt" wrapping onto a continuation line read `running` (typedRe
     blanks only the ❯ line). Both read idle at 139 — the wider pane fixes
     them; the 80-col bugs remain for detached chats outside focus.
   - an option label is cut at its first wrap at both widths (the wrapped
     tail is indistinguishable from a description), so where it is cut
     depends on width; the key sent is the same. And `reviewPicks` misses
     2.1.303's `│ ● …` current question at both widths. Pre-existing,
     width-independent — follow-ups.
   - the taller pane (50 rows) looked like it pushed the folder-trust
     question out of `DetectPicker`'s 40-line lookback — but only in the
     raw fixture: `run()` trims the capture, so the blank rows under a
     top-drawn modal never reach it. The test reads captures exactly as
     `CapturePane` returns them.
   The phone's chat view is built from the transcript, not the pane;
   "show pane" puts the capture in a `white-space: pre; overflow: auto`
   box, so 139 columns scroll sideways further (80 already did on a
   phone) and never wrap or widen the page. Nothing to fix.
3. **Operator's own tmux config**: the hostile conf (`aggressive-resize
   on`, `set -g window-size smallest`, `pane-border-status top`) runs every
   attach leg green; `set -wu` falls back to the operator's global value.
4. **Two clients of different sizes on the cockpit session** (160x45 +
   120x40 pty clients; the cockpit window unpinned): tmux sized the
   cockpit window 120x39 (default `latest` and hostile `smallest` alike),
   the slot went 140x44 → 100x33, the window-resized hook re-synced every
   hidden chat to it, and a swap stayed on the fast path. When the 120x40
   client then `switch-client`ed into a hidden chat, the cockpit followed
   the remaining 160x45 client (slot 120x38) and the other hidden chats
   followed it again, while the switched-into chat followed its client.
   `window-size` of the cockpit window is still never touched by grove.
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
- **03 — attach + phone edges** (edge cases 1–4), after a short spike.
- **04 — measured before/after** on the dummy-data fixture (start →
  target visible, start → rail redrawn), plus docs: CLAUDE.md §Layout one
  line, docs/plugins.md if any event detail moves, the tmux-discipline
  skill gains the global-`window-size` rule.

## Out of scope

Keeping a persistent process in the dash to avoid `gv` start-up (would
violate the cockpit RAM rule), changing the horizontal layout, remote
chat swaps.
