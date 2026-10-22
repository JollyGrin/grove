# The probe protocol — qualifying a cheap lane

Read from `../SKILL.md` Step 4 before dispatching on any lane this
workspace has not verified. Capability data for cheap models is stale the
month it is published, and vendor benchmarks do not predict behaviour in
a specific harness on a specific repo. **Probing is cheaper than
researching**: one probe on a flash lane costs ~$0.15 and answers the
question for *this* workspace.

## Gate 0 — does the model speak Anthropic tool-use on this endpoint?

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
  session UUIDs, so lexical order is unrelated to time.

**`tool_use: 0` after a turn that clearly intended to act = the lane is
dead.** Stop immediately; no prompt tuning fixes it. The model emits its
own native tool syntax as *text* (or buries it in a thinking block), the
harness sees no `tool_use` block, executes nothing, and ends the turn
with no error — a silent, total failure that costs a rate-limit-free lane
nothing to repeat forever.

Verified 2026-08-27: `deepseek/deepseek-v4-flash` via OpenRouter reasoned
about grove-115 correctly, then emitted `<｜DSML｜tool_calls>` — DeepSeek's
native markup — inside its thinking block. 407 output tokens, 406 of them
thinking, **zero** `tool_use` blocks, $0.0018, 12 seconds. The model was
not too dumb; it was speaking the wrong protocol.

This is why an **Anthropic-native endpoint beats a cheaper generic one**.
z.ai's `api.z.ai/api/anthropic` is a real Anthropic-protocol surface and
GLM workers merge PRs; OpenRouter's generic endpoint passes some models'
native tool syntax straight through. Judge the *model + endpoint pair*,
never the model alone.

**You cannot tune your way out of Gate 0 from this harness.** The
published fixes for cheap-model tool-call failure are request parameters
Claude Code does not expose: DeepSeek V4 Flash goes from 40–50% clean
tool calls to 100% with `tool_choice="required"` or `reasoning_effort`
below `max` (vllm#53831), and *any* model's tool invocation collapses to
0% if `response_format` is sent alongside tools (arXiv:2606.25605).
Claude Code sends `tool_choice: auto` and owns the request shape, so none
of those knobs are reachable. Treat a Gate 0 failure as final for this
harness regardless of what the model's docs promise.

**Never gate completion on the worker's own claim.** Frontier models
self-report false success 45–75% of the time in ways the transcript does
not reveal; cheap models are worse. Completion means `gh pr view` says the
PR exists, CI is green, and the diff is non-empty — already grove's rule,
and the one guardrail that holds across every lane.

## Then, only if Gate 0 passes

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
