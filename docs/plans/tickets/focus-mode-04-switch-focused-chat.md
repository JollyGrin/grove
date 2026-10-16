# focus-mode 04: switch the focused chat — `gv chat focus <s>`, and chat-hide's existing show key does the same in `focus`

Labels: `focus-mode`
Depends on: focus-mode 03 merged into the train.

## Goal
In `focus`, pick which chat is shown without leaving the layout. One verb does the swap. **No new keybindings:** the CHATS box key that already means "show this hidden chat" (chat-hide car 03, `enter` on a CHATS row) keeps its key and, in `focus`, shows by swapping — the current chat is minimized, the chosen one takes its place. In `horizontal` it keeps its chat-hide meaning untouched.

## Scope
- `cmd/gv/chat_*.go`: `gv chat focus <session|session-id|prefix>` — resolve with `chat.Match` rules (as `gv chat show`). Layout `focus`: minimize the shown local chat (append to `@grove_focus_hid`), show the target (`ShowChatPane`, focused), drop the target from `@grove_focus_hid` if it was there, `RetileFocus`, rewrite `@grove_rail`. Layout `horizontal`: behave exactly as `gv chat show` and say so in the `✓` line. Appends `chat_hidden` then `chat_shown` (existing records), each after its move.
- `internal/tui/chatkeys.go`: the existing show key on a CHATS row calls the same internal helper when `@grove_layout == focus` (never a shell-out to `gv`). Key map unchanged; the help text for that key gains "(in focus: swaps)". The CHATS box marks the shown chat (`▶` on its row; table-tested).
- Closing the shown chat still leaves the dash alone (car 03); the operator picks the next from the CHATS box.

## Refusals (message says what to do instead; nothing mutated before)
- Target is the chat already shown → friendly no-op.
- Target is a remote chat (`@grove_remote` / `@<host>/<session>`) → "remote chats: `gv chat show` only, for now".
- Cockpit session not running → the message `gv chat show` gives.

## Acceptance criteria
- [ ] Isolated-socket test: focus with dash + A shown, B and C minimized. `gv chat focus <B>` → dash + B; A minimized and in `@grove_focus_hid`; B removed from it; pane ids and pids unchanged. Then C, then A: option ends `{B, C}`; the rail boxes follow (shown chat never has one).
- [ ] The CHATS-row show key in focus hits the same helper (tea model test with a fake runner, as `chatkeys_test.go` does); in horizontal the tmux calls are identical to chat-hide's.
- [ ] `gv chat focus` in horizontal == `gv chat show` (same tmux calls).
- [ ] Each refusal has a test.
- [ ] `events.jsonl`: `chat_hidden` then `chat_shown` per swap, each after its move.
- [ ] e2e: extend car 03's script with one swap.
- [ ] TASKS.md row.

## Out of scope
New keys (digits 1–8 already spawn profile chats; a prefix key table is #421), the rail click (car 05, inside the rail pane), remote chats.

## Train
Feature train `focus-mode` — branch `feature/focus-mode`, base `main`. **Your PR targets `feature/focus-mode`, never `main`.** Mockup: https://claude.ai/artifact/DZm15hBGJFgV1tEhKAZFy1.

## Gate
- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .` empty. `e2e/all.sh` green (`shipping-gates` first). `tmux-discipline` before tmux; isolated socket, `unset TMUX`.
