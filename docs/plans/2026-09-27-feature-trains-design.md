# Feature trains — first-class feature branches, a rail in the cockpit, serve and land

**Status:** DRAFT 2026-09-27. Directions chosen by the operator the same
day (rail panel + feature lens + trellis, serve, land, orchestrator
closes issues on order). Not yet design-reviewed.
**Mockups:** https://claude.ai/artifact/Hguts6XE4s8Jhuq1JLfFeY (private;
artboards B, C, D, E, F are the chosen ones — A was not picked).
**Goal:** make a long-lived feature branch a thing grove knows about, so
that dispatching a ticket against it, seeing the feature's progress,
running it locally and cleaning up after each merge each take one short
command or one key instead of a paragraph of brief text.

## The problem

Three trains have run so far (`chat-ux`, `feature/remote`,
`feature/gv-keys-secrets`), all by convention, because the code has no
notion of a feature:

| Fact today | Where |
|---|---|
| Base is one value per repo (`repos.<name>.base`, default `main`) | config.go:20, 267-268 |
| `gv grab` has no base option; the worktree forks from `origin/<repo.Base>` | main.go:1460-1464, 1598; worktree.go:97-114 |
| The PR target is never set by code; templates say "main" or "the base branch" | kickoff.go:48-52, default.tmpl:16, md_default.tmpl:20-22 |
| Nothing about base or parent is stored on a task | main.go:1711-1720, state.go:141-197 |
| `gv diff` and `SafeToRemove` compare against `repo.Base` | main.go:3062-3066, 3634, 3728 |
| Merge detection is head-branch-based with no base filter | github.go:59-62, 167-178 |
| No per-workspace run/dev command exists; "preview" is the PR's deploy URL | config.go:18-27, github.go:124-141 |
| Nothing closes an issue, and `Closes #N` only fires on the default branch | provider/github.go:20-23; chat-ux-train.md:54 |

So every train ticket carries a verbatim kickoff block in `--brief`
(reset onto the branch, `gh pr create --base …`, never rebase on main),
`gv diff` shows the whole train's delta, the cockpit shows train tickets
as unrelated rows, and a merged ticket leaves an open issue behind.

One fact works in our favour and is kept: because merge detection
ignores the base, `pr_merged`, `gv done`, `gv sweep` and `gv audit`
already treat a PR merged into a feature branch as merged.

## Vocabulary

- **Feature** — a long-lived branch plus a slug (`keys`,
  `feature-trains`). Declared once, open until it merges to its base.
- **Car** — one ticket of the feature. States, reusing the row glyphs:
  `·` queued (not grabbed), `●` working, `◆` question, `✓` reports done /
  PR ready, `⬢` landed.
- **Land** — for each car whose PR merged into the feature branch: `gv
  done` (window, worktree, local and remote branch removed), then the
  issue is closed by the orchestrator on the operator's order.
- **Serve** — run the feature's tip locally from a reviewed script.

## Decision 1: features live in events.jsonl

A feature is runtime state, so it is an event, not config. Two new
workspace-scoped record types:

- `feature_created` — data: `slug`, `repo`, `branch`, `base`, `label`
- `feature_closed` — data: `slug`, `reason` (`merged` | `abandoned`)

The fold gains a `Features` view keyed by slug. Editing config to open a
feature was rejected: `repos.<name>.base` is shared by every concurrent
grab on the host (secure-keys-design.md Decision 12), and a second
writable file is a second source of truth.

CLI:

```
gv feature new <slug> --repo R [--branch B] [--base main]
gv feature ls [--json]
gv feature close <slug> [--reason abandoned]
```

`new` creates `feature/<slug>` at `origin/<base>`, pushes it, and
appends the event. It refuses when the branch exists remotely unless
`--adopt` is passed, which registers an existing branch (the path for
the three live trains). It does not open the feature PR; that is the
cutover ticket's proposal, as today.

## Decision 2: a task carries its feature and its base

`gv grab grove-N --feature <slug>`:

- fetches and forks the worktree from `origin/<feature branch>`
- writes `feature` and `base` into `task_created` data, only when set, so
  existing events stay byte-identical (the `model_profile` precedent,
  main.go:1716-1720)
- `state.Task` gains `Feature` and `Base` (`omitempty`); `task_adopted`
  carries them through

Inference: when `--feature` is absent and the ticket has a label equal to
an open feature's `label`, grab uses that feature and says so on stdout.
An explicit `--feature none` opts out. Labels are already on
`provider.Task` (provider.go:27-35).

Everything that read `repo.Base` for a task reads `task.Base` first:
`gv diff`, `removeTaskArtifacts`, sweep's guard.

## Decision 3: the kickoff names the PR base

Template data gains `Base` and `Feature`. Every template replaces
"against main" / "against the base branch" with the explicit
`gh pr create --base {{.Base}}`, plus, when `Feature` is set, one fixed
paragraph: rebase only on `origin/{{.Base}}`, never on main. This
retires the hand-written train block in `--brief`.

`Closes #N` stays in the PR body. It does nothing on merge into a feature
branch; Decision 8 covers closing.

## Decision 4: feature status is computed on refresh, never per frame

`gv feature ls --json` and the cockpit share one function returning, per
open feature:

| Field | Source |
|---|---|
| `cars[]` with state | landed: `task_done` events with this feature (durable after sweep); active: folded tasks; queued: open backend issues carrying the label and not tracked |
| `landed`, `total` | counts over `cars` |
| `behind_base` | `git rev-list --count <branch>..origin/<base>` on the last fetched refs |
| `mergeable` | `git merge-tree` dry run, local only |
| `pr` | `PRForBranch(feature branch)` — the feature → base PR, if any |
| `est_usd` | ledger rollup over the feature's tickets |
| `serve` | Decision 7 |

Landed cars come from events because swept tasks vanish from the fleet;
a container that only lists tracked tasks would lose its own history.

The queued lookup is a network call. It runs only where PR refresh
already runs (`r`, and the existing refresh cadence) — no new poll,
goroutine or timer (living-grove-design.md:275-300).

Car order on the rail: landed by landing time, then active by created,
then queued by issue number. A body line `depends on #N` renders as
`after N` on a queued car; it is display only in v1.

## Decision 5: cockpit — rail panel, lens, trellis

**FEATURES rail panel (mockup B).** Between the header and AGENTS, only
when the workspace has an open feature; with none, every frame is
byte-identical to today. Per feature: a title line (slug, `landed/total`,
`↓N <base>`, serve state, est), a rail line of car glyphs ending in
`▷ <base>`, and a label line of issue numbers. The selected feature
shows one amber hint line (what needs the operator). Height is budgeted:
at most 3 features expanded, the rest as `+N more`; under 12 spare rows
each feature collapses to its title line with the glyph strip inline.
AGENTS keeps its needs-you-first sort and gains a `TRAIN` column, only
when features exist. `tab` moves focus between the panels.

**Feature lens (mockup C).** `enter` on a feature opens a full-screen
mode: TRAIN (every car, landed ones dimmed, est per car), BRANCH (tip,
gate at tip, behind base, feature PR, issues it will close), SERVE, and
NEXT (ordered, derived actions). `esc` returns. Row keys act on the
selected car as they do in AGENTS.

**Trellis (mockup D).** In the scene, a feature's trees stand together
under a bracket labelled `<slug> landed/total`. Queued cars are seeds
(`.`). Only at fx ≥ calm; fx=off stays byte-identical. No glyph outside
the locked set.

New list-mode keys, all free today: `s` serve, `l` land, `tab` focus.
`m` in the lens opens the feature PR.

Constraints that bind every piece: data is assembled in `assemble()`,
never per frame; every line is `truncPad`-ed; total render ≤ `m.height`;
palettes and tables are package-level.

## Decision 6: land

`gv feature land <slug> [--json] [--yes]` builds the plan — every task
with this feature whose PR state is `MERGED` — shows it, and on confirm
runs `finishTask` per row. Working and queued cars are listed as skipped.
It emits the landed issue numbers; it does not touch the backend.

Cockpit `l` on a feature opens the same plan as a confirm modal
(mockup F, without the issue lines).

## Decision 7: serve

**Contract.** `<workspace>/.grove/run.sh`, committed with the workspace.
gv exports `GROVE_WORKTREE`, `GROVE_BRANCH`, `GROVE_PORT`, `GROVE_FEATURE`
and runs it in a tmux window named `▶ <slug>` in the workspace session.
The script prints one line `GROVE_READY <url-or-path>` when up. A path is
valid output: for a CLI repo such as grove the script is a throwaway
build (`go build -o /tmp/gv-<slug> ./cmd/gv`) and the path is what the
operator is handed.

**Where it runs.** A serve worktree gv creates,
`~/git/.worktrees/<repo>/<slug>-serve`, checked out detached at the
feature tip and moved to the new tip on each start. gv created it, so gv
may remove it on `feature close`.

**Ports.** One per feature, allocated from 4100 upward at first serve
and recorded, so trains serve side by side.

**Trust.** The script runs only if its sha256 matches a recorded
`run_script_trusted` event. A changed script shows the review modal
again. A drafted script is never trusted implicitly.

**Drafting.** `gv serve init` starts a worker with a fixed prompt: read
the README, package manifests and Makefile, write `.grove/run.sh` to the
contract, stop. The operator reviews it in the cockpit modal (mockup E).

**Events.** `feature_served` (port, tip, window), `feature_serve_stopped`,
`run_script_trusted` (sha256). Liveness is the window's existence at
refresh. The lens flags a serve whose tip is behind the branch.

CLI: `gv serve <slug>`, `gv serve stop <slug>`, `gv serve init`.
Serving a single ticket's worktree is a follow-up, not v1.

## Decision 8: the orchestrator closes issues, on the operator's order

The hard rule stands: zero terminal-state mutations in Go. Closing is
the orchestrator's act and needs the operator's words. The brain gains
two phrases:

- **`land <slug>`** — run `gv feature land <slug> --yes`, then for each
  landed ticket `gh issue close N --comment "landed in <branch> via PR
  #M"`, and add `Closes #N` to the feature PR body if one exists.
- **`keep <slug> landed`** — a standing order, scoped to that feature,
  until the feature PR merges or the operator says stop: on each
  `pr_merged` for a task with that feature, do the same.

The guardrail "never close any issue" gains exactly this exception: an
issue may be closed only under a land order, only for a ticket whose PR
is `MERGED` into that feature's branch. Merging the feature into its
base stays the operator's.

## Contract changes (all additive)

- `task_created` / `task_adopted` data: `feature`, `base`
- `gv ls --json` rows: `feature`, `base`
- new events: `feature_created`, `feature_closed`, `feature_served`,
  `feature_serve_stopped`, `run_script_trusted`
- new output: `gv feature ls --json` (payload key `features`)
- `docs/plugins.md`, the plugin-authoring skill and `e2e/plugin.sh`
  updated in the same ticket that adds each field

## Decision 9: built as a feature train, by hand one last time

Branch `feature/feature-trains`, cut at the main tip that carries this
doc. The train cannot use `--feature` until it has built it, so every
grab carries the verbatim kickoff block (the gv-keys tickets show the
shape). Label: `feature-trains`.

| # | Ticket | After | Model |
|---|---|---|---|
| 01 | feature events + fold + `gv feature new/ls/close` (+ `--adopt`) | — | opus |
| 02 | `gv grab --feature`, label inference, per-task base in diff/remove/sweep | 01 | opus |
| 03 | kickoff templates name the PR base | 02 | sonnet |
| 04 | feature status function + `gv feature ls --json` fields + plugin contract | 02 | opus |
| 05 | `gv feature land` | 04 | sonnet |
| 06 | cockpit FEATURES rail panel + TRAIN column + `tab` | 04 | opus |
| 07 | feature lens | 06 | opus |
| 08 | trellis in the scene | 06 | opus |
| 09 | serve: run.sh contract, trust, ports, `gv serve` CLI | 01 | opus |
| 10 | serve in the cockpit: `s`, review modal, `gv serve init` | 06, 09 | opus |
| 11 | docs + brains: orchestrator seed (`land`, guardrail exception), ticket-writing skill, CLAUDE.md | 05 | sonnet |
| 12 | cutover: `e2e/all.sh` green on the branch, propose the merge to main | all | opus |

03 and 09 can run beside 04. 07 and 08 can run side by side (lens is
`tui.go`/`view.go`, trellis is `scene.go`). Every ticket's acceptance
criteria include the gate and, for anything touching the lifecycle or
the TUI, `e2e/all.sh`.

## Risks

- **Two trains at once.** `feature/gv-keys-secrets` is live and also
  edits `cmd/gv/main.go` and the docs. Whichever lands on main second
  eats a rebase; plan one for this branch after keys merges.
- **TASKS.md conflicts** between parallel cars: resolve by union
  (chat-ux-train.md).
- **Panel height.** The rail costs rows in a 59-row pane that already
  holds AGENTS, ACTIVITY and the scene. The collapse rule in Decision 5
  is the mitigation; ACTIVITY shrinks first.
- **Serve runs repo code on a keypress.** The sha256 trust gate is the
  only barrier; a worker that edits `run.sh` forces a re-review, which
  is the intent.
- **Label inference surprises.** A ticket labelled for a feature is
  grabbed against it without a flag. Grab prints the base it chose, and
  `--feature none` overrides.
- **`gh pr list --limit 1`** returns only the latest PR for a head
  branch (github.go:59-62); a re-opened ticket branch could report the
  older PR's state. Unchanged by this design, noted because land trusts
  it.

## Alternatives considered and rejected

- **Grouped rows inside AGENTS** (mockup A) — cheapest, but breaks the
  needs-you-first sort that makes the list useful.
- **`features:` in config** — see Decision 1.
- **The binary closes issues** — makes `l` self-contained, at the price
  of the rule that keeps grove safe on every provider.
- **Per-repo `base` flip for the duration of a train** — redirects every
  concurrent grab on the host.
- **Stacked PRs** — ruled out by the ticket-writing skill.
