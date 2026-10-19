# focus-mode 02: the chat rail — a short grove-drawn pane beneath the chat, one box per minimized chat, in `focus` only

Labels: `focus-mode`
Depends on: focus-mode 01 merged into the train (the `focus` layout name). Does NOT depend on PR #411.

## Goal
In `focus`, a short pane sits directly beneath the chat pane in the right column and shows one box per chat that is not on screen: its state glyph, number and ticket or label on the first line, its state and age on the second. It is how a minimized chat stays one glance away. It is drawn by grove (`gv chat rail`), lives only while the layout is `focus`, and is never a chat: no chat verb may hide, show, close or list it.

**Not the bottom bar.** tmux's status line (the window list the operator clicks to open a worker window) is untouched by this train.

## Design
- **The pane:** split from the right column under the chat slot (`split-window -v -l <h>` against the chat pane, or against the dash's right neighbour when no chat is shown), running `gv chat rail --session <s>`. Height `h` = 4 rows (one border/title row + two content rows + one spare; make it a constant, not config). Stamped `@grove_rail 1` so every pane classifier (`chat.IsOrchestratorPane`, `PaneClosable`/`closablePane`, `gv chat ls`, chat-hide's hide/show) treats it like the dash: not a chat, not closable by chat verbs, not listed. `pane-border-format` shows `chats` for it (same mechanism as the profile/remote tags).
- **Lifecycle:** created when the layout becomes `focus` (cockpit build and the `L` handler); killed when it becomes `horizontal`. Exactly one per cockpit window; a build that finds one already stamped reuses it. If its process dies, the next `L`/build respawns it; nothing else watches it (no supervisor goroutine).
- **Re-tile rule for `focus` (used by this car and every later one):** `select-layout main-vertical` → `resize-pane -t <rail> -y <h>` → `select-pane -t <chat>` (or the dash when no chat). tmux's layouts give stacked panes equal height, so the resize after every re-tile is the whole trick; put it in one helper `tmux.RetileFocus(session)` and make every focus-mode path call it.
- **Data:** the rail is a dumb viewer, not a second poller. The cockpit TUI already fetches chat rows; it writes the rendered rail content (one line per box, tab-separated fields, plus which chat is shown) to the session option `@grove_rail` only when it changed. The rail program reads that option on a 2s tick (one `show-options` call; no `gv chat ls`, no gh, no transcript reads) and redraws. When the cockpit is not running the rail shows its last content and a `· stale` mark after 30s.
- **Boxes:** `renderRail(rows, shown, width) string` is pure and golden-tested. One box per chat NOT currently in the cockpit window (minimized by focus, or hidden by hand — both are off-screen, both get a box). Line 1: `<glyph> <n> <ticket|label>` with the glyph from `chat.Row.Turn` (`running ●`, `waiting ?`, `idle ✓`, `errored !`, else `·`) and the label rune-safe-truncated (grove-131 helper); line 2: `<turn> · <age since last_active>`. Box width = pane width ÷ box count, min 16 cells; beyond what fits, the last box reads `+N more`. Boxes are separated by `│`, no outer frame (the pane border is the frame). Last-message text is NOT in this car (that is #416's panel; when it lands, line 2 takes it).
- **No mouse in this car** (car 05). Bubbletea mouse mode may be enabled now so the plumbing exists, but clicks do nothing until car 05.
- **Memory:** one small resident Go process per cockpit. Measure it (`ps -o rss`) and put the number in the PR body; keep it under 20 MB RSS at rest. No per-tick allocation beyond the parse of `@grove_rail`.

## Acceptance criteria
- [ ] `renderRail` goldens: 0 boxes (`no minimized chats`), 1, 4, and 9 boxes at 80 and 160 cells; glyph per `Turn`; ticket preferred over label; `+N more` overflow.
- [ ] Isolated-socket test: layout → focus creates exactly one pane stamped `@grove_rail 1` under the chat slot at height `h`; a second build reuses it; layout → horizontal kills it and only it (chat pane ids and pids unchanged).
- [ ] `RetileFocus` after adding a third pane leaves the rail at height `h` and the dash at `cockpitMainWidth`%.
- [ ] `gv chat ls --json` never lists the rail; `gv chat hide` / `show` / `close` aimed at it refuse with "that is the rail, not a chat".
- [ ] The cockpit writes `@grove_rail` only on change (fake runner: 3 identical ticks → 1 `set-option`).
- [ ] The rail process reads only `show-options` on its tick (fake runner asserts the command set).
- [ ] e2e on a scratch socket: build with `cockpit.layout: focus`, one chat hidden by hand, `capture-pane` the rail → its box shows the chat's label.
- [ ] RSS of `gv chat rail` at rest in the PR body.
- [ ] TASKS.md row; LEARNINGS.md if tmux surprised you (split targets, resize after select-layout).

## Out of scope
Minimizing (car 03), switching (car 04), mouse (car 05), last-message text (#416), anything on tmux's status line.

## Train
Feature train `focus-mode` — branch `feature/focus-mode`, base `main`. **Your PR targets `feature/focus-mode`, never `main`.** Mockup: https://claude.ai/artifact/DZm15hBGJFgV1tEhKAZFy1 — the boxes under the chat in the FIRST version's layout are the target; the `chats ›` status strip in the current version is NOT.

## Gate
- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .` empty. `e2e/all.sh` green (`shipping-gates` skill first). `tmux-discipline` before tmux; isolated socket, `unset TMUX`. The cockpit TUI never grows: no new goroutines, polls, caches, or per-frame allocations (the rail is its own process by design).
