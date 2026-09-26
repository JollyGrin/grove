# model-lanes — calibration data

Dated observations behind the model-lanes skill's constants and priors.
They are evidence, not inputs: re-measure (`calibrate.sh`, the probe
protocol) rather than quoting a number from here into a proposal. Append
new probe results; keep the date, slug, endpoint and workspace on each.

## GLM credit weights (`calibrate.sh`)

- `glm` 0.204 credits/turn per 1k resident tokens at **peak**: a measured
  grove ticket at 91k resident ran 80 turns for 1,485 credits (18.6
  credits/turn). `glm_flash` 0.067 is the same measurement at Flash's rate.
- Off-peak is half. z.ai peak is Mon–Fri 14:00–18:00 SGT.
- Worked check, z.ai Lite, 5-hour bucket `usage` 2,000 credits (read live
  2026-08-29 on the `unit:3,number:5` limit), grove at 91k resident:

  | | peak | off-peak |
  |---|---:|---:|
  | credits/turn | 18.6 | 9.3 |
  | whole bucket ÷ credits/turn | ~107 turns | ~215 turns |
  | ceiling under the ≤75% in-flight rule | ~80 turns | ~160 turns |

  The measured 80-turn ticket used 74% of the bucket — the peak ceiling
  exactly. An earlier hand calculation applied the peak factor twice and
  put the ceiling at 40/80; `calibrate.sh` now applies it once.
- Fleet width at peak: one average grove ticket (~1,485 credits) is ~74%
  of the bucket, so width 1. Off-peak the same ticket is ~742 credits
  (~37%): width 2 fits (~74%), width 3 does not.
- Resident context drifts as a repo grows: grove read ~108k resident, 95%
  cache read on 2026-08-29 (16 tickets, ~1,070 turns, ~115M tokens), 107k
  an hour later.
- The flat plan is a subsidy, not a cheap model: `z-ai/glm-5.3` on
  OpenRouter cost ~$2.40/ticket, the same as Sonnet 5 for identical token
  volumes. A GLM-5.3 batch merged 2/4, half its spend on tickets that
  produced no PR.

## OpenRouter price tiers (grove workspace, 2026-08)

- ~$0.06/ticket — `qwen/qwen3.7-flash` (verified, below). `z-ai/glm-5.3-flash`
  is cheapest per credit on the flat plan but carries a 46% episode-level
  no-tool-call rate; probe it on z.ai's Anthropic-native endpoint.
- ~$1–2/ticket — `moonshotai/kimi-k2.5`, `kimi-k2.7-code`,
  `minimax/minimax-m3`: the default overflow tier then.
- ~$2.40/ticket — `z-ai/glm-5.3`.

## Verified lanes (grove workspace, Go repo with e2e, ~91k resident)

Real dispatch on real tickets, 2026-08-27.

| Lane | Slug | $/ticket | Verdict |
|---|---|---:|---|
| `qwen-flash` | `qwen/qwen3.7-flash` | 0.056 | **PASS** — verbatim spec matched character-for-character, gate green, correct scoping on a judgment call, TASKS.md row, clean sentinel. First choice for rote. |
| `qwen-coder` | `qwen/qwen3-coder-next` | 0.566 | **PASS with caveat** — correct fix + real regression test, but exceeded the enumerated surface and invented default config values instead of asking. Reserve for tickets needing judgment. |
| `deepseek-0731` | `deepseek/deepseek-v4-flash-0731` | 0.104 | **FAIL — loops.** Correct diff, then 203 turns / 20 min / no commit re-verifying the same file ("the change is already in place… let me verify"). |
| — | `deepseek/deepseek-v4-flash` (undated) | 0.139 | **FAIL — Gate 0.** Zero `tool_use`; emitted native `<｜DSML｜>` markup as text. |

- The dated `deepseek-v4-flash-0731` clears Gate 0 where the undated alias
  does not — same vendor, family and endpoint.
- The cheapest lane won on the most tightly specified ticket (grove-115,
  its correct output quoted verbatim in the body) while the $0.566 lane
  drifted on a looser one.
- Gate 0 failure in full: `deepseek/deepseek-v4-flash` via OpenRouter
  reasoned about grove-115 correctly, then emitted `<｜DSML｜tool_calls>`
  inside its thinking block — 407 output tokens, 406 of them thinking,
  zero `tool_use` blocks, zero text blocks, $0.0018, 12 seconds.

## Why Gate 0 can't be tuned from Claude Code

The published fixes are request parameters Claude Code does not expose:
DeepSeek V4 Flash goes from 40–50% clean tool calls to 100% with
`tool_choice="required"` or `reasoning_effort` below `max` (vllm#53831),
and any model's tool invocation collapses to 0% if `response_format` is
sent alongside tools (arXiv:2606.25605). Claude Code sends
`tool_choice: auto` and owns the request shape.

## Published tool-call reliability, cheap tiers (independent, 2026-08)

Harness-dependent — a prior for what to watch, not a verdict.

| Model | Failure signal |
|---|---|
| Qwen3-Coder-Next | ~7% format failure — best of six |
| Kimi K3 | Pass^3 68.5 (above Opus 4.8's 66.7), but ~190 silent parser failures/24h in production |
| DeepSeek V4 Flash 0731 | Pass^3 58.3; 40–50% clean tool calls at `auto`+`max` |
| GLM-5.3-Flash | 46% of episodes hit ≥1 no-tool-call turn |
| MiniMax M2.1 | ~23% format failure |
| GLM-4.7 / 4.7-Flash | Pass^3 10.2; 0.0 on one real scaffold — disqualified |

Vendor-published agentic scores run inflated (DeepSeek 82.7 vendor → 78.7
independent; Kimi 88.3 → 85.0), and dated snapshots differ: DeepSeek V4
Flash went 35.2 → 58.3 Pass^3 between `0423` and `0731`.
