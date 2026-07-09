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
- **Any Anthropic-compatible backend**: named model profiles run workers
  *and* orchestrators on OpenRouter (or any compatible endpoint) per
  dispatch — mix a GLM orchestrator with Claude workers or vice versa.
- **Self-configuring**: `gv init` detects the stack, wires connections
  (auth, hooks, worker environment), and notices when one goes missing.
- **Compounding**: a layered, human-gated learnings system so grove grows
  with people and projects as they use it.

Grove is the OSS-ready successor to
[overstory-tui](https://github.com/JollyGrin/overstory-tui), a working
orchestrator field-tested on The Grid's real ticket flow. Team-specific
setups (like the Grid's) become **packs** — versioned overlays of
conventions, checks, and prompts — instead of hardcoding.

## Status: live daily driver (2026-07-09)

The namespace split from overstory is done (P0.0, 2026-07-04) — the binary
is safe to run and is the operator's daily driver. See [TASKS.md](TASKS.md)
for the phase board and [CLAUDE.md](CLAUDE.md) for the one remaining
coexistence caution (`gv hooks install` writes a shared settings file).

## Model profiles (OpenRouter etc.)

A **model profile** names an Anthropic-API-compatible backend — endpoint,
credential env var, and which slug fills each Claude model class:

```yaml
# .grove/config.yaml (or ~/.config/grove/config.yaml)
model_profiles:
  openrouter-glm:
    base_url: https://openrouter.ai/api
    auth_token_env: OPENROUTER_API_KEY   # key itself lives in ~/.config/grove/.env
    opus: z-ai/glm-5.2
    sonnet: z-ai/glm-5.2
    haiku: z-ai/glm-4.5-air
cost:
  pricing:
    z-ai/glm-5.2: {input: 0.42, output: 1.32}   # $/Mtok; dated API slugs prefix-match
```

```sh
gv grab grove-42 --repo grove --profile openrouter-glm   # worker on GLM
gv orchestrator new --profile openrouter-glm             # orchestrator pane on GLM
gv adopt grove-42                                        # revives on the profile it was grabbed with
```

Any orchestrator can dispatch onto any profile — the flag is per-dispatch,
so models freely delegate to other models. Visibility is built in and the
no-profile path is untouched: profiled workers get a `· <profile>` window
suffix and a `PROFILE` column in `gv ls`; a profiled orchestrator pane
carries a permanent `⚡ <profile>` border tag; profiled orchestrators keep
their own `--continue` history per profile. Set `model_profile: <name>` on
a repo to make it that repo's default; `--profile anthropic` strips it.
Secrets are self-sourced from `~/.config/grove/.env` at launch — never
inherited from the shell — and only the env var *name* appears in config.

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
go build ./... && go vet ./... && go test ./...   # expected green
go install ./cmd/gv                               # refreshes ~/go/bin/gv in place
```
