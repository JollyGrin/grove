# Multi-model orchestration — ideation

Status: **raw ideation, not a design doc yet**. Captured 2026-07-08 to be
elaborated later (Dean: "I'll throw fable on it"). Not a ticket — no
implementation implied by this doc's existence.

## Original prompt

> At some point in the future I would like to implement more models (glm,
> gpt, etc) that can be used for orchestration or workers (or whatever
> other role we add in the future). We'll need to not only be able to use
> models, but somehow think of a system to evaluate the effectiveness of
> each. I'm not quite sure how to go about that. We already have cost
> tracking, is there other logs that would be useful to evaluate? how do
> we evaluate work done? or proposals made? to compare models. a dummy
> ticket with traps built in? I'm sure this problem has had smarter minds
> think about this, so the answer is out there.
>
> 1. how can we integrate new models (perhaps non-claude through opencode?
>    any other recommended harnesses?)
> 2. what data should we collect from sessions, so we can evaluate models
>    over time?
> 3. would a dummy project to get results around a control be effective?
>    or give useless results compared to real world work

## 1. Integration path — new models / harnesses

*(fill in: opencode, other agent harnesses, how grove's `claude:` config
knob per-repo could generalize to a `model:`/`harness:` knob, what
changes at the tmux/worker-dispatch layer vs. what's harness-specific)*

## 2. Evaluation data — what to log beyond cost

*(fill in: turn counts, steering/nudge frequency, question-asked rate,
time-to-PR, revert/rework rate, human edit distance post-PR, CI
pass-on-first-try, diff size vs ticket size, self-reported confidence,
tool-call error rates)*

## 3. Dummy/control project vs. real-world signal

*(fill in: pros/cons of a synthetic benchmark ticket with known traps vs.
mining real ticket outcomes; risk of overfitting to synthetic tasks;
possible hybrid — small fixed control suite + real-world longitudinal
tracking)*

## Related prior art to look into

*(fill in: SWE-bench, METR / model evals literature, Devin's internal
eval harness, LMSYS/Chatbot-arena-style pairwise comparison, existing
agent-eval frameworks)*

## Open questions

- Does model choice vary by role (orchestrator vs. worker) or is one
  eval harness enough for both?
- How much of this eval infra is grove-specific vs. reusable/generic?
- Where would results live — new `gv` subcommand, or out-of-band
  analysis over `events.jsonl`?
