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
  *and* orchestrators on OpenRouter, a Kimi Code plan, or any compatible
  endpoint, per dispatch — mix a GLM orchestrator with Claude workers or
  vice versa. A native Codex CLI lane (GPT subscription) is in progress.
- **Self-configuring**: `gv init` detects the stack, wires connections
  (auth, hooks, worker environment), and notices when one goes missing.
- **Compounding**: a layered, human-gated learnings system so grove grows
  with people and projects as they use it.

Grove is the OSS-ready successor to
[overstory-tui](https://github.com/JollyGrin/overstory-tui), a working
orchestrator field-tested on The Grid's real ticket flow. Team-specific
setups (like the Grid's) become **packs** — versioned overlays of
conventions, checks, and prompts — instead of hardcoding.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/JollyGrin/grove/main/install.sh | bash
```

Prebuilt binaries for macOS and Linux (amd64/arm64), installed to
`~/.local/bin/gv`. Every merge to main auto-releases a patch version;
update any time with `gv update`. Or build from source:
`go install github.com/JollyGrin/grove/cmd/gv@latest` (Go 1.26+).

Then:

```sh
gv doctor                            # preflight: tmux, gh, claude
cd ~/projects/your-app && gv init    # 🌱 take root
gv                                   # open the cockpit
```

Full walkthrough: [docs/GETTING-STARTED.md](docs/GETTING-STARTED.md).

## Status: live daily driver, dogfooding itself (2026-07-18)

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
- **Model profiles** ([below](#model-access)) — workers *and* orchestrators
  on OpenRouter, a Kimi Code plan, or any Anthropic-compatible backend,
  per dispatch; ACCOUNT tab is a per-profile key manager with live fuel
  gauges.
- **Phase 2** (model routing/tiers) is parked — not worth it at solo
  scale; the cost ledger it would measure against already exists.
- **Codex lane** (native GPT-subscription workers/orchestrators) is a
  build-ready spec mid-flight as a 6-ticket merge train
  ([issue #62](https://github.com/JollyGrin/grove/issues/62)) — not yet
  live, tracked as "coming soon" in the model-access matrix below.

See [TASKS.md](TASKS.md) for the live phase board and
[CLAUDE.md](CLAUDE.md) for the one remaining coexistence caution
(`gv hooks install` writes a shared settings file).

## Model access

| Backend | How | Status |
|---|---|---|
| Claude (Anthropic API) | native, default — no config needed | live |
| Claude-protocol via OpenRouter (GLM, etc.) | model profile (`.grove/config.yaml`) | live |
| Kimi Code plan (K3) | model profile, same Anthropic-protocol path | live |
| Codex (GPT subscription) | native Codex CLI lane, own harness | coming soon — build-ready spec, [issue #62](https://github.com/JollyGrin/grove/issues/62) |

Any orchestrator can dispatch to any worker on any live backend — mix and
match per ticket. The `)` picker in the cockpit lists every configured
profile (and will list Codex lanes once shipped) so switching backends is a
keypress, not a config edit.

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
  kimi:
    base_url: https://api.kimi.com/coding
    auth_token_env: KIMI_CODE_API_KEY
    opus: "k3[1m]"
    sonnet: "k3[1m]"
    haiku: "k3[1m]"
    env:   # backend-specific vars beyond the six built-ins; exported first, built-ins win on collision
      CLAUDE_CODE_AUTO_COMPACT_WINDOW: "1048576"
      ENABLE_TOOL_SEARCH: "false"
      ANTHROPIC_DEFAULT_FABLE_MODEL: "k3[1m]"
      CLAUDE_CODE_SUBAGENT_MODEL: "k3[1m]"
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
| [.claude/skills/](.claude/skills/) | Distilled hard-won rules, auto-loaded by workers: tmux discipline, shipping gates, Claude Code integration facts, plugin authoring (copyable) |
| [DESIGN.md](DESIGN.md) | Founding spec: architecture, TaskProvider, routing, workspaces, phasing |
| [docs/plugins.md](docs/plugins.md) | Surface-plugin contract: `--json` + events.jsonl are the API; build a `gv-<surface>` repo, tag it `grove-plugin` |
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
