# OpenRig research — what grove can learn

Living doc: started 2026-10-18 in an orchestrator chat; append, revise
verdicts as evidence lands. Each idea carries a **verdict** and **what it
rests on** — "intuitively good" is not a verdict. OpenRig source read at
`mvschwarz/openrig@042e813` (2026-10-18, v0.6.x).

## What OpenRig is, in one paragraph

A self-hosted "harness for harnesses" (TypeScript): a daemon (SQLite, an
append-only events table, SSE) plus a `rig` CLI, a TUI and an MCP server
over it. Teams are YAML (`RigSpec` → pods → members), each member a
**persistent seat** (`dev-build@starter`) that successive sessions occupy —
Claude Code, Codex or Pi, each in its own tmux session. Seats coordinate by
typing into each other's panes (`rig send`), a durable owner+status queue,
and daemon-enforced workflows; a watchdog wakes them on schedules. The
human talks to one lead agent. Grove's unit is the opposite shape:
ephemeral worker = one ticket = one worktree = one PR. Most of OpenRig's
machinery exists because seats persist; filter every idea through that.

Blog posts worth rereading: [context engineering as world
building](https://openrig.dev/blog/how-my-agent-teams-find-their-way),
[software-factory building
blocks](https://openrig.dev/blog/software-factory-building-blocks),
[agent civilizations](https://openrig.dev/blog/agent-civilizations).

## Ideas, by area

### Forest from the trees (cockpit)

| Idea | Effort | Verdict | Rests on |
|---|---|---|---|
| Exceptions strip above AGENTS: NEEDS YOU / STALLED / PR BROKEN / READY, empty section omitted | M | untested — promising | OpenRig `packages/tui/src/pulse/pulse-model.ts`; grove's `SortRank` (`internal/state/state.go`) ties ready-to-review with stalled, and PR state never moves a row |
| Idle age instead of created age | S | untested — cheap | same |
| Context % column per worker | S | untested | duty 8's signal lives behind `gv cost --context` today |
| "Seen" ack for finished work (DONE-UNSEEN) | S/M | untested | OpenRig declares it, never computes it |
| Honest `unknown` + "sources partial" footer; rollups say how trustworthy each count is | S | untested | OpenRig `agent-state-taxonomy.md`, `activity-visuals.ts` |
| Command bar + socket with one grammar, so the orchestrator chat can steer the view | L | untested | OpenRig TUI control socket |

OpenRig state model worth copying as vocabulary: three orthogonal axes —
session `present·detached·exited·absent`, activity
`working·idle-at-prompt·unknown`, resumability
`live·resumable·context-walled` — with diagnoses (PARKED = idle ×
open obligation) computed at read time, never stored.

### Context engineering

| Idea | Effort | Verdict | Rests on |
|---|---|---|---|
| 4a `## Read first` ranked reading list in the PR handoff | S | **weak** | see Evidence §4a |
| 4b `STATUS: DONE — PARTIAL` + "Not shipped & why" PR section | S | **section: adopt; sentinel: weak** | see Evidence §4b |
| 5 growth-triggered refocus (three questions + criteria) | M | **counter-indicated as specified** | see Evidence §5 |
| Lore: named stories in Moment/Reflex/Instead/Because form | S | **untested — promising for onboarding/review, not as mid-task refocus** | see Lore |
| `gv ask` — question a finished worker's transcript | M | **promising, tiered** | see Ask |
| Planning dial P0–P4 on tickets | M | untested | OpenRig `docs/reference/planning-dial.md`; would sit beside the effort dial in the ticket-writing skill |
| Warrant rule: a skill rule cites the incident that earned it; canon changes read as additions | S | untested — mostly already practice | OpenRig `knowledge-maturity.md`; tmux-discipline already does it |
| `STATUS: WAITING` (or "block on the job") | S | **plausible — real misuse observed** | Evidence §4b: ~16 of 32 BLOCKED sentinels meant "waiting on CI/job/subagent" |

### Coordination / structure

Adopt candidates: a typed `gv adopt` outcome with no silent fresh
fallback (OpenRig rule 15) plus `--session-id` generated at grab; required
closure reason on `done`/`untrack`; durable one-shot wakes
(`gv remind --after`) that wake only the orchestrator; a UserPromptSubmit
hook so operator typing shows as `working`. All untested.

Do **not** copy: the queue/workflow engine (duplicates what grove reads
from git/GitHub/backends), agents closing their own work (breaks
propose-then-dispose), the daemon + SQLite (grove's `events.jsonl` +
derived view is a deliberate simplicity win). Both projects rejected
pane-scraping as truth; grove's hook sentinels are where OpenRig landed.

## Evidence (2026-10-18 pass)

Corpus: 355 retained worker transcripts (grove, unbrewed, tableplace,
runelite), events.jsonl from five workspaces (~450 tasks with an end
report), `gv cost --analyze`, PR searches. Transcripts before ~September
are gone. Steering classification is regex + manual read, ±10%.

**§4a Read first — weak.** 31 adoptions in ~450 tasks; of 14 inspectable,
11 resumed the same session (full context, list adds nothing). Tool calls
before first mutation: median 2, range 1–7. All 5 compactions in the
corpus happened at idle, none mid-task. Duty 8's five-heading handoff
appears in one grove PR (#188, handoff shakedown). OpenRig's only number:
their PR #591 reports restore cost ~133–157k tokens with the map vs
400–600k without — tokens, not quality — and their issue #1341 says the
PreCompact writer never loaded in seats until 2026-10-17.
*Cheapest settle:* replay the 3 fresh-session pickups (unbrewed
pro-server-568, -631, tableplace-271) via `claude -p` with/without a
hand-written block, 3 runs each; win = >50k tokens saved per pickup.

**§4b PARTIAL — section yes, sentinel weak.** 2–3 of 32 BLOCKED cases fit
PARTIAL (unbrewed p2p-1176 objective counter, p2p-1027 prod pass); workers
already list unshipped items unprompted in 26 of 449 end reports; no
recorded case of building a missing capability in-slice. Risk: label
grabbing — BLOCKED-as-waiting shows workers take the nearest label.

**§5 Refocus — counter-indicated.** 0 grove transcripts reach the 2.6MB
trigger; bytes are a bad token proxy (52k–424k actual context at 2.6MB).
Of 197 operator steering messages: rebase/"main moved" 31%, review fixes
18%, rulings 9%, gate/evidence/tool/spec ~36%, early-stop "continue" 2%,
**goal drift ~1% (0–2)**. Delivery only lands on UserPromptSubmit, which
for a grove worker is kickoff/answer/nudge — when a human already steers.
Research: no study tests mid-session goal re-injection; Arike et al. 2025
([arXiv 2505.02709](https://arxiv.org/abs/2505.02709)) — a strong goal
stated once held near-perfect adherence past 100k tokens. OpenRig has no
measurement and the hook barely ran (#1341).

**Biggest finding the OpenRig lens didn't predict:** a third of all
steering is rebase/conflict churn. Next research target — see Open
threads.

## Lore

OpenRig's best-known parable isn't seat lore (that machinery is
seat-local, grant-gated, `docs/reference/lore-routing.md` — doesn't
transfer); it's the shared onboarding piece
(`packages/daemon/assets/onboarding/01-world-and-purpose.md`):

- **Moonbase** — asked for a doghouse, the agent adds a light → power →
  generator → fuel; "every step is locally defensible, and an hour later
  there is a moon base while the dog is still waiting outside." Stop
  question: *Does the thing actually need this?*
- **Dog door** — exactly the doghouse asked for, with a door the dog
  can't fit. Stop question: *How big is the dog?*

The value is compression: a named failure becomes shared vocabulary
("is this a moonbase?") between operator, orchestrator and workers.
Item template: **Moment** / **Reflex** (the tempting wrong move) /
**Instead** / **Because**.

Grove's own parables, currently filed as rules:
- *The thirty monitors* (grove-252) — unbrewed grew 30 hand-rolled
  `monitor-*.sh` pollers, all written after `gv watch` existed, because
  delivery/liveness had no stream. Reinventing the wheel.
- *The prompt that answers itself* — grepping a pane for `STATUS: DONE`
  fires at second zero because the kickoff prompt ends with it.
- *The rule that already existed* (grove-94) — tmux-discipline forbade
  exact window-name compare; glyphs shipped and nobody re-audited the
  old check, so audit called every live worker disconnected.
- *The empty fetch* (#482) — failed `gh pr view` → empty body → `gh pr
  edit` wiped it.

Shape that keeps "nothing lives in two places": `docs/lore/<slug>.md`
holds the story; skill rules cite it by path instead of retelling;
kickoff carries only the stop questions. Evidence above says drift is
rare, so lore's case is onboarding and review (reinvention, repeat
incidents), not mid-task refocus. Test: seed 3–4, check whether the names
get *used* in nudges and reviews.

## Ask — querying a past session

OpenRig `rig ask` is three tiers (`packages/cli/src/commands/ask.ts`,
`packages/daemon/src/domain/history-query.ts`, `packages/cli/src/ask-wake.ts`):

1. **L1/L2 search — no model at all.** Stop-word-filtered keywords →
   `rg -i` over the session JSONL(s) → matching lines cut to 240 chars.
   Costs the asker only the excerpts.
2. **L3 `--wake` — explicit only, never an automatic escalation.**
   `claude -p --resume <token> "Answer directly…: <q>"`, 180s timeout,
   output only returns. Note: no `--fork-session` — the Q&A appends to the
   original session.

Does it blow up context? Not the asker's: search returns snippets, and a
wake runs in a separate process that hands back only its answer. The
wake itself re-reads the resumed session as uncached input — bounded by
that session's own window (resume loads from the last compaction), so
"one full prompt, once", not "all heavy convos". Their own note: minutes
on a 635MB session file.

Measured on grove (40 largest worker transcripts, 2026-10-18): median
0.87MB, max 72MB; assistant/user **prose is ~2% of bytes** — median 18KB
(~4.5k tokens), max 149KB (~37k tokens). Most of a transcript is tool
output and bookkeeping.

Implication — a grove `gv ask DEV-X "q"` tiered cheapest-first:
- **L0** the PR body, commits, end report (`last_message`) — curated, ~free.
- **L1** `rg` over the task's transcript(s) — snippets, no model.
- **L2** distilled ask: strip to prose (plus tool *names*, not outputs) →
  ≤40k tokens → one cheap-model call. Fits every transcript measured;
  doesn't touch the original session.
- **L3** `--wake` with `--fork-session` — only when the answer needs the
  worker's live reasoning over tool outputs; propose, never automatic.

Open: does L2 answer as well as L3? Settle with ~10 questions whose
answers are known from PRs, scored on both.

## Rebase churn (2026-10-18 pass)

Not an OpenRig idea — the evidence pass surfaced it as the largest
steering cost. 62 operator messages were primarily conflict/rebase
(re-verified; +12 incidental), across 54 tasks: unbrewed 26, grove 21,
tableplace 11, chronicle 3, waterhouse 1.

| Kind | → main | → train | Total |
|---|---|---|---|
| Real textual conflict (code/docs/registries) | 8 | 22 | 30 |
| TASKS.md / LEARNINGS.md only (all grove) | 13 | 0 | 13 |
| Generated files (goldens, protocol.ts, migration numbers) | 0 | 5 | 5 |
| Behind but clean | 1 | 4 | 5 |
| Dependency PR merged | 3 | 4 | 7 |
| Train integration ticket | 0 | 2 | 2 |

Findings (measured):
- **Mechanical, not judgment.** All 62 finished after one message; no
  worker ever raised QUESTION over a conflict hunk. ~61% were pure recipe
  (keep both / take main / regenerate); ~15 carried a judgment the
  operator wrote into the nudge. Workers fixed 18 more conflicts unasked.
  The cost is the operator noticing and typing — 62 interruptions — not
  the fix (median 21 calls, ~4 min; ~$130–235 total at Opus rates).
- **Trains are 60% of it.** Many cars in flight on one train hitting the
  same hub/registry files (tableplace `feature/table-feel`: 29 cars in a
  day; `verbs/registry.ts`, `e2e/specs.ts`, `Pane.svelte`).
- **Grove's TASKS.md is a guaranteed conflict.** 76% of grove's merged PRs
  touch it and `log-append.py` always inserts at the same line; the
  2026-10-07 model-fit batch cost 12 messages, 10 log-only. `main.go` is
  touched by 82 PRs and conflicted once.
- **Nothing automatic today.** `pr_conflicting` pushes ntfy
  (`internal/supervise/push.go`) but nudges nobody; the kickoff never says
  "rebase before the final push"; no branch protection, auto-merge or
  merge queue on any repo (private repos can't have merge queue).
  unbrewed keeps a hand-written `nudge-branch-moved.txt`.

Candidate fixes, ranked by messages removed per effort:
1. **Supervise auto-nudge on `pr_conflicting`** with a fixed recipe
   (fetch, rebase, keep-both / regenerate, marker check, light gate,
   `--force-with-lease`, QUESTION if a hunk needs judgment). ~38–50 of 62.
   M. A standing steering default — needs explicit per-workspace opt-in.
2. **TASKS.md rows that can't collide** — per-ticket file folded later, or
   `gv done` writes the row to main after merge. 13 + part of 6. S/M.
   Avoid `merge=union`: GitHub mergeability likely ignores it
   (unverified), and divergent archive rotations can duplicate rows.
3. **Kickoff: rebase onto latest base before the final push and before
   DONE.** ~5 + 12 incidental. Template-only.
4. **"Regenerate, never hand-merge" rule for generated files** per repo.
   5 + ~4 episodes. Trivial.
5. **Train sequencing** — order cars that touch the same hub files
   (ticket-writing skill); a grab-time overlap warning is the expensive
   version. Biggest root cause, effect inferred.
6. GitHub "update branch" — clean-behind only (5). Lowest.

## Open threads

- [ ] Rebase churn fixes 1–4 — ticket them (see above).
- [ ] Cockpit exceptions strip — draft a design (`docs/plans/`).
- [ ] Seed `docs/lore/` with the four stories above; review in a month
      whether the names are in use.
- [ ] `gv ask` L0–L2 prototype; L2-vs-L3 quality check.
- [ ] `STATUS: WAITING` vs teaching workers to block on the job.
- [ ] Re-check OpenRig after #1341's fix — do they publish refocus or
      restore-map numbers once the hooks actually run?

## Sources

- [openrig.dev](https://openrig.dev), repo
  [mvschwarz/openrig](https://github.com/mvschwarz/openrig)
- [Context engineering as world building](https://openrig.dev/blog/how-my-agent-teams-find-their-way)
- [Building blocks of a software factory](https://openrig.dev/blog/software-factory-building-blocks)
- [Rogue is doing a lot of work](https://openrig.dev/blog/agent-civilizations)
- [Claude and Codex are two halves of one brain](https://openrig.dev/blog/cross-harness-agents)
- [Third-party review (andrew.ooo)](https://andrew.ooo/posts/openrig-review-multi-agent-harness-claude-code-codex/)
- Arike et al. 2025, goal drift — <https://arxiv.org/abs/2505.02709>
- Lost in the Middle — <https://arxiv.org/abs/2307.03172>
- Chroma, context rot — <https://www.trychroma.com/research/context-rot>
