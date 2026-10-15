# focus-mode 05: click a rail cell to focus that chat — `range=user` cells + a wrapped `MouseDown1Status`

Labels: `focus-mode`
Depends on: focus-mode 04 merged into the train.

## Goal
In `focus`, a left click on a chat's rail cell does what `gv chat focus <that chat>` does. The operator's existing status-line click behaviour is preserved everywhere else: other status lines, other sessions, clicks between cells.

## Verified on the operator's tmux 3.6a (2026-10-15)
- `#[range=user|X]…#[norange]` in a `status-format` marks a clickable cell; a click there fires the `Status` mouse key with `#{mouse_status_range}` = `X` (max 15 bytes) and `#{mouse_status_line}` = the line index (the rail is line `1`).
- The operator's root binding is NOT tmux's default: `bind-key -T root MouseDown1Status switch-client -t =`. tmux key bindings are server-global; there is no per-session binding. So the only safe shape is a **wrapper that keeps the previous command as its else branch**.

## Design
- **Cell names:** car 02's `renderRail` wraps each cell in `#[range=user|c<n>]…#[norange]` where `<n>` is the chat's number in `gv chat ls` order (fits 15 bytes; a table maps `c<n>` back to the chat session at click time — never trust the number alone across a reorder: resolve against the rows the rail was rendered from, stored in `@grove_rail_map` as `c<n>=<session>` pairs).
- **The binding, installed at cockpit build (idempotent):**
  `bind-key -T root MouseDown1Status if-shell -F '#{&&:#{==:#{mouse_status_line},1},#{m:c[0-9]*,#{mouse_status_range}}}' 'run-shell -b "gv chat focus --cell #{mouse_status_range} --session #{session_name}"' '<previous command>'`
  where `<previous command>` is whatever `list-keys -T root MouseDown1Status` reported before grove touched it (`switch-client -t =` on the operator's machine; tmux's own default elsewhere). A marker in the wrapper (e.g. the literal `gv chat focus --cell`) makes install idempotent: if the current binding already contains it, leave it; never wrap a wrapper. Unbind on nothing — grove never removes it; `gv doctor` reports it as "grove rail click binding: installed (wraps: …)" so the operator can see and remove it by hand.
- **`gv chat focus --cell c<n> --session <s>`:** reads `@grove_rail_map` on that session, resolves the chat, and runs exactly car 04's focus path. A stale cell (chat closed between render and click) → one `display-message` on that session, nothing mutated. `run-shell -b` keeps the click non-blocking.
- **Gating:** the wrapper's condition is false on every status line but `1` and every range not named `c<n>`, so non-grove sessions, the window list, and the operator's `status-right` all keep their behaviour. Only grove ever emits a `c<n>` range.
- **No TUI key changes.** This is a mouse binding on tmux's status line, not a cockpit key.

## Acceptance criteria
- [ ] Isolated-socket test: set a custom `MouseDown1Status` (`switch-client -t =`) on the socket, build the cockpit → `list-keys -T root MouseDown1Status` is the wrapper and its else branch is byte-for-byte the custom command; build again → unchanged (idempotent); a socket with tmux's default binding → the else branch is that default.
- [ ] `renderRail` golden: each cell carries `#[range=user|c<n>]` … `#[norange]`; `@grove_rail_map` matches.
- [ ] `gv chat focus --cell c2 --session <s>` on a socket with dash + A shown, B and C minimized → dash + B (reuses car 04's test fixture); `--cell c9` (no such chat) → message, no tmux mutation.
- [ ] `gv doctor` line present with the wrapped command shown.
- [ ] `tmux send-keys -M`-style synthetic click is unreliable across versions; if a real click cannot be driven in a test, say so in the PR and cover the binding text + the `--cell` verb instead. Do not spend more than one attempt on synthetic mouse events.
- [ ] TASKS.md row; LEARNINGS.md entry on `range=user` / `mouse_status_line` and the wrapper shape (this is a verified surprise worth keeping).

## Out of scope
Right-click menus, wheel on the rail, clicks in `horizontal` (no rail there), a prefix key table (#421).

## Train
Feature train `focus-mode` — branch `feature/focus-mode`, base `main`. **Your PR targets `feature/focus-mode`, never `main`.** Mockup: https://claude.ai/artifact/DZm15hBGJFgV1tEhKAZFy1.

## Gate
- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .` empty. `e2e/all.sh` green (`shipping-gates` first). `tmux-discipline` before tmux; isolated socket, `unset TMUX`. **Never install the binding on the operator's live server from a test.**
