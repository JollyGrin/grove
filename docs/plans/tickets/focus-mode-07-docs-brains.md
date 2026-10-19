# focus-mode 07: docs + brains + e2e wrap — CLAUDE.md, orchestrator seed, plugin contract, HANDOFF

Labels: `focus-mode`, `rote`
Depends on: focus-mode 01–06 merged into the train.

## Goal
Everything a fresh operator, a fresh orchestrator chat, and a plugin author need to know about the two layouts, focus mode, `gv chat focus`, the rail, rail clicks, and the TRAINS box — written where each of them reads.

## Scope (enumerated — rote)
- `CLAUDE.md` §Layout: one sentence on the two layouts and `L`; one on focus mode hiding chats and `gv chat focus`.
- `.grove/orchestrator/CLAUDE.md` (the seed, `orchestrator/CLAUDE.md`) tools block: `gv chat focus <s>` beside `chat close`; a line under "Dispatch-and-dismiss" noting that in focus mode a closed chat leaves the window dash-only and the operator picks the next with enter. Re-stamp the seed per the brain procedure (memory `orchestrator-seed-brain-gap`).
- `docs/plugins.md`: any additive field or event data added in cars 02–04 (`reason: "focus"` on `chat_hidden`, if car 03 added it); `e2e/plugin.sh` stays green.
- `docs/grove-cockpit-design.md`: a dated "Status 2026-10" paragraph pointing at the mockup and the train; do not rewrite the doc.
- `HANDOFF.md`: the train, its cars, and what is deferred (NEEDS YOU, brief popup, since-you-looked, status-bar diet, prefix key table #421, remote chats in focus, AGENTS fold).
- `.claude/skills/tmux-discipline/SKILL.md`: one rule if cars 02 or 05 learned one about session-scoped `status` / `status-format`  (only if LEARNINGS.md got the entry first — a note moves up when it proves out).
- `e2e/all.sh`: confirm the focus scripts from cars 02–04 are wired; run the whole suite once on a scratch socket and paste the tail.
- `gv update` note for the operator in the PR body: after merge, `gv update --yes` on both hosts; restart `gv-chat.service` on groveremote.

## Acceptance criteria
- [ ] `go test ./internal/guidance` green (TASKS.md/LEARNINGS.md caps).
- [ ] `orchestrator/seed_test.go` green with the re-stamped seed.
- [ ] `e2e/all.sh` green, tail in the PR.
- [ ] `grep -rn "vertical\|tiled" docs CLAUDE.md HANDOFF.md` names only the retirement note.
- [ ] TASKS.md row.

## Train
Feature train `focus-mode` — branch `feature/focus-mode`, base `main`. **Your PR targets `feature/focus-mode`, never `main`.**

## Gate
- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .` empty. `e2e/all.sh` green (`shipping-gates` first).
