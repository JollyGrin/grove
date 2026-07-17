---
name: ticket-writing
description: Use when writing, splitting, sequencing, or labeling grove tickets — sizing rules, the merge-train vs feature-branch decision, and the rote test for routing a ticket to a cheaper model. Costs are set at ticket-writing time, not at execution time.
---

# Ticket writing

Grove's spend is decided when the ticket is written, not when the worker
runs. The 2026-07 cost ledger (`gv cost --analyze`): two-thirds of merged
tickets land for $1–7; nine outliers at $9–23 were ~60% of all spend, and
each outlier was really several tickets living in one ever-growing context
(grove-36: 192 turns, $23; grove-63: 136 turns averaging 250k context per
turn). Cost ≈ turns × resident context, and every turn re-reads the whole
context — a worker's expensive turns are the late ones.

## Sizing

- **If the honest estimate is more than ~60 turns, split it.** Three $4
  tickets beat one $23 ticket, and each sub-ticket spends its life under
  100k context where the model is sharpest.
- Every sub-ticket states acceptance criteria checkable against main on
  its own — "part 2 of X" is a sequencing note, not a substitute for
  criteria.

## Sequencing: merge train by default

Decision rule for a multi-ticket goal: **can each sub-ticket land on main
green on its own?**

- **Yes → merge train.** Write "depends on #N" in the body; the
  orchestrator dispatches N+1 only after N merges. Each PR is reviewed
  against main; all grove machinery (grab base, sentinel, sweep) just
  works.
- **No → feature branch, consciously.** Reserve for rewrites where a
  half-migrated state breaks the gate for everyone (#79-scale). Accept
  the taxes up front: the branch drifts from main and a human eats the
  rebases; PRs review against a moving target; grab/diff/sentinel don't
  know alternate bases today.
- **No stacked PRs for autonomous workers.** Stacks mean rebase churn,
  and an agent resolving mid-stack conflicts is the steering-heavy
  failure mode the train avoids.

## The rote test: routing to cheaper models

A ticket may carry the `rote` label — dispatched via
`gv grab grove-N --repo X --model claude-sonnet-5` (later: cheap
`--profile` lanes) — only if **all three** hold at writing time:

1. **Executable acceptance criteria** — the done-check is a command or
   test, not a judgment call.
2. **Enumerable surface** — the files/packages it touches are listed in
   the ticket body.
3. **Zero open design decisions** — if the worker could plausibly ask
   "should it work like A or B?", it is not rote.

Rote tickets get only what's enumerated — a cheap model does exactly
what the acceptance criteria list and won't infer house conventions.
List the docs rows explicitly (TASKS.md; LEARNINGS.md if the work
surfaced a surprise). Field-verified 2026-07-17: two Sonnet workers
(grove-89/94) shipped clean code but skipped TASKS.md; the Fable
worker (grove-90) added its row unprompted.

Feedback loop: two or more steers on a rote-labeled ticket means the
label was wrong — fix the test or the ticket, not the model.
`gv cost --analyze` (steers + $/merged-PR on rote tickets) is the
scoreboard; rote tickets that clear cleanly become the eval set for
trying cheaper OpenRouter lanes later.
