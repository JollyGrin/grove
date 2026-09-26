#!/bin/sh
# calibrate.sh [bucket_credits] — this workspace's cost shape, from
# `gv cost --analyze --json`. Run it with cwd inside the workspace.
#
# Prints resident context (total tokens / total turns), the token-share
# weights openrouter-rank.py takes, GLM plan credits per turn, and — given
# the flat plan's 5-hour bucket size (the `usage` of its unit:3,number:5
# limit) — the per-ticket turn ceiling under the ≤75% in-flight rule.
#
# The total is the sum of the five token counters under each row's `cost`;
# there is no total_tokens field, and models[].tokens is that same sum per
# model, so adding it would double-count. The peak/off-peak factor is
# applied exactly once here: the credit weights below are PEAK rates.
set -eu

BUCKET="${1:-}"
GV="${GV:-gv}"
HERE="$(cd "$(dirname "$0")" && pwd)"

"$GV" cost --analyze --json | jq -r --arg bucket "$BUCKET" --arg here "$HERE" '
  # GLM plan credits per turn per 1k resident tokens, at PEAK. Calibrated
  # against a measured ticket; see calibration.md. Off-peak is half.
  def glm: 0.204; def glm_flash: 0.067;
  [ .report.rows[].cost | select(.turns > 0) ] as $c
  | if ($c | length) == 0 then "no priced tickets with turns yet — nothing to calibrate\n" | halt_error(1) else . end
  | ($c | map(.input_tokens) | add) as $in
  | ($c | map(.output_tokens) | add) as $out
  | ($c | map(.cache_create_5m_tokens + .cache_create_1h_tokens) | add) as $cw
  | ($c | map(.cache_read_tokens) | add) as $cr
  | ($in + $out + $cw + $cr) as $tok
  | ($c | map(.turns) | add) as $turns
  | ($tok / $turns / 1000) as $rk
  | def r(n): (n * 1000 | round) / 1000;
    def ceil75(cpt): if $bucket == "" then "-" else ((($bucket|tonumber) * 0.75 / cpt) | floor | tostring) end;
    "tickets            \($c | length)",
    "turns              \($turns)",
    "tokens             \($tok)",
    "resident_k         \($rk | floor)",
    "cache_read_share   \(r($cr / $tok))",
    "tokens_per_ticket  \($tok / ($c | length) | floor)",
    "",
    "lane            credits/turn peak  off-peak  ceiling@75% peak  off-peak",
    "glm (5.x)       \(r($rk * glm))\t\(r($rk * glm / 2))\t\(ceil75($rk * glm))\t\(ceil75($rk * glm / 2))",
    "glm flash       \(r($rk * glm_flash))\t\(r($rk * glm_flash / 2))\t\(ceil75($rk * glm_flash))\t\(ceil75($rk * glm_flash / 2))",
    "",
    "# rank OpenRouter models on this workspace'"'"'s token shape:",
    "curl -s https://openrouter.ai/api/v1/models | python3 \($here)/openrouter-rank.py \(r(($in + $cw) / $tok)) \(r($out / $tok)) \(r($cr / $tok)) \($tok / ($c | length) | floor)"
'
