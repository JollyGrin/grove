# Handoff — picking grove up fresh

> The complete pickup path; nothing about grove lives only in someone's
> head. The 2026-07-03 original with its dated progress updates is
> archived at [docs/archive/HANDOFF-2026-07-03.md](docs/archive/HANDOFF-2026-07-03.md);
> the narrative behind each era is in [docs/journal.md](docs/journal.md).

## What this repo is

Grove is the generalized, OSS-ready successor to `overstory-tui` (`ovs`) —
the operator's Go CLI that turns tickets into autonomous Claude Code
sessions (worktree + tmux + kickoff → PR). Overstory was the solo trial
run; grove drops into **any** repo or parent-of-repos, wizards itself into
readiness, and — configured with the Grid *pack* — must behave exactly
like hand-tuned ovs with zero hardcoding. Then ovs retires and teammates
adopt grove.

Grove is the live daily driver and builds itself: the backlog is GitHub
issues on this repo (`grove-N` = issue #N), worked by grove workers. A
remote host (**groveremote**, a VPS reached via Tailscale SSH, provisioned
per `docs/remote-host-setup.md`) is wired into the global `hosts:` config;
`gv handoff --to/--from` has moved live tasks both ways.

## What you do first

1. **Read, in order:** [CLAUDE.md](CLAUDE.md) (rules and what to read
   when), this file, [TASKS.md](TASKS.md) §Now, and the `.claude/skills/`
   whose trigger matches your task. [LEARNINGS.md](LEARNINGS.md),
   [DESIGN.md](DESIGN.md), [docs/roadmap.md](docs/roadmap.md), and the
   `docs/` designs only as the work demands
   ([docs/seed-manifest.md](docs/seed-manifest.md) when touching a copied
   package). Driving grove as an operator?
   [docs/GETTING-STARTED.md](docs/GETTING-STARTED.md).
2. **Do not relitigate the locked decisions** — the table is in
   [docs/grove-readiness-review.md](docs/grove-readiness-review.md) §5
   (name/binary, dedicated worker profile default, no trust gate until
   revisit-before-public, pack terminology, native-Go providers,
   LEARNINGS.md as L2, orchestrator skip-permissions default, goreleaser
   from day one).
3. **`~/git/thegrid/overstory-tui` is frozen** (CLAUDE.md hard rule). It
   is also your reference: when a copied package confuses you, diff
   against upstream and read its DESIGN.md/LEARNINGS.md.

## Structural traps that predate the skills and still hold

The live-incident traps (`$TMUX` isolation, the piped gate, `go install`
from a branch, Claude Code hook/transcript mechanics) are in the skills —
the distillation is canonical, LEARNINGS.md + `docs/archive/` is the
dated log behind it. Four that no skill carries:

- **Both ovs and gv hooks fire on the same worker sessions** during any
  transition window. "Not my session" must key on *task ownership in this
  workspace's tasks.json*, NOT on workspace resolution — an ovs-created
  worktree can resolve to a grove workspace via the `.grove/` walk-up
  (DESIGN.md §12, design review I-6).
- **The parity gate's byte-comparison is against an empty learnings
  corpus** (docs/grove-connections-design.md §8.2) — don't chase
  byte-parity once learnings inject.
- **A git-inited $HOME shadows parent-folder detection** — `gv init` once
  made HOME the workspace because a dotfiles repo enclosed the cwd;
  parent-of-repos detection tests the cwd itself first.
- The state/tmux/git/worktree/detect/transcript packages were copied into
  ovs from parkranger and have survived two tools — treat them as the
  most-proven code in the tree.
- **A cockpit's memory is measured, not guessed** (grove-580). Every
  long-lived verb (dash, chat serve, supervise, watch) arms
  `internal/memprof`: `kill -USR2 <pid>` writes
  `<state>/profiles/<verb>-<pid>-<ts>.pprof` + `.goroutines` and the
  process keeps running — `go tool pprof -top ~/go/bin/gv <file>` reads
  it, `less` the goroutine dump (debug=2: every goroutine with how long
  it has waited). USR1 is the rail's wake; a binary older than the seam
  (≤ v0.1.84) dies on USR2 — check `gv version` first. Footprint is
  `vmmap -summary <pid>` ("Physical footprint"), never `ps rss`.

## Train in flight: focus-mode

Feature train `focus-mode` (branch `feature/focus-mode`; mockup
https://claude.ai/artifact/DZm15hBGJFgV1tEhKAZFy1). Cars: #485 two layouts
(`horizontal` | `focus`, `L`) · #486 the chat rail pane · #487 focus
minimizes (enter/leave/spawn) · #488 `gv chat focus <s>` · #489 rail click
· #490 TRAINS box · #491 docs. Deferred, not built: NEEDS YOU, the brief
popup, since-you-looked, status-bar diet, the prefix key table (#421),
remote chats in focus (a remote chat is never minimized or swapped in), the
AGENTS fold.

## Train: focus-swap (cars done; its PR into main is the operator's call)

Feature train `focus-swap` (branch `feature/focus-swap`; design + measured
before/after: docs/plans/2026-10-19-focus-same-size-swap-design.md). Landed:
#525 hidden chat windows pinned to the slot's PANE size (`gv chat
sync-sizes`, internal; `internal/tmux/slot.go`) · #526 a focus swap is one
`swap-pane` + `rename-session` with a stable `@grove_chat_home`
(`internal/tmux/swap.go`; any precondition miss falls back to
break/join) · #528 CHATS box sorted by stable name, shared with the rail ·
#531 per-session attach/detach hooks so a direct attach follows its
client, plus 80- vs slot-width chatweb captures · #533 the measurement
(`e2e/focus-bench.sh`, not in all.sh) and the docs/skill wrap. Left: the
train's PR into main; four pre-existing chatweb capture bugs drafted as
issues in the grove-533 PR description (not filed); a hidden window a client
`switch-client`s away from stays at that client's size until the next
sync (one slow swap, never a wrong one).

## The end state you are building toward

`gv init` on a stranger's repo → wizard → working fleet with local
markdown tasks. `gv init` on `~/git/thegrid` with the Grid pack → passes
the parity acceptance test (docs/grove-connections-design.md §8) —
line-for-line doctor coverage, byte-comparable kickoffs (empty corpus),
identical worker lifecycle, orchestrator duty parity, diagnostics
familiarity, break-a-connection-and-grove-notices. Then ovs retires.
