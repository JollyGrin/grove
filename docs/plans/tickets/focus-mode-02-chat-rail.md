# focus-mode 02: the chat rail — tmux status line 2 on the cockpit session, shown in `focus`

Labels: `focus-mode`
Depends on: focus-mode 01 merged into the train (the `focus` layout name). Does NOT depend on PR #411.

## Goal
A second tmux status line across the bottom of the cockpit window lists every live chat of the workspace — number, state glyph, short label — so a chat that is not on screen is still one glance away. This is the "minimized" surface that car 03 minimizes into. It is drawn by tmux from a session option grove writes; nothing polls, nothing spawns, nothing is rendered into a chat pane.

Shown when the session's `@grove_layout` is `focus`; in `horizontal` the session keeps the operator's single status line.

## Design
- **Session-scoped only, never `-g`:** `set-option -t =<session> status 2` and `status-format[1] "#{@grove_rail}"` when the layout becomes `focus` (cockpit build and the `L` handler); `status on` and the two options unset when it becomes `horizontal`. The operator's global `status-right` / `status-format[0]` are never touched. `tmux-discipline` applies: a test proves no `-g` and no write to any non-grove session.
- **Content:** `@grove_rail` is one pre-styled string grove renders: one cell per live chat in `gv chat ls` order — `#[fg=…]<glyph> <n> <label>#[default]`, two spaces between cells; a chat currently in the cockpit window gets reverse video (in car 03 that is "the focused one"; here it is every shown chat). Glyph from `chat.Row.Turn`: `running ●`, `waiting ?`, `idle ✓`, `errored !`, anything else `·`. Label = the chat's ticket if it has one, else the row label rune-safe-truncated (grove-131's helper) to a cell budget of window width ÷ chat count, min 12. Pure function, table-tested: `renderRail(rows, shownPanes, width) string`.
- **Writer:** the cockpit TUI, from the same chat rows it already fetches (no new goroutine, no new poll — cockpit-RAM rule). It writes the option only when the rendered string changed, then `refresh-client -S` so tmux redraws now rather than at `status-interval`.
- **No keys in this car.** Switching is car 04 (verb + existing CHATS key) and car 05 (click on a cell). Wrap each cell in `#[range=user|c<n>]…#[norange]` now so car 05 only adds the binding; write the `c<n>=<session>` pairs to `@grove_rail_map` beside `@grove_rail`.

## Acceptance criteria
- [ ] `renderRail` table tests: glyph per `Turn`, highlight on shown chats, truncation at narrow widths, zero chats → `""`, label prefers ticket.
- [ ] Isolated-socket test: layout → focus: `show-options -t =<s> status` is `2` and `status-format[1]` is `#{@grove_rail}`; layout → horizontal: `status` is `on`, both options unset. `show-options -g` is byte-identical before and after.
- [ ] The TUI writes the option only on change (fake runner counts `set-option` calls across 3 identical ticks → 1).
- [ ] e2e on a scratch socket: build a cockpit with `cockpit.layout: focus` and one chat, `capture-pane` the window, the chat's label appears on the rail line.
- [ ] TASKS.md row; LEARNINGS.md if tmux surprised you (status-format evaluation, `refresh-client` scope).

## Out of scope
Hiding anything (car 03), switching (car 04), the click binding (car 05), per-chat last-message text (#416), the Claude Code statusLine.

## Train
Feature train `focus-mode` — branch `feature/focus-mode`, base `main`. **Your PR targets `feature/focus-mode`, never `main`.** Mockup: https://claude.ai/artifact/DZm15hBGJFgV1tEhKAZFy1 (the `chats ›` strip under the mock; card "tmux · status line 2").

## Gate
- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .` empty. `e2e/all.sh` green (`shipping-gates` skill first). `tmux-discipline` before tmux; isolated socket, `unset TMUX`.
