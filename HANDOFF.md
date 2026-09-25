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

## The end state you are building toward

`gv init` on a stranger's repo → wizard → working fleet with local
markdown tasks. `gv init` on `~/git/thegrid` with the Grid pack → passes
the parity acceptance test (docs/grove-connections-design.md §8) —
line-for-line doctor coverage, byte-comparable kickoffs (empty corpus),
identical worker lifecycle, orchestrator duty parity, diagnostics
familiarity, break-a-connection-and-grove-notices. Then ovs retires.
