# OpenRouter cost mechanics — ideas to explore (ideation, 2026-07-09)

Trigger: first live GLM request via the openrouter-glm profile — a trivial
"echo 1→10" turn billed **49,328 tokens / $0.0646**, of which 48,816 (99%)
was prompt and only 512 (1%) cached. Model `z-ai/glm-5.2-20260616`.

## What's actually in those 49k tokens

Claude Code's fixed per-turn payload, not the task: system prompt + every
tool schema + CLAUDE.md chain + memory index + full skill descriptions
(clearly visible in the OpenRouter request viewer). This is the floor cost
of ANY Claude Code turn on any backend; the Claude sub just never itemizes
it. Cheap sessions depend entirely on prompt caching re-reading that
prefix at a fraction of full price on every subsequent turn.

> **VERIFIED 2026-07-09, same day:** second request in the pane showed
> **Cached 49,792 (99.3%), prompt 344, cost $0.0137** — caching passes
> through OpenRouter→Z.AI intact. The cost track below is closed; ideas
> 1/2/5 are moot at current scale, ideas 3/4 remain worth a look someday.

## The load-bearing unknown (verify first, ~$0.10)

**Does the cache warm on GLM via OpenRouter?** Send 2–3 messages in one
profiled pane and read the *later* requests in the OpenRouter activity
view. Two worlds:

- **Cached ≈ 90%+** → non-issue. Long GLM sessions are near-free; close
  this doc's cost track.
- **Cached ≈ 0%** → every turn repays ~50k input (~$0.02/turn input at
  GLM prices, more as conversation grows). A 100-turn worker ≈ $6–10.
  Still viable as a burst/backup lane; bad for marathon sessions — the
  ideas below become worth building.

Also check whether `gv cost` sees it: the transcript records cache-read
tokens per turn, and `gv cost --analyze` already computes cache-read
share — a profiled ticket with near-zero cache share should already
stand out as "context thrash."

## Ideas (unordered, unbuilt — pick after the verify)

1. **Low-cache-share flag on profiled sessions** — `gv cost --analyze`
   grows a per-profile view: if a profile's average cache-read share is
   near zero, flag it ("backend not honoring prompt cache") so the
   operator learns per-backend economics from real usage, not billing
   surprises.
2. **Slim worker profile for cheap backends** — the fixed 50k is mostly
   skills + MCP + global config inherited from `~/.claude`. A dedicated
   minimal `CLAUDE_CONFIG_DIR` for profiled workers (few skills, no MCP)
   could cut the floor substantially. Tension to resolve: grove
   deliberately uses the default `~/.claude` everywhere (memory:
   ccwork-is-work-only), and workers-without-conventions was the original
   doctor incident — a slim profile must be a *designed* pack, not an
   accident.
3. **OpenRouter slug modifiers, config-only** — profile slugs are free
   text, so `z-ai/glm-5.2:floor` (cheapest provider) / `:nitro`
   (fastest) already work with zero code. Worth documenting once
   verified; provider choice may also determine cache support.
4. **Per-profile pricing sanity vs OpenRouter's own meter** — we now have
   two meters for the same turns (`gv cost` estimate vs OpenRouter
   billing). One-off comparison after a real ticket would calibrate the
   estimate and validate the prefix-match pricing.
5. **Turn-count budgets for profiled workers** — if caching is broken,
   cost scales with turns, so a cheap-backend worker wants tighter
   kickoff prompts and fewer steering rounds. The existing steering-count
   analysis in `gv cost --analyze` is the signal; a per-profile "expected
   $/ticket" note in LEARNINGS.md may be all that's needed.

## Future pickup (logged 2026-07-09, not investigated)

- **Are orchestrator panes in `gv cost` at all?** Cost is computed
  per-ticket from worker transcripts; orchestrator chats (default dir and
  the new per-profile `.grove/orchestrator/<profile>/` dirs) may be
  invisible to it. Each pane open carries the ~$0.065 uncached floor +
  its conversation. Worth answering when touching the cost page: do
  orchestrator sessions get counted anywhere, and should the cost
  analysis show a per-session "open floor" line so operators understand
  the fixed overhead? Do not build until the question is confirmed real.

## Non-ideas (documented so nobody chases them)

- Trimming Claude Code's own system prompt — not ours to change.
- Per-request cache-control injection — Claude Code owns request
  construction; grove only sets env.
