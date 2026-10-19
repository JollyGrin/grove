# model-fit train — bring grove's prompts, dials and memory up to the Claude 5 family (2026-10-07)

**Driver:** the grove-repo orchestrator. **Judge:** Dean.
**Shape:** merge train on `main` (each car lands green on its own; "depends
on #N" in the body orders them). Label: `model-fit`.

## Why

Grove's architecture (deterministic outer loop, worktree-per-task,
hooks-as-truth, propose-then-dispose) still matches what Anthropic
recommends for long-running agents. What aged is the layer that talks to
the model: the kickoff templates are a seven-step script with hedges
written for models that over-asked and under-delegated; grove has a
`--model` dial but no `--effort` dial, although effort is now the primary
cost/quality control on one frontier model; workers are never told about
the per-repo auto-memory surface Claude Code gives them (0 of 24 worker
sessions ever wrote one; all nine memory files are the orchestrator's);
LEARNINGS.md carries ~12 entries contradicted by later ones; and the
"discard and restart" bet holds on Opus/Sonnet lanes but not on Fable 5.1,
where compaction with a deterministic re-orientation is the cheaper move.

The full audit is in the 2026-10-07 orchestrator chat; the measured
background is [2026-09-06-token-diet-research.md](2026-09-06-token-diet-research.md)
and [2026-09-28-boot-cost-analytics-investigation.md](2026-09-28-boot-cost-analytics-investigation.md).

## Sources (published, reviewed guidance the cars cite)

- Anthropic, Claude Code docs — *Best practices*:
  https://code.claude.com/docs/en/best-practices (verify-your-work,
  specific context, CLAUDE.md "would removing this cause mistakes?",
  manage context, Stop hook as a deterministic gate, adversarial review
  subagent, compaction instructions in CLAUDE.md).
- Anthropic, Claude Code docs — *How Claude remembers your project*:
  https://code.claude.com/docs/en/memory (auto memory is per repository,
  shared across worktrees; first 200 lines / 25 KB of MEMORY.md loaded;
  `type` + `modified` frontmatter; `autoMemoryDirectory`,
  `autoMemoryEnabled`; project-root CLAUDE.md is re-injected after
  compaction).
- Anthropic, Claude Code docs — *Model configuration / Adjust effort
  level*: https://code.claude.com/docs/en/model-config (levels per model;
  resolution order env var > `--effort` > settings > model default; Fable
  5.1 defaults `high`, Opus 5.5 and Sonnet 5.5 default `medium`;
  `effortLevel`, `modelSettings`, `maxEffortLevel`; `effort` frontmatter in
  skills/subagents).
- Anthropic, Claude Code docs — *Hooks reference*:
  https://code.claude.com/docs/en/hooks (SessionStart `source: compact`,
  stdout added to context; PreCompact/PostCompact; Stop `decision: block`;
  PreToolUse `permissionDecision` — the reason is NOT shown to the model,
  `additionalContext` is).
- Anthropic Engineering — *Effective harnesses for long-running agents*:
  https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents
  (progress log + feature list + init script + git history are the memory
  that survives a context window; one feature at a time; verify end to end
  before marking passing; leave the environment clean).
- Anthropic Engineering — *Effective context engineering for AI agents*:
  https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
  ("right altitude" system prompts; no brittle if/else logic in prompts;
  just-in-time retrieval; compaction, structured note-taking and
  sub-agents as the long-horizon techniques).
- Anthropic Engineering — *Claude Code best practices*:
  https://www.anthropic.com/engineering/claude-code-best-practices.
- Anthropic, `claude-api` skill bundled with Claude Code 2.1.292:
  `shared/model-migration.md` § *Migrating to Claude Fable 5.1* →
  *Behavioral shifts* and *Long-running agent recommendations* ("prompts
  and skills written for prior models are often too prescriptive … A/B
  with scaffolding removed"; ground progress claims; state boundaries;
  delegate asynchronously; give it a memory surface; autonomy block), and
  `shared/prompt-audit.md` (the dated-pattern rubric and keep list).
  Prices verified there (cached 2026-09-25): Opus 5.5 $4/$20 with cache
  reads $0.20/MTok; Sonnet 5.5 $2/$10, cache reads $0.20; Fable 5.1
  $10/$50, cache reads $0.25.

## Decisions assumed (Dean to confirm or overrule)

- **Templates state goals, constraints and verification; the STATUS
  sentinel block stays byte-identical.** The sentinel is a format-pinning
  example (keep-list item 7), not choreography.
- **Effort is the first routing axis on the default model; `--model` /
  `--profile` stay the second (lanes, budgets).** Nothing routes
  automatically — `no-model-routing-without-ask` still holds.
- **Three memory surfaces, one job each:** auto memory (machine-local,
  Claude-written working notes and corrections), LEARNINGS.md (git,
  cross-machine, dated verified surprises), skills (rules that
  generalized). Promotion is proposed by the orchestrator, applied by the
  operator.
- **Compaction over kill-and-restart on the Claude lanes.** Default stays
  off in this train; the recommended cap is documented and the
  re-orientation hook makes it safe to turn on.
- **PR #278** (guidance-surface diet) and **PR #292** (cost --context)
  are decided (merged or re-scoped) before cars 02 and 08 run; the train
  does not duplicate them.

## Mechanics

- Merge train on `main`. Cars 01, 03, 05, 09, 10 are independent; 02
  follows 01; 04 follows 03; 06 and 07 follow 05; 08 follows the PR #292
  decision.
- Each PR: gate green (`go build ./... && go vet ./... && go test ./...`,
  bare, never piped), `gofmt -l .` empty, `e2e/all.sh` for anything that
  touches the lifecycle or hooks; a TASKS.md row; a LEARNINGS.md entry for
  any verified surprise. Load `shipping-gates` first; `claude-code-facts`
  before hook/kickoff work.
- Templates are a `--json`-adjacent contract only through the sentinel
  lines; `e2e/plugin.sh` stays the tripwire for any field change.

## Cars

| # (issue) | Car | Label(s) | Depends on |
|---|---|---|---|
| 01 (#433) | Kickoff templates: goal and constraints, not choreography | model-fit | – |
| 02 (#434) | Kickoff A/B: old vs new template on matched rote tickets | model-fit, documentation | 01, PR #292 decision |
| 03 (#435) | `--effort` as a first-class dial (grab/adopt/orchestrator, config, ls, ledger, doctor) | model-fit, rote | – |
| 04 (#436) | Route by effort before model: ticket-writing skill, seed, roadmap | model-fit, documentation, rote | 03 |
| 05 (#437) | Workers get the auto-memory surface (kickoff, CLAUDE.md, doctor) | model-fit | 01 |
| 06 (#438) | LEARNINGS.md retirement pass: fact + rule + ticket, contradicted entries resolved | model-fit, documentation | 05 |
| 07 (#439) | Learnings promotion: `gv learnings` read-only report + orchestrator duty | model-fit | 05 |
| 08 (#440) | Compaction over discard: `autocompact:` config, CLAUDE.md preservation line, SessionStart re-orientation | model-fit | PR #292 decision |
| 09 (#441) | Stop hook: evidence gate for DONE + last-match, format-tolerant sentinel | model-fit | – |
| 10 (#442) | Price table + tier classifier for the Claude 5.5 family and Fable ids | model-fit, rote | – |

## Car 02 result (2026-10-07, orchestrator-run)

Three rote tickets × three arms on Fable 5.1, same afternoon. A = pre-#444
template via a temporary `grove-old` repo entry (`prompt:` →
`2026-10-07-model-fit-train/kickoff-old.tmpl`), B = current template,
C = current template + `--effort medium`. B/C ran on cloned issues
#450–#455 (`ab-test`). All nine: PR opened, bare gate green on re-run,
DONE verified by the Stop evidence gate, zero steers.

| ticket | arm | task | PR | est $ | calls | avg ctx | min | diff | tests | outside surface |
|---|---|---|---|---|---|---|---|---|---|---|
| #145 merge gate | A | grove-old-145 | #459 | 2.11 | 17 | 64k | 7.3 | +311/−33 | 5 | internal/tui/view.go |
| | B | grove-450 | #457 | 2.45 | 22 | 71k | 5.8 | +250/−30 | 4 | – |
| | C | grove-451 | #462 | 1.70 | 15 | 61k | 5.8 | +262/−48 | 5 | – |
| #131 paper cuts | A | grove-old-131 | #458 | 2.14 | 18 | 61k | 6.6 | +343/−44 | 8 (new test file) | notify.go* |
| | B | grove-452 | #456 | 1.87 | 15 | 60k | 5.5 | +285/−44 | 7 | notify.go* |
| | C | grove-453 | #463 | 1.82 | 18 | 59k | 5.8 | +288/−44 | 6 | notify.go* |
| #169 doctor tmux | A | grove-old-169 | #460 | 3.10 | 25 | 76k | 8.7 | +275/−16 | 2 | internal/connections/* † |
| | B | grove-454 | #461 | 3.41 | 26 | 80k | 10.6 | +295/−24 | 2 | internal/connections/* † |
| | C | grove-455 | #464 | 2.20 | 16 | 70k | 7.7 | +303/−16 | 2 | internal/connections/* † |

\* the truncation had moved to `internal/notify` since the ticket was written; all arms found it.
† the doctor's checks live in `internal/connections` now; all arms followed the existing structure as the ticket asked.

**Decision:** keep the current template (cost-neutral, better scope and
test-placement discipline, evidence in every PR body); `--effort medium`
is the rote test's first action (already in the skill via #447) — it was
the cheapest arm on all three tickets with no measurable quality loss.
Caveat: n=3, one model, one afternoon.
