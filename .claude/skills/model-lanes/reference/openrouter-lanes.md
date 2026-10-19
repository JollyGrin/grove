# Lane reference — OpenRouter

Read from `../SKILL.md` Step 4 when routing to the OpenRouter tier.
Observations, not guarantees: re-probe when a slug or harness version
changes.

## Discover live

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

## Which tier to pick

Not the floor. At normal volume, tokens are 1–2% of what a ticket costs —
the rest is operator attention — so the gap between a $0.06/ticket lane
and a $0.56/ticket lane is *noise* against a single steer or rescue. Sort
by capability first and treat everything under ~$2/ticket as free. The
tiers that have held up:

- **~$0.06/ticket** — `qwen/qwen3.7-flash` is **verified working** on real
  tickets (below) and is the default first choice for rote work.
  `z-ai/glm-5.3-flash` is cheapest per credit on the flat plan but
  carries a 46% episode-level no-tool-call rate — probe it on z.ai's
  Anthropic-native endpoint, never a generic one.
  `deepseek/deepseek-v4-flash` (undated) is **dead** — fails Gate 0.
  Price is not the risk in this tier; the tool protocol and the
  finishing loop are.
- **~$1–2/ticket** — `moonshotai/kimi-k2.5`, `kimi-k2.7-code`,
  `minimax/minimax-m3`. The sensible default overflow tier.
- **~$2.40/ticket** — `z-ai/glm-5.3`. Costs the **same as Sonnet 5** for
  identical token volumes; the flat plan is a subsidy, not a cheap model,
  and there is no thrift argument for the paid GLM lane.

## Verified lanes (grove workspace, Go repo with e2e, ~91k resident)

Measured by real dispatch on real tickets, 2026-08-27.

| Lane | Slug | $/ticket | Verdict |
|---|---|---:|---|
| `qwen-flash` | `qwen/qwen3.7-flash` | 0.056 | **PASS** — verbatim spec matched character-for-character, gate green, correct scoping on a judgment call, TASKS.md row, clean sentinel. **First choice for rote.** |
| `qwen-coder` | `qwen/qwen3-coder-next` | 0.566 | **PASS with caveat** — correct fix + real regression test, but exceeded the enumerated surface and *invented* default config values instead of stopping to ask. 7× the cost of qwen-flash. Reserve for tickets needing judgment. |
| `deepseek-0731` | `deepseek/deepseek-v4-flash-0731` | 0.104 | **FAIL — loops.** Produced the correct diff, then could not recognise it was finished: 203 turns / 20 min / no commit, re-verifying the same file. Not viable unattended. |
| — | `deepseek/deepseek-v4-flash` (undated) | 0.139 | **FAIL — Gate 0.** Zero `tool_use`; emitted native `<｜DSML｜>` markup as text. |

**Pin dated slugs, never the undated alias.** `deepseek-v4-flash-0731`
clears Gate 0; the undated `deepseek-v4-flash` does not. Same vendor, same
family, same endpoint — the alias points wherever the provider decides.

**The cheapest model won, and not by luck.** #115 was the most tightly
specified ticket in the batch (its correct output was quoted verbatim in
the body), and the $0.056 lane nailed it while the $0.566 lane drifted on
a looser one. In this tier **specification quality dominates model
choice** — the same claim `ticket-writing` makes about cost. Buy
capability only for tickets you could not specify tightly.

**Watch for the loop, not just the bad diff.** A cheap model's most
expensive failure is finishing the work and failing to notice: turns
climb with no commit, and the transcript repeats "let me verify" on a
file it already fixed. Catch it by turn count against estimate (2×
estimate with no commit = intervene), not by reading the diff. It is
recoverable — a nudge naming the exact remaining steps and forbidding
further investigation ships the work already sitting in the worktree.

## Published tool-call reliability, cheap tiers

Independent, 2026-08; harness-dependent — a prior for what to watch, not
a verdict:

| Model | Failure signal |
|---|---|
| Qwen3-Coder-Next | ~7% format failure — best of six |
| Kimi K3 | Pass^3 68.5 (above Opus 4.8's 66.7), but ~190 silent parser failures/24h in production |
| DeepSeek V4 Flash 0731 | Pass^3 58.3; 40–50% clean tool calls at `auto`+`max` |
| GLM-5.3-Flash | **46% of episodes** hit ≥1 no-tool-call turn |
| MiniMax M2.1 | ~23% format failure |
| GLM-4.7 / 4.7-Flash | Pass^3 10.2; 0.0 on one real scaffold — disqualified |

Two cautions. Vendor-published agentic scores run inflated (DeepSeek 82.7
vendor → 78.7 independent; Kimi 88.3 → 85.0), and dated snapshots are
different animals — DeepSeek V4 Flash went 35.2 → 58.3 Pass^3 between
`0423` and `0731`, so a result attributed to an undated alias may not
describe the model you dispatch. **A sub-cent Gate 0 on the exact slug
outranks every number above** — see `probe-protocol.md`.
