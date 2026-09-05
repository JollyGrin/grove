---
name: model-lanes
description: Explicitly invoked only (/model-lanes) — never for ordinary dispatch. Use when the operator asks to split a workload across the Claude sub, a flat-rate coding plan (z.ai GLM), and pay-per-token OpenRouter lanes: reads live capacity per lane, calibrates this workspace's cost-per-turn, sizes the open backlog, and proposes per-ticket routing with grab commands for approval. Earns its keep when the Claude sub runs low or a flat plan caps out.
---

# Model lanes — capacity-aware dispatch routing

Produce **one proposal the operator approves or edits**: which ticket goes
to which lane, in what order, with the grab command for each, and what it
costs against what is actually left. Never dispatch from this skill
without that approval.

Works in any grove workspace on any host. Everything workspace-specific
(repos, provider, backlog source, cost-per-turn) is **derived at run time**
in Steps 0–2 — nothing about one repo is baked in.

## The totem pole

**Claude → flat-rate plan → OpenRouter.** Fill from the top. Drop a tier
only when the tier above is genuinely scarce, and drop the *ticket* down
the pole rather than the whole backlog — the cheapest lane that can still
finish a given ticket wins, and "can still finish" is a property of the
ticket, not the day.

Each lane fails differently, which is what makes routing non-obvious:

| Lane | Fails how | Consequence |
|---|---|---|
| Claude sub | soft — you watch the % fall | plan around it |
| Flat plan (z.ai) | **hard** — 429 mid-turn | uncommitted work on the floor, needs a rescue |
| OpenRouter | never — it just bills | capability risk only |

The hard failure is why a flat plan needs sizing discipline the other two
do not. The *absence* of failure is why OpenRouter is the right overflow
even though it is the only one that costs marginal money.

**Spend the expiring resource first.** Subscription percentage and plan
credits are use-it-or-lose-it on a reset clock; OpenRouter dollars keep.
So when two lanes could both take a ticket, give it to the one whose
budget expires sooner — and when a plan's window is momentarily dry but
its period still has credits left, reach past it to OpenRouter rather than
idling, then come back to the plan when the window refills.

## Step 0 — locate the workspace

```bash
cat ~/.config/grove/registry.yaml        # every workspace: root, label, scope
gv ls --json --no-pr                     # what is in flight here, right now
```

Read that workspace's `.grove/config.yaml` for `repos:` (the `--repo`
names you will emit), `provider.kind` (where the backlog lives), and any
workspace-level `model_profiles:` overrides. The global
`~/.config/grove/config.yaml` holds the default profiles and pricing.

**How the two layers combine** (`internal/config/merge.go`) — this decides
what you are actually allowed to read out of the global file:

| Section | Behavior when the workspace sets it |
|---|---|
| `repos:`, `provider:` | **replaced wholesale** — the global entries vanish, no per-repo or per-sub-field merging |
| `orchestrator:` | workspace-only; the global block is dropped *even when the workspace sets nothing* |
| everything else (`model_profiles:`, `cost:`, `hosts:`, `linear:`, `notify:`, …) | deep-merged field-wise, workspace wins on a collision |

So a workspace that lists one repo has exactly one repo — never assume a
globally-configured repo is reachable from inside a workspace, and never
emit a `--repo` name you did not read out of the merged view.

If the operator named a host, plan for it: `grab`, `ls`, `answer`,
`nudge`, `diff`, `pause`, `untrack`, `adopt`, `handoff` all take `--host`,
so a lane assignment can target the remote box. Note where this skill
lives: it is **repo-tracked** at `<workspace>/.claude/skills/model-lanes/`,
so it travels with a clone but not with a `--host` dispatch — a remote host
needs the repo checked out (or its own copy under `~/.claude/skills/`)
before an orchestrator there can load it.

## Step 1 — read live capacity on every lane

**Claude sub** — no API. Take the percentage from the invocation
(`/model-lanes 18%`); if absent, ask in one line — the whole plan pivots
on it — and keep working on everything that does not depend on it.

**z.ai coding plan** — exact, always check it rather than assuming:

```bash
set -a; . ~/.config/grove/.env; set +a
: "${ZAI_API_KEY:?not in ~/.config/grove/.env — see below}"
curl -s -H "Authorization: Bearer $ZAI_API_KEY" \
  https://api.z.ai/api/monitor/usage/quota/limit
```

**Where `ZAI_API_KEY` comes from.** The *name* is not special to grove: it
is whatever the profile's `auth_token_env` says, and the shipped `zai-glm`
profile says `ZAI_API_KEY` (`auth_token_env` holds the env VAR NAME, never
the key — see `config.example.yaml`). The *value* lives in
`~/.config/grove/.env`, the same file `gv` sources when it wraps a worker,
as `export ZAI_API_KEY=<key from z.ai's API-key page>`. That file is
per-machine and never committed, so on any host but the one where the plan
was set up this check **401s silently** and the lane looks capped when it is
merely unauthenticated. Read the profile's `auth_token_env` first and probe
that variable; a 401 here means "no key on this box", not "no credits".

`limits[]` carries `unit:3,number:5` (the **5-hour** bucket) and
`unit:6,number:1` (the **weekly** bucket), each with `usage` (cap),
`currentValue` (spent), `remaining`, `nextResetTime` (epoch ms, **SGT** —
convert to the operator's local time, that is the point of converting).
`level` confirms the tier.

**OpenRouter** — balance and burn rate:

```bash
curl -s -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  https://openrouter.ai/api/v1/key
```

`limit_remaining` (null = pay-as-you-go, no cap), `usage_weekly`,
`usage_monthly`. This lane cannot run out mid-ticket; it can only cost
more than intended, so the number to report is burn rate, not headroom.

**The clock.** z.ai peak is **Mon–Fri 14:00–18:00 SGT**; everything else
is **half rate**. Convert to the operator's timezone and say which side of
the line the proposal falls on — it is a straight 2× on plan throughput
and routinely changes the answer.

## Step 2 — calibrate this workspace

Cost per ticket is `turns × resident context`, and resident context is a
property of the *repo* (its CLAUDE.md, skills, tool surface, typical file
sizes), not of the model. Derive it here instead of assuming:

```bash
gv cost --analyze --json    # tokens and turns per ticket, this workspace
```

There is **no `total_tokens` field**. Each row is a ticket, and the total
is the sum of the five token counters under `cost:` —
`input_tokens + output_tokens + cache_create_5m_tokens +
cache_create_1h_tokens + cache_read_tokens`. (`models[].tokens` is that
same sum for one model, so never add it to the others — it double-counts.)
Mean resident context is that total over total turns:

```bash
gv cost --analyze --json | jq -r '
  [ .report.rows[].cost | select(.turns > 0) ] as $c
  | ($c | map(.input_tokens + .output_tokens + .cache_create_5m_tokens
              + .cache_create_1h_tokens + .cache_read_tokens) | add) as $tok
  | ($c | map(.turns) | add) as $turns
  | ($c | map(.cache_read_tokens) | add) as $cache
  | "tickets      \($c|length)
turns        \($turns)
tokens       \($tok)
resident_k   \(($tok/$turns/1000)|floor)k
cache share  \((($cache/$tok)*100)|floor)%"'
```

Live on the grove workspace, 2026-08-29: 16 tickets, ~1,070 turns, ~115M
tokens → **~108k resident, 95% cache read** — it moves as work lands,
which is why you re-run it rather than quoting this line. Then:

| Lane | credits or $ per turn |
|---|---|
| GLM-5.3 (peak) | `resident_k × 0.20` credits |
| GLM-5.3-Flash (peak) | `resident_k × 0.067` credits |
| off-peak | halve either |
| OpenRouter | `resident_k × cache_read_$/Mtok × 1.26 / 1000` |

The `1.26` grosses up cache reads to include fresh input and output.
Calibrated against a measured grove workspace: 91k resident → 18.6
credits/turn on GLM-5.3 at peak; 80 turns → 1,485 credits (measured
1,485). Resident context drifts as a repo grows — the same query read 108k
on 2026-08-29, i.e. ~21.6 credits/turn — so **re-run the query each time**
rather than reusing a number from a previous proposal. Re-derive the
weights too for a workspace whose cache-read share is far from ~95%; the
query above prints it.

## Step 3 — size the backlog

Pull candidates from the workspace's provider — `gh issue list --state
open --limit 40 --json number,title,labels` for `kind: github`, the Linear
MCP tools for `kind: linear`.

Routing needs **size and difficulty together**. A `rote`-style label is a
*difficulty* signal; grove has no size label, and that gap is what kills
tickets on a windowed lane. Estimate turns from the body:

| Shape | turns |
|---|---:|
| one function, exact `file:line`, exact fix stated | 20–30 |
| 2–4 enumerated call sites in one package | 30–50 |
| new subcommand, or cross-package change | 60–90 |
| touches UI **and** e2e | +30 |
| a bundle of N independently-located fixes | N × 15 |
| "part N of", "half A / half B", "depends on #M" | **split before routing** |

Add the workspace's fixed gate cost (build/vet/test/lint + e2e + docs
rows) — ~10–15 turns in a Go repo with e2e suites, less in a docs repo.

**Which sizing rule wins.** `ticket-writing` says split anything over ~60
turns; the table above still carries a 60–90 band because *estimating an
already-open ticket* is a different act from *writing one*. **The split
rule wins whenever splitting is still available** — an honest 60–90
estimate is a ticket-splitting signal first and a routing input second, and
splitting an oversized ticket is called out in Step 4 as the best use of a
nearly-drained Claude sub for exactly this reason. Route a 60–90 ticket
whole only when it is already open, cannot be split without losing
acceptance criteria that check against main on their own, and the lane's
ceiling covers it — which at grove's resident size rules out a **peak**
z.ai window (ceiling ~80 turns) for anything near the top of the band.

Then apply the **windowed-lane ceiling**: a ticket must fit inside the
5-hour bucket alongside whatever else is in flight. Compute it from the
credits/turn you calibrated in Step 2; never carry a number over from a
previous run.

**Worked example** — z.ai Lite, grove at 91k resident. The 5-hour bucket's
`usage` field reads 2,000 credits (confirmed live 2026-08-29 on the
`unit:3,number:5` limit):

| | peak | off-peak |
|---|---:|---:|
| credits/turn — `91 × 0.20`, halved off-peak | 18.6 | 9.3 |
| whole bucket ÷ credits/turn | ~107 turns | ~215 turns |
| **ceiling under the ≤75% in-flight rule** | **~80 turns** | **~160 turns** |

Cross-check: the measured 80-turn ticket cost 1,485 credits — 74% of the
bucket, i.e. exactly the peak ceiling, which is what makes 80 the number
rather than a rounding. **Apply the peak multiplier once.** It is already
baked into the 0.20 credits/k weight; halving a second time for off-peak
(or doubling a second time for peak) is the arithmetic error that used to
put this ceiling at 40/80.

## Step 4 — route

**Claude sub** — anything with an open design decision, anything where
being wrong is expensive, orchestration, and review of the other lanes'
PRs. As the sub drains this set shrinks toward design-only; it never
empties, because reviewing a cheap lane's output is itself Claude work.
When the sub is nearly gone, the highest-leverage use of what remains is
usually **splitting oversized tickets** — that converts one ticket the
cheap lanes cannot take into several they can.

**Flat plan (z.ai)** — rote **and** single-change **and** inside the
windowed ceiling. Three rules:

1. **Keep in-flight credits under ~75% of the 5-hour bucket.** Fleet width
   is not a constant — it is the bucket divided by what the tickets
   actually cost, and that swings 2× with peak/off-peak. At peak the
   average grove ticket (80 turns, 1,485 credits) is ~74% of the whole
   2,000-credit bucket, so width really is 1. Off-peak the same ticket is
   742 credits — **~37%**, not 19% — so width 2 fits (1,484, still ~74%)
   and width 3 does not (2,226, over the whole bucket).
2. **Prefer off-peak.** Same work, half the credits.
3. **Never raise `CLAUDE_CODE_AUTO_COMPACT_WINDOW` on the credit meter**
   — not to `"1000000"`, even though z.ai's own Claude Code docs recommend
   it. Credits ∝ resident context × turns, so on a credit meter aggressive
   compaction is *cheaper* — the opposite of the Claude sub. The shipped
   `zai-glm` profile correctly sets no such var.
   **Exception — profiles whose backend requires it.** The shipped `kimi`
   profile does set `CLAUDE_CODE_AUTO_COMPACT_WINDOW: "1048576"`
   (README.md, `config.example.yaml`, and the grove-103 `env:` passthrough
   it exists for): Kimi Code's 1M window needs it to function, and kimi is
   a pay-per-token lane where the trade is a dollar cost, not a hard
   window that strands work. The rule is about the **flat-rate credit
   meter**, not about the variable in general — never strip it from a
   profile that ships with it.

**OpenRouter** — see the lane reference below. Where the backlog goes
when the sub is low **and** the flat plan is capped; the only lane that
cannot strand work mid-ticket.

## Step 5 — propose

| Ticket | Shape | Est. turns | Lane | Cost | Why |
|---|---|---:|---|---:|---|

Then the grab commands in dispatch order, the total against what is
actually remaining on each lane, and an explicit note of anything deferred
and why. If the plan exceeds a bucket, name the ticket that falls off
rather than silently trimming. Then stop and wait.

```bash
gv grab <ticket> --repo <repo> --profile <profile>            # a lane
gv grab <ticket> --repo <repo> --profile <p> --model <slug>   # lane + model pin
gv grab <ticket> --repo <repo> --model claude-sonnet-5        # Claude, cheaper tier
gv grab <ticket> --repo <repo> --profile <p> --host <host>    # on the remote box
```

`--model` composes with `--profile` and needs no config edit. Caveat: the
pin is clobbered when the repo's `claude:` line already carries `--model`
(last flag wins) — check that line before promising a pin.

## Lane reference and probing (read on demand)

- **OpenRouter tier**: [reference/openrouter-lanes.md](reference/openrouter-lanes.md)
  — the live discovery query (never quote a frozen model table; require a
  cache-read price), which tier to pick (capability first; everything
  under ~$2/ticket is noise against one steer), the verified-lane table
  (qwen3.7-flash PASS, dated deepseek loops, undated deepseek dead), and
  the published reliability priors. Read it whenever the proposal routes
  anything to OpenRouter.
- **Unverified lane**: [reference/probe-protocol.md](reference/probe-protocol.md)
  — Gate 0 (does the model + endpoint pair speak Anthropic tool-use? one
  turn, read the transcript, `tool_use: 0` = dead, no tuning fixes it),
  then the five-step probe and scoring order. Read it before dispatching
  on any lane this workspace has not recorded a verdict for.

### Cheap lanes get an enumerated brief

A cheap model does exactly what it is told and infers nothing about house
conventions. That is a *fixable* deficit — pass `--brief` with the things
a Claude worker would have inferred. Adapt the surface and gate to the
workspace:

```
gv grab <ticket> --repo <repo> --profile <lane> --brief "
Touch ONLY these files: <exact list from the ticket>. Do not refactor
adjacent code, rename anything, or 'improve' code you are not asked to
change. If the ticket specifies text verbatim, copy it exactly.
Before committing, run the full gate and paste its output: <gate command>.
Required docs rows: <status board / learnings / changelog rows>.
If the fix needs a decision the ticket does not answer, STOP and end with
STATUS: QUESTION rather than guessing."
```

Each clause maps to an observed failure: scope creep, silent convention
skips, paraphrasing a verbatim spec, and guessing at an unanswered
decision. Cost is a few hundred prompt tokens against a lane that bills
cache reads at cents — the cheapest quality lever available.

**The structural safety net is that nothing merges itself.** A cheap
lane's bad output is a closed PR, not a bad commit — so the exposure is
review attention, not code quality, and the brief is what keeps review
attention low. Never relax the human-merges rule to make a cheap lane
look better.

## The economics that should drive the picks

At normal volume, **tokens are 1–2% of what a ticket costs**; a ticket is
a couple of dollars of inference and an hour of operator attention. So
`$/Mtok` is nearly irrelevant below ~$2/ticket, and the only lane metrics
that matter are **merge rate** and **steers**. A model 20× cheaper that
halves the merge rate is a bad trade; one 2× dearer that merges everything
is a bargain. Field evidence: a GLM-5.3 batch merged 2/4, with half the
spend burned on tickets that produced no PR at all.

## Feedback loop

After any lane experiment run `gv cost --analyze --json` and read
**steers** and **outcome**, not the dollar column — flat-rate lanes report
`cost_known: false` by design, and an unpriced model reports `$0`, never
free. Two or more steers on a rote ticket means the *routing* was wrong,
not the model: fix the sizing rule or the ticket, then re-propose.
