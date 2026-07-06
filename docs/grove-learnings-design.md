# Grove learnings — a layered, propose-only memory system (research + design draft)

> **Status: investigation / design draft. No code.** Companion to
> [grove-spec.md](../DESIGN.md); sibling of
> [grove-connections-design.md](grove-connections-design.md) (the wizard /
> connection-detection side — the two meet at the *pack* concept).
>
> **The ask (the operator, 2026-07-03):** grove should grow with people and projects
> as they use it — documenting learnings unique to each project, shared
> amongst worktrees of the same project, and generalized learnings saved
> globally. Investigate how others have attempted to make LLM memory better
> (Karpathy's LLM wiki, Mark Pollack's agent-native knowledge systems).

---

## 1. What exists today (the seed grove generalizes)

`ovs` already has a primitive version of every piece:

- **`LEARNINGS.md`** — a hand-maintained, repo-committed surprises file with a
  disciplined entry format (date · context · fact · what it changed) and an
  update rule in `CLAUDE.md` ("update when you get surprised").
- **Orchestrator duty 7** — the cost-analysis duty already ends in *"suggested
  edits to LEARNINGS.md … go to the operator as drafts — they approve before anything
  is written. One insight per proposal, with the ledger rows that support it."*
  That is a propose-only Reflector/Curator in embryo.
- **`events.jsonl`** — an append-only event substrate that a learnings inbox
  can ride with zero new infrastructure.
- **Claude Code native auto-memory** — workers and orchestrators already get
  `~/.claude/projects/<encoded-repo>/memory/` keyed **by git repo** (all
  worktrees of a repo share one memory dir), with a 200-line/25KB `MEMORY.md`
  index loaded per session.

What's missing is exactly what the ask names: **layers** (repo vs workspace vs
global), a **capture → curate → promote pipeline** that doesn't depend on the operator
hand-editing a file, and **hygiene** (rot detection, budgets) so the system
compounds instead of decaying.

---

## 2. Prior art — what the field converged on

*(Full citations in the appendix. This is the digest that drives the design.)*

### 2.1 Karpathy — "system prompt learning" + the LLM wiki

- **System prompt learning** (X, May 2025): pretraining is for knowledge,
  finetuning is for habitual behavior, and a third paradigm is missing —
  learning that is *"a change in system prompt"*: encounter a problem, figure
  something out, "remember something in fairly explicit terms for the next
  time." Edits to explicit text are a **much higher-dimensional feedback
  channel than a scalar reward**. "LLMs are like the guy in Memento, except we
  haven't given them their scratchpad yet."
- **LLM wiki** (gist, April 2026): instead of re-retrieving raw chunks (RAG),
  the LLM **incrementally builds and maintains a persistent wiki** — "compiled
  once and kept current, not re-derived on every query." Three layers: raw
  sources (immutable) · the wiki (LLM-writes, human-reads markdown) · **the
  schema** (a CLAUDE.md that defines structure and conventions — what makes
  the LLM "a disciplined wiki maintainer rather than a generic chatbot").
  Three operations: **Ingest** · **Query** (good answers get filed back, so
  exploration compounds) · **Lint** (periodic health check: contradictions,
  stale claims, orphan pages). Navigation via a small `index.md` (one line per
  page — beats embeddings at ~100-source scale) plus an append-only `log.md`.

### 2.2 Mark Pollack — agent-native knowledge systems ("Look Ma, No RAG!")

The "matt pollack" reference resolves to **Mark Pollack** (Spring Framework
veteran, blog.pollack.ai), who built the same pattern independently months
before Karpathy's gist, at production scale (six federated KBs, ~1,800 files):

- "At project scale, knowledge isn't a search problem — it's a **navigation
  problem**." No embeddings; **index files as routing tables** with explicit
  *"Read when…"* columns; an agent reaches grounded answers in 2–3 file hops
  using only `read`/`grep`/`list`.
- **Faceted YAML frontmatter** from a controlled vocabulary → a flat-file
  graph database traversable with grep.
- **Seven health checks** (`/kb-reindex`): cross-reference integrity, orphan
  detection, index freshness, summary–source consistency… "Knowledge bases
  rot; it catches drift before it compounds."
- **Portability proof**: cloned a KB + fresh session, re-ran 25 known
  questions, comparable answers — "you're a `git clone` away from sharing
  everything your agent knows."
- CLAUDE.md as the "prescriptive session bridge," including **negative
  knowledge** (what the KB *can't* answer, to prevent wasted search).

### 2.3 Memory systems — the mechanisms worth stealing

| System | Mechanism grove should steal |
|---|---|
| **MemGPT/Letta** | Always-in-context vs on-demand split; **hard size budgets** per memory block; background "sleep-time" reorg as a separate process. |
| **mem0** | The write path is a **merge, never a blind append**: each candidate fact is reconciled against near-duplicates → ADD / UPDATE / DELETE / NOOP. |
| **Zep/Graphiti** | **Supersede, don't delete**: contradicted facts get `invalid_at` + provenance, never destructive removal. |
| **Claude Code auto-memory** | Memory keyed **by git repo → all worktrees share it** (exactly layer (b) of the ask); index-loads-always + topics-on-demand; 200-line/25KB index cap. |
| **Cursor Memories** | A sidecar model *suggests*, the human **approves or rejects** — at-scale validation of propose-only. |
| **Windsurf** | The bright line: **machine-generated memories stay local; human-curated rules get committed.** |
| **Voyager / Reflexion** | Store *verified* skills (execution-tested before saved); after failures, write a distilled verbal lesson prepended to the next attempt. |
| **ACE (Stanford, 2025)** | Context as an **evolving playbook** of itemized bullets with stable IDs + helpful/harmful counters; Generator / Reflector / Curator roles; **delta updates only** — holistic rewrites cause *context collapse* (their case study: one rewrite shrank 18k tokens → 122 and dropped below the no-memory baseline). |
| **beads (Yegge)** | Proof that **git is the right sync/distribution layer** for agent memory — merges, review, provenance for free. |
| **Compounding engineering (Klaassen/Every)** | Plan → Work → Review → **Compound** as an explicit loop step; antipatterns from PR reviews flow into CLAUDE.md; warns undisciplined capture creates "dead documentation." |

### 2.4 Documented failure modes (the FMA inputs)

1. **Memory poisoning** — injected/bad content recalled later as truth
   (Unit 42 demonstrated persistent indirect-prompt-injection into agent
   memory; red-teams report 70–95% success entrenching a poison). *Strongest
   known mitigation: a human approval gate — which grove already has.*
2. **Context collapse / brevity bias** — LLM rewriters drop domain detail
   every pass. *Mitigation: delta-only edits to itemized entries (ACE).*
3. **Staleness/rot** — true-when-written facts silently invalidated by
   refactors and dependency bumps. *Mitigation: provenance + lint pass
   (Karpathy Lint, Pollack's health checks, Graphiti bi-temporal marks).*
4. **Over-generalization** — one flaky incident becomes a permanent global
   rule. *Mitigation: promotion criteria + per-entry confirmation signals.*
5. **Context-window bloat** — every system converges on hard budgets
   (Claude Code 200 lines, Windsurf 6K/12K chars, Pollack ~100-line indexes).

---

## 3. The design — six scopes, one pipeline

### 3.1 The scope ladder

Learnings live at the narrowest scope that fits and are **promoted** upward
only when they prove general. Broadest injected first, most-specific last
(Claude Code's own concatenation rule).

| # | Scope | Storage | Committed? | Who sees it |
|---|---|---|---|---|
| L0 | **Session** | Claude Code native (context, auto-memory) | no | one worker |
| L1 | **Repo staging** | `<root>/.grove/learnings/<repo>/` — index + topic files | no (state-like, local) | **every worktree of that repo, instantly** — no waiting for a merge |
| L2 | **Repo committed** | `LEARNINGS.md` (or `AGENTS.md` section) in the repo itself | yes, via PR | anyone who clones; travels with the repo forever |
| L3 | **Workspace** | `<root>/.grove/learnings/workspace.md` | optional | all repos under one grove workspace (the Grid shape: cross-repo conventions) |
| L4 | **Global (user)** | `~/.config/grove/learnings/` — index + topics | n/a | every workspace this user runs; *cross-project strategies only*, never project facts |
| L5 | **Pack (team)** | the pack repo/dir (see grove-connections-design) | yes, via PR | every teammate using the pack — the team's crystallized conventions |

Two deliberate asymmetries, both lifted from Windsurf's local-memories vs
committed-rules split:

- **L1 is the machine's inbox-adjacent scratch layer; L2 is the human-reviewed
  durable layer.** Machine-proposed content lives local (L1) until a human
  promotes it into the committed layer (L2). This is also what solves the
  worktree-propagation problem the ask names: a learning discovered in
  worktree A's branch would otherwise not reach worktree B until merged to
  main — L1 lives *outside* all worktrees at the workspace root, so every
  kickoff sees it immediately.
- **L4 holds strategies, not facts.** "Aurora reader endpoints are read-only"
  is L2/L3; "when a pnpm setup fails in a fresh worktree, check for
  gitignored codegen artifacts first" is L4 material (Karpathy's
  "general problem-solving knowledge").

**Relationship to Claude Code native auto-memory:** don't fight it. Native
memory is per-agent, automatic, unreviewed — grove's system is cross-session,
cross-agent, human-gated. The schema file (§3.5) draws the line: native
memory for "what I noticed," grove learnings for "what we verified."

### 3.2 Storage shape — index + topics, hard budgets

Every scope uses the same two-file pattern (Karpathy's `index.md`, Claude
Code's `MEMORY.md`, Pollack's routing tables):

- **`index.md`** — one line per learning: stable ID, date, one-line hook,
  optional *"read when…"* pointer to a topic file. **Hard cap ~100–150
  lines**; the linter flags overflow and proposes archiving.
- **`topics/*.md`** — detail files read on demand (an agent greps the index,
  then reads the one topic it needs).
- **`log.md`** — append-only record of every accepted/superseded/rejected
  proposal with provenance (grep-parseable prefixes). The audit trail that
  makes propose-only trustworthy.

Entry format (the ovs LEARNINGS.md discipline, extended with lifecycle
fields): `id · date · scope · the fact · what it changed · source
(session/PR/incident) · last-confirmed`, plus the **activation metadata**
of §3.3 (`mode`, `repos`, `paths`, `labels`, `keywords`). Never deleted —
**superseded**, with a pointer to what replaced it (Graphiti's
edge-invalidation, adapted to markdown).

### 3.3 Selection & injection — deterministic retrieval (no wasted tokens)

**The scaling problem (the operator, 2026-07-03):** most learnings are irrelevant to
most tasks. As the corpus grows, injecting whole indexes stops being
acceptable — grove needs to *deterministically* grab the right learnings per
task without an LLM call and without embeddings.

The answer is **activation metadata per entry**, filtered by the cheap task
signals grove already computes. Precedent is strong: Windsurf rules carry
activation modes (`always_on` / `glob` / `model_decision` / `manual`);
Claude Code `.claude/rules/*.md` supports `paths:` frontmatter for lazy
path-scoped loading; and Jeff Clune's reply to Karpathy's system-prompt-
learning thread made exactly this point — notes should be retrieved
per-task, not piled into one giant prompt.

Each entry (index line + topic frontmatter) may carry:

- `mode: always | auto | on-demand` — `always` is a deliberately tiny core
  (the "never do X" tier); `auto` activates on signal match; `on-demand`
  is reachable only via index navigation.
- `repos:` — which repos it applies to (L3/L4 entries).
- `paths:` — glob(s); activates when the task plausibly touches them.
- `labels:` — task labels from the provider.
- `keywords:` — matched against task title/body (`migration`, `worktree`,
  `pnpm`, …).

At `gv grab`, the kickoff assembler — deterministic Go, zero network —
matches these against the task's known signals: repo (resolved), labels
(from the provider), title/body keywords, and likely-touched paths. **These
are the same pre-dispatch signals the router computes** (grove-spec §7.2) —
one signal extraction feeds both model routing and learnings selection.

The injected block is then (budget target ≤150 lines, broadest-first):

1. all `always` entries across L4 → L3 → L1 (capped; linter enforces the cap)
2. `auto` entries whose activation matched, as index lines with "read
   when…" pointers
3. one pointer line per layer to the full index ("more learnings:
   `<path>/index.md` — grep it if you're stuck on something unexpected")
4. L2 arrives for free — it's a committed file in the branch the worktree
   checked out; the kickoff just *points* at it ("read LEARNINGS.md before
   forming a plan").

Index lines and matched entries only, never topic bodies — the worker reads
topic files on demand via the pointers (Pollack's 2–3-hop navigation).
Selection is grep-shaped and auditable: `gv learn why <task-id>` can print
exactly which entries activated and on which signal. The orchestrator's
kickoff gets the same treatment, which is how fleet-level judgment improves
too.

### 3.4 Capture — where candidate learnings come from

Five inlets, all funneling into one place — `learning_proposed` events
appended to `events.jsonl` (the existing append-only substrate; no new state
files):

1. **Worker self-trigger** — the kickoff template defines *when* a worker
   should emit a learning, not just *that it may*. The trigger criteria
   (the operator's framing: things that happened **redundantly** or **outside
   expectations** — prevent the next agent falling in the same hole):
   - a failure that took more than one attempt to diagnose (you hit the
     wall twice — someone else will too)
   - reality contradicted the docs, the config, or a reasonable expectation
     (the gitignored-codegen-artifacts class of surprise)
   - a workaround you'd want the next agent to know without re-deriving
   - explicitly **not**: routine task facts, anything the diff/PR already
     explains, one-off ticket context (the schema's forbidden list, §3.8)

   Mechanically: before the STATUS sentinel, the worker emits
   `LEARNING: <one line — what surprised you and what you verified>`
   (repeatable). The Stop hook — which already parses
   `last_assistant_message` for the sentinel — harvests it. Zero new
   plumbing; graceful degradation (no line → no candidate), matching the
   sentinel's existing compliance model.
2. **A `learn` skill** — grove ships a small Claude Code plugin whose
   `learn` skill is the *manual and interactive* trigger: it auto-triggers
   on phrases ("remember this", "add a learning", "note for next time",
   "we keep hitting this") in worker and orchestrator sessions alike, reads
   `SCHEMA.md`, formats the entry, proposes activation metadata (§3.3) and
   a scope, and calls `gv learn` to file it. The skill is the schema's
   enforcement arm — free-text capture without it tends to produce entries
   the curator has to rewrite. (Distribution: the same channel as the rest
   of the worker environment — a plugin the pack/worker-env declares,
   see grove-connections-design §6.4.)
3. **`gv learn "<text>"`** — the bare CLI inlet underneath the skill, for
   humans and scripts. Cheapest possible capture ("that's worth
   remembering" → one command).
4. **The orchestrator as Reflector** — a new duty mirroring duty 7: mine what
   the deterministic layer already collects — cost-ledger anomalies, audit
   classes, PR review findings, escalation events (once routing exists) — and
   distill *lessons* ("tickets shaped like X keep stalling because Y").
   ACE's key finding applies verbatim: natural execution feedback (CI results,
   review outcomes) is sufficient signal; no labels needed.
   **Cross-session redundancy detection belongs here**, and part of it is
   deterministic: a fold over `events.jsonl` can spot repeated
   blocker/question patterns across tasks ("3 workers hit the same pnpm
   wall this week") and hand the cluster to the orchestrator to distill —
   the individual worker can only see its own hole; the fleet layer sees
   the pattern.
5. **A3/incident flow** (Grid pack) — the pack can declare extra
   inlets, e.g. "when an A3 lands in `docs/a3/`, propose its countermeasures
   as L2 candidates."

**Capture lessons, not transcripts.** The claude-mem critique ("automatic
memory is not learning") is the boundary: raw observation logs stay in native
auto-memory; grove's pipeline only carries distilled "when X, do Y because Z."

### 3.5 Curation — the Curator is the orchestrator, the gate is the human

- Pending candidates surface as a **learnings inbox** (same mail-shaped UX as
  questions: a count in the TUI header, a `gv learn ls` list, orchestrator
  summarizes on request).
- The orchestrator proposes, per candidate: **dedup verdict** (mem0's
  ADD/UPDATE/SUPERSEDE/NOOP against existing entries), **target scope**
  (default: narrowest plausible), and **final wording** conforming to the
  schema.
- The human disposes: accept / edit / reject / change scope — designed to be
  a *seconds-per-item* interaction, because the biggest practical risk of
  propose-only is the queue rotting into rubber-stamping or backlog.
- **Delta operations only.** An accepted proposal touches one entry (append a
  line, amend a line, mark one superseded). The orchestrator is structurally
  forbidden from rewriting a learnings file wholesale — this is the ACE
  context-collapse defense, and it makes every acceptance a reviewable
  one-liner in `log.md` and git history.
- Writes: L1/L3/L4 are direct file writes on acceptance; **L2/L5 acceptances
  produce a draft commit/PR** (they're committed artifacts owned by the team,
  so they ride normal code review — beads' insight that git *is* the sync
  layer).
- **Concurrency invariant: agents write events, only curation writes
  files.** N workers finishing simultaneously all append
  `learning_proposed` to `events.jsonl` (O_APPEND + flock, the existing
  model); the learnings files themselves are touched only by the serial,
  human-gated curation step — so the index/topic files never need their own
  locking story.

### 3.6 Promotion — how learnings climb the ladder

Promotion is a curation verdict, never automatic:

- **L1 → L2** ("this staging entry keeps proving true"): the natural trigger
  is the linter noticing an L1 entry confirmed N times or older than N weeks
  and still referenced; the orchestrator drafts the PR moving it into the
  repo's `LEARNINGS.md`.
- **L2 → L3/L5** ("this isn't about one repo"): same fact observed in ≥2
  repos → propose the workspace/pack version and *supersede* the per-repo
  copies with a pointer (no duplication between layers).
- **anything → L4** ("this is about how to work, not about this code"):
  strategy-shaped entries.

The inverse also exists: **demotion/retirement**. A contradicted or
dead-reference entry gets superseded, with the tombstone in `log.md`.

### 3.7 Hygiene — `gv learn lint`

A deterministic + agent-assisted health pass (Karpathy's Lint, Pollack's
`/kb-reindex`), run on demand and/or as a doctor section:

- deterministic: index over budget · topic files orphaned from any index ·
  entries referencing files/commands that no longer exist (grep-able) ·
  entries past a staleness horizon with no `last-confirmed` touch
- agent-assisted (propose-only): contradictions between layers · duplicates
  across scopes · candidates for promotion/retirement

### 3.8 The schema file — the system that governs the system

One small committed doc (`~/.config/grove/learnings/SCHEMA.md`, overridable
per pack) defines: entry format, scope definitions and what does **not**
belong (secrets, one-off ticket archaeology — grove inherits The Grid's
"forbidden comment patterns" taste here), promotion criteria, budgets, and
the negative-knowledge section (what this system doesn't try to capture).
Karpathy's third layer: the schema is what keeps a propose-only curator
disciplined, and it itself evolves propose-only.

---

## 4. How this feeds the rest of grove

- **Router** (grove-spec §7): L1/L2 learnings about task shapes ("X keeps
  escalating") are exactly the routing-feedback signals §7.6 wants — the
  learnings pipeline is the delivery mechanism for them.
- **Wizard/pack** (grove-connections-design): a team's L5 layer ships
  *inside* the pack, so a new teammate's first `gv init` inherits years of
  crystallized conventions — this is the concrete mechanism behind "grove
  grows with people and projects."
- **Ticket sharpening** (orchestrator duty 5): "why isn't this grabbable"
  verdicts are L3-shaped learnings about ticket-writing; today they evaporate
  in chat, tomorrow they accumulate.
- **`ovs` trial — declined (2026-07-03 interview).** The L1 staging layer +
  `LEARNING:` sentinel would have been small enough to field-test in ovs
  first, but the operator chose to keep the freeze guarantee strict: **learnings
  ship in grove only** (Phase 4+), accepting that the newest design gets its
  first field data there.

---

## 5. FMA

| Risk | Criticality | Mitigation |
|---|---|---|
| Memory poisoning — a worker (or injected content in a ticket) plants a false learning that misleads every future worker | **Critical** | Human approval gate on every write (validated by Cursor at scale; the strongest known mitigation); provenance on every entry; L2/L5 additionally ride PR review. |
| Context collapse — curation gradually rewrites away the detail that made learnings useful | Important | Delta-only operations against itemized entries with stable IDs (ACE); wholesale-rewrite structurally impossible; `log.md` + git history make erosion visible. |
| Rot — stale learnings steer workers wrong long after the codebase moved | Important | Lifecycle fields (`last-confirmed`), supersede-don't-delete, `gv learn lint` dead-reference and staleness checks. |
| Approval queue rots into rubber-stamping or backlog | Important | Seconds-per-item UX (inbox count, one-key accept/reject); orchestrator pre-curates (dedup, wording, scope) so the human only judges truth and placement; batch review in chat. |
| Bloat — injected learnings eat the kickoff context budget | Important | Deterministic activation filtering (§3.3) injects only signal-matched entries; hard caps per scope and on the `always` tier; on-demand topic reads; linter flags overflow. |
| Over-generalization — one incident becomes a global rule | Acceptable | Narrowest-scope default; promotion requires repeated confirmation; demotion path exists. |
| Two memory systems confuse agents (grove learnings vs native auto-memory) | Acceptable | Schema file draws the line ("noticed" vs "verified"); kickoff names exactly which files to read. |
| `LEARNING:` sentinel non-compliance starves the pipeline | Acceptable | It's one of four inlets; graceful degradation mirrors the existing STATUS sentinel model; compliance is a watched metric. |

---

## 6. Open questions

1. **Sentinel and skill coexistence** — both inlets now exist by design
   (§3.4: sentinel for autonomous wrap-up, skill for interactive/manual);
   the open part is whether the kickoff should *require* the skill for
   anything beyond one-liners, and what sentinel compliance looks like in
   practice. Measure.
2. **Helpful/harmful counters** (ACE) — worth the bookkeeping for a solo
   user, or is `last-confirmed` enough until fleets get bigger?
3. **Activation-signal quality** — `paths:`/`keywords:` matching against a
   task *description* (pre-work, files unknown) is heuristic; how often do
   `auto` entries mis-fire or miss? The `gv learn why` audit trail exists to
   measure exactly this before tightening the matcher. *(Note: activation
   filtering itself is deferred to post-first-cut per spec §13 Phase 5 —
   this question activates when it ships.)*
4. ~~**L3 ownership on The Grid**~~ **Resolved (2026-07-03 interview):
   upstream PRs only.** For the Grid pack, L3 has no grove-owned file —
   accepted workspace-level learnings become proposed PRs to the workspace
   marketplace, riding the existing "feed insights back" rule. Grove's own
   L3 file exists only for workspaces without a marketplace.
5. ~~**L2 file identity**~~ **Resolved (2026-07-03 interview):
   `LEARNINGS.md`** at repo root + one pointer line in `AGENTS.md`, keeping
   the portable file small.
6. **Does the orchestrator get a standing Reflector cadence** ("after every
   `gv done`, consider a learning") or stay purely on-demand? On-demand
   matches the no-daemons stance; a `done`-time nudge is a middle ground.
7. **Cross-user sharing for OSS** — L5 packs work for teams; is there a
   public commons story (e.g. shareable strategy packs) or is that scope
   creep? (Probably creep — park it.)

---

## Appendix — sources

**Karpathy:** system-prompt-learning thread (x.com/karpathy/status/1921368644069765486) ·
LLM-wiki gist (gist.github.com/karpathy/442a6bf555914893e9891c11519de94f, via
x.com/karpathy/status/2039805659525644595).

**Mark Pollack:** "Look Ma, No RAG! Building Agent-Native Knowledge Systems"
(blog.pollack.ai/look-ma-no-rag) · "I Read My Agent's Diary"
(blog.pollack.ai/i-read-my-agents-diary) · Agento Studio
(github.com/markpollack/agento-studio).

**Systems:** MemGPT (arxiv.org/abs/2310.08560) + Letta memory blocks
(letta.com/blog/agent-memory) · mem0 (arxiv.org/abs/2504.19413,
docs.mem0.ai) · Zep/Graphiti (arxiv.org/abs/2501.13956,
github.com/getzep/graphiti) · Claude Code memory
(code.claude.com/docs/en/memory) · Cursor Memories (cursor.com/docs) ·
Windsurf memories/rules (docs.devin.ai/desktop/cascade/memories) · AGENTS.md
(agents.md) · claude-mem (docs.claude-mem.ai) · Voyager
(arxiv.org/abs/2305.16291) · Reflexion (arxiv.org/abs/2303.11366) · ACE
(arxiv.org/abs/2510.04618) · beads
(steve-yegge.medium.com/introducing-beads-…, github.com/gastownhall/beads) ·
compounding engineering (every.to/guides/compound-engineering).

**Failure modes:** Unit 42 memory-poisoning writeup
(unit42.paloaltonetworks.com/indirect-prompt-injection-poisons-ai-longterm-memory) ·
mem0 poisoning post · "Automatic Memory Is Not Learning"
(medium.com/@brentwpeterson/automatic-memory-is-not-learning-4191f548df4c) ·
ACE's context-collapse case study (op. cit.).
