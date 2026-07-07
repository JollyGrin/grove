# Obsidian as a live board — one-way state projection onto markdown tickets

> Status: **design draft — REVISE per design-reviewer round 1** (findings
> folded below; needs a re-review before it becomes an executable plan).
> The concept survived review (the authored-vs-runtime split and the
> one-way projection are sound); what changed is that three premises the
> first draft presented as *already existing* are actually **new
> components to build**, and the git-cleanliness lifecycle — the feature's
> sharpest edge — was missing. Corrected inline; see "§ Design-review
> round 1" for the audit trail.
> Driver (the operator): the markdown provider already gives great
> *authoring* ergonomics in Obsidian (Bases board, tag filters, one-file
> -per-ticket). What it can't do is show **live progress** — an agent's
> `status: in-progress` is stranded on its worktree branch until merge, so
> the main-checkout board never moves. The ask: get the Linear/GitHub
> "feed a ticket, watch it progress" feel, on a markdown+Obsidian
> foundation, plus a cross-project aggregate board filterable by tag.
> Design basis: DESIGN.md principle 2 (event-sourced state — `events.jsonl`
> append-only, `tasks.json` a derived view), §5.2 (markdown provider +
> the write-location rule), §5.4 (agent-owned transitions), principle 7
> (binary never mutates terminal state), principle 6 (propose-then-dispose).
> Prior context: grove issue #9 (Obsidian exploration); the vault set up
> at `<repo>/.grove/tasks/` with the Base Board plugin.

## The problem, stated precisely

A ticket carries **two different kinds of status**, and today the markdown
provider only surfaces one of them well:

1. **Authored status** — the human's intent and the merged truth:
   `backlog → todo → review → done`. Changes at human-meaningful moments
   (I groom the backlog; the agent proposes `review`; I mark `done` after
   merge). Belongs in git, in the ticket's frontmatter. Obsidian edits it
   beautifully.
2. **Runtime status** — what is happening *right now*: queued, agent
   running, waiting on a question, PR open, CI red, merged. Ephemeral,
   high-frequency, machine-generated, fleet-wide. Belongs in an event log,
   **not** in a git-tracked file.

Linear/GitHub *feel* unified because they are one hosted database that
stores both and renders both in one UI (the card = authored fold; the
activity timeline = runtime log). **grove already has this same split** —
`events.jsonl` (the append-only runtime log) → `tasks.json` (a derived
fold) → the cockpit (the render). The gap is not a missing data model; it
is that the runtime picture renders **only** to the TUI cockpit, never to
the vault. So Obsidian is a grooming board, not a live board.

**Correction from review (was overstated):** "the runtime picture" is not
one fold. `tasks.json` carries the **agent half** (setup/working/waiting/
idle/dead + done + human) — see `internal/state/state.go` `Task`. The
**delivery half** the operator most wants (PR open, CI red, merged) is
**not** in `tasks.json`; the cockpit/audit compute it from a **live `gh`
call** (`internal/audit/gather.go:100`). So "project the fold" only covers
~half of `gv_state`; the delivery half needs its own source (see New
component B and OQ3).

The write-location rule (§5.2) is why we can't just "let the agent update
the file": worktree isolation is non-negotiable (principle 3), so the
agent's `status` edit lands on its branch and is invisible to the
main-checkout board until merge. That rule is correct and stays. This
design does **not** touch it.

## Non-goals (what this is explicitly not)

- **Not** making tickets append-only / event-shaped. Tickets stay
  one-file-per-task, human-authored, git-durable. (Answering the operator's
  literal question: no — the *log* is append-only; a *ticket* is a
  projection target. Event-sourcing is "append-only log **plus** derived
  views," not "everything is an event.")
- **Not** moving tickets out of git into config/state. That would forfeit
  history-rides-with-code and reintroduce a mutable shared store that N
  worktrees race to write — exactly what the append-only log exists to
  avoid.
- **Not** a new source of truth. `events.jsonl` stays the runtime truth;
  the ticket frontmatter stays the authored truth. This adds a **render
  target**, nothing more.
- **Not** an agent writing to the main checkout. The projector is grove
  (the deterministic outer loop), reading the state it already holds.

## Core idea: project the runtime picture down onto the note, under reserved keys

Keep three layers with strict, non-overlapping ownership:

| Layer | Writer | Store | In Obsidian? |
|---|---|---|---|
| **Authored** — title, labels, priority, body, `status` (backlog/todo/review/done) | human, by hand | ticket frontmatter (git) | yes — you edit it |
| **Log** — every runtime event | grove hooks | `events.jsonl` (append-only) | no |
| **Live projection** — running / waiting / pr-open / ci / merged | grove | reserved `gv_*` frontmatter keys on the **main-checkout** note | yes — read-only mirror |

The one new mechanism: a **projector** that computes each task's runtime
picture and **writes the runtime fields onto the main-checkout ticket
file** under a namespaced, grove-owned key block. The human owns
`status`/`labels`/`priority`/body; grove owns `gv_*`. They are disjoint
keys, and Obsidian auto-reloads a file changed on disk.

**This introduces a genuinely new architectural stance** (flagged in
review, finding 5): today the markdown provider is **read-only by
deliberate design** (`internal/provider/provider.go:6-9` — the write path
is explicitly deferred), and the hard rule is "grove **reads**; agents
transition; humans finish." This feature makes the binary a **writer to
the git-tracked task backend for the first time.** It does *not* violate
principle 7 (that rule is specifically about **terminal** state —
`DESIGN.md:92-94` — and grove never writes `status`), nor §5.4
(agent-owned transitions are untouched). But the precedent — binary writes
a machine-owned namespace into backend files — must be **argued and
blessed explicitly**, not slipped past. The justification: disjoint
ownership (`gv_*` only), full reversibility (`--clear`), local and
non-outward (satisfies principle 6 without a prompt).

### Reserved-key contract (the interface)

grove writes only these keys, and must treat everything else in the file
as read-only:

```
gv_state:    running        # queued|running|waiting|review|pr-open|ci-red|merged|idle
gv_pr:       123            # PR number/url once open
gv_worker:   task-014-persist-filter   # tmux window / worktree slug
gv_updated:  2026-07-07T18:22:04Z      # last projection write (staleness tell)
gv_question: "which cache backend?"    # present only when waiting on a human
```

Rules:

- **Never writes `status`.** The authored column is the human's and the
  agent's (via merge). `gv_state` is a *mirror of runtime*, not a
  transition. On the board you group by `status` for grooming and show a
  `gv_state` chip for live signal.
- **Touches only the `gv_*` keys.** ← *This is a new component, not a rule
  the current code can satisfy — see New component A. The existing parser
  drops unknown keys and has no serializer.*
- **Local, reversible, non-outward** — `gv board sync --clear` strips
  every `gv_*` key to undo it completely.

### Why the projection writes to the *main* checkout (not the worktree)

The vault points at the main checkout (that's the copy the human grooms).
The agent's `status` edit is stranded on its branch by design; the
projector's job is precisely to bridge that gap. The two never fight over
`status` because the projector never writes `status`.

## New components this requires (the honest build surface)

The first draft assumed these existed. Review verified they do not. Each is
a plan task in its own right.

### A. Frontmatter-preserving read-modify-write  [BLOCKER-class]

The whole interface rests on "write only `gv_*`, preserve the human's keys,
body, comments." Verified against `internal/provider/markdown.go:47-57,
119-146`: `parseTaskFile` unmarshals into a **fixed 9-key struct** and
**silently drops unknown keys**; there is **no serializer at all** in
`internal/provider/` (the provider is read-only by design). So this is a
**new component to build**, and "round-trips byte-for-byte" is harder than
it sounds — even a `yaml.Node` round-trip doesn't reliably preserve
comments, quoting, or key order. Likely the right shape is a **line-scoped
edit of just the `gv_*` block** (locate/replace/insert those lines, leave
the rest of the file's bytes untouched), with a golden round-trip test over
a file full of human comments/odd quoting. **Deliverable:** the RMW writer
+ its preservation guarantee + tests, before anything else.

### B. A source for the delivery half of `gv_state`  [MAJOR]

`pr-open / ci-red / merged / gv_pr` are **not** in `tasks.json`
(`state.go` `Task` has no PR/CI/merged fields). They come from a live `gh`
call in `internal/audit/gather.go:100`. So the projector needs a delivery
source: either **piggyback on `audit`'s `gh` gather** (accept its cost +
staleness) or **persist last-known `PRState`** into the fold. Decide the
source, its refresh cadence, and its staleness semantics (`gv_updated` is
the tell). This is materially larger than "project `tasks.json`."

### C. A projection home + cadence  [BLOCKER-class — the draft's was wrong]

The first draft said "fold into the existing post-event hook cadence."
Verified false (`internal/hooks/hooks.go:52-72`): the hook receiver is
**read-only** — it uses `state.ReadTasks`, and the comment explicitly says
it **never calls `state.Load`, which folds and rewrites** the derived view
(that runs on *read commands* like `gv ls`, `state.go:103-128`). Hooks also
fire in the **worker's worktree cwd**, are scoped by `FindByCwd` to that
task, must stay "fast, silent, exit 0," and fire during live-ovs
coexistence — the wrong place to hang a cross-tree file writer. And PR/CI/
merged transitions don't come from hooks at all. **Realistic home:** the
read-command path (`state.Load` already runs on every `gv ls`) writes the
projection as a side effect, **or** a dedicated `gv board sync` on a poll
timer. Pick one; re-derive OQ1 against it.

## Git lifecycle of `gv_*` keys — the sharpest edge (was missing entirely)

Grove writes `gv_*` into git-tracked `.grove/tasks/*.md` in the main
checkout. Consequences the first draft ignored (review finding 4):

- **Every projection dirties the working tree.** Modified ticket files show
  in `git status`/`git diff` and can be **accidentally committed** or swept
  into an unrelated commit. You **cannot `.gitignore` individual
  frontmatter keys**, so there's no clean way to hide them.
- **Merge/checkout interaction.** The agent's branch sets `status:` but has
  **no** `gv_*` keys; the main checkout has **uncommitted** `gv_*` keys. On
  merge/pull/checkout these can conflict or be clobbered, and nobody owns
  stripping them. This undercuts the "history-rides-with-code" virtue the
  design leans on.

**This must be decided before a plan.** Two candidate stances:
- **Transient (recommended lean):** `gv_*` keys are runtime-only, never
  committed. Enforce with a **pre-commit strip** hook grove installs (or a
  documented "never `git add` a `gv_*` change" + a `gv board sync --clear`
  users run before committing). Downside: they always show as working-tree
  noise.
- **Committed:** accept them in history as a lightweight audit trail.
  Downside: pollutes diffs, guarantees merge churn, and re-raises the
  binary-writes-backend concern at commit granularity.
- **Sidecar reconsidered:** a separate uncommitted `*.gv.md` / `.json`
  avoids polluting the ticket file — but Obsidian Bases can't *join* two
  notes into one card, so the live chips wouldn't render on the ticket
  card. If the git lifecycle proves intractable, this is the fallback, at
  the cost of the unified-card UX.

## Cross-project aggregate (mostly free today)

- **Tag/label filtering:** labels already live in frontmatter; Obsidian
  Bases filters and groups on them natively. Zero work.
- **Multi-project board:** a symlink vault — `~/grove-vault/<project> ->
  <repo>/.grove/tasks` per project, plus one `.base` grouped by
  `file.folder`. One board, swim-lanes per project, filter across all.
  Candidate future convenience: `gv vault [--add <repo>]` to generate and
  refresh the symlink set and scaffold the aggregate `.base`. Not required
  — the symlink folder works by hand today.

Each repo keeps its **own** in-repo `.grove/tasks/`; there is no shared
ticket pool. `provider.markdown.dir` is one string but resolves relative to
each repo, so isolation is automatic. The aggregate is a *view* over many
folders, not a merged store.

## Surface / CLI shape (sketch, for the plan to firm up)

- `gv board sync [--repo R]` — one-shot projection of the current runtime
  picture onto the main-checkout notes. Writes only `gv_*` keys; prints
  what changed.
- `gv board sync --clear` — strip all `gv_*` keys (full undo).
- **Automatic mode** — projection as a side effect of the read-command fold
  (`state.Load` on `gv ls`) or a poll timer; **not** the hook receiver
  (New component C). Gated behind config so Linear/GitHub users are
  unaffected.
- **Config** — `provider.markdown.liveProjection: true` (default off);
  reserved-key prefix fixed at `gv_`.

## Open questions

- **OQ1 — cadence + Obsidian reload churn.** Rewriting files the human has
  open triggers reloads. Write **on runtime-change only** (idempotent
  no-ops), debounce, atomic temp-file rename. Decide against the chosen
  home (New component C).
- **OQ2 — conflict window (revised — old mitigation was not credible).**
  Human saves in Obsidian while the projector writes. Obsidian holds **no
  OS advisory lock**, and "human is mid-edit" is **not observable** from an
  external writer — so "skip on lock/dirty" was wrong. Honest mitigations:
  write-on-change-only, **atomic temp-file rename**, RMW-with-re-read to
  narrow (not close) the window, accept the benign reload. Sidecar is the
  escape hatch if this bites.
- **OQ3 — promoted to a decision (was understated).** Where does the
  delivery half come from — piggyback `audit`'s `gh` gather, or persist
  `PRState` in the fold? Cost and staleness must be specified. See New
  component B.
- **OQ4 — `gv_pr` vs the dormant `pr:` field.** `pr:` exists in the schema
  (`markdown.go:56`) but **nothing writes it today** — it's read-only and
  unpopulated. Lean: keep `gv_pr` separate (clean human/agent-vs-grove
  ownership split); note `pr:` is a dormant field.
- **OQ5 — markdown-only, or generalize** so Linear/GitHub repos could also
  project a runtime mirror into an Obsidian vault? Out of scope here; parked.
- **OQ6 — dogfooding gap (new, review finding 6).** This repo runs
  `provider: github` (`.grove/config.yaml`), so grove can't dogfood the
  live board on itself. The plan needs a dedicated `provider: markdown`
  E2E workspace (extend the dummy-data pattern) — otherwise it ships with
  no self-hosted exercise path.

## Risks / FMA

| Risk | Mitigation |
|---|---|
| Frontmatter RMW clobbers human keys/body/comments | New component A: line-scoped `gv_*` edit + golden round-trip test over a comment-heavy file; the current struct parser **cannot** do this — build it first |
| Projector wired to hooks (draft's original plan) fails | New component C: hooks are read-only and worktree-scoped; home the projection on the read-command fold or a poll timer instead |
| `gv_state` delivery half has no data source | New component B: piggyback `audit`'s `gh` gather or persist `PRState`; specify cost + staleness (OQ3) |
| `gv_*` keys pollute git / merge churn / accidental commit | Git-lifecycle decision **before plan**: transient + pre-commit strip (lean) vs committed vs sidecar fallback |
| Binary-writes-to-backend precedent set implicitly | State it explicitly; justify via `gv_*` namespace + `--clear` reversibility + non-outward; get it blessed |
| Reads as a `status` transition → principle 7 / §5.4 | grove never writes `status`; `gv_state` is a namespaced runtime *mirror*; board groups by `status`, not `gv_state` |
| Save-vs-write race corrupts a file | OQ2 — atomic temp-file rename + write-on-change + RMW re-read; **no** false claim of lock/dirty detection |
| Feature bloats the provider for non-Obsidian users | Opt-in config (`liveProjection: false` default); Linear/GitHub untouched; golden/kickoff tests stay green |
| No self-hosted exercise path (grove is `provider: github`) | OQ6 — dedicated markdown E2E workspace in the dummy-data pattern |
| "Live board" oversold as source of truth | Docs frame it exactly: `events.jsonl`/cockpit is truth; the vault mirror is a convenience render with a `gv_updated` staleness tell |

## Design-review round 1 (audit trail)

Reviewed against the code (not the draft's paraphrase). Verdict **REVISE**.
The concept and the authored-vs-runtime split survived; four premises were
re-scoped from "already exists" to "new component / unaddressed":

1. **[blocker]** Frontmatter RMW doesn't exist — read-only fixed-struct
   parser, no serializer (`markdown.go:47-57,119-146`,
   `provider.go:6-9`). → New component A.
2. **[blocker]** "Existing post-event hook cadence" is the wrong home —
   hooks are read-only, never fold `tasks.json`, fire in the worktree cwd
   (`hooks.go:52-72`; fold runs on read commands, `state.go:103-128`). →
   New component C.
3. **[major]** The fold has **no** PR/CI/merged fields (`state.go` `Task`);
   delivery status comes from live `gh` (`audit/gather.go:100`). → New
   component B / OQ3.
4. **[major]** Git-cleanliness + merge lifecycle of `gv_*` was missing
   entirely → new "Git lifecycle" section.
5. **[major]** Principle-7 argument was narrowly true but dodged the
   binary-writes-to-backend precedent → stated explicitly in Core idea.
6. **[minor]** Unexercised by dogfooding (`provider: github`) → OQ6.
7. **[minor]** OQ2 concurrency mitigation not credible (no observable lock)
   → OQ2 revised.
8. **[minor]** `pr:` is a dormant, unwritten schema field → OQ4 note.

**Next:** a second design-review pass on this revision, then — only if it
clears — promote to an executable plan (`docs/plans/…-plan.md`) with the
four new components as tasks. **Paused here deliberately.**

## Recommendation (unchanged in spirit, honest about cost)

Markdown is the right foundation; the fix is not to event-shape tickets or
exile them from git. Add **one one-way projection**: grove's runtime
picture written onto the main-checkout notes under reserved `gv_*` keys,
opt-in, homed on the read-command fold or a poll timer. That turns the
vault from a grooming board into a Linear-style live board — without
touching the write-location rule, worktree isolation, the append-only log,
or the terminal-state rule. But it is **not** a small wiring job: it needs
a frontmatter-preserving writer, a delivery-status source, and a decided
git lifecycle — three real components the first draft hand-waved. The
aggregate/tag-filter half remains free via Bases + a symlink vault.
