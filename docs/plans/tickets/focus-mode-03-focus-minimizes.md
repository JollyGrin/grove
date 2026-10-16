# focus-mode 03: `focus` shows one chat and minimizes the rest to the rail — enter, leave, spawn

Labels: `focus-mode`
Depends on: focus-mode 02 merged into the train; **PR #411 (chat-hide train) merged into main and merged forward into `feature/focus-mode`** — this car uses `HideChatPane` / `ShowChatPaneUnfocused` from it.

## Goal
In `focus`, the cockpit window holds the dash and ONE chat. Every other local chat is **minimized**: its pane leaves the window (chat-hide's `break-pane` into a `grove-chat-<label>-<n>` session — the process keeps running, the pane id and pid are stable) and its cell in the rail (car 02) is what you see of it. Toggling back to `horizontal` restores exactly the chats focus mode minimized, and re-tiles. Spawning a chat in `focus` minimizes the shown one and shows the new one, so the thing you just started is the thing you see.

"Minimize" is one operation with two halves — the pane move and the rail cell — and this car ships both together: a chat is never off-screen without a rail cell. This is the deliberate reversal of chat-hide's "nothing auto-hides" invariant, scoped to the `focus` layout only. In `horizontal` nothing moves.

## Design (decisions made; keep them in pure, table-tested helpers)
- **Which chat stays shown on entering focus:** the active pane if it is a chat; otherwise the most recently active chat pane; if there is no chat, the window is just the dash.
- **Remembering what focus minimized:** a session user option `@grove_focus_hid` holding the space-separated chat session names focus mode minimized. Leaving focus restores exactly those (`ShowChatPaneUnfocused`, then one re-tile) and clears the option. A chat the operator hid by hand (`h`, `gv chat hide`) is NOT in the list and stays hidden — their bookmark, not ours. It still has a rail cell, same as any live chat.
- **Rail highlight:** the shown chat is the reverse-video cell; minimized chats are plain. Rewrite `@grove_rail` after every move (the cockpit's writer from car 02 picks it up on the next tick; the CLI paths rewrite it directly since they have the rows in hand).
- **Spawn in focus** (`O`, `)`, digits 1–8, `gv orchestrator new`, `--brief`, `--profile`): minimize the shown chat (append to `@grove_focus_hid`), spawn, re-tile main-vertical. One helper on the spawn path, called only when `@grove_layout == focus`. **No key changes:** the same keys do the same thing; only what happens to the previous chat differs.
- **Remote chat panes (`@grove_remote`)** are left visible in this car: the swap minimizes local chats only. If a remote pane is present the window may show two chats; accepted for the MVP.
- **Closing the shown chat** (`gv chat close`, `gv orchestrator close`): the window drops to dash-only, no auto-promotion; the next chat is picked in car 04.
- **Safety:** never touch the dash pane; never a `:0` target (`base-index 1` safe, grove-168); the event is appended after its tmux move (chat-hide's #412 — fix it here if it is still open rather than inherit it). Records: existing `chat_hidden` / `chat_shown`; an additive `reason: "focus"` is allowed and, if added, documented in `docs/plugins.md` with `e2e/plugin.sh` green.

## Acceptance criteria
- [ ] Isolated-socket test: dash + 3 chats, chat 2 active. `L` → focus: window has 2 panes (dash + chat 2); sessions `grove-chat-<label>-1` and `-2` exist holding chats 1 and 3, same `%pane` ids and pids; `@grove_focus_hid` names both; `@grove_rail` has three cells with chat 2 highlighted. `L` → horizontal: 4 panes, option cleared, even-horizontal, `status on`.
- [ ] Same test with chat 1 hidden by hand BEFORE entering focus: after leaving focus, chat 1 is still hidden and still in the rail.
- [ ] Spawn in focus: dash + chat A → `O` → dash + new chat; A minimized, listed in `@grove_focus_hid`, rail highlights the new chat.
- [ ] Spawn in horizontal is byte-identical to today (a test asserts no hide call when the layout is horizontal; `git diff` shows the spawn helpers gated on the layout).
- [ ] Cockpit build with `cockpit.layout: focus` and no chats → dash only, rail empty, no error.
- [ ] `gv chat ls --json` after entering focus reports the minimized chats as `kind: chat`, `writable: true`, with no new field (or exactly the one additive field, documented).
- [ ] e2e script on a scratch socket: enter → ls → leave; wired into `e2e/all.sh`.
- [ ] TASKS.md row; LEARNINGS.md for any verified surprise.

## Out of scope
Choosing which chat is shown (car 04), remote-pane minimizing, keybindings inside chat panes (#421), any new cockpit key.

## Train
Feature train `focus-mode` — branch `feature/focus-mode`, base `main`. **Your PR targets `feature/focus-mode`, never `main`.** Mockup: https://claude.ai/artifact/DZm15hBGJFgV1tEhKAZFy1 (card "tmux · hide / show" and the rail).

## Gate
- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .` empty. `e2e/all.sh` green (`shipping-gates` first). `tmux-discipline` before tmux; isolated socket, `unset TMUX`.
