# Token diet — research (2026-09-06)

Trigger: Spotify's "Portal by Spotify cut my Claude Code token usage by
90%" (engineering.atspotify.com, 2026-09). Question: which of its ideas,
or any cheaper cousin, would cut grove's worker spend without regressing
how well workers ship. This is research only — nothing here is built.
Every number below is measured from grove's own ledger and transcripts
(25 tickets, grove-156 … grove-263, 1,443 API calls, $178.64 estimated),
plus a handful of controlled Haiku sessions run in scratch directories.

## TL;DR (revised after the fleet-wide pass, same evening)

0. **Grove is the small fleet. The others are where the money is.**
   Same decomposition over every local worker transcript: unbrewed
   workers sent **7.85 billion** context tokens (211 sessions, avg
   262k per API call, p90 520k, one session hit the 1M wall with 4,431
   API calls); thegrid 4.0B (avg 276k). On those fleets an
   `--autocompact 120000` cap saves **58–70%** of context tokens, not
   the 18–33% grove sees. Orchestrator chats are a second, invisible
   fleet: ~$4.2k estimated across workspaces, never shown by `gv cost`.
   Full table in §2.5.
0b. **A $0 non-Claude subagent already works today, no pi needed.** A
   Fable worker can shell out to the z.ai flat lane from its Bash tool:
   a raw `/v1/messages` call summarised `hooks.go` in 12 s (3.7k tokens
   in, 640 back), and `claude -p --bare` under the z.ai env ran a
   2-turn agentic read in 24 s with a 6k floor. Tested in §3 L2b. The
   summary is all that enters the Fable context.
0c. **Early compaction is safe enough to try, with the state kept
   outside the transcript.** Seven real compactions in the transcripts:
   context fell from 130–1000k to 50–65k, the worker re-read 2–37k chars
   in the next ten calls, no task was lost. §3 L1 and L6 lay out the
   checkpoint mechanics that make it routine.

1. **The 90% is per-read, not per-bill.** Spotify's headline is "mean
   bulk-read savings" — a 2,000-line file becomes a bullet summary. In
   grove's ledger, whole-file reads over 350 lines are **15 of 178 reads
   and ~6% of all context tokens sent**. Copying the bulk-reader
   verbatim caps out around 6–10% here.
2. **Grove's bill is context × turns, and the context never shrinks.**
   97% of all tokens are prompt-cache reads. Average context per API
   call is 122k; sessions grow to 190–290k and only 3 of 26 sessions ever
   compacted (the pinned `[1m]` model means the 1M window never fills).
   41% of every dollar is the fixed ~40–60k first-turn floor re-sent per
   turn; the other 59% is accumulated tool output re-sent per turn.
3. **The single cheapest, biggest lever is a config line, not a hook:**
   `claude --dangerously-skip-permissions --autocompact 120000` on the
   worker command. Simulated on the real transcripts, a 120k cap saves
   18–33% of context tokens; 100k saves 27–40%. Zero code. The risk is
   compaction losing state, which the existing checkpoint-in-PR
   discipline (duty 8) already mitigates — and a `PreCompact` hook can
   automate it.
4. **Spotify's real insight is enforcement, not delegation.** Grove's
   kickoff already tells workers to fan reconnaissance out to an Explore
   subagent. Across 26 sessions the Agent tool was used **once**.
   Instructions don't change reading behaviour; a `PreToolUse` deny with
   a reason does. The deny-with-reason hook is worth building even
   without any cheap model behind it.
5. **Skip:** code-writer mode, skill-description trimming, slim
   `CLAUDE_CONFIG_DIR`, `--no-chrome`, `--strict-mcp-config`, `--bare`.
   Each was measured; the table in §5 says why.

## 1. What Spotify actually built

Portal is Spotify's internal platform for "AiKA Modes": declarative
agents (instructions + model + params + MCP tools) on ephemeral runtimes.
The Claude Code integration is three layers:

- **Hooks (PreToolUse).** `check-file-size` blocks a `Read` of a file
  over a threshold (default 350 lines, `SHUNT_MIN_LINES`) and tells Claude
  to use the `/bulk-reader` skill instead. `check-bash-read` does the
  same for `cat`/`grep`-style commands on big files. Targeted reads
  (offset/limit, piped greps) pass through — "Claude already knows what
  section it needs".
- **Scripts.** `bulk-read` / `code-write` wrappers call the Portal CLI,
  handle errors, and report tokens.
- **Skills.** Markdown telling Claude when to invoke the scripts.

Two modes:

- **Bulk-reader** (Gemini 2.5 Flash): "Output structured bullets only. No
  greetings, no prose, no preambles." The big file never enters Claude's
  context; the bullets do. "Mean bulk-read savings were around 90%",
  measured on a Java monorepo across four scenarios. No token-level
  methodology, sample size, or $ per invocation is given.
- **Code-writer**: generates boilerplate (tests, configs, type stubs) by
  pattern-matching against a mandatory reference file; output goes to
  disk, never into Claude's context.

What they say did not work: editing (the worker's summaries have no
reliable line numbers), complex reasoning (missed a thread-safety bug),
and latency (10–30 s per round trip, 30 s cap — "counterproductive for
small ones").

Nothing in the article addresses caching, CLAUDE.md size, compaction,
subagents, or MCP tool bloat. Their whole lever is "keep big reads out
of context".

## 2. Where grove's tokens actually go

Source: `gv cost --analyze --json` (pure read) plus a transcript walk
over `~/.claude/projects/-Users-grins-git--worktrees-grove-grove-*`
deduplicated by `(message.id, requestId)` — the same rule
`internal/cost` uses (a transcript line is not a message; the undeduped
walk double-counts by 1.9×).

### 2.1 Token classes

| class | tokens | share |
|---|---|---|
| cache read | 173.3M | 97.1% |
| cache write (1h) | 4.3M | 2.4% |
| output | 0.95M | 0.5% |
| uncached input | 4.5k | ~0 |

1,443 API calls; mean context per call **122k**; mean cost **$0.12 per
call**. The Stop hook's `turns` in the ledger (1,463) is this same count.

### 2.2 Dollars by model (grove's price table, `internal/cost/cost.go`)

| model | API calls | est $ | cache read | cache write | output | avg ctx | $/call |
|---|---|---|---|---|---|---|---|
| claude-fable-5 | 576 | 130.16 | 47% | 34% | 19% | 111k | 0.226 |
| claude-sonnet-5 | 687 | 28.30 | 66% | 22% | 12% | 138k | 0.041 |
| claude-fable-5-1 | 62 | 10.20 | 15% | 46% | 38% | 106k | 0.165 |
| claude-opus-5 | 114 | 9.36 | 54% | 29% | 17% | 90k | 0.082 |

Two things to hold onto:

- On every model except Fable 5.1, **context (read + write) is 80–90% of
  the cost.** Output is a rounding error. The diet is a context diet.
- **Fable 5.1 prices cache reads at 2.5% of input** (`cost.go:41-46`), so
  on the lane `~/.claude/settings.json` currently pins
  (`claude-fable-5-1[1m]`), context is four times cheaper than on
  Fable 5 and output becomes the biggest slice. A context diet still
  pays there (cache *writes* at 2× input are 46%), but less.

### 2.3 What the context is made of

Every token that enters a session's context is written to cache once
(2× input price) and re-read on every later turn (0.1× input, or 0.025×
on Fable 5.1). A 25k-token file read at turn 10 of a 150-turn session is
re-sent 140 times: 3.5M cache-read tokens for one Read. That
amplification is what the decomposition below measures — each content
block weighted by the number of API calls that came after it.

| source | share of context tokens sent |
|---|---|
| **turn-1 floor × turns** (system prompt, tool schemas, CLAUDE.md, kickoff) | **41%** |
| Bash reads (`cat`, `sed -n`, `grep`, `head`…) | 15% |
| assistant tool inputs (Bash command text, Edit old/new strings) | 15% |
| `Read` whole file | 12.5% |
| `Read` with offset/limit | 7% |
| kickoff prompt, nudges, answers | 4% |
| e2e shell output | 3% |
| `go build/test/vet` output | 1% |
| Edit/Write results, gh, git, gv, everything else | <2% |

The big-read tail Spotify targets, isolated:

| bucket | share of total |
|---|---|
| Bash reads ≤350 lines | 14.6% |
| `Read` whole file ≤350 lines | 6.8% |
| `Read` targeted ≤350 lines | 6.5% |
| **`Read` whole file >350 lines** | **5.6%** |
| Bash reads >350 lines | 0.3% |
| `Read` targeted >350 lines | 0.2% |

So the 350-line gate catches ~6% of context in grove. Reads are mostly
*many medium files*, not a few huge ones: the p50 `Read` is 3.2k chars,
p90 is 13.8k, max 54k (`cmd/gv/main.go`, 1,400 lines). The biggest
single amplified reads were `internal/tui/tui.go` (43k chars, re-sent
113 times), `docs/plugins.md` (23k, ×182), `docs/seed-manifest.md`
(22k, ×180), `internal/tmux/grove.go` (30k, ×100 and ×91). Note that
two of those are *docs* a worker read whole and never edited.

Growth is monotonic. Across 2,730 assistant lines there were **3
compactions** and **zero** context drops otherwise — Claude Code 2.1.263
does not prune old tool results (confirmed against the docs: compaction
is all-or-nothing). Last-turn context per session: 60–290k.

### 2.4 The floor, measured

Controlled interactive Haiku sessions in scratch directories (isolated
tmux server, trust dialog accepted by hand), first-turn context in
tokens:

| setup | turn-1 context |
|---|---|
| empty dir, default flags | 35.6k |
| + grove `CLAUDE.md` + the six project skills | 37.7k (+2.0k) |
| same, skill descriptions cut to one line | 37.6k (−0.1k) |
| `--no-chrome` | 34.6k (−1.1k) |
| `--disable-slash-commands` | 37.7k (0) |
| `--setting-sources project` | 35.6k (0) |
| `--strict-mcp-config` | 35.7k (0) |
| `--bare` | not logged in (skips credentials; unusable on the sub) |
| `claude -p` (headless) | 22.3k |

Workers' real first turns are 40–60k because the worker floor adds the
kickoff prompt, git status, memory index, and a bigger model's tool
schemas; the Aug→Sep drift from ~25k to ~42k is Claude Code's own
system-prompt growth, not config. Conclusions: grove's own contribution
to the floor is ~2k of ~40k; there are no MCP servers attached to
workers (the `mcpServers` key in `~/.claude/settings.json` is dead —
`--strict-mcp-config` changes nothing); skill descriptions are already
capped at 1,536 chars each by Claude Code (docs: `skillListingMaxDescChars`),
so the six whole-body-in-description skills cost a few hundred tokens,
not the 8k their byte size suggests. **The floor is not ours to trim.
The only lever on the 41% is fewer turns.**

### 2.5 The other fleets (every local worker transcript, same method)

Workers (`~/.claude/projects/*--worktrees-*`, thegrid under `~/.cc-work`):

| fleet | sessions | API calls | ctx tokens | avg ctx | p90 | max | compactions | est $ | floor share | Agent calls |
|---|---|---|---|---|---|---|---|---|---|---|
| grove | 26 | 1,439 | 176M | 122k | 209k | 286k | 3 | 178 | 41% | 1 |
| unbrewed (p2p/artgen/pro-server) | 211 | 29,900 | 7,850M | 262k | 520k | 999k | 2 | ~5,000 | 17% | 18 |
| thegrid (cc-work) | 72 | 14,533 | 4,015M | 276k | 472k | 675k | 0 | ~2,500 | 25% | 85 |
| waterhouse (remotion) | 16 | 1,468 | 251M | 170k | 316k | 449k | 1 | 191 | 25% | 1 |
| deanlol | 4 | 133 | 11M | 85k | 151k | 164k | 1 | (GLM, unpriced) | 42% | 0 |

Orchestrator chats (`*--grove-orchestrator*`), which `gv cost` never counts:

| workspace | sessions | API calls | ctx tokens | avg ctx | max | est $ |
|---|---|---|---|---|---|---|
| unbrewed | 130 | 9,923 | 1,788M | 180k | 878k | ~2,500 |
| thegrid | 110 | 3,871 | 658M | 169k | 859k | ~1,050 |
| grove | 52 | 2,080 | 282M | 135k | 548k | 411 |
| waterhouse | 27 | 1,410 | 208M | 147k | 482k | 209 |
| deanlol | 8 | 525 | 60M | 114k | 209k | 68 |

Dollar figures use grove's price table at API list prices; on the
subscription they are plan-usage, not invoices, but the ratios hold.
Three things the big fleets add to the grove picture:

- **Context, not the floor, is the whole bill there.** Floor share is
  17–25% because sessions run 140+ API calls at 260k+ context. The
  `[1m]` pin means nothing ever compacts (2 compactions in 211
  unbrewed sessions). Cap simulation on the real transcripts:

  | cap | unbrewed workers | thegrid workers | unbrewed orchestrator |
  |---|---|---|---|
  | 100k | 64–74% saved | 64–67% | 49–58% |
  | 120k | 58–70% | 57–64% | 41–54% |
  | 150k | 50–65% | 48–59% | 33–47% |
  | 200k | 39–57% | 34–50% | 22–38% |

  At 120k that is roughly two forced compactions per unbrewed session.
- **Reads go through Bash, not the Read tool.** unbrewed: Bash reads
  48% of context, `Read` ~0%. thegrid: 30% vs 5%. The cause is the
  harness itself: in bypass-permissions mode Claude Code's system
  reminder tells the model to "read files with cat, head, or sed -n,
  search with grep … rather than using the dedicated Read, Edit, or
  Write tools". So a Spotify-style `Read` hook would never fire on
  those fleets; the gate has to sit on `Bash`. The tail is not the
  problem either: 25,206 Bash reads on unbrewed, p50 367 chars, p90
  4.1k; results over 10k chars are only 24% of the amplified total.
  It is *volume* — many small reads, re-sent 100+ times each.
- **Assistant tool inputs are 24–35%** (Edit old/new strings, long Bash
  command lines, heredocs). On waterhouse it is the largest single
  source. Nothing in the Spotify article touches this; only a cap on
  accumulated history does.

## 3. Levers, ranked by measured impact

Impact is "share of context tokens sent" unless stated. Risk is the
chance of making workers ship worse.

### L1 — cap the context with `--autocompact` (config only)

`claude --autocompact <100k–1M tokens>` (v2.1.263 `--help`; settings key
`autoCompactWindow`). Simulated on the real transcripts — optimistic =
`min(ctx, cap)` per call; pessimistic adds a 10k summary and a 20k
re-read after every forced compaction:

| cap | saved (optimistic) | saved (pessimistic) | forced compactions |
|---|---|---|---|
| 80k | 38% | 47% | 79 |
| **100k** | **27%** | **40%** | 30 |
| **120k** | **19%** | **33%** | 17 |
| 150k | 11% | 25% | 8 |
| 200k | 3% | 14% | 3 |

(The pessimistic column saves *more* because a forced compaction also
resets the accumulated junk that the optimistic model keeps paying for.)
On the big fleets the same cap is worth 57–70% (§2.5).

What a compaction actually costs, from the seven real ones in the
transcripts (5 manual, 2 auto):

| session | ctx before → after | summary size | re-read in next 10 calls | wall time |
|---|---|---|---|---|
| grove-177 (remote handoff) | 177k → 66k | 14k | 37k chars, 10 reads | 101 s |
| grove-178 (fleet view) | 138k → 51k | 6k | 16k chars, 4 reads | 103 s |
| unbrewed-pro-server-410 | **1,000k** → 61k | 13k | 1.8k chars, 1 read | 157 s |
| unbrewed-pro-server-401 | 152k → 50k | 8k | 11k chars, 5 reads | 133 s |
| waterhouse-mono-150 | 143k → 56k | 12k | 2.5k chars, 5 reads | 138 s |
| deanlol-books-2 | 165k → 49k | 16k | 6.8k chars, 4 reads | 68 s |

The post-compaction re-reads are targeted (`sed -n` ranges, `git
log`, one file), 2–10k tokens, and every one of these tasks went on to
finish. The cost is one to two minutes of wall time per compaction and
a ~10k re-orientation, against 100k+ of context dropped. The risk that
remains is the invisible one: a fact the summary dropped that the
worker does not know to re-read. That is what L6 is for.

Effort: one line in the repo's `claude:` command (`.grove/config.yaml:8`,
`~/.config/grove/config.yaml:16`). Risk: compaction summaries lose
detail; a worker mid-implementation can forget a verified fact and
re-derive it. Mitigations already in hand: the PR-body handoff
discipline (orchestrator duty 8), the cheap `PreCompact` hook (L6). The two
most expensive tickets in the ledger (grove-177/178 remote handoff:
150+ calls each, 190–230k context, 3 steers each, $34 each — 38% of the
whole ledger) are exactly the sessions a cap would have hit.

Verify with an A/B on rote tickets before rolling out: same ticket
class, capped vs uncapped, compare `$/call`, avg ctx, steers, PR
outcome. The `gv cost --analyze` flags already exist for the steer side.

### L2 — enforce read discipline with a PreToolUse hook (Spotify's core)

Grove installs four hook events (`SessionStart`, `Notification`,
`Stop`, `SessionEnd` — `internal/hooks/hooks.go:215-220`); no
`PreToolUse` exists, the installer cannot write a `matcher`, and the
receiver never returns JSON on stdout. All three are small additions.
Claude Code's contract (verified against code.claude.com/docs/hooks):
print `{"hookSpecificOutput":{"hookEventName":"PreToolUse",
"permissionDecision":"deny","permissionDecisionReason":"…"}}` and the
reason is shown to the model; `updatedInput` can rewrite the call
instead; exit 2 + stderr blocks unconditionally. `tool_input` carries
`file_path/offset/limit` for Read and `command` for Bash.

Three escalating versions, cheapest first:

- **L2a — deny + reason, no cheap model.** `gv hook pre-tool-use`: a
  whole-file `Read` (no offset/limit) of a file over N lines is denied
  with "this file is 1,412 lines; read it with offset/limit around the
  symbol you need, or `grep -n` first". Same for `cat`/`sed -n` over
  large files. The model already has the right tools (offset/limit,
  grep); it just doesn't reach for them unprompted. Expected: most of
  the 5.6% tail plus some of the medium reads. Zero latency, zero
  external dependency, zero new failure modes beyond a worker that
  argues with the hook (it can't — deny is final).
- **L2b — a cross-lane subagent on the z.ai flat plan (tested, works
  today).** Claude Code's native Agent tool cannot do this: `model:`
  frontmatter and `CLAUDE_CODE_SUBAGENT_MODEL` pick a model *name*, but
  `ANTHROPIC_BASE_URL` is process-global, so a Fable parent's subagents
  always hit Anthropic. The way around is Spotify's own "scripts"
  layer: the worker calls a CLI from its Bash tool, the CLI runs the
  micro-job on another lane, and only the text result enters the Fable
  context. Two shapes, both measured on `internal/hooks/hooks.go`
  (13.8k chars, ~3.4k tokens) against `api.z.ai/api/anthropic` with the
  key already in `~/.config/grove/.env`:

  | shape | latency | tokens in → back | notes |
  |---|---|---|---|
  | raw `/v1/messages`, `glm-5.3-flash`, thinking disabled | 11.9 s | 3,736 → 612 | bullets per function with line numbers; correct on spot-check |
  | same, thinking left on (default) | 32 s | 3,736 → 2,500 of thinking, **0 text** | GLM spends the whole budget thinking; `thinking: {type: disabled}` is mandatory |
  | `claude -p --bare --model glm-5.3-flash` with `ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN` | 24 s, 2 turns | 5.8k floor + 0.9k cached → 961 | full agentic subagent (Bash/Read/Grep) on the flat lane; `--bare` skips hooks, CLAUDE.md, skills, so no recursive `gv hook` firing; the "unrecognized_model" warning is cosmetic |
  | `claude -p` without `--bare` | 108 s | — | wandered for extra turns; always cap with `--max-turns` |

  Marginal cost on the flat plan: $0. The Portal caveats transfer
  exactly: 10–30 s per call, no reliable line numbers for *editing*,
  not for reasoning. So the job list is narrow and clear: "summarise
  this file / this test log / this diff", "list the call sites of X
  across the package", "what does this 2k-line migration do". A raw
  API call is right for summarise-this-blob (fast, deterministic
  input); `--bare` headless is right for "go look" jobs that need
  tools.

  Grove already has everything needed: the profile table (base URL,
  token env, per-tier model), `internal/config.WrapProfile` for the
  env, and a Bash tool in every worker. The deliverable is a
  `gv sub --lane zai-plan-glm-flash "<prompt>" [files…]` (name open)
  that prints the result, plus the deny reason in L2a naming it and a
  short skill telling the worker when it pays. pi is not needed for
  this — pi earns its keep when the *worker* harness should be
  non-Claude (the parked pi-harness pivot), not for a subagent.

  What it cannot fix: the 24–35% of context that is the worker's own
  Edit/Bash inputs, and the sheer count of small reads. Those only
  yield to L1.
- **L2c — Bash-side gate (the one that matters on the big fleets).**
  Same hook, `Bash` matcher: a command whose argv is `cat <file>` /
  `sed -n 1,9999p` on a >N-line file is denied with the same reason.
  Piped greps pass. On grove Bash reads are 15% of context; on
  unbrewed 48% and thegrid 30%, because bypass-permissions mode steers
  the model to Bash instead of `Read` (§2.5). Even there the >10k-char
  tail is only a quarter of it, so the win is from the *habit* change
  (grep before cat, `gv sub` for whole files), not the tail.

Honest ceiling for L2 as a whole: **6–15%**, depending on how much the
deny reason changes the ≤350-line habit. The deliverable that makes it
worth it is L2a; L2b is a nice-to-have once L2a shows workers obeying.

### L3 — cap Bash output (config only)

`BASH_MAX_OUTPUT_LENGTH` (default 30,000 chars; also
`bashOutputMaxChars` in settings, v2.1.261+). Output beyond the cap is
saved to a file and the path returned — the worker can `grep` it.
Measured tail: 7 of 261 Bash reads exceeded 10k chars; e2e output is 3%
and `go test` output 1% of context. Setting 12,000 for workers is worth
**~2–4%**, costs one env line in the profile/settings, and the only
regression is a worker that needs the whole `go test -v` log (it can
read the saved file). Low priority, near-zero risk.

### L4 — fewer, cheaper turns (already the biggest $ lever)

Not new, but the ledger makes it stark: **$0.226 per call on Fable 5,
$0.041 on Sonnet, $0 marginal on the flat GLM lane** — a 5.5× spread
per turn at similar context sizes. The 41% floor share only moves with
turn count. Existing doctrine (rote routing, merge trains, the
`model-lanes` skill) is the answer; the diet levers above multiply it.
One new observation: Fable 5.1's 2.5% cache-read rate means a Fable 5.1
worker's spend is 46% cache *writes* + 38% output — on that lane, "read
less" matters half as much and "think less / edit surgically" matters
twice as much as on Fable 5.

### L5 — measure it: a context view in `gv cost`

None of the above can be A/B-tested from the cockpit today. `gv cost`
reports totals; it does not say *what* the context is made of or how
fast it grows. A `gv cost --context grove-N --json` (or columns in
`--analyze`): avg context per call, last-turn context, compactions,
share by source (floor / Read / Bash-read / assistant / other), top-10
amplified tool results with file paths. The transcript walk is ~80
lines of Go on top of `internal/transcript`; the Python prototype is in
the appendix. This is the tripwire for every other lever and the
first ticket to write.

### L6 — make compaction safe: state outside the transcript, re-injected after

With L1 on, compaction becomes routine (about one to two per unbrewed
session at 120–150k) instead of rare. The worry is real: the summary
is written by the model, cannot be steered for auto-compactions (no
settings key or CLAUDE.md section is honoured; `/compact
<instructions>` is manual only — verified against the docs), and
whatever it drops is dropped silently. What the docs *do* give us is
exactly the right hook: **`SessionStart` fires after every compaction
with `source: "compact"`, and whatever the hook prints to stdout is
injected into the model's context.** Grove already installs
`SessionStart` → `gv hook session-start` with no matcher
(`internal/hooks/hooks.go:216`), so the receiver already runs at that
moment today; it just ignores the source and prints nothing.

The design, in three parts, none of which is a new hook event:

1. **The PR body is the checkpoint, kept live.** The kickoff gains one
   rule: every commit updates the handoff block in the PR description
   (the duty-8 five headings: Goal, Done + verified, Verified
   surprises, Remaining, Next step). Workers already commit per plan
   task and open the PR early, so this is a habit, not a new step.
   `gh pr edit --body-file` is one command. The transcript is then a
   cache of the PR body, not the other way round.
2. **`gv hook session-start` on `source == "compact"` prints a
   re-orientation.** Deterministic, ~1–2k tokens, built from ground
   truth rather than the summary: the ticket's acceptance criteria,
   `git log --oneline -8` on the branch, `git status --short`, and the
   PR body's handoff block (`gh pr view --json body`, with a git-only
   fallback if `gh` is slow — the receiver must never block). It also
   appends a `compaction` record to `events.jsonl` so the cockpit and
   `gv watch` show it and `gv cost --context` can count it. The
   receiver keeps its exit-0-always contract.
3. **The cap is set where the lane says.** For the Claude lane, the
   flag on the `claude:` line or `CLAUDE_CODE_AUTO_COMPACT_WINDOW` in
   the settings `env` block (the env var takes precedence over the
   flag; it is an absolute token count, 100k–1M). For profiled lanes
   the existing grove-103 `env:` passthrough already carries it, and
   the `model-lanes` skill's rule that on a credit meter "aggressive
   compaction is cheaper" points the same way. Only the `kimi` profile
   needs its 1M value left alone.

With 1 and 2 in place, the invisible-loss risk becomes: "a fact that
was neither in the PR body, nor in git, nor in the ticket, nor in the
summary". That is a narrow gap, and it is the same gap `gv adopt` and
`gv handoff` already live with — the transcript never travelled for
those either. Evidence from the seven real compactions (L1 table) is
that workers re-orient in 2–10k tokens without any of this help.

Two cheaper bridges before the code exists: put the "after a
compaction, re-read the PR body and the ticket before continuing" line
in the kickoff now (zero code), and run L1 at 150k first (≈1
compaction per session) rather than 100k.

## 4. What the data says NOT to build

| idea | measured | verdict |
|---|---|---|
| Code-writer mode (Spotify) | `Write` results 0.1%; Edit/Write *inputs* ~5% of assistant chars; output is 12–19% of $ (38% on Fable 5.1) | Skip. The savings pool is small and the documented failure modes (no line numbers, context-free code) are the ones that cost steers. Revisit only for a Fable 5.1-only fleet. |
| Trim skill descriptions (six skills carry their whole body in `description:`) | −0.1k tokens/turn; Claude Code caps each at 1,536 chars | Skip for tokens. Still worth fixing for correctness — the cap silently truncates those descriptions mid-sentence. |
| Slim `CLAUDE_CONFIG_DIR` worker pack (openrouter-cost-ideation idea 2) | grove's share of the floor is ~2k of ~40k | Skip. The floor is Claude Code's. |
| `--no-chrome`, `--strict-mcp-config`, `--disable-slash-commands`, `--setting-sources` | −1.1k, 0, 0, 0 | Skip. |
| `--bare` | skips credentials → not logged in on the sub | Unusable without an API key. |
| Shorter kickoff / fewer nudges | prompts + nudges are 4% | Already tight (`md_default.tmpl` is 29 lines). |
| `--exclude-dynamic-system-prompt-sections` | improves *cross-user* cache reuse; grove is one user with a 1h TTL | No effect expected. |

## 5. Proposed tickets (drafts — nothing filed)

In merge-train order; each is one PR. Revised after §2.5: the cap
moves to the front because it is config-only and worth 57–70% on the
fleets that spend, and the z.ai subagent CLI replaces the native
bulk-reader agent.

0. **Worker `--autocompact` on one big fleet, now** (L1). Config-only:
   add `--autocompact 150000` to the unbrewed `claude:` line for one
   week (150k is the gentle setting: ~1 forced compaction per session,
   50–65% saved). Compare `avg ctx`, steers, PR outcome against the
   prior week via ticket 1. Requires ticket 4's checkpoint rule in the
   kickoff first, or at least the one-line "after a compaction, re-read
   the PR body" instruction.
1. **`gv cost --context`** (L5). Context decomposition per ticket from
   transcripts; `--json` fields are a contract addition (additive). Rote
   candidate once the field list is nailed — the prototype below is the
   spec. Acceptance: reproduces this doc's table for grove-253 within
   5%; `e2e/plugin.sh` green.
2. **Worker `--autocompact` A/B** (L1). Config-only experiment on three
   rote tickets at 120k, three uncapped, same week; report `$/call`, avg
   ctx, steers, outcome via ticket 1. Decide the default from the
   result; document in LEARNINGS.md. No code unless the winner needs a
   config key (a per-repo `autocompact:` next to `claude:` would be
   nicer than editing the command string).
3. **`PreToolUse` hook plumbing + read gate** (L2a + L2c). Installer
   learns `matcher`; receiver gains a stdout-JSON path; `gv hook
   pre-tool-use` denies whole-file `Read` / `cat` over `N` lines (config
   `read_gate_lines`, default 350) with a reason that names
   offset/limit and grep. Must stay exit-0 on any internal error (never
   block a worker on a grove bug). Tests: table-driven over hook
   payloads; `e2e/dummy.sh` gains a hook-JSON assertion.
4. **Compaction checkpoint** (L6). `gv hook session-start` branches on
   `source == "compact"`: prints the re-orientation block (acceptance
   criteria, `git log -8`, `git status`, PR-body handoff via `gh` with
   a git-only fallback) to stdout and appends a `compaction` event;
   cockpit/`gv watch` surface it; kickoff gains the "update the PR-body
   handoff on every commit" and "after a compaction, re-read the PR
   body" rules. No new hook event is needed.
5. **`gv sub` — micro-jobs on another lane** (L2b). `gv sub --lane
   <profile> "<prompt>" [file…]` resolves the profile (base URL, token
   env, model), sends one `/v1/messages` call with thinking disabled
   and the files inlined, prints the text; `--agentic` runs `claude -p
   --bare --max-turns N` under the profile env instead. Refuses
   `openrouter-*` lanes unless `--allow-paid`. The L2a deny reason
   names it; a 20-line skill says when it pays (whole-file summaries,
   test logs, call-site hunts) and when it does not (anything you will
   edit). Measure adoption via `gv cost --context` (subagent calls are
   Bash results, so they show up as small `bash:other` rows).
6. **`BASH_MAX_OUTPUT_LENGTH=12000` for workers** (L3). One env line in
   the worker launch; note in `claude-code-facts`.
7. **Orchestrator sessions in `gv cost`** (§2.5). They are ~$4.2k of
   estimated spend across workspaces and invisible. A per-workspace
   "orchestrator" row (transcripts under `<workspace>/.grove/orchestrator*`)
   in `gv cost --json`, additive field. The same `--autocompact` cap
   applies to the orchestrator `claude` command (`gv orchestrator new`)
   and is worth 41–58% there.

## 6. Verified surprises (for LEARNINGS.md once acted on)

- Grove's context never shrinks: 3 compactions in 26 sessions; last-turn
  context up to 286k on Sonnet 5. The `[1m]` pin in
  `~/.claude/settings.json` is why — a 200k model would have compacted.
- 97% of all worker tokens are cache reads; the average API call
  carries 122k of context. Per-turn cost is a context number.
- Fable 5.1 cache reads are priced at 2.5% of input (Fable 5: 10%). The
  same session costs 4× less in reads on 5.1; output becomes the
  biggest slice there.
- Skill `description:` is capped at 1,536 chars by Claude Code. Six grove
  skills exceed it (their whole body is the description) and are being
  silently truncated in the listing.
- `~/.claude/settings.json` `mcpServers` is not read — workers attach no
  MCP servers (`--strict-mcp-config` measured 0 change).
- `--bare` skips credentials, so it cannot run on the subscription.
- The Agent tool was used once across 26 worker sessions despite the
  kickoff instruction to fan reconnaissance out. Prompt instructions do
  not move reading behaviour; hooks do.
- Transcript chars per context token is ~1.8 for grove's Go-heavy
  content (line-numbered code, JSON-escaped tool inputs), stable across
  a session — so no hidden pruning, and char counts overstate tokens by
  ~2× vs the usual 4:1 rule of thumb.
- An undeduplicated transcript walk over-counts usage by 1.9× (multi-block
  assistant messages repeat `usage` per line). `internal/cost` already
  dedups; any new reader must too.

## 7. `gv sub` deep-dive — micro-tasks on the z.ai plan (added later the same evening)

Question from Dean: would handing one-off micro-tasks from Fable/Opus
workers and orchestrators to the z.ai flat plan actually cut their
tokens? Two halves: how much of the context is made of things a
micro-task could replace, and is GLM 5.3 Flash good enough at them.

### 7.1 What is delegable, measured

Every tool result in every fleet, classified by what it was (whole-file
reads over 120 lines, grep sweeps, build/test logs, `git diff`, `gh`
payloads, `gv --json`, MCP results, pane captures) and weighted by how
many later API calls re-sent it. "Saved" assumes the delegated call
returns a summary one fifth the size.

| fleet | delegable blobs | saved if all delegated | investigation bursts (≥4 reads, no edit) | burst share of ctx |
|---|---|---|---|---|
| grove workers | 25% of ctx | ~20% | 35 bursts / 285 calls | 29% |
| unbrewed workers | 18% | ~14% | 1,118 / 13,167 | 30% |
| thegrid workers | 20% | ~16% | 419 / 3,384 | 23% |
| grove orchestrator | 21% | ~17% | 88 / 623 | 22% |
| unbrewed orchestrator | 16% | ~13% | 319 / 2,228 | 13% |
| thegrid orchestrator | 24% | ~19% | 175 / 1,295 | 15% |

The two columns overlap (a burst is mostly file reads), so the honest
band for full adoption is **15–25% of context tokens per fleet**, on
top of whatever the cap (L1) saves. Two specifics stand out:

- **thegrid orchestrator: Linear MCP results are 16% of its context on
  their own** (622 payloads). That is the single best orchestrator
  target — but the fix there is as much "make grove render Linear/`gv
  ls` output compactly" as it is delegation.
- **unbrewed workers: 16,712 small reads at 20% of ctx.** Individually
  undelegable, collectively the biggest bucket. Only an Explore-style
  "go find out and report" delegation (a burst → one report) touches
  it, which is what the agentic mode is for.

### 7.2 Bake-off: GLM 5.3 Flash on real grove jobs

All against `api.z.ai/api/anthropic`, thinking disabled, system prompt
"terse technical summarizer, bullets only, cite file:line".

| job | mode | wall | tokens in → out | verdict |
|---|---|---|---|---|
| A. summarise `internal/tui/tui.go` (60k chars) for "add a key binding" | raw | 24.5 s | 17.0k → 1.1k | good: types, every Model method with approximate line, where key dispatch lives. Lines are `~L880` style — orientation, not edit anchors |
| B. triage `gv ls --json` | raw | 4.0 s | 1.0k → 0.1k | correct (named the one row needing a human) |
| C. review PR #279 diff (55k chars) | raw | 83 s | 16.5k → 1.3k | specific risks with file:line (e.g. `forget()` not clearing shadow state; `Append` failing mid-loop) — reviewer-grade orientation |
| D. call sites of `tmux.WindowID` | `--bare` agentic, `--max-turns 8` | 78 s, 11 turns | 8.4k + 32.5k cached → 2.4k | precise on what it found, but **9 of 14** real call sites (missed 5 helpers inside grove.go) |
| E. "where does `gv audit` decide abandoned/idle/disconnected" | `--bare` agentic, `--max-turns 8` | 68 s, 9 turns | — | **no answer** (turn cap hit, `result: null`) |
| E′. same, `--max-turns 15` + "start with grep -rn abandoned" | `--bare` agentic | 49 s, 4 turns | 4.7k + 5.0k cached → 0.9k | correct and precise: `audit.go:79/83/86/90` with the rule for each class |
| 4 parallel trivial calls | raw | 0.9–1.5 s each | — | no throttling; **2 of 4 returned empty text** |

Cost of the entire bake-off (≈130k tokens processed, 11 calls): **5
credits** of the 2,000-credit 5-hour bucket, 4 of the 10,000 weekly.
A micro-task is ~0.5–1 credit. Live quota at the time: 5-hour bucket
7/2,000, weekly 6,198/10,000 (62%). The weekly bucket is the only one
that could ever bind, and only if GLM *workers* are also drawing on it.

What the bake-off says about the shape of the tool:

- **Summarise-a-blob is solid and fast enough** (4–25 s for up to 60k
  chars; the 55k diff review took 83 s). Output is 5–15% of input.
- **Agentic "go find out" needs a starting instruction and a real turn
  budget**, or it returns nothing. E failed at 8 turns with a bare
  question and passed at 4 turns once told where to start. Recall on
  exhaustive hunts is partial (D: 64%). So: orientation and "what does
  X do" jobs yes; "find every call site before a refactor" no — that
  stays with the worker's own grep.
- **Line numbers are approximate in raw mode** (the model counts) and
  exact in agentic mode (it ran grep). Spotify's "not for editing"
  caveat holds; the worker must re-read the exact lines it will edit.
- **Empty responses happen** (2 of 4 trivial calls, and the thinking
  default). The CLI needs thinking disabled, an empty-text retry, and a
  visible "no answer" instead of a blank.

### 7.3 Design sketch (for the ticket, not built)

    gv sub [--lane zai-plan-glm-flash] [--agentic [--max-turns 12]]
           [--out summary|json] "<prompt>" [file|dir …]

- Resolves the lane from the profile table (`base_url`,
  `auth_token_env`, tier model). Default lane from config
  (`sub_lane:`), refuses `openrouter-*` without `--allow-paid`.
- **Raw mode** (default): one `/v1/messages` call, `thinking` disabled,
  the files inlined in `<file path=…>` tags, the "bullets only, cite
  file:line" system prompt, `max_tokens` ~2k, one retry on empty text.
  Stdin is accepted so `go test ./... 2>&1 | gv sub "what failed and
  why"` works.
- **Agentic mode**: `claude -p --bare --model <tier> --max-turns N
  --allowedTools "Read,Grep,Glob,Bash(rg:*),Bash(grep:*),Bash(sed:*),
  Bash(cat:*)"` under the lane env, cwd = the worktree, prompt
  prefixed with a "start with …" hint when the caller gives a symbol.
  `--bare` means no hooks fire, no CLAUDE.md, no skills — safe from
  inside a worker's Bash tool. `result: null` → exit 3 with "no answer
  in N turns; try a starting hint".
- Prints the text; nothing else enters the caller's context. Appends a
  `sub_call` line (lane, mode, tokens, ms) to a per-workspace ledger so
  `gv cost --context` can show adoption and credits.
- The kickoff/skill line for workers and the orchestrator brain: use
  it for whole-file orientation, test/e2e logs, PR diffs, "what does
  this do", and `gv ls --json`/Linear triage; never for the lines you
  are about to edit, never for exhaustive call-site hunts.
- Enforcement is L2a/L2c: the deny reason for a big `cat`/`Read` names
  `gv sub`. Without the gate, adoption will look like the Agent tool
  today (once in 26 sessions).

### 7.4 Expected effect, honestly

Per fleet, full adoption is worth ~15–25% of context tokens (§7.1);
realistic adoption with the gate is maybe half that in the first
month. It stacks with the cap (L1), which is still the bigger and
cheaper lever on the fleets that spend. Its distinctive value is not
the percentage: it is that the delegated work costs **zero marginal
dollars**, keeps the Fable/Opus context clean of raw blobs, and gives
the orchestrator a way to triage Linear and `gv ls` payloads without
carrying them. The credit budget is a non-issue at micro-task scale.

## Appendix — the decomposition prototype

Python, reads `~/.claude/projects/<encoded worktree>/*.jsonl`, dedups
by `(message.id, requestId)`, attributes each content block × the
number of later API calls. Port this into `internal/cost` for ticket 1.

```python
import json, glob, os, collections, re
def txt(c):
    if isinstance(c, str): return c
    if isinstance(c, list):
        return ''.join(txt(x.get('text','')) if x.get('type')=='text'
                       else txt(x.get('content','')) for x in c if isinstance(x, dict))
    return ''
A = collections.defaultdict(float); actual = 0; base_amp = 0
for f in glob.glob(os.path.expanduser('~/.claude/projects/<encoded-worktree>/*.jsonl')):
    seen, tu, items, turn, base = set(), {}, [], 0, None
    for l in open(f):
        L = json.loads(l); t = L.get('type'); m = L.get('message') or {}
        if t == 'assistant':
            u = m.get('usage'); key = (m.get('id'), L.get('requestId'))
            if u and key not in seen:
                seen.add(key); turn += 1
                ctx = sum(u.get(k, 0) for k in ('cache_read_input_tokens','cache_creation_input_tokens','input_tokens'))
                actual += ctx; base = base if base is not None else ctx
            for b in m.get('content') or []:
                if b.get('type') == 'tool_use':
                    tu[b['id']] = (b['name'], b.get('input', {}))
                    items.append(('asst:tool_input', len(json.dumps(b.get('input', {}))), turn))
                elif b.get('type') == 'text':
                    items.append(('asst:text', len(b.get('text') or ''), turn))
        elif t == 'user':
            for b in (m.get('content') if isinstance(m.get('content'), list) else []):
                if b.get('type') == 'tool_result':
                    name, inp = tu.get(b.get('tool_use_id'), ('?', {}))
                    if name == 'Read': name = 'Read:targeted' if (inp.get('offset') or inp.get('limit')) else 'Read:whole'
                    if name == 'Bash' and re.search(r'\b(cat|sed -n|grep|rg|head|tail)\b', inp.get('command', '')): name = 'bash:read-like'
                    items.append((name, len(txt(b.get('content', ''))), turn))
    base_amp += (base or 0) * turn
    for name, n, at in items:
        if at: A[name] += n * max(0, turn - at)
tot = sum(A.values()); growth = actual - base_amp
print('floor share', base_amp / actual)
for k, v in sorted(A.items(), key=lambda kv: -kv[1]):
    print(k, round(100 * v / tot * growth / actual, 1), '% of total')
```

Measurement caveats: Read results are attributed by tool_use id, Bash
reads by a regex on the command; assistant thinking is not in
transcripts (0 chars) so it is not counted; the `[1m]` context pin means
the compaction simulation had almost no real compactions to calibrate
against — the optimistic/pessimistic band is the honest answer until
ticket 2 runs.
