# Feature drive — brief template

A standing brief for one orchestrator chat that drives a feature train from
"cars filed" to "one reviewed PR into base, with a report". Copy it, fill
every `<…>`, and launch:

    gv orchestrator new --brief-file docs/briefs/<slug>-drive.md

The filled copy lives beside this file so the run is reproducible. Every
grant below is scoped to ONE slug; the feature branch is the trust boundary
— whatever lands inside `feature/<slug>` is reversible and never reaches
base, so the chat may own it. Whatever crosses that boundary stays the
operator's. Rules the orchestrator seed states (propose-then-dispose, never
edit code, never route models unasked) hold unless a line here grants the
exception by name.

---

Drive feature train `<slug>` (repo `<repo>`, branch `feature/<slug>`,
base `<base>`, label `<label>`) until it meets its **Done when** block or a
cap below trips. This is a feature drive: for this slug only, the grants in
§Allowed are my explicit yes in this chat and supersede the supervision
mandate's out-of-scope list where the two disagree. §Never still wins over
everything.

**Done when** is the `## Done when` section of `<design-doc-path>`. Read
that section first, then `gv feature ls --json` for the cars. Do not read
the whole design unless a car's acceptance sends you there.

**Watch** with one `gv watch --json` Monitor over this workspace. Never
derive state from a pane. Keep a running summary in this chat, one line per
action: `HH:MM grove-N <event> → <action>`.

## Allowed, without asking (act, then log)

1. `gv grab grove-N --repo <repo> --feature <slug>` for the next queued car
   whose `depends on` predecessor has MERGED into the train. One car in
   flight per dependency chain; parallel only where the chain allows.
2. `gv answer` when the answer is in the ticket, the design's Done when, or
   §Pre-answered below. A design decision none of those settles is NOT
   derivable: push, keep waiting.
3. `gv nudge` on `pr_ci_failed` (name the check), `pr_conflicting`
   ("rebase on feature/<slug>"), and a failed review (paste the findings).
   At most <steer-cap> steers per car; the next one is a push.
4. Review every car before merging it. Delegate the review, do not do it
   in this chat: `/code-review <PR#>` or a subagent given the PR, the
   car's `Acceptance:` lines, and the Done when block. A pass needs: CI
   green, every acceptance line checked, no finding above minor. Record
   the verdict and the reviewer's three strongest findings in the journal.
5. Merge a passed car into the train: `gh pr merge <PR#> --squash` — the
   PR's base must read `feature/<slug>`; abort if it reads `<base>`.
   Then `gv feature land <slug> --yes`, and for each ticket it reports
   landed: `gh issue close N --comment "landed in feature/<slug> via PR #M"`
   (duty 10, the `land <slug>` order is given here).
6. File a bug or gap found while driving as a new issue carrying label
   `<label>`, body in the ticket-writing skill's shape with a
   `depends on #N` line, and grab it as a car. Cap: <self-filed-cap>
   self-filed cars for the whole drive; the next one is a push with the
   draft, not an issue.
7. Checkpoint nudge, then `gv pause`, on rot signals (duty 8). Never
   `gv adopt` — when it comes back is my call.
8. Open or update ONE draft PR `feature/<slug>` → `<base>`. Its body is
   the report (§Report). Update it after every merged car. Never mark it
   ready for review; never merge it.
9. Append to the journal (§Journal). Nothing else in the repo is yours to
   write.

## Never, however this brief reads

Merge anything into `<base>`. Close or comment on an issue outside the
land step above. `gv done`, `untrack`, `adopt`, `sweep`, `handoff`,
`gv account use`, `--model`/`--profile`/`--account` routing not named in
§Pre-answered. Touch another train's cars or branch. Run any
operator-facing mutation on a real workspace or host — anything a car
ships that writes config, brains, hooks, or state is exercised ONLY in a
scratch workspace (docs/seed-manifest.md §Dummy-data E2E). Edit code,
tests, or any file other than the journal and the draft PR body.

## Caps — any one trips → push, then stop acting, keep watching

- wall clock: <until, e.g. 2026-10-25 07:00 local>
- self-filed cars: <self-filed-cap> (recommended 3)
- steers per car: <steer-cap> (recommended 2)
- spend: `gv cost --json` sum over this train's cars above <usd-cap>
- a car `pr_closed` without merge, or `worker_vanished` twice on one car

## Pre-answered (do not wake me for these)

- <each open question from the design, with the decision>
- <lane/effort/account decisions, if any; otherwise "repo default model
  and effort, no routing">
- <anything the workers always ask: base branch, skip rules for red e2e
  that need live tokens, etc.>

## Pushes (ntfy)

```
url=$(awk '/^notify:/{n=1} n && $1=="ntfy:"{print $2; exit}' ~/.config/grove/config.yaml)
[ -n "$url" ] && curl -s -H "Title: gv <slug>: <what>" -H "Priority: <high|default|low>" -d "<body>" "$url"
```

Read the URL from the global config every time; never echo it. Priority
`high` for a question, a cap, or done; `low` for a merged car (title only).
If `ntfy_body:` reads `title-only`, send no body.

## Journal

`<journal-path>` (recommended `docs/plans/<YYYY-MM-DD>-<slug>-journal.md`),
append-only, one dated line per entry, categories exactly:
`decision` · `merged` · `filed` · `answered` · `steer` · `review` · `push`
· `cap`. Form: `- YYYY-MM-DD HH:MM <category> grove-N — <what and why, one
sentence; a link if a PR or issue>`. Do not commit it; I commit docs to
base myself at the end. A fresh chat on this brief re-reads the journal
and continues from its last line.

## Report (the draft PR body, rewritten after every merged car)

1. **Goal** — the design's problem paragraph, restated in three sentences.
2. **Done when** — every line of the block, each marked ✅ with the
   evidence (PR, test name, e2e leg, or journal line) or ⬜ with what is
   missing.
3. **Cars** — table: ticket, title, PR, review verdict, merged at, est_usd.
4. **Decisions** — the `decision` and `answered` journal lines, verbatim.
5. **Found along the way** — the `filed` lines, each with its outcome.
6. **Operator steps after merge** — anything the design leaves to me
   post-release, as a checklist. Not part of done.
7. **Open** — anything you could not settle, each with your recommendation.

## When it ends

Every Done-when line is ✅, or a cap tripped, or I say stop. Then: final
report into the draft PR, one summary push, the same summary as the last
message of the turn — counts per event type, every action, what is still
open. Do not close this chat; I read it next.
