# focus-mode 06: TRAINS box — one line per train with a progress bar; nothing else in the dash changes

Labels: `focus-mode`
Depends on: nothing in this train (can run in parallel with 01). Does not touch chats or tmux.

## Goal
FEATURES becomes TRAINS: one line per open train — slug, a progress bar of cars landed over cars total, how far behind base, serve state — and the "#N is waiting on you" line under a train only when one of its cars is in `question`. The seven-car list of today's box collapses into the bar. AGENTS and ACTIVITY are unchanged; **no key changes** (the AGENTS fold from the mockup is deferred until the keybind question is settled).

## Design
- **Row:** `<slug>  ▰▰▰▱▱▱▱ 3/7 · ↓3 main · serve ▶` — bar cells = cars (max 12 cells, beyond that the `3/7` carries it), filled = landed, a car with an open PR drawn in the car's glyph colour, empty = queued/working. Behind-base and serve state exactly as `gv feature ls --json` reports them today. Pure render function, golden-tested, one line per train; the `question` line keeps today's exact copy.
- **Row budget:** the lines freed go to ACTIVITY through the existing `rowBudgets` math; no new allocation per frame (cockpit-RAM rule).
- Header counter and footer legend unchanged.

## Acceptance criteria
- [ ] Golden frames: (a) one train, 7 cars, 3 landed, 1 with PR, ↓3; (b) a train with a car in `question` keeps its waiting line; (c) two trains; (d) no trains → box absent, as today; (e) 14 cars → bar capped at 12 cells, `n/14` shown.
- [ ] Row budget test: ACTIVITY gains the rows the old car list used.
- [ ] Existing per-frame alloc test shows no increase (name it in the PR).
- [ ] `git diff --stat` touches no key handler and no tmux code.
- [ ] TASKS.md row.

## Out of scope
AGENTS fold, NEEDS YOU, since-you-looked digest, ACTIVITY run-collapse, status-bar renames — later, after the MVP has been lived with.

## Train
Feature train `focus-mode` — branch `feature/focus-mode`, base `main`. **Your PR targets `feature/focus-mode`, never `main`.** Mockup: https://claude.ai/artifact/DZm15hBGJFgV1tEhKAZFy1 (left pane, TRAINS box only).

## Gate
- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .` empty. `e2e/all.sh` green (`shipping-gates` first). The TUI never grows: no new goroutines, polls, caches, or per-frame allocations.
