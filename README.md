# grove (`gv`)

<img width="1892" height="963" alt="image" src="https://github.com/user-attachments/assets/e744695c-b6b9-44f6-afed-2905afa0e59f" />

_The chats are orchestrators, not workers. Orchestrators dispatch, review, and manage a fleet of workers based off your task list, and update your task list in real time_

---

A repo-agnostic orchestrator for autonomous Claude Code sessions. Tell an orchestrator a rough idea; it sharpens it into a spec a worker can actually execute. `gv grab` the ticket and grove cuts a git worktree, spawns an autonomous Claude Code worker — on your Claude plan, a flat-rate coding plan like z.ai's GLM, or any OpenRouter model; on your laptop or a cheap VPS — and walks it through to a reviewed pull request. Questions reach your phone with answers pre-drafted. You focus on the product. You review and merge — or, once a repo has earned your trust, grant permission and grove carries it the last step too.

- **Backend-agnostic**: local Markdown tasks by default; Linear / GitHub
  Issues as adapters. The binary reads tasks, agents transition them,
  humans finish them — grove never mutates a tracker's terminal state.
- **Multi-machine**: one fleet across hosts over Tailscale — `--host`
  dispatch to a remote grove host, `gv handoff` to move a *running* task,
  phone access to every orchestrator chat.
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

## The loop

1. **Ideate out loud.** Describe a rough idea in an orchestrator chat. It pushes back like a good tech lead: scores it for agent-suitability, asks what's missing, drafts the acceptance criteria.
2. **A backlog of grabbable specs.** The output is GitHub issues good enough that an agent can execute them unsupervised: sized small, sequenced into merge trains, labeled `rote` when a cheap model can do it. The quality of the ticket sets the cost of the work.
3. **Dispatch — the right worker, wherever there's room.** `gv grab grove-N --repo X` puts a worker on it; `--model` or `--profile` picks the lane (Claude plan, flat-rate GLM at $0 marginal, OpenRouter per-token overflow); `--host vps` runs it on a remote grove host. Laptop closing mid-task? `gv handoff grove-N --to vps` and the work keeps going without you.
4. **They walk it through.** The worker implements, commits, self-reviews with your taste (your CLAUDE.md, your skills, screenshots on the ticket), opens the PR, rides CI. Questions land on your dashboard with a drafted answer — unblocking is usually one "yes", tappable from your phone (`gv chat serve`).
5. **You ship product, not process.** By default the merge button is yours — grove never closes a ticket or touches terminal state. Per repo, you can grant your agents merge permission and grove carries it the last step too.

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

## Status: live daily driver, dogfooding itself (2026-09-01)

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
- **Remote-overflow train** (#176–#199) — `hosts:` config, `--host`
  passthrough verbs, `gv handoff --to/--from`, one-fleet `gv ls`, cockpit
  `@`-armed remote spawn (see [Remote hosts](#remote-hosts) below).
- **`gv chat` + `gv chat serve`** (#215–#228) — read and steer every
  orchestrator chat from a phone browser over Tailscale (see
  [Chats from a phone](#chats-from-a-phone-gv-chat-serve) below).
- **`gv watch`** (#205) — the workspace's transition stream as one event
  per line, with sentinels and `--until` for completion.
- **`gv brains`** (#236) — sweep every registered workspace's orchestrator
  brain against the seed this binary carries, report-only.
- **Phase 2** (model routing/tiers) is parked — not worth it at solo
  scale; the cost ledger it would measure against already exists.
- **Codex lane** (native GPT-subscription workers/orchestrators) is a
  build-ready spec mid-flight as a 6-ticket merge train
  ([issue #62](https://github.com/JollyGrin/grove/issues/62)) — not yet
  live, tracked as "coming soon" in the model-access matrix below.

See [TASKS.md](TASKS.md) for the status board, [docs/roadmap.md](docs/roadmap.md)
for the open phases, and
[CLAUDE.md](CLAUDE.md) for the one remaining coexistence caution
(`gv hooks install` writes a shared settings file).

## Model access

| Backend | How | Status |
|---|---|---|
| Claude (Anthropic API) | native, default — no config needed | live |
| Claude-protocol via OpenRouter (GLM, etc.) | model profile (`.grove/config.yaml`) | live |
| z.ai coding plan (GLM, flat rate) | model profile (`zai-plan-*`), same Anthropic-protocol path — flat-rate, not per-token | live |
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

## Remote hosts

One fleet across machines: a laptop and one or more cheap VPS hosts over
Tailscale, dispatching to whichever has room. Name a host in config.yaml:

```yaml
# .grove/config.yaml (or ~/.config/grove/config.yaml)
hosts:
  vps:
    ssh: dean@grove-host          # Tailscale MagicDNS name
    gv: /home/dean/go/bin/gv      # absolute — same reason the hooks use one
```

`--host <name>` runs a verb over ssh on that host's own gv instead of here:
`grab`, `ls`, `adopt`, `handoff`, `answer`, `nudge`, `diff`, `pause`,
`untrack`, and `orchestrator new` (that one spawns the chat in the host's
twin of this workspace). For `answer`/`nudge` the flag comes **before** the
ticket — `gv answer --host vps grove-42 "yes"` — because relay free text may
legitimately mention `--host`.

Moving a *running* task between machines is `gv handoff grove-42 --to vps`
(and `--from vps` to bring it back): grove sends a checkpoint nudge, verifies
the work is pushed, the worktree clean, and the PR body carries the handoff
state, asks you to confirm, untracks the task here, and adopts it over ssh
there. The transcript does not travel — the PR body is the handoff.

There is no state sync between hosts: GitHub is the only shared layer, and
every task is tracked by exactly one host at a time. Full runbook — Tailscale
SSH setup, sizing, gh/claude auth on the host — in
[docs/remote-host-setup.md](docs/remote-host-setup.md).

## Chats from a phone (`gv chat serve`)

Read, continue and start orchestrator chats in every registered workspace
from a phone browser, with no terminal. Two commands:

```sh
gv chat serve                 # binds 127.0.0.1:3000, prints its URL, ^C stops it
tailscale serve --bg 3000     # tailnet-only HTTPS in front of it
```

→ `https://<your-host>.<tailnet>.ts.net`: three screens — projects, the
chats in a project, the chat itself — with live replies streaming in,
`+ new chat`, `revive` for an archived one, and a raw-key row when the
agent hits a permission prompt. Where the host has `model_profiles`
configured, `+ new chat` asks which backend to spawn on (the host's own
Claude, or any configured profile) — the same choice the cockpit's `)`
hotkey offers at the desk; with none configured there is no picker. It is one Go binary and one embedded page
(no npm, no node, no build step), so `gv update` ships it.

**Read this before exposing it.** `gv chat serve` types into live agent
panes and spawns Claude sessions, and it has **no authentication of its
own** — `tailscale serve` is the entire auth story, which is why the
default bind is loopback and any other bind prints a warning naming what
someone on that network could do. `tailscale funnel` publishes to the
whole internet and is **never** correct here.

Two prerequisites, both operator-side:

- **HTTPS enabled for the tailnet** (Tailscale admin → DNS → HTTPS
  Certificates). Without it there is no secure origin, so the service
  worker and any PWA install are off. Note this publishes the host's
  MagicDNS name to public Certificate Transparency logs — a name leak, not
  access.
- **Key expiry disabled for the host.** A node key that expires drops the
  machine off the tailnet silently, taking ssh and `gv --host` with it.

`gv chat serve` serves **orchestrator chats only** — read, relay, spawn.
There is no route to `gv done`, `gv untrack --rm`, or any task-backend
mutation, and fleet-shaped surfaces (task rows, cost charts, audit, sweep)
stay in the TUI or go to an external plugin against `--json`. It is off
unless invoked: no daemon, no autostart, nothing in the cockpit starts it.

## Doc map

| Doc | What |
|---|---|
| [HANDOFF.md](HANDOFF.md) | **Start here** — full pickup path for a fresh agent/human |
| [docs/GETTING-STARTED.md](docs/GETTING-STARTED.md) | Operator guide: install → wizard → cockpit → first ticket |
| [.claude/skills/](.claude/skills/) | Distilled hard-won rules, auto-loaded by workers: tmux discipline, shipping gates, Claude Code integration facts, plugin authoring (copyable) |
| [DESIGN.md](DESIGN.md) | Founding spec: architecture, TaskProvider, routing, workspaces, phasing |
| [docs/plugins.md](docs/plugins.md) | Surface-plugin contract: `--json` + events.jsonl are the API; build a `gv-<surface>` repo, tag it `grove-plugin` |
| [docs/plans/2026-08-31-gv-chat-design.md](docs/plans/2026-08-31-gv-chat-design.md) | `gv chat`: chat identity, the four verbs, and the phone UI behind `tailscale serve` |
| [docs/remote-host-setup.md](docs/remote-host-setup.md) | VPS grove-host runbook: Tailscale SSH, sizing, gh/claude auth |
| [docs/grove-connections-design.md](docs/grove-connections-design.md) | Wizard, doctor, drift detection, connections manifest, pack system, parity gate |
| [docs/grove-learnings-design.md](docs/grove-learnings-design.md) | Layered learnings/memory system |
| [docs/grove-cockpit-design.md](docs/grove-cockpit-design.md) | Cockpit UX: activity feed, parallel orchestrators |
| [docs/grove-readiness-review.md](docs/grove-readiness-review.md) | Readiness review + the locked-decisions table (§5) |
| [docs/seed-manifest.md](docs/seed-manifest.md) | Exactly what was copied from ovs + the generalization map |
| [TASKS.md](TASKS.md) | Status board — current month; older rows in [docs/archive/](docs/archive/) |
| [docs/roadmap.md](docs/roadmap.md) | Open phases (1–6) and parked ideas |
| [LEARNINGS.md](LEARNINGS.md) | Verified surprises — current month; older entries in [docs/archive/](docs/archive/) |

## Build

```sh
go build ./... && go vet ./... && go test ./...   # expected green (run bare — never pipe the gate)
```

`gv update --yes` is the only way to refresh the installed `~/go/bin/gv` —
every push to main auto-releases within ~a minute, so after a merge you
wait, then update. For testing an unmerged branch, hand over a throwaway
build — `go build -o /tmp/gv-<ticket> ./cmd/gv` — never install from
source: hooks and live sessions reference `~/go/bin/gv` by absolute path.

Two field-tested rules (details in [LEARNINGS.md](LEARNINGS.md) and
`.claude/skills/`):

- **Run `e2e/dummy.sh` before merging anything that touches the task
  lifecycle** — it exercises grab/ls/hook/untrack/done against fully
  scratch state (the dummy-data pattern; other suites: `wizard.sh`,
  `workspace.sh`, `github.sh`, `cockpit.sh`; `e2e/all.sh` runs them all —
  no CI covers them, so run it before merging anything that touches the
  TUI too).
