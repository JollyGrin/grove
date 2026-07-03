# grove (`gv`)

A repo-agnostic orchestrator for autonomous Claude Code sessions: drop it
into any repo (or a parent folder of repos), let the wizard wire it up, and
run the loop — **one task → git worktree + tmux window + autonomous agent →
PR** — with a single inbox answering *"what can I act on right now?"*

- **Backend-agnostic**: local Markdown tasks by default; Linear / GitHub
  Issues as adapters. The binary reads tasks, agents transition them,
  humans finish them — grove never mutates a tracker's terminal state.
- **Right-sized models**: routes rote work to small models, reasoning to
  frontier ones, and escalates on failed gates — erring upward when unsure.
- **Self-configuring**: `gv init` detects the stack, wires connections
  (auth, hooks, worker environment), and notices when one goes missing.
- **Compounding**: a layered, human-gated learnings system so grove grows
  with people and projects as they use it.

Grove is the OSS-ready successor to
[overstory-tui](https://github.com/JollyGrin/overstory-tui), a working
orchestrator field-tested on The Grid's real ticket flow. Team-specific
setups (like the Grid's) become **packs** — versioned overlays of
conventions, checks, and prompts — instead of hardcoding.

## Status: pre-Phase-0 scaffold (2026-07-03)

The design corpus is complete and reviewed; the Go tree is a verbatim seed
from overstory (builds green, tests pass) awaiting generalization.

> ⚠️ **Do not run the binary yet** — the seeded code still points at
> overstory's live config/state namespaces. See [CLAUDE.md](CLAUDE.md).

## Doc map

| Doc | What |
|---|---|
| [HANDOFF.md](HANDOFF.md) | **Start here** — full pickup path for a fresh agent/human |
| [DESIGN.md](DESIGN.md) | Founding spec: architecture, TaskProvider, routing, workspaces, phasing |
| [docs/grove-connections-design.md](docs/grove-connections-design.md) | Wizard, doctor, drift detection, connections manifest, pack system, parity gate |
| [docs/grove-learnings-design.md](docs/grove-learnings-design.md) | Layered learnings/memory system |
| [docs/grove-cockpit-design.md](docs/grove-cockpit-design.md) | Cockpit UX: activity feed, parallel orchestrators |
| [docs/grove-readiness-review.md](docs/grove-readiness-review.md) | Readiness review + the locked-decisions table (§5) |
| [docs/seed-manifest.md](docs/seed-manifest.md) | Exactly what was copied from ovs + the generalization map |
| [TASKS.md](TASKS.md) | Status board (phases 0–6) |
| [LEARNINGS.md](LEARNINGS.md) | Verified surprises (seeded from ovs's generic entries) |

## Build

```sh
go build ./... && go vet ./... && go test ./...   # safe, expected green
# go install / running gv: NOT until TASKS.md P0.0 is done
```
