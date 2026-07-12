# grove (`gv`)

<img width="1892" height="963" alt="image" src="https://github.com/user-attachments/assets/e744695c-b6b9-44f6-afed-2905afa0e59f" />

_The chats are orchestrators, not workers. Orchestrators dispatch, review, and manage a fleet of workers based off your task list, and update your task list in real time_

---

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

## Status: live daily driver, dogfooding itself (2026-07-12)

The namespace split from overstory is done (P0.0, 2026-07-04) — the binary
is safe to run and is the operator's daily driver. Grove now builds itself
through itself: its real backlog is **GitHub issues on this repo**
(`grove-N` = issue #N), grabbed and shipped by grove workers. Shipped and
proven live:

- **Phase 0** (extraction) and most of **Phase 1** (wizard, doctor,
  connections manifest, workspaces + ambient walk-up) — remainder is pack
  loading (1b) and drift detection (1c).
- **Phase 3** (`github-issues` provider) — the TaskProvider seam held with
  zero changes to the other providers.
- Much of **Phase 4**: the cockpit is mature — one `grove-<label>` tmux
  session per workspace (window 0 = dashboard + orchestrator chats,
  windows 1+ = workers with live task-state glyphs), costs page with a
  persistent spend ledger, layout cycling, effects, a `?` help overlay.
- **Model profiles** (below) — workers *and* orchestrators on OpenRouter
  or any Anthropic-compatible backend, per dispatch.
- **Phase 2** (model routing/tiers) is parked — not worth it at solo
  scale; the cost ledger it would measure against already exists.

See [TASKS.md](TASKS.md) for the live phase board and
[CLAUDE.md](CLAUDE.md) for the one remaining coexistence caution
(`gv hooks install` writes a shared settings file).

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
| [docs/GETTING-STARTED.md](docs/GETTING-STARTED.md) | Operator guide: install → wizard → cockpit → first ticket |
| [.claude/skills/](.claude/skills/) | Distilled hard-won rules, auto-loaded by workers: tmux discipline, shipping gates, Claude Code integration facts |
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
go build ./... && go vet ./... && go test ./...   # expected green (run bare — never pipe the gate)
go install ./cmd/gv                               # refreshes ~/go/bin/gv in place — from main ONLY
```

Two field-tested rules (details in [LEARNINGS.md](LEARNINGS.md) and
`.claude/skills/`):

- **Never `go install` from an unmerged branch** — hooks and live sessions
  reference `~/go/bin/gv` by absolute path. For manual testing of a branch,
  hand over a throwaway build: `go build -o /tmp/gv-<ticket> ./cmd/gv`.
- **Run `e2e/dummy.sh` before merging anything that touches the task
  lifecycle** — it exercises grab/ls/hook/untrack/done against fully
  scratch state (the dummy-data pattern; other suites: `wizard.sh`,
  `workspace.sh`, `github.sh`, `cockpit.sh`).
