# focus-mode 05: click a rail box to focus that chat — mouse handled inside the rail pane

Labels: `focus-mode`
Depends on: focus-mode 04 merged into the train.

## Goal
In `focus`, a left click on a chat's box in the rail does what `gv chat focus <that chat>` does: the shown chat is minimized into the rail and the clicked one takes its place. No tmux key or mouse binding changes: the rail is a bubbletea program in its own pane, and tmux's `mouse on` already delivers clicks to the pane under the pointer.

## Design
- **Hit-testing:** `renderRail` (car 02) already fixes each box's column span; expose it as `railHit(x int) (chatSession string, ok bool)` from the same layout computation, pure and table-tested, so the click and the drawing can never disagree.
- **Action:** on `tea.MouseLeft` release inside a box, call car 04's internal focus helper directly (never shell out to `gv`). Clicking the `+N more` box does nothing. A stale box (the chat closed between render and click) → the rail flashes `gone` for one tick, nothing mutated.
- **Feedback:** the clicked box inverts for one tick before the swap; after the swap the cockpit's next `@grove_rail` write removes it from the rail (it is now on screen).
- **Keyboard:** none. The rail pane takes no keys (a stray focus on it must not eat typing; `esc`/any key is ignored, and the swap helper ends by selecting the chat pane so the cursor lands where the operator will type).
- **Wheel:** ignored in this car.

## Acceptance criteria
- [ ] `railHit` table tests at 80 and 160 cells with 1, 4 and 9 boxes, including separators and the overflow box.
- [ ] Model test: a left click at a box's column calls the focus helper with that chat's session (fake runner); a click on a separator or `+N more` calls nothing.
- [ ] Isolated-socket test (reuse car 04's fixture: dash + A shown, B and C minimized): drive the swap through the rail model's mouse handler → dash + B; A minimized; focus on the chat pane afterwards (`#{pane_active}`).
- [ ] Stale box → no tmux mutation (fake runner asserts zero calls).
- [ ] `tmux list-keys -T root` is byte-identical before and after the cockpit build (no binding added).
- [ ] TASKS.md row.

## Out of scope
Right-click menus, wheel, drag, keyboard navigation of the rail, a prefix key table (#421).

## Train
Feature train `focus-mode` — branch `feature/focus-mode`, base `main`. **Your PR targets `feature/focus-mode`, never `main`.** Mockup: https://claude.ai/artifact/DZm15hBGJFgV1tEhKAZFy1.

## Gate
- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .` empty. `e2e/all.sh` green (`shipping-gates` first). `tmux-discipline` before tmux; isolated socket, `unset TMUX`.
