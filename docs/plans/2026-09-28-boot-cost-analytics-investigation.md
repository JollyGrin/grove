# Boot cost analytics — intent + investigation (2026-09-28)

Trigger: Dean, reading OpenRouter's per-request breakdown, saw 80–100k
tokens sent on the FIRST request of a chat. Grove's design bet is that
workers and orchestrators are cheap to discard and restart from a
compact checkpoint. That bet only holds if a fresh start is cheap.
Research only — nothing here is built or filed. Builds on
[2026-09-06-token-diet-research.md](2026-09-06-token-diet-research.md),
which measured context growth; this doc measures the START.

## 1. Intent, formalized

| # | Intent | Question it answers | Phase |
|---|---|---|---|
| I1 | **Boot cost as a first-class metric** | How many tokens does request #1 send, per chat, per workspace, per kind (worker / orchestrator), over time? | now |
| I2 | **Boot decomposition** | What is that number made of, and which parts are ours to trim? | now |
| I3 | **Discard economics** | At what context size is "kill and restart from checkpoint" cheaper than continuing? | now |
| I4 | **A stats surface that can grow** | One hidden page / one CLI verb where this and later analytics land | next |
| I5 | **Audit** | Periodic read over the stats that names the worst offenders and proposes a trim | later |
| I6 | **Bench (testing area)** | Does a trimmed config finish the same jobs at the same competence for fewer tokens? | later, design first |

Order matters: I5 and I6 are where this rabbit-holes. Neither can be
judged without I1–I3, so they stay parked behind them.

## 2. Method

Every local transcript under `~/.claude/projects` and
`~/.cc-work/projects` (950 sessions with usage, Aug–Sep 2026). Boot cost
= `input + cache_creation + cache_read` of the first deduped
(`message.id` + `requestId`) non-sidechain, non-synthetic assistant
line. Scripts: [2026-09-28-boot-cost-scripts/](2026-09-28-boot-cost-scripts/)
(`boot.py` measures, `floor2.py` decomposes).

**The finding that makes I2 cheap:** transcripts record what the harness
injected. Before the first assistant line sit `attachment` records —
`prompt_snapshot` (the system prompt), `instructions` (every CLAUDE.md /
AGENTS.md / MEMORY.md loaded, with path), `skill_listing`,
`deferred_tools_delta`, `agent_listing_delta`, `mcp_instructions_delta`.
So the floor decomposes from the transcript alone, no proxy, no
OpenRouter. Only tool schemas are not recorded; they are the residual.

Caveats: component sizes are chars ÷ 3.6, an estimate; the residual
absorbs that error. Sessions whose first prompt carried an image are
excluded from the decomposition (base64 inflates char counts). Dollar
figures are list-price estimates, never billing.

## 3. Findings

### 3.1 Boot cost per fleet (last 14 days)

| fleet | n | p50 | p90 | max | served from cache |
|---|---|---|---|---|---|
| grove workers | 44 | 46k | 48k | 58k | 61% |
| grove orchestrator | 19 | 59k | 68k | 68k | 49% |
| unbrewed workers | 86 | 49k | 61k | 64k | 57% |
| unbrewed orchestrator | 73 | 69k | 72k | 76k | 52% |
| thegrid workers | 27 | 79k | 81k | 82k | 36% |
| **thegrid orchestrator** | 46 | **94k** | 97k | 118k | 37% |
| waterhouse orchestrator | 5 | 63k | 67k | 67k | 61% |

Dean's 80–100k is real and is thegrid's number. Everything else boots
at 45–70k.

Trend: orchestrator p50 went 49k (Aug) → 63k (Sep); workers 37k → 49k.
The floor is growing about 30% a month and nothing reports it.

### 3.2 By lane — why OpenRouter makes it visible

| lane | boot p50 | served from cache |
|---|---|---|
| Claude models | 47–72k | 44–66% |
| `glm-5.3-flash` (z.ai plan) workers | 31k | 12% |
| `z-ai/glm-5.3-flash` (OpenRouter) orchestrator | 118k (n=2) | 0% |
| `meta/muse-spark` orchestrator | 97k | 0% |

On Claude lanes about half of a boot is a cache READ, because sessions
started within the hour share a prefix. On OpenRouter lanes the first
request is 0–19% cached: the whole boot is billed at full input price.
Same tokens, several times the cost. The per-token lane is where boot
cost hurts most and where it is currently least measured.

### 3.3 What a boot is made of (estimated tokens)

| component | grove worker | grove orch | unbrewed orch | thegrid worker | thegrid orch | ours? |
|---|---|---|---|---|---|---|
| tool schemas (residual) | 31.5k | 33.0k | 38.2k | 40.3k | 46.0k | partly |
| repo CLAUDE.md / AGENTS.md | 1.7k | 1.8k | – | **15.7k** | **12.3k** | yes |
| orchestrator brain | – | 7.9k | 7.7k | – | 7.4k | yes |
| system prompt | 3.7k | 6.6k | 6.4k | 2.7k | 7.6k | no |
| skill listing | 5.0k | 4.9k | 4.1k | 6.8k | 7.0k | partly |
| memory index (MEMORY.md) | 1.9k | 2.1k | 4.7k | 5.0k | 3.6k | yes |
| MCP names + instructions | – | 1.7k | 1.4k | – | 3.7k | yes |
| agent listing | 0.7k | 0.8k | 0.7k | 2.5k | 2.7k | partly |
| kickoff / first prompt | 1.7k | 0.5k | 0.5k | 2.7k | – | yes |
| **total (mean)** | **47k** | **60k** | **65k** | **77k** | **92k** | |

Specific offenders, named:

- `~/git/thegrid/CLAUDE.md` is 41.9k chars — 12–16k tokens on every
  thegrid boot. Largest single trimmable item in any fleet.
- thegrid orchestrator boots with **241 deferred tools and 62 skills**
  (14 plugins enabled in `~/.cc-work/settings.json`); grove boots with
  66 and 42.
- The orchestrator brain costs ~8k tokens on every chat in every
  workspace (26–33k chars).
- The unbrewed orchestrator loads `~/.claude/projects/-Users-grins/memory/MEMORY.md`
  (16.9k chars) — a HOME-level memory index, not the workspace's.
- One unbrewed telemetry repo carries a 32–34k char `AGENTS.md`.
- Orchestrator chats attach claude.ai connectors (Waterhouse, Stripe,
  Cloudflare, Gmail, Calendar, Drive) in every workspace, relevant or not.

Correction to the September doc: it concluded "the floor is not ours to
trim" from grove alone (~2k of ~40k). Fleet-wide that is wrong — on
thegrid 25–30k of the 92k is operator-controlled content.

### 3.4 Discard economics

Boot is 0.1–3% of all context tokens a fleet sends. The boot itself is
not the bill; the bill is the floor re-sent every turn (17–41% per the
September doc). What boot cost sets is the restart price.

Restart cost = boot (half cache-write at 2×, half cache-read) + ~15k
re-orientation. Saving = (bloated − fresh) × cache-read rate, per turn.

| boot | bloated context | breakeven, standard read rate (0.1×) | breakeven, Fable 5.1 (0.025×) |
|---|---|---|---|
| 46k | 150k | 9–14 turns | 34–55 turns |
| 46k | 250k | 4–6 turns | 16–26 turns |
| 60k | 180k | 9–14 turns | 35–57 turns |
| 76k | 276k (thegrid avg) | 6–10 turns | 23–39 turns |
| 91k | 170k (thegrid orch avg) | 20–33 turns | 76–132 turns |

Two conclusions:

1. **Discard is already cheap for workers.** A worker at 250k pays back
   a restart in about 5 turns on Opus/Sonnet. Sessions run 100+ turns.
2. **It is not cheap for a thegrid orchestrator, or on Fable 5.1.** A
   91k boot against a 170k chat needs 20–33 turns to pay back, and
   Fable 5.1's cheap reads stretch every breakeven about 4×. On an
   uncached OpenRouter lane the restart price roughly doubles again.
   The design bet holds per lane, not globally — which is exactly what
   a metric should show.

## 4. What exists already

| item | state | relation |
|---|---|---|
| issue #289 / PR #292 — `gv cost --context` | PR open since 2026-09-07, unmerged | Already defines `Floor`, `FloorShare`, growth buckets, compactions, an orchestrator row. The foundation for I1. |
| issue #275 / PR #278 — guidance-surface diet | PR open, review-only | Trims CLAUDE.md / brain / skills for grove. No before/after metric to judge it by. |
| `gv cost --analyze` | shipped | Per-ticket, workers only. No per-session boot, no orchestrator chats. |
| cockpit costs page (grove-87 tab bar: SPEND, ACCOUNT) | shipped | Built to take more tabs — the natural home for I4. |

## 5. Proposed work (drafts — nothing filed)

| # | Ticket | Size | Depends on |
|---|---|---|---|
| T0 | **Decide PR #292** (cost --context): review and merge, or close and re-scope. Everything below builds on its transcript decoder. | review | – |
| T1 | **Boot record**: `internal/cost` gains `Boot{Tokens, CacheRead, CacheWrite, Model, Lane, Parts[]}` per session, parts decoded from the transcript `attachment` records; `gv cost --boot [--json]` rolls up per workspace × kind × lane with p50/p90 and a 30-day trend. Counts orchestrator chats. Additive JSON. | one PR, rote once fields are fixed | T0 |
| T2 | **Restart advisor**: breakeven turns per live session from its current context, boot and lane read rate; surfaces as a column in `--boot` and as a rot signal for orchestrator duty 8. Pure arithmetic over T1. | small | T1 |
| T3 | **Stats tab** on the costs page: reads `gv cost --boot --json` on open only. No poll, no goroutine, no cache — the cockpit RAM rule applies. | small TUI | T1 |
| T4 | **Boot budget in `gv doctor`**: warn when a workspace's boot p50 passes a configured budget, naming the top three parts by path. | small | T1 |
| T5 | **Bench — design doc only.** A frozen set of 5–8 solved tickets replayed in a scratch workspace under config variants; score = acceptance tests pass, steers, tokens. No code until the design is reviewed. | design | T1 |

Recommended first step is T0 then T1. T3–T5 are the rabbit hole and
wait for numbers from T1.

## 6. Open hypotheses for the bench (unverified)

- Restricting built-in tools on workers shrinks the 31–46k schema
  residual. Whether the flag exists in a usable form, and whether
  workers ship as well without the dropped tools, is untested.
- Splitting `thegrid/CLAUDE.md` into a short root plus nested
  per-package files loads only what a task touches.
- Moving orchestrator brain reference sections into skills loads them
  on demand instead of on every chat.
- Disabling unrelated claude.ai connectors per workspace for
  orchestrator chats.
- A 1h cache-warm boot (restart inside the TTL of a sibling session)
  versus a cold one: the 36% vs 61% cache share between thegrid and
  grove suggests fleet density already changes the price.

## 7. Quick wins visible today, none applied

All are config or doc edits outside grove's code, each Dean's call:

1. Trim `~/git/thegrid/CLAUDE.md` (41.9k chars) — up to ~12k tokens off
   every thegrid boot.
2. Audit the 14 plugins in `~/.cc-work/settings.json` against what
   thegrid workers actually invoke.
3. Find why the unbrewed orchestrator loads the home-level MEMORY.md.
4. Prune MEMORY.md indexes (grove 7k chars, unbrewed 16k, thegrid 19k).
