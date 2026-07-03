# Handoff — picking grove up from the scaffold

> Written 2026-07-03 by the session that designed grove and built this
> scaffold. You are (probably) a fresh agent in a fresh repo. This file is
> the complete pickup path; nothing about grove lives only in someone's
> head.

## What this repo is

Grove is the generalized, OSS-ready successor to `overstory-tui` (`ovs`) —
Dean's working Go CLI that turns Linear tickets into autonomous Claude Code
sessions (worktree + tmux + kickoff → PR) for The Grid. Overstory was the
solo trial run; grove is the version that drops into **any** repo or
parent-of-repos, wizards itself into readiness, and — configured with the
Grid *pack* — must behave **exactly** like today's hand-tuned ovs, with
zero hardcoding. Then ovs retires and teammates adopt grove.

## Where things stand (2026-07-03)

Everything below is DONE:

1. **Design corpus, reviewed.** [DESIGN.md](DESIGN.md) (founding spec) +
   [docs/grove-connections-design.md](docs/grove-connections-design.md)
   (wizard / doctor / drift / connections manifest / pack system / parity
   test) + [docs/grove-learnings-design.md](docs/grove-learnings-design.md)
   (layered memory) +
   [docs/grove-cockpit-design.md](docs/grove-cockpit-design.md) (UX).
   A design-reviewer pass returned APPROVE_WITH_FIXES; **all findings are
   already applied** to the docs (record:
   [docs/grove-readiness-review.md](docs/grove-readiness-review.md) §4).
2. **All interview-level decisions locked.** The full table is in
   [docs/grove-readiness-review.md](docs/grove-readiness-review.md) §5 —
   name/binary, dedicated worker profile default, no trust gate (flagged
   revisit-before-public), pack terminology, Grid pack in the workspace
   marketplace, native-Go providers, LEARNINGS.md as L2, orchestrator
   skip-permissions default, learnings grove-only, goreleaser day one.
   **Do not relitigate these.**
3. **Code seeded.** The entire ovs Go tree copied byte-identical (module
   path rewritten to `github.com/JollyGrin/grove`; `cmd/ovs` →
   `cmd/gv`). `go build ./... && go vet ./... && go test ./...` green,
   `gofmt -l .` empty at seed time. Provenance + generalization map:
   [docs/seed-manifest.md](docs/seed-manifest.md).

## What you do first

1. **Read, in order:** [CLAUDE.md](CLAUDE.md) (rules — especially the
   ⚠️ do-not-run warning), this file, [DESIGN.md](DESIGN.md), then the
   three docs/ designs, then [docs/seed-manifest.md](docs/seed-manifest.md)
   and [LEARNINGS.md](LEARNINGS.md).
2. **Do NOT run the `gv` binary** until P0.0 (namespace rename) is done —
   the copied code still points at the live overstory config/state/hooks.
   Build and test freely.
3. **Write the Phase 0 plan** (`docs/plans/`) against DESIGN.md §13's
   redrawn phasing. Phase 0 = P0.0 namespace rename → markdown provider
   (with the event-state-authoritative rule and no-remote degraded path,
   DESIGN.md §5.2) → `gv grab/ls/done` E2E on a dummy repo (worker =
   `echo`) → dual-hook coexistence smoke test (DESIGN.md §12).
   [TASKS.md](TASKS.md) has the seeded breakdown. Get the plan reviewed
   (plan-reviewer) before executing — the design corpus is deliberately
   what/why; the plan is yours to write.
4. **Never edit `~/git/thegrid/overstory-tui`.** It is frozen and is
   Dean's daily driver. It is also your reference: when a copied package
   confuses you, diff against upstream and read its DESIGN.md/LEARNINGS.md.

## Traps we already know about (don't rediscover)

- **Running gv pre-P0.0 corrupts live ovs state** — the whole reason P0.0
  exists. Config `~/.config/overstory/`, state `~/.local/state/overstory/`,
  env `OVERSTORY_STATE_DIR`, hook commands `ovs hook <event>` +
  `~/go/bin/ovs` — all must be renamed/namespaced first.
- **Both ovs and gv hooks will fire on the same worker sessions** during
  the transition window. "Not my session" must key on *task ownership in
  this workspace's tasks.json*, NOT on workspace resolution — an
  ovs-created worktree can resolve to a grove workspace via the `.grove/`
  walk-up (DESIGN.md §12, design review I-6).
- **The parity gate's byte-comparison is against an empty learnings
  corpus** (docs/grove-connections-design.md §8.2) — don't chase
  byte-parity once learnings inject.
- **LEARNINGS.md here is seeded from ovs's generic entries** — they are
  verified facts (tmux SendKeys is single-line, squash-merge defeats
  `branch -d`, transcripts key on encoded cwd, …). Trust them; they were
  each learned the hard way.
- The state/tmux/git/worktree/detect/transcript packages were themselves
  copied into ovs from parkranger and have survived two tools — treat them
  as the most-proven code in the tree.

## The end state you are building toward

`gv init` on a stranger's repo → wizard → working fleet with local
markdown tasks. `gv init` on `~/git/thegrid` with the Grid pack → passes
the parity acceptance test (docs/grove-connections-design.md §8) —
line-for-line doctor coverage, byte-comparable kickoffs (empty corpus),
identical worker lifecycle, orchestrator duty parity, diagnostics
familiarity, break-a-connection-and-grove-notices. Then ovs retires.
