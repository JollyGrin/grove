# Jev research — a typed judge inside grove

Living doc: started 2026-10-19 in an orchestrator chat; append, revise
verdicts as evidence lands. Each idea carries a **verdict** and **what it
rests on** — "intuitively good" is not a verdict. Nothing here is built;
the first step for every row is a measurement grove can already take.

## What Jev is, in one paragraph

TypeSafe's "System One" model. A call carries a **state** (text or JSON,
budget about 64K tokens) and one or more typed **questions**; the answer is
a typed value with a probability distribution and a confidence, never
prose. Three primitives: **Choice** (one of N options → `choice`,
`probabilities`, `confidence`), **Score** (a rubric → `score`,
`probabilities`, `confidence`), **Noul** (is this statement true → `noul`
0–1). Questions in one call are evaluated in parallel against the same
state, so adding questions barely moves latency and never rots context.
Vendor figures: about 100–300 ms warm, $0.042 per million input tokens,
output free. It is served on OpenRouter as `typesafe/jev-1.14`, so grove's
existing `OPENROUTER_API_KEY` (resolved by `internal/openrouter`) already
reaches it — no new secret, but a per-token lane.

The caveats that shape every design below, all from outside write-ups
rather than the vendor:

- **Not smarter than a frontier model** — 67.8% vs 74.1% on the vendor's
  own dashboard, whose labels came from other models, not ground truth.
- **Sensitive to question wording** — wrong criteria text dropped one
  study's accuracy to 16.7%. The question is the program.
- **No "none of the above" unless you add it** — 0 of 30 out-of-scope
  inputs flagged, each at 0.99 confidence.
- **Calibration degrades out of distribution**; Score was the weakest
  primitive in an outside study (44.7%).
- **Everything judged leaves the machine** (TypeSafe or OpenRouter).

Rule of thumb that falls out: Jev earns its keep where a question **fires
often, needs no prose, and is recoverable or confidence-gated with
escalation**. Where a deterministic rule exists, use the rule. Where the
decision is irreversible, propose-then-dispose stays — Jev only makes the
proposal cheaper and better labelled.

## The ecosystem, read 2026-10-19

| Project | Shape | What to take |
|---|---|---|
| [jev-kit](https://github.com/jonathanavis96/jev-kit) (Python) | Airlock `PreToolUse` guard, tier guard on `Agent` dispatches, plocate file index, review/belay/compaction opt-ins | Rules table first, Jev only for the ambiguous remainder; deny needs confidence ≥ 0.8 **and** a 0.4 margin over the runner-up; **shadow log** of what enforce mode would have done + a report over it; redact every string before it leaves; fail open on any error. Numbers: +33 ms median for a rule-covered call, ~0.3 s for a judged one; tier guard 98.2% on 57 labelled dispatches with zero false denies; search intent 84.4% on 32 rows (its weakest) |
| [jev-use](https://pi.dev/packages/jev-use?name=jev) (npm) | Library + MCP + `hook gate`; `check` / `pick` / `rate` | Every answer carries `escalate: true` with a typed reason when Jev shouldn't decide; a dispatch layer routes some steps away from Jev before any call. Claims p50 ~230 ms, ~$0.02 per 1,000 judgments |
| [BorisLeMeec/jev](https://github.com/BorisLeMeec/jev) (**Go**) | `find` / `ask` / `scan` CLI + a `PreToolUse` hook on `Read` | Skeleton-screen each file (declarations, routes), re-score the leaders on full contents, chunk the winners to a line. Read hook trims 400-line-to-80 KB files to ~⅕ centred on the current request, with a note on how to re-read whole. Gates: explicit `offset` reads untouched; > 80 KB refused rather than narrowed (confidence across sections isn't comparable); `JEV_HOOK_DISABLE=1`. Local `jev gain` ledger of runs/tokens/spend. Same stack as grove |
| [jev-guard](https://github.com/leepokai/jev-guard) | Risk-scores every tool call with session context (deny/ask/allow), flags prompt injection in results | The "session context" part — a call is judged against what the session has been doing, not alone |

## Ideas, by area

### Guard (PreToolUse)

Grove installs `Stop`, `SessionStart`, `SessionEnd`, `Notification` hooks
today and no `PreToolUse`. The only guard that exists is the no-secrets
script in the operator's config dir, armed on contributor lanes only
(LEARNINGS 2026-10-11, grove-470). The roadmap already carries
"worker-autonomy core safety guard (connections §6.4)" — this is that row.

| Idea | Effort | Verdict | Rests on |
|---|---|---|---|
| Rules table in Go (`internal/guard`), fail **closed**: `rm -rf` outside the worktree, `git push --force` to base, `DROP`/`TRUNCATE`, `gh issue close`, `gh pr merge`, writes to `~/.cc-work/settings.json`, worktree/branch deletes grove didn't create | M | untested — do first, no Jev needed | CLAUDE.md hard rules are already a rules table in prose; jev-kit's "most calls never reach Jev" |
| Jev Choice {allow, warn, block} on the ambiguous remainder, fail **open**, deny only at conf ≥ 0.8 with 0.4 margin | M | untested — shadow mode first, weeks not days | jev-kit Airlock thresholds; calibration-drift caveat above |
| Session-context state: last N tool calls + ticket title in the judged state, not the call alone | S on top | untested | jev-guard; `rm` of a file the worker just created is not the same `rm` |
| Matcher on `Bash` and `Write` only — never `Read`/`Grep`/`Edit` | — | decided | workers make hundreds of tool calls a turn; +33 ms × N is the budget, +300 ms × N is not |
| Gate on a grove env var (the `GROVE_DATA_LANE` pattern) | — | decided | hooks live in the **shared** `~/.cc-work/settings.json`; without the gate the guard fires in the operator's own chats |
| Redact before judging (`~/.config/grove/.env` names, tokens, the ntfy URL) | S | required for the Jev layer | jev-kit `redact.py`; everything judged leaves the box |

"Prevent deleting the database" is a rules-table row, not a Jev row. Jev's
job is the long tail the table can't name.

### Finding files before the worker boots

| Idea | Effort | Verdict | Rests on |
|---|---|---|---|
| At `gv grab`, run `jev find "<ticket title + body>"` over the repo and inject the top K paths into the kickoff prompt as **hints** | S | untested — cheapest to try, zero transcript cost | BorisLeMeec `find`; 84.4% search-intent calibration says hints, never constraints |
| Same at `gv adopt` with the PR body as the query | S | untested | the handoff body is the state that survives (duty 8) |
| Feed the hints to the Explore subagent instead of the main context | S | untested | keeps a wrong hint out of the worker's own window |

Honest comparison is against the `grep`/Explore pass the worker already
does in its first turns. Jev's edge is price per file screened and that it
runs **outside** the session. Count first: how many turns and tokens do
recent workers spend before their first edit? (`gv cost --context`, the
`top` reads.)

### Read trimming

| Idea | Effort | Verdict | Rests on |
|---|---|---|---|
| Install the Go Read hook on a per-repo opt-in | S | **unproven for grove until measured** | value = share of context growth that is whole-file reads of 400-line+ files; `gv cost --context DEV-X` already reports `growth` and `top` per ticket — run it across the last 20 done tickets before deciding |
| Risk: a trimmed view drops the target chunk → re-read (cost doubles) or an edit against a partial mental model | — | real | hook's own gates (offset untouched, > 80 KB refused) are the right shape; keep them |
| Exposure: ships file contents off-box | — | largest of the three | private repos opt in per `repo:` block, never globally |

### Judges inside grove itself

Each of these is a regex or heuristic today, or a hole. Each is a typed
question over ≤ 64K of text.

| Site | Today | Jev question | Effort | Verdict | Rests on |
|---|---|---|---|---|---|
| Stop hook, worker stops with **no STATUS line** | classified as an idle `agent_status` stop — monitoring rule 4's hole | Noul "this message asks the operator something" + Choice {done, question, blocked, working} over the last assistant message; override only at conf ≥ 0.8 | M | untested — highest value: turns silence into a sentinel | the kickoff prompt is never in the judged state (only the agent's message), so rule 1 holds |
| `gv supervise` liveness: `worker_errored` markers (429, sleep-cut, API error) | pane-tail regex | Choice {ok, errored, waiting, vanished} over the pane tail | S | untested | regex misses new wordings; Jev needs an explicit `other` option |
| Backlog triage (duty 2) agent-suitability | the orchestrator reads each ticket | Score rubric per ticket: criteria present, surface size, repo inferable, blocked-by | S | untested — Score is Jev's weakest primitive | ticket-writing skill's sizing rules are the rubric text |
| Effort routing (ticket-writing's rote test) | operator judgment | Choice {low, medium, high, xhigh} over the ticket body | S | untested | jev-kit tier guard: 98.2%, zero false denies, same shape |
| `gv handoff` verify: "is the PR body a real handoff" | thinness heuristic | Score against the five-heading contract | S | untested | duty 8 checkpoint headings |
| `gv learnings` candidates: entry names a skill the skill hasn't absorbed | string match | Noul "this entry is already a rule in the skill text" | S | untested | duty 11 |
| Tier guard on `Agent` dispatches: worker spawning a subagent on a model/effort a rung too expensive | nothing | Choice over the dispatch prompt → rung | M | parked | jev-kit; grove routes by ticket, not by subagent |

## Telemetry — what is missing

Grove has the spine: `events.jsonl` (append-only, a contract) and
`gv cost`, which parses transcripts into per-tool `pre_tokens` /
`post_tokens` / `ms`, compactions, `growth` and `top` reads
(`internal/cost/context.go`). What a judge needs on top:

- **A judgment ledger**: site, question id + wording hash, answer,
  probabilities, confidence, latency ms, state tokens, action taken
  (allow / deny / trim / none), mode (shadow / enforce), ticket, session.
- **Not in `events.jsonl`.** That file is the transition stream
  `gv watch` tails and every plugin reads; hundreds of judgments per task
  would flood it and every additive-only consumer. A separate
  `<state>/judgments.jsonl` per workspace, same O_APPEND + flock
  discipline, summarized into `gv cost --json` (additive field).
- **A calibration report** (`gv cost --judge`): bucket confidence against
  what the transcript later shows actually happened — did the denied
  command get re-run and succeed harmlessly, did the trimmed read get
  re-read whole, did the no-STATUS stop turn out to be a question. This
  is jev-kit's shadow log + `airlock.report`, and it is the only thing
  that licenses flipping a site from shadow to enforce.
- **Question wording is versioned state**: a changed question is a new
  calibration series. Hash it into every row.

## Order of work

1. **Measure, free, this week.** `gv cost --context` over the last ~20
   done tickets → share of growth from big reads (decides Read trimming)
   and tokens-before-first-edit (decides find hints). Count no-STATUS
   stops in `events.jsonl` (decides the Stop-hook judge).
2. **Rules-table guard**, Go, tested, fail closed, env-gated, no Jev.
   This is the roadmap row and stands alone.
3. **Shadow Jev layer** on the guard + the judgment ledger + the report.
   Weeks of shadow before any enforce.
4. **Find hints at grab**, hints only.
5. **Stop-hook judge** in shadow, compared against the operator's own
   answers to "was that a question?".
6. Read trimming only if step 1 says reads dominate.

## Open threads

- Lane: `typesafe/jev-1.14` on OpenRouter is per-token. At $0.042/M it is
  noise next to a worker turn, but it is still money the flat lanes don't
  spend; a direct TypeSafe key may be cheaper and is one more secret.
  Operator's call, never the orchestrator's (no-model-routing memory).
- Does a `PreToolUse` hook on `Bash` fire for the orchestrator chat's own
  `gv` commands? If so the guard must whitelist `gv` or the chat guards
  itself out of steering.
- jev-kit's **belay** (send a finished-but-unverified agent back to check
  its work) overlaps grove's Stop-hook + `STATUS:` contract. Worth a
  second read once the Stop-hook judge exists — it may be the same site.
- jev-kit's compaction plugin sends 25K tokens of raw tool results per
  request. Never, for private repos.

## Sources

- [TypeSafe docs](https://docs.typesafe.ai) — primitives, confidence,
  patterns (no pricing or schema on the intro page; `/llms.txt` indexes
  the rest)
- [OpenRouter `typesafe/jev-1.14`](https://openrouter.ai/typesafe/jev-1.14)
- [jev-kit](https://github.com/jonathanavis96/jev-kit) ·
  [jev-use](https://pi.dev/packages/jev-use?name=jev) ·
  [BorisLeMeec/jev](https://github.com/BorisLeMeec/jev) ·
  [jev-guard](https://github.com/leepokai/jev-guard)
- [Jev: what we know](https://www.orcarouter.ai/blog/jev-typesafe-system-one-what-we-know)
  — the caveats section above; outside studies, small samples
- [Sanity glossary entry](https://www.sanity.io/glossary/jev-typesafe-ai-model)
