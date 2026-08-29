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
tokens → **~108k resident, 95% cache read**. It moves as work lands (the
same query an hour later read 107k), which is exactly why you re-run it
rather than quoting this line. Then:

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

Never quote a frozen model table; prices and model names turn over
monthly. **Discover live**, ranked by this workspace's own token shape:

```bash
curl -s https://openrouter.ai/api/v1/models | python3 -c "
import json,sys
# weights = this workspace's fresh-input / output / cache-read shares
W_IN, W_OUT, W_CACHE = 0.029, 0.0084, 0.963
TOKENS_PER_TICKET = 7.29e6
MIN_CTX = 200000
for m in sorted(json.load(sys.stdin)['data'], key=lambda x: x['id']):
    p = m.get('pricing') or {}
    try:
        i=float(p.get('prompt') or 0)*1e6; o=float(p.get('completion') or 0)*1e6
        c=p.get('input_cache_read'); c=float(c)*1e6 if c else None
    except (TypeError, ValueError): continue
    if i<=0 or o<=0 or c is None: continue
    if (m.get('context_length') or 0) < MIN_CTX: continue
    blend=(W_IN*i + W_OUT*o + W_CACHE*c) * TOKENS_PER_TICKET/1e6
    print(f\"{blend:7.2f}  {m['id']:44} in {i:6.2f} out {o:7.2f} cache {c:7.3f}\")
" | sort -n | head -30
```

Substitute the workspace's own weights and tokens-per-ticket from Step 2.
**Require a cache-read price** — a model without prompt caching costs
~30× more at grove's 96% cache-read share, which no headline `$/Mtok`
will warn you about.

**Which tier to pick.** Not the floor. At normal volume, tokens are 1–2%
of what a ticket costs — the rest is operator attention — so the gap
between a $0.06/ticket lane and a $0.56/ticket lane is *noise* against a
single steer or rescue. Sort by capability first and treat everything
under ~$2/ticket as free. The tiers that have held up:

- **~$0.06/ticket** — `qwen/qwen3.7-flash` is **verified working** on real
  tickets (see Verified lanes below) and is the default first choice for
  rote work. `z-ai/glm-5.3-flash` is cheapest per credit on the flat plan
  but carries a 46% episode-level no-tool-call rate — probe it on z.ai's
  Anthropic-native endpoint, never a generic one. `deepseek/deepseek-v4-flash`
  (undated) is **dead** — fails Gate 0. Price is not the risk in this
  tier; the tool protocol and the finishing loop are.
- **~$1–2/ticket** — `moonshotai/kimi-k2.5`, `kimi-k2.7-code`,
  `minimax/minimax-m3`. The sensible default overflow tier.
- **~$2.40/ticket** — `z-ai/glm-5.3`. Note this costs the **same as
  Sonnet 5** for identical token volumes; the flat plan is a subsidy, not
  a cheap model, and there is no thrift argument for the paid GLM lane.

### Verified lanes (grove workspace, Go repo with e2e, ~91k resident)

Measured by real dispatch on real tickets, 2026-08-27. Re-probe when a
slug or harness version changes; these are observations, not guarantees.

| Lane | Slug | $/ticket | Verdict |
|---|---|---:|---|
| `qwen-flash` | `qwen/qwen3.7-flash` | 0.056 | **PASS** — verbatim spec matched character-for-character, gate green, correct scoping on a judgment call, TASKS.md row, clean sentinel. **First choice for rote.** |
| `qwen-coder` | `qwen/qwen3-coder-next` | 0.566 | **PASS with caveat** — correct fix + real regression test, but exceeded the enumerated surface and *invented* default config values instead of stopping to ask. 7× the cost of qwen-flash. Reserve for tickets needing judgment. |
| `deepseek-0731` | `deepseek/deepseek-v4-flash-0731` | 0.104 | **FAIL — loops.** Produced the correct diff, then could not recognise it was finished: 203 turns / 20 min / no commit, re-verifying the same file ("the change is already in place… let me verify"). Not viable unattended. |
| — | `deepseek/deepseek-v4-flash` (undated) | 0.139 | **FAIL — Gate 0.** Zero `tool_use`; emitted native `<｜DSML｜>` markup as text. |

**Pin dated slugs, never the undated alias.** `deepseek-v4-flash-0731`
clears Gate 0; the undated `deepseek-v4-flash` does not. Same vendor, same
family, same endpoint — the alias points wherever the provider decides.

**The cheapest model won, and not by luck.** #115 was the most tightly
specified ticket in the batch (its correct output was quoted verbatim in
the body), and the $0.056 lane nailed it while the $0.566 lane drifted on
a looser one. In this tier **specification quality dominates model
choice** — which is the same claim `ticket-writing` makes about cost. Buy
capability only for tickets you could not specify tightly.

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
# Claude Code's project dir encodes the cwd with BOTH rules: / -> - AND . -> -
# /home/dean/git/grove/.grove/orchestrator -> -home-dean-git-grove--grove-orchestrator
TD=~/.claude/projects/$(pwd | sed -e 's#/#-#g' -e 's#\.#-#g')   # or the worker's worktree
python3 -c "
import json,os,sys,glob
files = glob.glob(sys.argv[1]+'/*.jsonl')
if not files: sys.exit('no transcript under '+sys.argv[1])
f = max(files, key=os.path.getmtime)   # newest by mtime: filenames are UUIDs, unordered
tu=txt=0; model=None
for l in open(f):
    try: r=json.loads(l)
    except: continue
    if r.get('type')!='assistant': continue
    model = r['message'].get('model')
    for b in r['message'].get('content',[]):
        tu  += b.get('type')=='tool_use'
        txt += b.get('type')=='text'
print(os.path.basename(f), model,'tool_use:',tu,'text:',txt)
" "$TD"
```

Two traps in that one command, both of which fail *silently* — a wrong
directory and a stale transcript both read as `tool_use: 0`, which is the
kill-the-lane verdict:

- **Encode `.` as well as `/`.** A one-rule `sed 's#/#-#g'` gets the
  orchestrator's own `.grove` dir wrong and lands on a path that does not
  exist. Grove's `transcript.EncodePath` applies both rules.
- **Select by mtime, never `sorted(...)[-1]`.** Transcript filenames are
  session UUIDs, so lexical order is unrelated to time; sorting can hand
  you a weeks-old transcript from the same worktree.

**`tool_use: 0` after a turn that clearly intended to act = the lane is
dead.** Stop immediately; no prompt tuning fixes it. The model emits its
own native tool syntax as *text* (or buries it in a thinking block), the
harness sees no `tool_use` block, executes nothing, and ends the turn with
no error — a silent, total failure that costs a rate-limit-free lane
nothing to repeat forever.

Verified 2026-08-27: `deepseek/deepseek-v4-flash` via OpenRouter reasoned
about grove-115 correctly, then emitted `<｜DSML｜tool_calls>` — DeepSeek's
native markup — inside its thinking block. 407 output tokens, 406 of them
thinking, **zero** `tool_use` blocks, zero text blocks, $0.0018, 12
seconds. The model was not too dumb; it was speaking the wrong protocol.

This is why an **Anthropic-native endpoint beats a cheaper generic one**.
z.ai's `api.z.ai/api/anthropic` is a real Anthropic-protocol surface and
GLM workers merge PRs; OpenRouter's generic endpoint passes some models'
native tool syntax straight through. Judge the *model + endpoint pair*,
never the model alone.

**You cannot tune your way out of Gate 0 from this harness.** The
published fixes for cheap-model tool-call failure are request parameters
Claude Code does not expose: DeepSeek V4 Flash goes from 40–50% clean tool
calls to 100% with `tool_choice="required"` or `reasoning_effort` below
`max` (vllm#53831), and *any* model's tool invocation collapses to 0% if
`response_format` is sent alongside tools (arXiv:2606.25605). Claude Code
sends `tool_choice: auto` and owns the request shape, so none of those
knobs are reachable. Treat a Gate 0 failure as final for this harness
regardless of what the model's docs promise under a tuned configuration.

**Published tool-call reliability, cheap tiers** (independent, 2026-08;
harness-dependent — treat as a prior for what to watch, not a verdict):

| Model | Failure signal |
|---|---|
| Qwen3-Coder-Next | ~7% format failure — best of six |
| Kimi K3 | Pass^3 68.5 (above Opus 4.8's 66.7), but ~190 silent parser failures/24h in production |
| DeepSeek V4 Flash 0731 | Pass^3 58.3; 40–50% clean tool calls at `auto`+`max` |
| GLM-5.3-Flash | **46% of episodes** hit ≥1 no-tool-call turn |
| MiniMax M2.1 | ~23% format failure |
| GLM-4.7 / 4.7-Flash | Pass^3 10.2; 0.0 on one real scaffold — disqualified |

Two cautions on that table. Vendor-published agentic scores run inflated
(DeepSeek 82.7 vendor → 78.7 independent; Kimi 88.3 → 85.0), and dated
snapshots are different animals — DeepSeek V4 Flash went 35.2 → 58.3
Pass^3 between `0423` and `0731`, so a result attributed to an undated
alias may not describe the model you dispatch. **A sub-cent Gate 0 on the
exact slug outranks every number above.**

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
   the workspace's learnings log. A probe you do not write down gets
   re-run.

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
