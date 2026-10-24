# Feature drive: a feature train from intent to a reviewed PR, with one human gate

**Status:** REVISED 2026-10-24 after an independent design review (2
blockers, 9 majors, 10 minors, all folded in; see §Review log). For the
operator's review. Written in the grove-repo orchestrator chat while the
first dry run (brain-overlay, below) was in flight. No code was edited.
**Goal:** an operator states a feature; one scoping conversation ends with
a design the operator signs; one drive chat then takes the train to a draft
PR into base whose body is a human-readable report. The operator's
attention is spent at the scoping session and at the final review, and at
nothing in between unless a cap or a real question fires.
**Shape:** feature train `feature-drive`, cars drafted in §Tickets, not
filed. Why a train and not a merge train: cars 01, 03 and 05 would land on
main green alone (ticket-writing's rule says merge train), but the drive
exists to drive trains, so the shape is chosen for the dogfood, and
ticket-writing gains that clause in car 02. The docs half (brief template,
scoping skill, this design) goes direct to main.
**Design path:** this file (until `--design` ships, the pointer lives here).

## The problem in one paragraph

Grove has the parts: feature trains with label inference and `land`, the
supervision mandate with its `gv watch` Monitor, `--brief-file` chats,
`land <slug>` as a scoped close order, the design-reviewer convention. But
driving a train to completion was still an operator loop: re-authorize
grab/merge/file in prose per feature (the seed's mandate forbids all three
"however worded"), review each car by hand or inside the chat's own
context, merge, keep decisions in a transcript that only `gv chat tail`
can read, and write the final report from memory. Each feature re-derived
the procedure.

## Verified premises

Each line checked against the code or the live fleet on 2026-10-24.

| Premise | How checked | Holds |
|---|---|---|
| The seed forbids grab, merge and issue-filing under any mandate | `orchestrator/CLAUDE.md` §Out of scope — "always, however the mandate is worded" | yes |
| A brief can nevertheless carry those grants and a chat on today's seed acts on them | brain-overlay drive: 4 cars grabbed, reviewed, squash-merged, landed and closed with no operator turn (journal 05:04–09:33) | yes — the seed's "act on the operator's yes" reads the brief as the yes |
| A feature record has no pointer to its design or acceptance | `gv feature ls --json` row (docs/plugins.md) | yes |
| No review independent of the worker exists | `default.tmpl` tells the worker to run its own pr-reviewer agent; no grove verb or event | yes |
| Decisions live only in transcripts | archived transcripts are readable (`gv chat tail`, `orchestrator new --resume`) but not in a grep-able, non-transcript place a fresh chat or a report can compile from | yes, narrowed |
| grove's repo has no PR CI | `.github/workflows/`: release + pages on push to main (journal 05:22) | yes — "CI green" needs a per-repo meaning |
| Event data is flat strings | `internal/state/state.go:114` `Data map[string]string` | yes — no nested event payloads |
| The Stop hook knows three sentinels and emits only `agent_status` | `internal/hooks/sentinel.go:9-13`, `hooks.go` | yes |
| Tasks fold by ticket; a second task on `grove-N` collides with the car | `internal/state/state.go:336` | yes |
| supervise has no per-task deadline | grep `deadline|timeout` over `internal/supervise/`: none | yes |
| Seed length is paid per turn and tested | `orchestrator/seed_test.go:187-191` caps the supervision mandate at 95 lines | yes |
| In-chat review subagents stalled | two subagents, one PR (#589), 70+ min, no report; the chat then ran the same review itself in ~4 min (journal 07:45, 08:40, 08:44) | observed once; cause **not diagnosed** — see Decision 5 |

## Evidence: the first dry run (brain-overlay, 2026-10-24, in flight)

Brief: `docs/briefs/brain-overlay-drive.md`. Journal:
`docs/plans/2026-10-24-brain-overlay-journal.md`. Report: draft PR #588.
Through car 04 (about 4.5 hours of wall clock):

| | |
|---|---|
| cars merged / in flight / queued | 4 / 1 / 1 |
| operator turns in the drive chat | 0 (see the caveat) |
| pushes sent | 4 low (merged car), 0 high |
| answers from the Pre-answered block | 1 (Q1, which the worker had defaulted the other way) |
| steers | 1 (a Done-when line with no test; fixed in one round) |
| review subagents stalled | 2 (both on PR #589) |
| self-filed cars | 0 of 3 |
| est. spend | $28.72 over 4 cars |

What it showed, including what the review added:

- The grants work on today's seed, and the report PR reads without
  opening a car PR.
- **The journal is hand-kept and already diverged from the report**: PR
  #588 carries decision lines (06:36, 08:40) and a steer time (09:25) the
  journal lacks (07:45, 09:10). A report compiled from that journal would
  drop the second stall. The journal needs the binary's timestamp and the
  binary's append (Decision 6).
- **Backend mutations are unattributable.** Duplicate car #581 was closed
  and #568's body edited at 11:29Z under the operator's login, with no
  journal line; every `gh` call from a chat carries the same login. The
  zero-turns count cannot be verified and a Never-list breach would be
  invisible (Decision 6, Decision 8).
- **No car owned the final tip.** The report defers the `claude -p` probe
  and `plugin.sh` to "the final-tip check" that no car runs (Decision 5,
  tip review).
- **The train was 18 behind main and `mergeable: false`** and the drive
  listed it under Open instead of pushing high (Decision 8, caps).
- **A review pass let out-of-ticket work through**: car 04's worker dated
  and rotated TASKS rows outside its Files list (Decision 5, pass
  criteria).
- Two chats filed car 06 independently (#568, #581): a self-filed car
  needs a duplicate check, and closing the duplicate is nobody's under the
  brief (Decision 8).

## The journey

1. **Intent.** The operator states a goal, usually with a claimed cause
   and a preferred approach.
2. **Scoping session.** A fresh orchestrator chat under the
   `feature-scoping` skill. It ends with a design doc carrying: the
   problem with each premise verified; an options table with at least two
   real alternatives, costs, a recommendation and what it rests on; a `##
   Done when` block whose lines are checkable in a scratch workspace; cars
   derived from Done when with `depends on #N`; a `## Decisions` block;
   and an independent review's log. The chat may say the feature is three
   features, or that the code already does it. For a train the Tickets
   section is the plan; the reviewer reviews both.
3. **The gate.** The operator reads and says go. On yes the cars are
   filed, `gv feature new --design` opens the train, and `gv feature
   drive` shows the grants and caps and launches on the operator's second,
   explicit yes (Decision 4).
4. **Drive.** A second chat, fresh context, carrying only the brief. It
   grabs cars in dependency order, gets each PR reviewed by a watched
   worker, squash-merges a passed car into the train, lands and closes,
   files what it finds under caps, journals through the binary, and
   regenerates the report PR after every merge. It wakes the operator for
   a question the design did not settle, a cap, a train that stopped
   being mergeable, or done.
5. **Review.** The operator reads the report, opens a car PR only when a
   line looks off, and merges the train into base themselves.
6. **Retro.** Journal and ledger feed duty 11.

The scoping chat ends with a context full of rejected alternatives; the
drive must not inherit it. The brief is the handoff artifact. A dead drive
chat is relaunched on the same brief plus the journal.

## Options compared

| | A — docs only | B — brief generated, mandate in the brief | C — B + review worker + journal/report verbs | D — a Go driver loop | E — hybrid: chat decides, `gv feature advance` acts |
|---|---|---|---|---|---|
| What | brief template, scoping skill, Done-when convention; the rest by hand | `gv feature new --design`, `gv feature drive` writes the brief from the design (mandate text embedded in the binary) and launches on a yes | B + `gv review` as a grove task emitting `pr_reviewed`, `gv feature journal` (binary-timestamped), `gv feature report` compiled from it | the drive as a deterministic loop: grab on merge, review worker, merge on pass, no chat | chat judges; one verb does check-base → squash-merge → land → journal on the chat's say-so |
| Operator cost per feature | fills slots, pre-answers by hand, pastes the report shape | one command after the gate | one command; report derived | one command | one command |
| Risk | brief copies drift; seed and brief disagree | mandate text in every drive brief (paid in drive chats only) | three verbs, two events, one new task kind | judgment calls (is this a question, is this review a pass, is this bug a car) have no home | moves a PR merge into the binary; "grove reads, agents transition, humans finish" |
| Verdict | **shipped** (this commit) | **the train** | **the train, cars 03–05** | rejected — the drive is judgment with mechanical steps; the loop is the inverse | rejected for the merge; its precondition check (PR base = train, mergeable, review pass present) is what `gv feature ls --json` already exposes and the brief tells the chat to read before `gh pr merge` |

D corrected after review: merging into a feature branch is not a backend
terminal state, so D is not barred by the hard rule; it is rejected because
the judgment has no home, not because it is forbidden.

## The design

### Decision 1: the feature branch is the trust boundary

Everything that lands inside `feature/<slug>` is reversible and never
reaches base, so a drive chat may own it: grab a car, merge a passed car,
land, close that car's issue with a landing comment, file a labelled car
under a cap. Everything that crosses the boundary is the operator's: the
merge into base, merging base into the train, closing any issue outside
land (a duplicate car included), another train, `done`/`untrack`/`adopt`/
`sweep`/`handoff`, routing, accounts, any shipped mutation against a real
workspace. A standing pre-authorization names its action explicitly; the
brief's grants are that list.

### Decision 2: `Done when` and `Decisions` are design sections; the train points at the design

A design gains `## Done when` (checkable lines, scratch-only; the post-
merge operator steps and any real-run measurement in a `### Operator step`
subsection, not part of done) and `## Decisions` (every settled question,
in the one-line form the brief's Pre-answered block uses). `gv feature new
--design <path>` records the path in `feature_created.data.design`
(additive); `gv feature ls --json` exposes `design`. Ticket-writing gains
two rules: a car's acceptance lines cite the Done-when line they
discharge; a goal that will be driven is a train even when its cars would
land green alone.

### Decision 3: the mandate lives in the generated brief, not the seed

The review's cost argument stands: the seed is resident in every
orchestrator turn in every workspace, and a 95-line test guards the one
mandate it has. The feature mandate (grants, never-list, caps, journal and
report rules, end condition) is an embedded template in the binary,
rendered into each drive's brief, so it is paid only in drive chats and
needs no per-repo skill. The seed gains four lines: a drive brief's grants
are standing pre-authorizations for its slug; `drive`, `review`, `report`,
`journal` in the tools block. `docs/briefs/feature-drive.md` stays as the
human-readable copy of the template and shrinks to that role.

### Decision 4: `gv feature drive <slug>` renders, confirms, records, launches

Reads the design's Done when and Decisions and the caps from flags
(`--until`, `--max-filed`, `--max-steers`, `--max-usd`; defaults 36h, 3,
2, 80). Refuses a design missing either section, by name. Writes
`.grove/features/<slug>/brief.md` (workspace-local, like everything else
grove writes; never under a repo's `docs/`). Then **prints the grants and
caps and asks `launch? [y/N]`** (`--yes` for scripts), because the brief
carries the issue-close grant and the operator must type that yes, not
inherit it from the design gate. On yes it appends
`feature_drive_launched {slug, brief, until, max_filed, max_steers,
max_usd, design}` (flat strings) so the authorization is in events.jsonl,
then runs `gv orchestrator new --brief-file`. `--relaunch` re-renders and
re-launches on the same record (not `--resume`, which `orchestrator new`
already owns and which refuses a brief); the brief tells the chat to read
the journal first.

### Decision 5: review is a grove task, with a flat verdict event and an owned timeout

`gv review grove-N` creates a task whose ticket is `grove-N.review` — a
form no provider parses as an issue, folded separately from the car —
with `task_created.data.kind: review`, `data.car: grove-N`, `data.pr`,
`data.deadline` (RFC3339). It runs in its own window on the PR head in a
scratch checkout with the car's acceptance lines, the Done-when lines they
cite, the car's Files list and the repo's `gate:`. Its kickoff ends with
the STATUS contract plus one line the hook parses for `kind: review`
tasks: `VERDICT: PASS|FAIL — <one line>`. The Stop hook then emits
`pr_reviewed {car, pr, verdict, gate_exit, findings_n, top}` (flat
strings; `top` is the single strongest finding, one line) in addition to
`agent_status`, and the DONE evidence gate (`gate.go` `doneEvidence`) is
skipped for review tasks, which have no PR of their own.

**Pass criteria**, in the review kickoff: gate exit 0 bar the repo's known
reds; every cited acceptance and Done-when line checked with a command;
no finding above minor; **no file changed outside the ticket's Files list**
(the dry run let TASKS housekeeping through). A fail carries the findings
for the drive's steer.

**`gate:`** is a per-repo config string with **no default**; `gv review`
refuses a repo without one, naming the key. A repo with real PR CI puts
`gh pr checks` in it; grove's is its CLAUDE.md gate line plus the named
e2e suites.

**Timeout has an owner: supervise.** `gv supervise` already runs the
liveness loop; it gains one check — a task whose `deadline` has passed and
whose window is still live emits `pr_reviewed {verdict: timeout}` once —
and nothing else. The drive, on that event, runs `gv pause --force
grove-N.review` (steering-class, in the grants) and treats it as a steer.
Default `--timeout 30m`.

**Tip review.** After the last car merges, the drive runs `gv review
--tip <slug>`: the same worker on the train tip, given the whole Done when
block instead of one car's lines. The report's Done when section cites
its `pr_reviewed`. This is the "final-tip check" the dry run deferred to
nobody.

**Premise, stated honestly.** That a worker stalls less than a subagent is
unverified: two stalls on one PR, cause undiagnosed. What a worker gives
that a subagent cannot is a window, a Stop hook, a `gv watch` line, a
deadline and `gv pause`. Done when 7's stall count is the test of the
premise, not the premise.

### Decision 6: the journal is appended by the binary; the report is compiled from it

`gv feature journal <slug> <category> [--car grove-N] "<text>"` appends one
line to `.grove/features/<slug>/journal.md` with the binary's timestamp;
categories `decision · merged · filed · answered · steer · review · push ·
cap · note · open · mutation`. The chat never writes the file directly.
**Every chat-side `gh` mutation gets a `mutation` line** (close, comment,
edit, merge, issue create) — that is what makes the Never-list auditable.
The journal stays a markdown file, not an event type: a drive writes
dozens of prose lines and plugins tail events.

`gv feature report <slug> [--json]` compiles: Goal (the design's problem
paragraph); Done when (each line with the `pr_reviewed`/journal evidence
citing it, or ⬜); Cars (`gv feature ls --json` plus `pr_reviewed`);
Decisions and Found along the way (`decision`/`answered`, `filed`/`note`
lines); Operator steps (the design's subsection); Open (`open` lines);
**Unattributed** — labelled issues whose state changed with no `mutation`
or `merged` line, and cars with no `task_created` after
`feature_drive_launched` that the journal never `filed`. The self-filed
count is derived from the same comparison, not from the chat's memory.

### Decision 7: the scoping session is a skill with required mechanics

`.claude/skills/feature-scoping/SKILL.md`: verify every premise with a tool
result; at least two real alternatives with costs, a recommendation, what
it rests on and what would change it; Done when before cars; Decisions
settled or listed with recommendations; `depends on #N` on every car; an
independent read-only reviewer with no conversation context, its log
folded in before the operator is asked; say no. It ships with this design.

### Decision 8: caps and pushes the dry run showed were missing

Added to the mandate template: `gv feature ls --json` showing `mergeable:
false` or `behind_base` above a threshold the brief sets (default 25) is a
high push — merging base into the train is the operator's; a self-filed
car is checked against open issues under the label first, and a title
match is a push with the draft, never an issue; closing a duplicate car is
a push; a `pr_reviewed timeout` counts as a steer.

## Contract changes (all additive)

- `feature_created.data.design`; `features[].design` in `gv feature ls --json`.
- New event `feature_drive_launched` (Decision 4), workspace-scoped like
  `feature_created`.
- New task kind: `task_created.data.kind: review`, `data.car`, `data.pr`,
  `data.deadline`; ticket form `grove-N.review`. `gv ls --json` rows gain
  `kind` (absent on ordinary tasks).
- New event `pr_reviewed` (Decision 5), in `gv watch`'s default types.
- `gv feature report <slug> --json` — new verb, pure read.
- `gv feature journal` — new verb; writes one workspace-local file.
- `gv feature drive <slug>` — new verb; one file under `.grove/features/`,
  one event, one chat.
- `gv review grove-N` / `gv review --tip <slug>` — new verb; creates a task.
- Repo config `gate:` (optional string; required by `gv review`).

## Rules this keeps

- **The binary never mutates a backend's terminal state.** The drive chat
  closes a landed car's issue under a named order, as `land <slug>` does
  today; `gv feature land` itself still closes nothing; no verb merges a
  PR.
- **Propose, then dispose** across the trust boundary, and at the launch:
  `gv feature drive` asks before the brief exists.
- **Never derive completion from pane text**: verdicts and timeouts are
  events on one `gv watch --json` Monitor.
- **events.jsonl additive-only**; the journal is not an event stream;
  every new event is flat strings.
- **Paused rows are bookmarks**: a timed-out review is paused and
  reported, never untracked by the drive.
- **Grove writes under `.grove/` and the state dir only.**

## Done when

Checkable from a throwaway build in scratch workspaces (dummy-data
pattern; chats stub `orchestrator.claude` as `e2e/chat.sh` does).

1. **Train points at its design.** `gv feature new S --repo R --design P`
   records `design` in `feature_created`; `gv feature ls --json` shows it;
   an omitted flag changes no existing field (fixture diff).
2. **Seed and skill rules.** `seed_test` asserts the four new seed lines;
   the ticket-writing skill carries the two rules of Decision 2; `docs/
   briefs/feature-drive.md` matches the embedded mandate template (test
   diffs them).
3. **`gv feature drive` renders, asks, records, launches.** On a design
   with both sections: brief written under `.grove/features/<slug>/` with
   Pre-answered equal to Decisions and the caps from flags; `n` at the
   prompt writes no event and spawns nothing; `y` appends
   `feature_drive_launched` and spawns one chat (stubbed); a design
   missing a section is refused by name; `--relaunch` re-renders without a
   second `feature_created`.
4. **Review is a watched task.** `gv review grove-N` creates `grove-N.
   review` with `kind: review`; the car's own task row is unchanged; a
   stubbed Stop with `VERDICT: FAIL — x` emits `pr_reviewed verdict=fail
   top=x`; `gv watch --until pr_reviewed` exits on it; `gv review` without
   `gate:` refuses naming the key; a past `deadline` makes one supervise
   tick emit `pr_reviewed verdict=timeout` exactly once.
5. **Journal and report.** `gv feature journal` appends a binary-stamped
   line; the chat-written form is rejected (no direct file writes in the
   verb's tests); `gv feature report` over a fixture renders every section
   of Decision 6 including Unattributed; `--json` keys documented in
   docs/plugins.md.
6. Gate green; `e2e/dummy.sh`, `e2e/plugin.sh`, `e2e/chat.sh` green bar
   the known macOS rows; docs/plugins.md carries every field in §Contract
   changes.

### Operator step (after the train lands, not part of done)

- One feature scoped under the `feature-scoping` skill, with its review
  log, before the next drive.
- Second dry run on the shipped verbs, counts in the §Evidence table:
  operator turns, pushes by priority, steers, stalled or timed-out
  reviews, Unattributed rows (target 0), spend.

## Decisions (the drive's Pre-answered block)

- Journal is a markdown file written only by `gv feature journal`.
- Review timeout 30 minutes by default; timeout = fail = steer; the drive
  pauses the review task with `--force`.
- Caps default 36h, 3 self-filed cars, 2 steers per car, 80 USD, 25
  commits behind base.
- `gate:` has no default; `gv review` refuses without it.
- Duplicate car: push, never close.
- Review pass requires no file outside the ticket's Files list.
- The feature-drive train is opened only after brain-overlay has merged
  to main, so car 02's seed lines sit under the overlay section; until
  then, no car is grabbed.

## Tickets (drafted, not filed)

Each lands green on the train on its own; `depends on #N` once filed.

- **01 `gv feature new --design` + `features[].design`.** Files:
  `internal/feature/`, `cmd/gv` (glue), docs/plugins.md. Acceptance: Done
  when 1. rote.
- **02 seed lines + ticket-writing rules + brief template as the embedded
  mandate.** Depends on 01. Files: `orchestrator/CLAUDE.md` (four lines),
  `orchestrator/seed_test.go`, `.claude/skills/ticket-writing/SKILL.md`,
  `internal/feature/mandate.tmpl` + `docs/briefs/feature-drive.md`
  (test-diffed). Acceptance: Done when 2. Grabbed only once `main`
  carries brain-overlay's seed (Decisions).
- **03 `gv feature journal` + `gv feature report`.** Depends on 02. Files:
  `internal/feature/journal.go`, `report.go` (pure over journal + feature
  JSON + events, golden-tested), `cmd/gv`, docs/plugins.md. Acceptance:
  Done when 5 (Unattributed uses `pr_reviewed`, which lands in 04b;
  fixture-driven here).
- **04a `gate:` + review kickoff + `gv review` task creation.** Depends on
  03. Files: `internal/config`, `internal/kickoff/review.tmpl`,
  `internal/state` (kind), `cmd/gv`, docs/plugins.md. Acceptance: Done
  when 4, first two clauses and the `gate:` refusal.
- **04b hook verdict → `pr_reviewed` + watch + done-gate exemption.**
  Depends on 04a. Files: `internal/hooks` (sentinel grammar, gate skip),
  `internal/watch`, docs/plugins.md, e2e/dummy.sh leg. Acceptance: Done
  when 4, the `pr_reviewed` and `gv watch` clauses.
- **04c review deadline in supervise + `gv review --tip`.** Depends on
  04b. Files: `internal/supervise`, `cmd/gv`, docs/plugins.md. Acceptance:
  Done when 4, the timeout clause; a `--tip` review on a fixture train.
- **05 `gv feature drive`.** Depends on 04c. Files:
  `internal/feature/brief.go` (pure: design → brief, table-tested),
  `cmd/gv`, docs/plugins.md. Acceptance: Done when 3.
- **06 docs wrap.** Depends on 05. Files: this design's Status, CLAUDE.md
  §Feature trains, HANDOFF.md, docs/plugins.md sweep. Acceptance: Done
  when 6.

Each car is under the ~60-turn estimate; 04 was split at writing time.

## Open questions for the operator

1. **Does the drive close car issues, or only land them?** The dry run
   closed them under the brief's land order and it read well. Recommended:
   keep it, and let `gv feature drive`'s prompt be where you grant it.
2. **`gv review` at lower effort?** Recommended: `--effort medium` by
   default; the review is command-driven. Open until the ledger says.
3. **Where does the journal end up in git?** It is workspace-local under
   `.grove/features/`. Recommended: `gv feature report` is the durable
   artifact (the PR body); the journal is kept until the train closes and
   archived by `gv feature close` into the state dir.
4. **behind-base threshold** default 25: too loose, too tight?

## Review log

Independent read-only review 2026-10-24 (fresh agent, no conversation
context; read the seed, plugins.md, feature-trains design, briefs,
journal, PR #588, internal/{feature,hooks,watch,state,supervise}, cmd/gv).

- **B1** `pr_reviewed` as nested JSON is impossible (`Data
  map[string]string`), the hook has no verdict grammar, a review task on
  `grove-N` collides in the fold, and the DONE evidence gate would block
  the reviewer. Fixed: flat event, `grove-N.review` identity with `kind`,
  `VERDICT:` line, gate exemption (Decision 5).
- **B2** the review timeout had no owner. Fixed: supervise checks
  `deadline` and emits once; the drive pauses (Decision 5, car 04c).
- **M1** car 02 collided with brain-overlay's in-flight seed rewrite and
  the grab rule cannot see across trains. Fixed: the train opens after
  brain-overlay merges; stated in Decisions.
- **M2** shape contradicted ticket-writing's rule. Fixed: said why in the
  header; ticket-writing gains the clause (car 02).
- **M3** the binary minted the issue-close grant. Fixed: `drive` prints
  grants and asks; `feature_drive_launched` records the yes (Decision 4).
- **M4** grove-repo-shaped paths and `gate:` default. Fixed: `.grove/
  features/<slug>/`, no default, refusal names the key.
- **M5** car 04 oversized at writing time. Fixed: 04a/04b/04c.
- **M6** hand-kept journal diverged from the report. Fixed: `gv feature
  journal`, binary timestamp, report compiled from it (Decision 6).
- **M7** stall premise unverified. Fixed: stated; Done when's operator
  step measures it.
- **M8** seed growth for every workspace. Fixed: mandate in the generated
  brief, four seed lines (Decision 3); option table gained the hybrid.
- **M9** backend mutations unattributable. Fixed: `mutation` journal
  lines, Unattributed report section, derived self-filed count, duplicate
  close is a push (Decisions 6, 8).
- **Minors:** premises reworded (transcripts readable; worker runs its
  own reviewer); floor reference dropped; Done when 1/7 moved to the
  operator step; fixture names the `orchestrator.claude` stub;
  `--relaunch`; second dry run is an operator step; option D corrected
  and E added; final-tip review, mergeable/behind-base push, Files-list
  pass rule added; skill: Shape-and-why header, `depends on`, Tickets
  section stands as the plan; "already in use" → "shipped this commit".
