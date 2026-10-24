Drive feature train `brain-overlay` (repo `grove`, branch
`feature/brain-overlay`, base `main`, label `brain-overlay`) until it meets
its **Done when** block or a cap below trips. This is a feature drive: for
this slug only, the grants in §Allowed are my explicit yes in this chat and
supersede the supervision mandate's out-of-scope list where the two
disagree. §Never still wins over everything. The template this follows is
docs/briefs/feature-drive.md; where this file is silent, that one speaks.

**Done when** is `## Done when` in
docs/plans/2026-10-19-brain-overlay-design.md. Read that section first,
then `gv feature ls --json`. The cars are grove-511 → 512 → 513 → 514 →
515 → 568, a linear chain: one car in flight at a time.

**Watch** with one `gv watch --json` Monitor over this workspace. Never
derive state from a pane. Running summary here, one line per action:
`HH:MM grove-N <event> → <action>`.

## Allowed, without asking (act, then log)

1. `gv grab grove-N --repo grove --feature brain-overlay` for the next car
   once its predecessor's PR has MERGED into the train. Start with
   grove-511 now.
2. `gv answer` when the answer is in the ticket, Done when, or
   §Pre-answered. Anything else is a push.
3. `gv nudge` on `pr_ci_failed` (name the check), `pr_conflicting`
   ("rebase on feature/brain-overlay"), and a failed review (paste the
   findings). Two steers per car; the third is a push.
4. Review every car before merging: `/code-review <PR#>` or a subagent
   given the PR, the car's `Acceptance:` lines from the design, and the
   Done when block. Pass = CI green, every acceptance line checked, no
   finding above minor. Journal the verdict and the three strongest
   findings.
5. Merge a passed car: `gh pr merge <PR#> --squash` — abort if the PR's
   base is not `feature/brain-overlay`. Then `gv feature land brain-overlay
   --yes` and `gh issue close N --comment "landed in feature/brain-overlay
   via PR #M"` for each landed ticket. The `land brain-overlay` order is
   given here.
6. File a bug or gap as a new issue labelled `brain-overlay` in the
   ticket-writing skill's shape with a `depends on #N` line, and grab it
   as a car. Cap 3 for the drive; the fourth is a push with the draft.
7. Checkpoint nudge, then `gv pause`, on rot signals. Never `gv adopt`.
8. One draft PR `feature/brain-overlay` → `main`; its body is the report,
   rewritten after every merged car. Never ready-for-review, never merged.
9. Append to docs/plans/2026-10-24-brain-overlay-journal.md. Nothing else
   in the repo is yours to write.

## Never, however this brief reads

Merge into `main`. Close or comment on an issue outside step 5. `gv done`,
`untrack`, `adopt`, `sweep`, `handoff`, `gv account use`, any
`--model`/`--profile`/`--account` not named below. Touch `keys`,
`cross-host`, `boot-stats`, or `worker-state`. **Run `gv brains
--refresh`, `--migrate`, or `--migrate --apply` against any real
workspace or the global brain dir** — this train ships brain-writing
verbs, and every check of them happens in a scratch workspace from a
throwaway build (`go build -o /tmp/gv-brain-overlay ./cmd/gv`, scratch
`HOME` + `GROVE_STATE_DIR`). Edit code, tests, or any file other than the
journal and the draft PR body.

## Caps — any one trips → push, stop acting, keep watching

- wall clock: 2026-10-26 09:00 local
- self-filed cars: 3
- steers per car: 2
- spend: `gv cost --json` sum over the six cars (plus self-filed) above
  80 USD
- a car `pr_closed` without merge, or `worker_vanished` twice on one car

## Pre-answered (do not wake me for these)

- Q1 `gv update` runs `gv brains --refresh` automatically and prints the
  report line; the command is not merely printed. Car 01 is therefore
  rote-shaped, but run it at the repo default model and effort anyway.
- Q2 thegrid and waterhouse: the migration's printed git commands track
  the overlay and untrack the seed. The commands are printed, never run.
- Q3 overlays in untracked workspaces are per host; migration reconciles
  drift once, no sync mechanism in this train.
- Q4 groveremote's grove-repo and tableplace stay pristine; the drafts
  for them are empty stubs.
- The PR base is always `feature/brain-overlay`, never `main`.
- Repo default model and effort for every car; no routing.
- A red e2e row that is also red on `main` today (the macOS /private rows:
  hooks tests, e2e plugin and supervise) is not a failure; the worker
  notes it in the PR.
- A seed move on `main` while the train is open: the worker on car 05
  rebases its text and re-runs the archive generator; it does not merge
  `main` into the train on its own — that is a push.

## Pushes (ntfy)

```
url=$(awk '/^notify:/{n=1} n && $1=="ntfy:"{print $2; exit}' ~/.config/grove/config.yaml)
[ -n "$url" ] && curl -s -H "Title: gv brain-overlay: <what>" -H "Priority: <high|default|low>" -d "<body>" "$url"
```

URL from the global config every time, never echoed. `high` for a
question, a cap, or done; `low` and title-only for a merged car. Respect
`ntfy_body: title-only` if set.

## Journal

docs/plans/2026-10-24-brain-overlay-journal.md, append-only, one dated
line per entry, categories exactly `decision` · `merged` · `filed` ·
`answered` · `steer` · `review` · `push` · `cap`. Form: `- YYYY-MM-DD
HH:MM <category> grove-N — <what and why; link if PR or issue>`. Do not
commit it. A fresh chat on this brief re-reads it and continues from the
last line.

## Report (the draft PR body)

Sections in this order: Goal · Done when (each line ✅ with evidence or ⬜
with what is missing) · Cars (ticket, title, PR, verdict, merged at,
est_usd) · Decisions (`decision` and `answered` lines verbatim) · Found
along the way (`filed` lines with outcomes) · Operator steps after merge
(the design's §Operator step as a checklist; not part of done) · Open
(what you could not settle, each with a recommendation).

## When it ends

Every Done-when line ✅, or a cap tripped, or I say stop. Final report into
the draft PR, one summary push, the same summary as the last message of
the turn: counts per event type, every action, what is still open. Do not
close this chat.
