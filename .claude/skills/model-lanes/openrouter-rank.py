"""Rank OpenRouter models by $/ticket on this workspace's token shape.

usage: curl -s https://openrouter.ai/api/v1/models \
         | python3 openrouter-rank.py W_IN W_OUT W_CACHE TOKENS_PER_TICKET [MIN_CTX]

calibrate.sh prints the exact line with this workspace's weights: the
fresh-input (incl. cache writes), output and cache-read shares of all
tokens, and mean tokens per ticket. Models with no cache-read price are
dropped — at a ~95% cache-read share an uncached model costs ~30x more,
which no headline $/Mtok shows.
"""
import json
import sys

w_in, w_out, w_cache, per_ticket = (float(a) for a in sys.argv[1:5])
min_ctx = int(sys.argv[5]) if len(sys.argv) > 5 else 200000

rows = []
for m in json.load(sys.stdin)["data"]:
    p = m.get("pricing") or {}
    try:
        i = float(p.get("prompt") or 0) * 1e6
        o = float(p.get("completion") or 0) * 1e6
        c = p.get("input_cache_read")
        c = float(c) * 1e6 if c else None
    except (TypeError, ValueError):
        continue
    if i <= 0 or o <= 0 or c is None:
        continue
    if (m.get("context_length") or 0) < min_ctx:
        continue
    blend = (w_in * i + w_out * o + w_cache * c) * per_ticket / 1e6
    rows.append((blend, m["id"], i, o, c))

for blend, mid, i, o, c in sorted(rows)[:30]:
    print(f"{blend:7.2f}  {mid:44} in {i:6.2f} out {o:7.2f} cache {c:7.3f}")
