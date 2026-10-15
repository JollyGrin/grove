# focus-mode 01: two layouts — `horizontal` | `focus` — and `L` toggles between them

Labels: `focus-mode`, `rote`

## Goal
The cockpit has exactly two pane layouts. `horizontal` is today's default (tmux `even-horizontal`: the dash and every chat as equal columns). `focus` is tmux `main-vertical` with the dash as the main pane at its tested width (`cockpitMainWidth`) and the chats stacked right. `L` toggles between the two. `vertical` and `tiled` are gone.

This car is the plumbing only: in `focus` every chat is still visible (stacked). Car 02 makes `focus` show one chat.

## Scope (enumerated — this is a rote ticket)
- `internal/tmux/grove.go`: `tmuxLayout` maps `focus` → `main-vertical`, anything else → `even-horizontal`; `SelectLayout("focus")` takes the `MainVertical(session, cockpitMainWidth)` path that `vertical` takes today. Drop the `vertical`/`tiled` cases.
- `internal/tui/tui.go`: `nextLayout` flips `horizontal` ↔ `focus`; the `L` handler is otherwise unchanged (session option `@grove_layout` stays the source of truth). Flash reads `layout: focus` / `layout: horizontal`.
- `internal/config/config.go`: `CockpitLayout()` returns `focus` for `focus`, `horizontal` for everything else (including the retired `vertical`/`tiled` — a stale config line must never break the cockpit). Update the field comment.
- `cmd/gv/main.go` (cockpit build ~1085–1113) needs no change beyond compiling; verify `cockpit.layout: focus` builds a main-vertical window.
- Footer legend / help (`internal/tui/footer.go`, `help.go`): the `L` hint reads "L focus/horizontal". Goldens regenerate if a golden frame carries the hint.
- Docs: the `cockpit.layout` line in `docs/*` and the wizard's default config template, if either names the retired values (`grep -rn "tiled\|vertical" docs internal/wizard`).

## Acceptance criteria
- [ ] `go test ./internal/tmux ./internal/tui ./internal/config` has table tests: `tmuxLayout("focus") == "main-vertical"`, `tmuxLayout("horizontal") == "even-horizontal"`, `tmuxLayout("tiled") == "even-horizontal"`; `nextLayout("horizontal") == "focus"`, `nextLayout("focus") == "horizontal"`, `nextLayout("anything") == "focus"`; `CockpitLayout()` for `""`, `focus`, `vertical`, `tiled`, `garbage`.
- [ ] Isolated-socket test: a cockpit window with a dash pane and 2 chat panes, `SelectLayout(s, "focus")` → `#{window_layout}` is main-vertical and the first pane holds `cockpitMainWidth`% ± 1 column; `SelectLayout(s, "horizontal")` → even-horizontal.
- [ ] `grep -rn '"vertical"\|"tiled"' internal cmd` returns nothing but the config forgiveness case.
- [ ] TASKS.md row.

## Out of scope
Hiding chats, the rail, cockpit folds, docs beyond the lines named above.

## Train
Feature train `focus-mode` — branch `feature/focus-mode`, base `main`. **Your PR targets `feature/focus-mode`, never `main`.** Mockup: https://claude.ai/artifact/DZm15hBGJFgV1tEhKAZFy1 (section "If we started over"; ignore NEEDS YOU, the brief and the popup — dropped for the MVP).

## Gate (paste output tails into the PR body)
- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .` empty.
- `e2e/all.sh` green — load the `shipping-gates` skill first (piped-gate trap).
- Load `tmux-discipline` before touching tmux. All tmux testing on an isolated socket with `unset TMUX` — never the operator's live server.
