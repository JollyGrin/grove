# Grove — a repo-agnostic autonomous-coding orchestrator (founding spec)

> **`grove`** (binary `gv`) — name locked 2026-07-03. A grove is a stand of
> trees — the generic sibling of `overstory`, which is the canopy over one
> team's forest.
>
> **Status: founding DESIGN.md of this repo.** Adopted 2026-07-03 from
> `overstory-tui/docs/grove-spec.md` @ `8c2f4f0`, after the decision
> interview (see [docs/grove-readiness-review.md](docs/grove-readiness-review.md)
> §5) and a design-reviewer pass (APPROVE_WITH_FIXES, all fixes applied).
> It is deliberately a *what/why* spec — each phase gets a `docs/plans/`
> plan (plan-reviewer gated) before code.
>
> **Source of truth for the extraction:** `overstory-tui` (`ovs`) at
> `~/git/thegrid/overstory-tui` is a working, field-tested ~8k-LOC Go
> orchestrator for The Grid — **frozen**, and the daily driver until the
> parity gate passes. Grove is its generalization; this repo's Go tree is
> seeded verbatim from it ([docs/seed-manifest.md](docs/seed-manifest.md)).
> Read ovs's `DESIGN.md` / `LEARNINGS.md` alongside this — most of Grove's
> plumbing already exists there, proven.

---

## 0. The three locked decisions

| # | Decision | Consequence for this spec |
|---|---|---|
| 1 | **New repo, adapter core.** Extract a generic core behind a `TaskProvider` interface. `ovs` stays frozen and untouched until Grove is proven; Grid later becomes one config on top. | Zero risk to the daily Grid tool. Everything Grid-specific is pushed behind an interface or into config. |
| 2 | **Claude-native routing, pluggable later.** Ship an abstract `Router` with a `ClaudeTiers` implementation (Opus/Sonnet/Haiku); design the seam for OpenRouter/RouteLLM but don't build it. | The smarter-swarm layer is real on day one but stays inside the clean Claude Code hook/skill world. |
| 3 | **OSS-ready now.** No Grid or personal assumptions in the core. First-run wizard, zero-assumption bootstrap, portable `AGENTS.md` context. | A stranger can clone, run `gv init`, and reach a first task without editing Go. |

---

## 1. What Grove is (one paragraph)

Grove drops into **any repo, or any parent folder of repos**, teaches itself
the project, wires up whatever task backend and integrations are present, and
then runs the same loop `ovs` runs for The Grid: **one task → git worktree +
tmux window + a kickoff-prompted autonomous Claude Code session → PR**, with a
single inbox answering *"what can I act on right now?"* The two things Grove
adds over `ovs` are (a) it is **backend-agnostic** — local Markdown tasks are
the default, Linear / GitHub Issues / Jira are optional adapters — and (b) it
**routes work to the right-sized model**, handing rote tasks to small models
and reserving frontier models for reasoning, erring upward when unsure.

Grove keeps `ovs`'s central bet: **the binary is dumb plumbing; judgment lives
in you, in an orchestrator Claude session, and in the workers.** Grove adds one
sliver of deterministic judgment the binary *does* own — pre-dispatch model
routing — because it must be cheap, synchronous, and auditable.

---

## 2. Design principles (inherited + new)

Inherited from `ovs`, unchanged because they are proven:

1. **Intelligence is not in the Go binary.** Deterministic code does
   deterministic things (spawn, poll, classify, gate, clean up). Ambiguity and
   judgment live in agents.
2. **Event-sourced state.** `events.jsonl` + `mail.jsonl` are append-only
   (O_APPEND + flock); `tasks.json` is a derived, rebuildable view. Concurrent
   hooks only ever append. Replay-debugging is free.
3. **Worktree-per-task isolation is non-negotiable.** One task = one branch =
   one worktree dir = one tmux window, no sanitization. This is also the
   industry-converged answer for parallel agents (Claude Squad, Conductor,
   Fleet, Cursor background agents all land here): independent worktrees =
   zero filesystem-level merge conflict.
4. **Deterministic outer loop, agentic inner worker.** The supervisor loop
   (grab → poll PR/CI → classify STATUS → gate) is fixed and enumerable, so it
   is code. Reasoning is confined inside each worker session. (Microsoft
   Conductor, Praetorian, and the broader field all converge on this split.)
5. **Chat is the primary UX; mail is the event substrate beneath it.**
   *(Revised from "mail as primary UX" after real `ovs` usage — see
   [grove-cockpit-design.md](docs/grove-cockpit-design.md).)* You live in the
   orchestrator chat (right pane); the left pane keeps the **AGENTS list**
   (open worktrees — the attach/manage launcher, unchanged) and, below it, an
   **ACTIVITY feed** — a rendered, newest-first projection of `events.jsonl`
   (the swarm's objective history). The **mail and review panels are dropped**:
   their signal is redundant with the AGENTS list columns + header counts +
   chat + push. Mail stays the append-only event log (source of truth), not a
   panel you browse. `gv ui` ships a `main-vertical` cockpit — TUI left
   (AGENTS + ACTIVITY), a **stack of orchestrator chats** right — from day one,
   and never renders into a chat pane. Parallel work is one keystroke:
   `O` / `gv orchestrator new` splits the right column into a fresh, correctly
   -launched orchestrator (right cwd → `CLAUDE.md` loads; untracked so hooks
   ignore it), keeping every existing chat open. Since the orchestrator is
   stateless-by-design (overview from the CLI, competence from `CLAUDE.md`),
   `/clear` at a topic boundary *is* a free fresh orchestrator; spawn a new pane
   only for genuinely parallel threads.
6. **Propose, then dispose.** The orchestrator session and any autonomy never
   take irreversible or outward-facing action without a human's confirmation.
7. **The binary never mutates the task backend's terminal state.** Grove reads
   tasks; agents transition them; humans finish them. (In `ovs`: reads Linear,
   agents move In Progress / In Review, humans move Done.)

New principles Grove adds:

8. **Zero-assumption drop-in.** Everything Grove needs about a repo it either
   detects deterministically or asks once in a wizard and records. No hidden
   dependence on the author's machine.
9. **Portable context.** Grove reads and writes `AGENTS.md` (the tool-neutral
   emerging standard) so its self-taught context isn't Claude-locked, and
   honors any pre-existing `AGENTS.md` / `CLAUDE.md` / `.cursorrules`.
10. **Route by difficulty, err upward.** Small models only for work an
    automated gate can catch and a human can cheaply undo. When uncertain,
    route up a tier. Cost savings never come at the cost of a silent regression.

---

## 3. Architecture: the layers

```
┌─ YOU ──────────────────────────── judge ───────────────────────────────────┐
│  review PRs/previews · answer questions · merge · pick what to grab        │
├─ ORCHESTRATOR (optional, one per workspace) ─ brain ────────────────────────┤
│  a Claude session you chat with; drives `gv` + the task backend            │
│  `gv switch` jumps between workspaces from anywhere (§6.5)                  │
│  triages backlog · proposes grabs · summarizes fleet · drafts answers      │
├─ gv CLI + TUI ──────────────────── hands ───────────────────────────────────┤
│  deterministic Go. every command --json.                                   │
│  NEW: bootstrap (init/doctor)  ·  NEW: router (model selection)            │
│  grab · ls · mail · answer · nudge · feedback · audit · sweep · done · cost │
├─ ADAPTERS ──────────────────────── seams ───────────────────────────────────┤
│  TaskProvider: markdown(default) · linear · github-issues · jira           │
│  Router:       claude-tiers(default) · openrouter(future)                  │
├─ WORKERS ───────────────────────── trees ───────────────────────────────────┤
│  one Claude Code session per task · tmux window · git worktree             │
│  role-typed (.claude/agents: planner/implementer/reviewer) when useful     │
└──────────────────────────────────────────────────────────────────────────┘
```

Two boxes are new relative to `ovs`: the **ADAPTERS** seam (Task 1 of the three
decisions) and the **router** inside the CLI layer (Task 2). Everything else is
`ovs`'s architecture with the Grid specifics lifted out.

---

## 4. What was Grid-specific vs what is already generic

The extraction is smaller than it looks — `ovs` is ~90% generic Go plumbing.
This table is the reassurance that Grove doesn't require rewriting `ovs`, only
re-homing a handful of surfaces behind interfaces or config.

| `ovs` surface | Today | In Grove |
|---|---|---|
| `internal/tmux · git · worktree · detect · transcript` | Generic (copied from parkranger) | **Copy verbatim.** Already backend-agnostic. |
| `internal/state · cost · audit` | Generic | **Copy verbatim.** Event log, inbox (the mail model lives *inside* `state` — there is no separate `mail` package), cost ledger, audit classifier are all provider-neutral. |
| `internal/tui` | Generic | **Copy;** relabel "ticket" → "task", make columns provider-driven. |
| `internal/hooks` | Generic (settings.json merge-installer, STATUS sentinel) | **Copy.** The STATUS sentinel is already tool-neutral. |
| `internal/linear` (GraphQL client) | Grid-specific backend | **Becomes the `linear` TaskProvider adapter.** Moves behind the interface, code mostly unchanged. |
| `internal/config` | Linear team + repo map | **Generalized:** `providers`, `routing`, `bootstrap`, `repos` sections. |
| `internal/doctor` | Checks Grid workspace marketplace, dev-linear MCP, universal CLAUDE.md symlink | **Split:** generic checks (gh, tmux, hooks, provider auth) stay in `doctor`; Grid-plugin checks become a *pack* contribution, not core. |
| `internal/kickoff` templates | Defer to Grid `dev-core` skills, Linear transitions | **Generalized:** default template defers to whatever skills/`AGENTS.md` the repo has; transition verbs come from the active provider. |
| `orchestrator/CLAUDE.md` | Grid duties, dev-linear MCP, team rules | **Generalized brain** + a Grid overlay that the operator keeps privately. |
| Grid workspace wiring (marketplace, MCP, symlink) | Baked into doctor/onboarding | **Out of core entirely.** Lives in the operator's private Grid config that layers on Grove. |

**Net:** Grove's core is `ovs` minus `internal/linear`'s hard-coding minus the
Grid doctor checks minus the Grid kickoff/orchestrator text, plus two new
packages (`provider`, `router`) and one new command (`init`).

---

## 5. The `TaskProvider` abstraction (Decision 1)

The single seam that makes Grove backend-agnostic. Modeled on how `ovs` already
treats Linear — **read-only for plumbing, transitions delegated to agents,
terminal state reserved for humans** — and on how MCP servers wrap trackers as
uniform typed tools.

### 5.1 Interface (shape, not code)

A provider answers a small, deliberately read-biased surface:

- `List(filter) → []Task` — backlog / assigned, for the picker and orchestrator triage.
- `Get(id) → Task` — full fetch: id, title, body, comments, labels, url, and
  provider-native status. Used at grab time to build the kickoff prompt.
- `TransitionVerbs() → {start, review}` — the *names/mechanism* an agent uses to
  move a task to In-Progress and In-Review. Grove never calls these itself; it
  hands the verbs to the worker (see 5.4).
- `Attach(taskID, prNumber)` — best-effort link of PR ↔ task if the backend
  doesn't do it automatically (Linear does it via branch name; local-md needs
  an explicit link written to the file).
- `Capabilities() → {canTransition, canComment, hasRemote, autoLinksPR}` — lets
  the TUI/orchestrator hide affordances a backend doesn't support.

Grove's **hard rule survives per-provider:** the binary performs *no* terminal
mutation. `canTransition`/`canComment` describe what the *agent* may do, not the
binary. This preserves `ovs`'s "reads / agents transition / humans finish"
split for every backend.

### 5.2 Default provider: `markdown` (git-native local tasks)

Adopt the **Backlog.md / claude-task-master** converged schema — it's git-first,
agent-first, and already the de-facto local standard. One file per task under
`.grove/tasks/` (or `backlog/`, configurable), YAML frontmatter + Markdown body:

```
---
id: task-014
title: Persist filter state in the URL
status: todo        # backlog | todo | in-progress | review | done
priority: medium
labels: [frontend]
assignee: ""
dependencies: [task-009]
created: 2026-07-03
pr: ""              # written by grove/agent when a PR opens
---

## Description
...

## Acceptance Criteria
- [ ] Filters survive a page reload
- [ ] Shareable URL reproduces the view

## Implementation Plan
(optional; agent may fill)
```

- **The directory is the queue; `status` is a frontmatter field.** Every op is
  an atomic git commit, so task history rides with code history.
- **Transitions for local-md** = the agent edits the file's `status` field
  (this is the `start`/`review` verb). Humans move to `done`. Grove reads.
- **Write-location rule (added per design review I-2):** the agent's
  `status` edit happens **on its task branch** (worktree isolation is
  non-negotiable, so it can't write to the main checkout) — which means the
  file's status is invisible to the backlog until merge. Therefore, for
  in-flight tasks, **grove's own event-state is authoritative** (`tasks.json`
  already carries the live agent/delivery/human dimensions); the frontmatter
  edit is the *durable* record that becomes canonical when the branch
  merges. The directory-is-the-queue property holds for the *backlog* (todo
  tasks on the default branch); live status of grabbed tasks is grove's job,
  exactly as it is for the Linear provider (where Linear's status also lags
  grove's live view). The picker excludes tasks grove has in flight
  regardless of file status.
- **Offline claim, qualified (per design review I-3):** local-md removes the
  *tracker* SaaS dependency, not the delivery loop's. The back half of the
  loop (PR → CI → preview → merge check in `done`) still assumes a git host
  + `gh`. With no remote at all, grove degrades gracefully: delivery
  dimensions stay empty, escalate-on-gate falls back to STATUS/stall
  signals only, and `done` requires human confirmation (`--no-remote`
  semantics) instead of the `gh` merge check. Design the degraded path in
  Phase 0 — the dummy-repo E2E exercises exactly this shape.

### 5.3 Optional adapters

- **`linear`** — the existing `ovs` GraphQL client, moved behind the interface.
  Transition verbs = "call the dev-linear MCP to move In Progress / In Review."
- **`github-issues`** — likely the most-wanted OSS adapter (generic repos live
  on GitHub). Read via `gh issue list/view --json`; transition = labels or
  Projects status via `gh`; PR auto-links via "Closes #N".
- **`jira`** — stub/future; via its MCP server or REST.

Adapters may be **native Go clients** (like `ovs`'s Linear GraphQL) or **thin
MCP clients**, since every major tracker now ships an MCP server that already
exposes `list/get/transition` as typed tools. Recommendation: native for the
two hot paths (local-md, github-issues), MCP-shaped for the long tail.

### 5.4 How transitions stay agent-owned across providers

The kickoff template renders provider-supplied verbs. Instead of `ovs`'s
hard-coded *"Move the ticket to In Progress (dev-linear MCP)"*, Grove injects
`{{.Provider.StartInstruction}}` — which for local-md is "set `status:
in-progress` in your task file and commit", for Linear is the MCP call, for
GitHub is the label/Projects move. **The binary still never does it.**

---

## 6. Self-bootstrapping: `gv init` (ask #1: "build context for itself, setup connections")

The drop-in experience. Deterministic where possible, agent-assisted where
judgment is needed, wizard-confirmed where it must ask. Runs on first drop-in
and is re-runnable to refresh.

> **Deepened in [grove-connections-design.md](docs/grove-connections-design.md)**
> (2026-07-03): the wizard, doctor, and reconnect prompts all derive from one
> declarative *connections manifest*; the wizard re-runs scoped to whatever
> connection has gone missing; the Grid becomes a versioned *pack* with a
> row-by-row parity mapping and acceptance test. The self-taught-context
> story grows a full layered learnings system in
> [grove-learnings-design.md](docs/grove-learnings-design.md).

### 6.1 Phase A — deterministic probe (no LLM, no network)

Cheap file-presence detection, à la Turborepo's lockfile-priority chain:

- **Language & package manager** — lockfile priority chain (`pnpm-lock.yaml` >
  `yarn.lock` > `package-lock.json`, `go.mod`, `Cargo.toml`, `pyproject.toml`,
  …). Resolves build/test/lint commands from the manifest scripts.
- **Repo shape** — single repo vs **parent-of-repos** (multiple `.git` dirs
  under the drop point) vs **monorepo** (workspace globs: `pnpm-workspace.yaml`,
  `turbo.json`, `nx.json`, Go multi-module). This decides whether Grove manages
  one `repos:` entry or auto-discovers several.
- **CI & conventions** — `.github/workflows`, existing PR template, default
  branch, remote host (GitHub/GitLab).
- **Existing agent context** — nearest `AGENTS.md` / `CLAUDE.md` / `.cursorrules`
  up the tree. **Honor it; never overwrite.**
- **Task backend signals** — presence of `.grove/tasks` or `backlog/` (→ md),
  a Linear/GitHub remote, `gh` auth state.

### 6.2 Phase B — agent-written memory (the "teach itself" step)

If no adequate `AGENTS.md` exists, Grove spawns a **one-shot bootstrap agent**
(cheap tier — this is `/init`-shaped work) that walks the repo and writes an
`AGENTS.md`: stack, folder layout, build/test/lint commands, test layout,
conventions, and where the interesting code lives. This is exactly Claude
Code's `/init` pattern, emitted in the **portable `AGENTS.md` format** so it's
not Claude-locked. Committed to git, regenerable.

For **per-task context injection** (what a worker gets in its kickoff), Grove
size-tiers the strategy (Sourcegraph's finding: small repos fit whole, large
need retrieval):

- **Small repo** → let the worker's own session read what it needs; inject the
  `AGENTS.md` + task only.
- **Large repo** → generate an **Aider-style repo map** (tree-sitter symbol
  table + PageRank ranking, token-budgeted) and inject the top-ranked slice.
  Cache it and recompute only changed subtrees (Cursor's Merkle-diff trick).
  A standalone port (RepoMapper) exists to lift from; no server/embeddings
  needed — right for a CLI.

### 6.3 Phase C — connection setup (the "setup connections" step)

A wizard that detects, then asks-once-and-records:

- **Task provider** — offer detected options (local-md default; Linear/GitHub
  if a remote/auth is present); write choice to config.
- **Auth** — check `gh auth status`; for Linear, the API-key env var (the
  `api_key_env` indirection from `ovs` — earned its keep day one). For MCP-based
  providers, run the one-time interactive OAuth *now* (it can't happen inside an
  autonomous worker later).
- **Hooks** — the `ovs` settings.json merge-installer (SessionStart /
  Notification / Stop / SessionEnd), unchanged.
- **Notifications** — optional ntfy topic (from `ovs`), unchanged.
- **Model routing** — pick a cost-quality dial default (see §7).

### 6.4 `gv doctor`

The generalized preflight: `gh` authed, tmux present, hooks installed, provider
auth valid, `AGENTS.md` present, worker command resolvable. **Grid-specific
checks (workspace marketplace, dev-linear MCP, universal CLAUDE.md symlink)
become a pluggable "pack" check, not core** — the operator's private Grid overlay
registers them; a stranger never sees them.

---

## 6.5 Workspaces & the session switcher (jump into any context from anywhere)

A **workspace** is one Grove instance — one orchestrator over one scope. From
anywhere in the terminal, a single command lists all of them with a live status
rollup and jumps you into the right one. Two workspace shapes (the same
distinction §6.1's probe already draws):

- **parent scope** — the root is a folder of *sibling repos*; worktrees land
  under `<root>/.worktrees/`. One orchestrator spans many repos. (The Grid
  shape: `~/git/thegrid/` with monorepo, backend_go, … as siblings.)
- **repo scope** — the root is a single repo; its own worktrees are the fleet.

`gv init` picks the shape (detected, wizard-confirmed) and writes a `.grove/`
marker at the root, then registers that root in a global index.

### 6.5.1 Layout — state & config go per-workspace

This is the change the switcher forces (and it resolves former Open Question 2):

- `<root>/.grove/config.yaml` — **committable** per-workspace config. A team can
  check in its Grove setup; a solo user just leaves it local.
- `<root>/.grove/state/` — `events.jsonl` · `mail.jsonl` · `tasks.json`,
  gitignored. Same event-sourced model as `ovs`, just rooted per workspace.
- `~/.config/grove/registry.yaml` — the global index: one line per workspace
  `{root, label, scope}`. `gv init` appends; `gv workspaces` edits.
  Concurrency: writes take a flock; beyond that, last-writer-wins is
  accepted (registry edits are rare, human-initiated, and trivially
  re-runnable — unlike fleet state, which stays event-sourced).

### 6.5.2 Ambient resolution — every command finds its workspace

Every `gv <cmd>` walks **up** from `cwd` to the nearest `.grove/` (exactly how
git finds `.git`). That directory is the active workspace, so `gv ls`,
`gv grab`, `gv mail`, and the bare `gv` dashboard all operate on wherever you
happen to be — including deep inside a worktree
(`…/.worktrees/<repo>/<name>` walks up through to the root). `$GROVE_STATE_DIR`
still overrides for tests.

### 6.5.3 The switcher — `gv switch` (your `gv sessions`)

The from-anywhere entry point. Also what a bare `gv` does when there's **no**
ambient workspace.

- Reads the registry and gathers a **cheap live rollup per workspace** by
  reading each `.grove/state/tasks.json` (no `gh`, no network) — e.g.
  `thegrid   5 working · 2 mail · 1 review`, `grove   1 working`,
  `dotfiles   idle`.
- Renders a fuzzy picker (bubbletea/huh; degrade to an `fzf` pipe if present),
  **sorted by actionability** — workspaces with mail/blocked/review float up,
  quiet ones sink. The switcher doubles as an all-fleets status glance.
- Selecting one is a **cross-workspace jump**: attach to that workspace's tmux
  cockpit session (`grove-<label>`), building it via `gv ui` if it isn't up
  yet. One keypress from any directory → the right dashboard + orchestrator.

Non-interactive forms for shell wiring:

- `gv switch <label>` — jump straight to a named workspace.
- `gv switch --print <label>` — print its root, so `cd "$(gv switch --print
  grid)"` becomes a zoxide-style hop; pairs well with a shell alias.
- `gv workspaces [--json | add <path> | rm <label>]` — manage the registry
  directly; `gv init` auto-adds.

**Prior art to mirror:** tmux session-switchers and `sesh`/`zoxide`
project-jumpers — a registry + fuzzy picker + jump action. Grove's twist: each
entry carries a live fleet rollup, so switching *is* triaging.

**`gv orchestrator new` reuses this same ambient walk-up** (see
grove-cockpit-design §4.6): invoked from a repo, it resolves that repo's
workspace and — if a cockpit for it is already running — spawns the new
orchestrator into it, scoped to that workspace's state. If nothing's running or
it's ambiguous, it prompts (pick a session, or create one for this repo) rather
than defaulting to a global context. From a bare terminal that makes
`orchestrator new` behave like "enter this repo's cockpit and add a chat."

**Nesting rule:** nearest `.grove/` wins. A repo-scope workspace living inside a
parent-scope one is allowed — when you're inside the inner repo it's active,
outside it the parent is. `gv init` detects nesting and asks which you meant
rather than guessing.

---

## 7. Model routing — the smarter swarm (ask #2, Decision 2)

The one place Grove's *binary* owns a sliver of judgment, because routing must
be cheap, synchronous, pre-dispatch, and auditable. Everything here is behind
an abstract `Router`; the shipped implementation is `ClaudeTiers`.

### 7.1 The Router interface

- `Route(task, signals) → Plan{tier, splitStrategy, budgetHint}` — pure
  function of pre-computed signals; no network call for the default heuristic
  impl.
- `Escalate(task, tier, failure) → nextTier` — the cascade step.
- Implementations: **`claude-tiers`** (ship now: Haiku/Sonnet/Opus), **`heuristic+classifier`** (later: a RouteLLM-style learned classifier trained on Grove's own cost-ledger→outcome history), **`openrouter`** (future stub for non-Anthropic cheap models).

### 7.2 Pre-dispatch classification (the liftable heuristic)

Compute cheap, observable signals at grab time — **before** any model runs:

- `#files` a change likely touches (from task body / labels / heuristics)
- keyword class: `rename|format|docs|boilerplate|test-stub` vs
  `debug|architecture|refactor|migration|security`
- spec completeness: are acceptance criteria present? is the repo/scope stated?
- sensitive paths touched: schema / migration / auth / infra / money

Decision (Anthropic tier guidance + Morph-style difficulty classing):

```
default = Sonnet
down to Haiku  ⟺  (mechanical keyword class) AND (≤1–2 files) AND (spec complete)
up to Opus     ⟺  (debug|architecture|refactor|migration keyword)
                  OR (ambiguous/incomplete spec)
                  OR (many files / long-context)
                  OR (schema|auth|security|infra path)
tie-break      ⟹  err UPWARD. Never down-route irreversible or hard-to-verify work.
```

### 7.3 One global dial

A single `routing.cost_quality` knob (0 = always most-capable, 10 = cheapest,
default ~4), scaling the thresholds — copied from OpenRouter's Auto Router. One
number the user understands and tunes as trust builds. Down-routing is
**opt-in-aggressive**, never silently applied to risky work.

### 7.4 Escalate-on-failed-gate (the cascade — Grove's cheap win)

Grove already has objective, free gates: **build, lint, test, CI, and the
STATUS sentinel.** So the FrugalGPT/AutoMix cascade is trivial to implement
honestly here:

1. Dispatch at the routed tier (often small).
2. On a **failed gate** — red CI, failed `STATUS`, or N stalled turns —
   **re-dispatch the same task to the next tier up**, with the failure log
   attached to the new kickoff.
3. Cap at **one escalation per tier**; forward the failure context each time.

This retains ~frontier-model accuracy (cascades hold 97–99% in the literature)
while spending small-model dollars on the majority of tasks that pass first try.

### 7.5 Optional architect/editor split (big tasks only)

For tasks routed to Opus, optionally split within the task (Aider's
architect/editor, Anthropic's advisor pattern): **Opus plans in prose → Sonnet
applies the diff.** ~11% cheaper, slightly *higher* scores than all-Opus in
Anthropic's own measurement. Ship as a config flag, off by default.

### 7.6 Routing feeds on the cost ledger

`ovs` already parses transcripts into an outcome-priced ledger
($/merged-PR, steering counts, stuck flags). Grove reuses it as the **routing
feedback loop**: the orchestrator (duty: cost analysis, propose-only) surfaces
"tasks of shape X keep escalating — raise their default tier" or "these
down-routed fine — lower the dial." This is how the learned-classifier Router
gets its training data later. **Propose, never auto-apply.**

### 7.7 Failure modes to respect (from the routing research)

- **Silent quality regression** — the top risk; cheap-model bugs surface days
  later, never on a dashboard. Mitigation: only down-route gate-catchable,
  reversible work; the cascade is the safety net.
- **Fallback tax** — if small models routinely fail a task shape, escalation
  makes total cost *higher* than starting big. Mitigation: the ledger loop
  raises that shape's default tier.
- **Routing collapse / err-upward** — small classifier errors flip orderings;
  the tie-break is always "route up," never down.

---

## 8. Swarm behavior — what to adopt, what to reject (ask #2, breadth)

Grove's shape is **many independent tasks, one worker each** — not one big
project decomposed across cooperating agents. That single fact (same one that
made `ovs` reject jaymin/overstory's merge queue) determines the swarm design.

**Adopt:**

- **Worktree-per-task parallelism** as the primary swarm axis. This is the
  field's strongest signal and already `ovs`'s model: N independent tasks =
  N worktrees = zero coordination overhead, zero filesystem conflict.
- **Role-typed workers via `.claude/agents/*.md`** — predefine
  planner / implementer / reviewer with per-role `model:` overrides (the native
  Claude Code mechanism). The router picks the role-and-model at dispatch. This
  is *within* a worker, not a second layer of orchestration.
- **Deterministic supervisor loop** for the outer orchestration (spawn, poll,
  classify, gate, escalate) — never an LLM loop, which compounds per-step error
  and costs ~70% more over long horizons.
- **Writer/reviewer split inside a task** — the worker opens its PR, then the
  `pr-reviewer`-style agent (cheaper tier) reviews and the worker addresses
  findings. `ovs` already does this via the Grid skill; Grove makes it a
  first-class role.

**Reject (as `ovs` already does, for the same reason):**

- **Merge queue + conflict tiers** — tasks are independent; GitHub PR + CI +
  review is the merge queue.
- **Agent hierarchy with inter-agent mail** — no leads spawning builders. Mail
  flows agent↔human and orchestrator↔human only.
- **LLM-as-outer-orchestrator** — the coordinator Claude session stays
  *optional and on-demand* (advice + triage), never the load-bearing control
  loop.
- **Subtask decomposition within one task as the default** — reserve it for the
  genuinely large ticket, where a planner agent creates child tasks that Grove
  then treats as ordinary worktree-per-task workers. Don't make it the norm;
  it adds synthesis burden.

**The one place a coordinator agent earns its keep:** the fuzzy edges —
backlog triage & agent-suitability scoring, "is this PR actually done,"
drafting unblock messages, cost-shape analysis. Exactly `ovs`'s Phase-3
orchestrator, kept propose-only.

---

## 9. OSS-readiness (Decision 3)

What "a stranger can use it" concretely requires:

- **No personal defaults in core.** No `~/git/thegrid`, no `ccwork`, no
  dev-linear assumptions compiled in. Worker command, provider, paths — all
  config or wizard-derived. (`ovs` already externalizes the worker command via
  `claude:` config; Grove finishes the job.)
- **`AGENTS.md` as the portable context format** — tool-neutral, Linux-Foundation-stewarded, consumed by Codex/Cursor/Windsurf too. Grove reads and writes it; a user not on Claude Code still benefits.
- **First-run wizard (`gv init`)** that reaches a first task with zero manual
  file editing, and a `config.example.yaml` that documents every knob.
- **Worker autonomy is an explicit wizard choice.** `--dangerously-skip-
  permissions` is never silently defaulted for users without a safety-guard
  layer (Grid safety comes from the dev-safety plugin, which a no-pack OSS
  user lacks) — see grove-connections-design §6.4 for the full rule.
- **Grid becomes a private overlay, not a fork.** the operator's Grid setup =
  a `config.yaml` (linear provider, repo map) + a private orchestrator
  `CLAUDE.md` overlay + a doctor "pack" plugin registering the workspace
  checks. None of it lives in the public core.
- **Docs for people who aren't you** — README (what/why + 60-second demo),
  `AGENTS.md`/`CLAUDE.md` for contributors, and a bootstrapping guide. Keep the
  `ovs` DESIGN/TASKS/LEARNINGS three-doc discipline.

---

## 10. State & config (generalized, per-workspace)

State and config are **per-workspace** (see §6.5.1), not global — this is the
change the session switcher forces:

- `<root>/.grove/state/` — `events.jsonl` · `mail.jsonl` · `tasks.json`
  (gitignored; `$GROVE_STATE_DIR` overrides for tests). Identical event-sourced
  model to `ovs`.
- `<root>/.grove/config.yaml` — committable per-workspace config (below).
- `~/.config/grove/registry.yaml` — global index of workspace roots.

`.grove/config.yaml`:

```yaml
workspace:
  label: thegrid          # shown in the switcher; must be unique in the registry
  scope: parent           # parent (folder of sibling repos) | repo

provider:
  kind: markdown          # markdown | linear | github-issues | jira
  markdown:
    dir: .grove/tasks
  # linear: { api_key_env: LINEAR_API_KEY, team: DEV }
  # github: { repo: owner/name }

routing:
  impl: claude-tiers
  cost_quality: 4         # 0 most-capable … 10 cheapest
  tiers: { rote: haiku, normal: sonnet, hard: opus }
  escalate_on_gate: true  # small → mid → frontier on failed CI/STATUS
  architect_split: false  # Opus plans, Sonnet applies (big tasks)

bootstrap:
  agents_file: AGENTS.md
  repo_map: auto          # auto (size-tiered) | always | never

repos:                    # auto-discovered by `gv init`; editable
  app:
    path: .
    base: main
    setup: pnpm install
    worker: claude --dangerously-skip-permissions

orchestrator: { dir: ~/.config/grove/orchestrator, worker: claude }
notify: { ntfy: "" }
audit:  { stale_days: 7 }
cost:   { stuck_turns: 30 }
```

Every field except `provider.kind` and `repos.*.path` has a sane default or is
wizard-filled.

---

## 11. Command surface

`ovs`'s surface, relabeled task-neutral, plus `init`, plus routing:

```
gv init                     # NEW: probe repo, write AGENTS.md + .grove/, register workspace
gv switch [<label>]         # NEW: cross-workspace picker (live rollup) → jump into its cockpit
gv switch --print <label>   # NEW: print a workspace root, for `cd "$(gv switch --print x)"`
gv workspaces [--json|add|rm]  # NEW: manage the workspace registry
gv                          # dashboard TUI for the ambient workspace (or the switcher if none)
gv ui                       # tmux cockpit (grove-<label>, main-vertical): TUI left | chats right
gv orchestrator new [label] # NEW: spawn a fresh orchestrator, stacking a chat pane on the right
                            #      (the `O` keybind in the TUI; replaces manual ctrl-b " + claude).
                            #      Workspace-aware: auto-joins the invoking repo's active cockpit,
                            #      else prompts (pick a session | create one for this repo).
gv grab <task-id> [--repo]  # task → worktree → routed kickoff
gv grab <id> --manual       # context-only, no autonomous kickoff
gv grab <id> --tier opus    # NEW: override the router for this grab
gv grab                     # picker (provider List)
gv ls [--json]              # fleet table (+ TIER column)
gv mail [ls|read|--json]    # inbox
gv answer <id> · nudge <id> · feedback <id>
gv attach <id> · adopt <id> · untrack <id> [--rm]
gv audit [--json] · sweep [--dry-run|--json] · done <id>
gv diff <id> [--stat]
gv cost [--json|--analyze]  # + routing-feedback signals
gv doctor · hooks install|status · hook <event>
gv mobile · orchestrator
```

Only genuinely new verbs: `gv init` and the `--tier` override. Everything else
is `ovs` with "ticket"→"task" and provider-driven columns.

---

## 12. Relationship to `ovs` — succession, with a freeze guarantee

*(Sharpened 2026-07-03: overstory was the solo trial run; Grove is the
successor the operator shares with the team. Retirement of `ovs` is the plan, not an
option — but only after the parity gate.)*

- **`ovs` is not touched.** Grove is a new repo. `ovs` keeps running Grid work
  through this entire effort.
- **Grove is seeded by copying `ovs`'s generic packages** (§4) into the new
  repo, not by refactoring `ovs` in place.
- **Grid is re-expressed as a Grove pack** (grove-connections-design §6):
  a `linear` provider config + orchestrator overlay + connections/doctor
  declarations + worker-env spec. When the parity acceptance test
  (grove-connections-design §8) passes — including the capability-surface
  audit of everything the ccwork profile carries today — **grove takes over
  the operator's daily Grid work and `ovs` retires.** Until then, both coexist and
  `ovs` stays the daily driver.
- **Team adoption follows retirement:** teammates onboard via released
  binary + `gv init` + the Grid pack pin (grove-connections-design §6.5)
  — never via "clone overstory and read ONBOARDING.md §4."
- **Dual-hook coexistence contract (design review I-6).** During the trial
  window both `ovs hook` and `gv hook` are installed in the shared ccwork
  settings and fire on every session. "Not my session" must be decided by
  **task ownership, not workspace resolution**: once `gv init` has run on
  `~/git/thegrid`, an ovs-created worktree under `.worktrees/` *does*
  resolve to a grove workspace via the ambient walk-up — so `gv hook` exits
  unless the cwd maps to a task in *this workspace's own* `tasks.json`
  (and `ovs hook` keeps its equivalent rule). Cutover rule: drain in-flight
  work in ovs, start new grabs in grove; `gv adopt` is the escape hatch for
  anything long-lived. Smoke-tested in Phase 0.
- **Learnings flow one way for now:** anything Grove discovers that also fixes
  `ovs` gets backported deliberately; the two don't share code until/unless a
  shared `treekit`-style module is extracted (the same deferral `ovs` already
  planned for parkranger).

---

## 13. Build phasing (for the new repo's own plan)

*(Redrawn 2026-07-03 per design review I-5/S-1: the earlier sketch
under-scoped the connections and learnings designs; each now has explicit
room, and the learnings first cut is deliberately lean.)*

- **Phase 0 — skeleton + local-md.** New repo, copy generic packages,
  `markdown` provider (including the event-state-authoritative /
  frontmatter-durable rule and the degraded no-remote path, §5.2),
  `gv grab/ls/done` end-to-end on a dummy repo with a `.grove/tasks` file
  and the worker command as `echo` (the `ovs` dummy-data E2E pattern).
  Smoke-test the **dual-hook coexistence contract** (§12) with ovs live.
  *Proves the extraction.*
- **Phase 1 — bootstrap.** Three sub-phases (this is bigger than one line):
  **1a** probe + `AGENTS.md` bootstrap agent + wizard core (detect-then-
  confirm, flags, re-runnable). **1b** the connections manifest + doctor
  derived from it + minimal pack loading (local-path pack, slot merge).
  **1c** drift detection: TTL-cached lazy checks, failure-signal degradation
  via the hook classifier, seeded-file hash drift. *Proves
  drop-in-to-any-repo and notices-when-broken.*
- **Phase 2 — routing.** `Router` interface + `ClaudeTiers`, pre-dispatch
  classifier, `--tier` override, `TIER` column. Then escalate-on-gate.
  *Proves the smarter swarm; measure against the cost ledger.*
- **Phase 3 — second provider.** `github-issues` adapter — the real test that
  the `TaskProvider` seam holds. *Proves backend-agnosticism.*
- **Phase 4 — relay + brain + cockpit.** Hooks/inbox (copy from `ovs`), the
  generic orchestrator (write the de-Gridded duties text) + pack overlay
  rendering, `gv ui` with the cockpit design (AGENTS + ACTIVITY,
  `orchestrator new`). *Reaches `ovs` feature parity, generically.*
- **Phase 5 — learnings, first cut (lean).** L0–L2 only + `gv learn` +
  the `LEARNING:` sentinel + the curation inbox with human gate.
  **Deliberately deferred until the corpus size hurts:** activation
  metadata/filtering, promotion automation, lint, counters
  (grove-learnings-design §3.3/§3.6/§3.7 stay designed, not built).
  *Proves the compounding loop without building the scaling machinery.*
- **Phase 6 — OSS polish + the Grid pack.** Docs, wizard hardening,
  `config.example.yaml`, architect/editor split; author the Grid pack from
  the live-machine capability audit; run the parity acceptance test
  (grove-connections-design §8) → `ovs` retirement.

---

## 14. Failure Mode Analysis (spec-level)

| Risk | Criticality | Mitigation |
|---|---|---|
| Down-routing causes a silent-bad merge that surfaces days later | **Critical** | Only down-route gate-catchable + reversible work; escalate-on-gate cascade; err-upward tie-break; ledger loop raises tiers for shapes that regress. |
| `TaskProvider` interface leaks Linear-isms (e.g. assumes rich comments / auto PR-link) and github/local can't satisfy it | Important | Design the interface against the *local-md* provider first (the poorest backend); `Capabilities()` gates optional affordances. Build github-issues in Phase 3 as the interface stress test. |
| Bootstrap agent writes a wrong/misleading `AGENTS.md` that poisons every worker | Important | Deterministic probe covers the load-bearing facts (build/test/lint); the agent-written prose is advisory; `AGENTS.md` is committed + human-reviewable + regenerable; honor pre-existing files. |
| Extraction diverges from `ovs`; bug fixes must be double-maintained | Acceptable | Accept short-term; extract shared `treekit` only if drift becomes real (same rule `ovs` uses for parkranger). |
| Router adds latency/complexity for little saving on a solo user's small fleet | Acceptable | Router is a pure local heuristic (no network); dial defaults conservative; `--tier` manual override always available; can be disabled. |
| Parent-of-repos auto-discovery mis-detects repo boundaries | Acceptable | Wizard confirms discovered repos before writing config; `repos:` is hand-editable. |
| Nested workspaces / ambient walk-up resolves the wrong `.grove/`, so a command hits the wrong fleet | Important | Nearest-`.grove/`-wins is the fixed, documented rule; `gv init` detects nesting and asks; every mutating command echoes the resolved workspace label before acting. |
| Registry drifts (a workspace root is moved/deleted) so the switcher lists dead entries | Acceptable | `gv switch` skips + flags roots whose `.grove/` no longer exists; `gv workspaces rm` prunes; registry is a plain editable file. |
| OSS users on non-Claude agents can't use it at all | Acceptable | `AGENTS.md` portability + provider/router seams keep the door open; core targets Claude Code first by design (Decision 2). |

---

## 15. Decision log

- **New repo, adapter core** (not refactor-in-place, not hard-fork) — protects
  the daily Grid tool; the `TaskProvider` seam is the whole point of a clean
  extraction; a hard fork would double-maintain 8k LOC forever.
- **Claude-native routing behind an abstract Router** — ships real value now
  inside the clean hook/skill world; the interface keeps OpenRouter/multi-vendor
  possible without paying for it up front.
- **OSS-ready from day one** — cheaper to keep Grid out of the core continuously
  than to de-Grid it later; `AGENTS.md` + wizard are the concrete requirements.
- **local-md is the default provider and the interface's design target** — it's
  the poorest backend, so designing against it keeps the interface honest; it's
  also the zero-dependency OSS happy path.
- **Routing is the only judgment the binary owns** — because it must be cheap,
  synchronous, and pre-dispatch; everything else stays in agents/humans, per
  `ovs`'s founding bet.
- **Escalate-on-gate over a smarter upfront classifier** — Grove already has
  free objective gates (CI/test/STATUS); a cascade on those is more honest and
  cheaper to build than a trained router, and generates the data a trained
  router would later need.
- **Swarm = worktree-per-task, not agent hierarchy** — same reasoning that made
  `ovs` reject jaymin/overstory's merge queue; independent tasks integrate via
  GitHub, not a shared branch.
- **Per-workspace state/config + a global registry + ambient walk-up** — a
  workspace switcher (§6.5) needs each Grove instance to own its state, be
  discoverable from any cwd (git-style `.grove/` walk-up), and be indexed
  globally. This supersedes `ovs`'s single global state dir and resolves the
  repo-local-vs-global config question. The switcher carries a live fleet
  rollup so selecting a workspace is also triaging it.

Locked in the 2026-07-03 interview (full table in
[grove-readiness-review.md](docs/grove-readiness-review.md) §5):

- **Name `grove` / binary `gv`**, repo `github.com/JollyGrin/grove`, private
  until parity; **goreleaser + tagged releases from day one**.
- **Dedicated worker Claude profile by default** in the wizard (generalized
  `~/.cc-work` pattern); sharing the main profile is the opt-out.
- **No trust gate** on cloned workspace config/scripts (ovs stance);
  Critical-flagged to revisit before public release.
- **The overlay concept is a "pack"** ("profile" reserved for Claude Code
  profiles); the **Grid pack lives in the workspace marketplace repo**.
- **Providers: native Go hot paths** (markdown, github-issues, linear),
  MCP for the long tail.
- **Orchestrator chats spawn with `--dangerously-skip-permissions` by
  default** — guardrails are CLAUDE.md-based, matching real usage.
- **Learnings ship in grove only** — no ovs trial; the freeze guarantee
  stays strict.
- Deferrals confirmed: github-issues transition mechanism (Phase 3),
  repo-map necessity (measure in Phase 1).

---

## 16. Open questions

1. ~~**Name.**~~ **Resolved (2026-07-03 interview): `grove`, binary `gv`.**
   Repo `github.com/JollyGrin/grove` (private until parity), module path
   likewise, config at `~/.config/grove/`, marker dir `.grove/`.
2. ~~**Repo-local vs global config.**~~ **Resolved by §6.5:** config + state are
   per-workspace under `<root>/.grove/` (config committable, state gitignored),
   with a global `registry.yaml` indexing workspace roots. The switcher requires
   it.
3. **github-issues transitions.** Labels vs Projects v2 status — which is the
   default agent transition mechanism? (Projects v2 is richer but gh API is
   fiddlier; confirm in Phase 3.)
4. **Repo-map cost.** Is the Aider-style tree-sitter map worth building, or does
   modern long-context + the worker's own file reads make it unnecessary for the
   repo sizes you actually target? Measure in Phase 1 before committing.
5. **Learned Router.** When does the cost-ledger have enough
   task-shape→outcome data to justify a RouteLLM-style classifier over the
   heuristic? (Likely never for a solo user; keep as future.)
6. ~~**Orchestrator overlay mechanics.**~~ **Resolved by
   [grove-connections-design.md](docs/grove-connections-design.md) §6.3:** the
   pack ships an overlay markdown; grove renders the *composed* file into
   the orchestrator dir at seed time (one file at runtime, no import magic)
   and records its content hash, so seed evolution and hand-edits surface as
   drift instead of being clobbered.
7. ~~**Provider via native client vs MCP.**~~ **Resolved (2026-07-03
   interview): native Go for the hot paths** — local-md, github-issues, and
   linear (client already exists) — **MCP-shaped adapters for the long tail**
   (jira etc.).

---

## Appendix — research sources

**Model routing (§7):** RouteLLM (lmsys.org, github.com/lm-sys/routellm) ·
OpenRouter Auto Router / NotDiamond (openrouter.ai/docs) · Morph difficulty
classing (morphllm.com) · Not-Diamond/awesome-ai-model-routing · Anthropic
model tiers + advisor pattern (platform.claude.com/docs/models,
mindstudio.ai) · Claude Code subagent `model:` (code.claude.com/docs) · Aider
architect/editor (aider.chat/docs/usage/modes) · FrugalGPT / AutoMix /
ModelSwitch cascades (tianpan.co, arxiv) · routing failure modes (merge.dev,
tianpan.co).

**Swarm orchestration (§8):** Claude Code subagents / agent-teams / headless
(code.claude.com/docs) · Anthropic multi-agent research system
(anthropic.com/engineering) · OpenAI Agents SDK handoffs · LangGraph supervisor
· CrewAI roles · AutoGen · Devin / Cursor background agents · Claude Squad,
Conductor, Fleet, cmux (github + writeups) · deterministic-vs-agentic
orchestration (Microsoft Conductor, Praetorian).

**Bootstrapping & providers (§5, §6):** Claude Code `/init` + CLAUDE.md
(claude.com/blog) · Aider repo map + RepoMapper (aider.chat, github) · Cursor
Merkle indexing (cursor.com/blog) · Sourcegraph Cody / Continue.dev ·
AGENTS.md standard (agents.md, Linux Foundation) · Turborepo lockfile detection
· Backlog.md + claude-task-master + git-bug (github) · MCP-as-adapter
(Atlassian/Linear/GitHub MCP servers).
```
