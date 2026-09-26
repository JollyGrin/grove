---
name: model-lanes
description: Explicitly invoked only — do NOT load for ordinary dispatch. Use when the operator runs /model-lanes or asks to split a workload across the Claude sub, a flat-rate coding plan (z.ai GLM), and pay-per-token OpenRouter lanes. Reads live capacity on every lane, calibrates this workspace's cost-per-turn, sizes the open backlog, and proposes per-ticket routing with grab commands for approval. Early in a Claude billing week you do not need this; it earns its keep when the Claude sub runs low or a flat plan caps out.
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
before an orchestrator there can load it. `<skill-dir>` below means that
directory; its scripts need `jq` and `python3`.

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
is whatever the flat-plan profile's (`zai-plan-*`) `auth_token_env` says —
conventionally `ZAI_API_KEY` (`auth_token_env` holds the env VAR NAME, never
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
sizes), not of the model. Measure it; never carry a number over from a
previous proposal, because it drifts as the repo grows:

```bash
<skill-dir>/calibrate.sh <bucket>   # <bucket> = the 5-hour limit's `usage` from Step 1
```

It reads `gv cost --analyze --json` and prints resident context, the
cache-read share, GLM credits/turn at peak and off-peak, the per-ticket
turn ceiling under the ≤75% in-flight rule, and the exact OpenRouter
ranking command for this workspace's token shape. The arithmetic lives in
the script (peak factor applied once); the constants and the measurements
behind them are in [calibration.md](calibration.md). Re-derive the credit
weights for a workspace whose cache-read share is far from ~95%.

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
estimate is a ticket-splitting signal first and a routing input second,
which is why splitting oversized tickets is the best use of a
nearly-drained Claude sub (Step 4). Route a 60–90 ticket whole only when
it is already open, cannot be split without losing acceptance criteria
that check against main on their own, and the lane's ceiling covers it.

Then apply the **windowed-lane ceiling** `calibrate.sh` printed: a ticket
must fit inside the 5-hour bucket alongside whatever else is in flight.

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
   actually cost, and that swings 2× with peak/off-peak. Sum the estimated
   turns × credits/turn of everything in flight; an average ticket often
   fills the peak window alone.
2. **Prefer off-peak.** Same work, half the credits.
3. **Never raise `CLAUDE_CODE_AUTO_COMPACT_WINDOW` on the credit meter**
   — not to `"1000000"`, even though z.ai's own Claude Code docs recommend
   it. Credits ∝ resident context × turns, so on a credit meter aggressive
   compaction is *cheaper* — the opposite of the Claude sub. A
   `zai-plan-*` profile sets no such var.
   **Exception — profiles whose backend requires it.** The `kimi` example
   profile in `config.example.yaml` sets
   `CLAUDE_CODE_AUTO_COMPACT_WINDOW: "1048576"` through the profile `env:`
   passthrough: Kimi Code's 1M window needs it to function, and kimi is
   a pay-per-token lane where the trade is a dollar cost, not a hard
   window that strands work. The rule is about the **flat-rate credit
   meter**, not about the variable in general — never strip it from a
   profile that ships with it.

**OpenRouter** — see the lane reference below. This is where the backlog
goes when the sub is low **and** the flat plan is capped, and it is the
only lane that cannot strand work mid-ticket.

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

## Lane reference — OpenRouter

Prices and model names turn over monthly, so discover live: run the
ranking command `calibrate.sh` printed (`openrouter-rank.py`, $/ticket on
this workspace's own token shape). It drops models without a cache-read
price — at a ~95% cache-read share an uncached model costs ~30× more,
which no headline `$/Mtok` shows.

**Which tier to pick.** Not the floor. At normal volume, tokens are 1–2%
of what a ticket costs — the rest is operator attention — so the gap
between a $0.06/ticket lane and a $0.56/ticket lane is *noise* against a
single steer or rescue. Sort by capability first and treat everything
under ~$2/ticket as free. Prefer a lane with a recorded PASS in
[calibration.md](calibration.md) for rote work; anything else gets the
probe protocol first. Price is not the risk in the cheapest tier; the
tool protocol and the finishing loop are.

**Pin dated slugs, never the undated alias.** The alias points wherever
the provider decides, and a dated snapshot and its alias can land on
opposite sides of Gate 0.

**Specification quality dominates model choice in the cheap tier** — the
same claim `ticket-writing` makes about cost. The cheapest lanes do well
on tickets whose correct output is stated in the body and drift on loose
ones. Buy capability only for tickets you could not specify tightly.

**Watch for the loop, not just the bad diff.** A cheap model's most
expensive failure is finishing the work and failing to notice: turns climb
with no commit, and the transcript repeats "let me verify" on a file it
already fixed. Catch it by turn count against estimate (2× estimate with
no commit = intervene), not by reading the diff. It is recoverable — a
nudge naming the exact remaining steps and forbidding further
investigation ships the work already sitting in the worktree.

### The probe protocol

Capability data for cheap models is stale the month it is published, and
vendor-reported benchmarks do not predict behaviour in a specific
harness on a specific repo. **Probing is cheaper than researching**: one
probe on a flash lane costs ~$0.15 and answers the question for *this*
workspace.

**Gate 0 — does the model speak Anthropic tool-use on this endpoint?**
Check this before anything else; it is binary, it costs under a cent, and
it kills lanes that look perfect on price and benchmarks. Dispatch the
probe, let it take **one turn**, then read the transcript:

```bash
python3 <skill-dir>/gate0.py <worker-worktree>   # set CLAUDE_CONFIG_DIR if the profile has its own
```

**`tool_use: 0` after a turn that clearly intended to act = the lane is
dead.** Stop immediately; no prompt tuning fixes it. The model emits its
own native tool syntax as *text* (or buries it in a thinking block), the
harness sees no `tool_use` block, executes nothing, and ends the turn with
no error — a silent, total failure that costs a rate-limit-free lane
nothing to repeat forever.

This is why an **Anthropic-native endpoint beats a cheaper generic one**.
z.ai's `api.z.ai/api/anthropic` is a real Anthropic-protocol surface and
GLM workers merge PRs; OpenRouter's generic endpoint passes some models'
native tool syntax straight through. Judge the *model + endpoint pair*,
never the model alone. A Gate 0 failure is final for this harness: the
published fixes are request parameters (`tool_choice`, reasoning effort,
`response_format`) that Claude Code owns and does not expose. Published
tool-call reliability numbers are in [calibration.md](calibration.md) as
a prior; **a sub-cent Gate 0 on the exact slug outranks all of them.**

**Never gate completion on the worker's own claim.** Frontier models
self-report false success 45–75% of the time in ways the transcript does
not reveal; cheap models are worse. Completion means `gh pr view` says the
PR exists, CI is green, and the diff is non-empty — which is already
grove's rule, and is the one guardrail that holds across every lane.

Then, only if Gate 0 passes:

1. Pick the smallest, most mechanical open ticket — a verbatim
   replacement, or one function with an exact `file:line` and the fix
   already stated. Never probe on something interesting.
2. Dispatch it on the candidate lane, fleet width 1, and watch it.
3. Score in this order, because the failure modes are not equally bad:
   - **produced a PR at all** — if not, the lane is out;
   - **the gate passes** — build/vet/test/lint, e2e;
   - **stayed on the enumerated surface** — invented scope is the
     expensive failure, worse than doing too little;
   - **did the conventions** — docs rows, learnings entries, ticket
     hygiene.
4. Verdict: passes 1–3 → viable for rote work on this repo. Fails only
   on 4 → viable, but enumerate the docs rows explicitly in the kickoff.
   Fails 2 or 3 → not viable; stop, do not tune the prompt.
5. Record the result — model slug, endpoint, date, workspace, verdict — in
   [calibration.md](calibration.md) and the workspace's learnings log. A
   probe you do not write down gets re-run.

Expected failure mode for cheap models that *do* clear Gate 0, from field
evidence: they write the code and skip the **conventions** — clean code,
missing status-board row. Cheap to catch, cheap to fix. Weight capability
risk toward "forgets the gate", not "writes subtly wrong code".

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

A ticket is a couple of dollars of inference and an hour of operator
attention, so below ~$2/ticket the only lane metrics that matter are
**merge rate** and **steers**. A model 20× cheaper that halves the merge
rate is a bad trade; one 2× dearer that merges everything is a bargain.

## Feedback loop

After any lane experiment run `gv cost --analyze --json` and read
**steers** and **outcome**, not the dollar column — flat-rate lanes report
`cost_known: false` by design, and an unpriced model reports `$0`, never
free. Two or more steers on a rote ticket means the *routing* was wrong,
not the model: fix the sizing rule or the ticket, then re-propose.
