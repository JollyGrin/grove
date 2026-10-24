# Brain overlay: seed + `CLAUDE.local.md`, so a seed move needs no merge

**Status:** IMPLEMENTED 2026-10-24 on `feature/brain-overlay` (PRs #582 #589
#590 #591 #592 #594 #595: cars #511–#515, #568 and the follow-up #593); the
2026-10-19 design review is in §Review log. Measured round (`e2e/brains.sh`,
`gv brains --refresh` section, 6 scratch brains): 1 pristine brain refreshed
in place with no `.new`, plus the absent global brain seeded; 1 edited brain
left for `gv brains --migrate` (a `.new` to diff, no three-way merge); 0
three-way merges anywhere.
Investigation for issue #508. No brain, seed or code was edited to write it.
**Train:** feature train `brain-overlay` (`feature/brain-overlay`, label
`brain-overlay`), cars #511–#515. The operator chose a feature train over
the merge train drafted below; the train has landed. Inside a train, the
seed freeze in §Seed freeze is unnecessary (see §Tickets).
**Goal:** keep per-workspace customization, and make a seed move a
replace-in-place with zero hand merges on every host.

## The problem in one paragraph

`.grove/orchestrator/CLAUDE.md` is one file with two authors. Grove writes
it from the embedded seed, and the operator edits it in place. A seed move
can only reach an edited brain through a three-way merge
(`gv init --only orchestrator-md` → `CLAUDE.md.new` → `git merge-file`
with the old seed found by its stamp). That merge has now been done by hand
about six times (stamps 9afe65e → … → a6a0462; memory
`orchestrator-seed-brain-gap`), each time across 15–20 brains on two hosts.
The 2026-10-19 round had 20 brains and 6 conflicts. The fix is to give each
author a file of their own.

## Inventory: all 20 brains against their base seed

Method: each brain's `CLAUDE.md.bak-seed-4cbfa51affd9` was diffed against
`git show 699e937:orchestrator/CLAUDE.md` (stamp 4cbfa51affd9). deancafe-
admin's backup is `bak-seed-183eb05a1451`, which is base `ba47c51`. Each live
brain was diffed against `origin/main:orchestrator/CLAUDE.md` (d1cb8d3,
stamp a6a0462035c0). Stamp lines were removed first. groveremote brains were
copied over `ssh dean@groveremote`. Every live brain carries stamp
a6a0462035c0. Line counts are `+added / −removed` against the seed. The
reviewer re-ran grove's own pristine check (body hash == stamp) on the
copies and got the same 11 / 9 split.

| # | Brain (host) | Live vs main | Bak vs base | Classes |
|---|---|---|---|---|
| 1 | bruce-explroe (mac) | 0 / 0 | 0 / 0 | pristine |
| 2 | deancafe-admin (mac) | 0 / 0 | 0 / 0 | pristine |
| 3 | deanlol (mac) | 0 / 0 | 0 / 0 | pristine |
| 4 | monacum-v2 (mac) | 0 / 0 | 0 / 0 | pristine |
| 5 | runelite (mac) | 0 / 0 | 0 / 0 | pristine |
| 6 | warcraft (mac) | 0 / 0 | 0 / 0 | pristine |
| 7 | xteink (mac) | 0 / 0 | 0 / 0 | pristine |
| 8 | deanlol (remote) | 0 / 0 | 0 / 0 | pristine |
| 9 | runelite (remote) | 0 / 0 | 0 / 0 | pristine |
| 10 | tableplace (remote) | 0 / 0 | 0 / 0 | pristine, H |
| 11 | wc-stylized-maps (remote) | 0 / 0 | 0 / 0 | pristine |
| 12 | grove-repo (remote) | 1 / 1 | 0 / 0 | I (stray), H |
| 13 | tableplace (mac) | 7 / 0 | 7 / 0 | C, H |
| 14 | waterhouse (mac) | 20 / 0 | 20 / 0 | C, B, R |
| 15 | waterhouse (remote) | 20 / 0 | 20 / 0 | C, B, R, H |
| 16 | deanbook (mac) | 42 / 0 | 42 / 0 | A, B, O |
| 17 | grove-repo (mac) | 84 / 73 | 76 / 65 | I, B, A, F, H |
| 18 | thegrid (mac) | 106 / 37 | 104 / 35 | I, B, A, N, O |
| 19 | unbrewed (mac) | 137 / 79 | 128 / 70 | I, B, A, C, O, F |
| 20 | unbrewed (remote) | 124 / 77 | 117 / 70 | I, B, A, C, O, F, H |

The bak column and the live column agree, so these customizations are
stable. They were carried through the merge, not created by it.

**Two more brains outside the registry.** Outside any workspace, the
orchestrator uses `cfg.Orchestrator.Dir` (`cmd/gv/main.go`,
`orchestratorDirFor`), which is `~/.config/grove/orchestrator/` on both
hosts. The Mac copy has **no stamp line** (unstamped, so hand-managed by
today's rules), but its body equals the seed at `cbe990c`. The groveremote
copy is stamped `16a561b74957` and differs by one line. Neither is ever
swept by `gv brains`, so both are many seeds behind. Car 01 adds the
global dir to the sweep.

**Classes.** I–O, A and B are #508's four categories. C, F, H, N and R are
new.

- **I: identifier substitution.** grove-repo (mac): every `DEV-X` →
  `grove-N` (about 35 sites) and "the operator" → "Dean" (about 25).
  unbrewed: `DEV-X` → `<task>` or `unbrewed-pro-server-42`, and operator →
  Dean. thegrid: operator → Dean only, because `DEV-X` is already its real
  id. The substitution is partial everywhere. Every customized brain still
  says "the operator" at seed lines added after it was personalized, and
  remote unbrewed still has `DEV-X` in the accounts paragraph. grove-repo
  (remote) is the pristine seed plus a single `DEV-X` → `grove-N` on one
  line, probably merge residue.
- **B: backend wording.** "(GitHub issues)" instead of "(Linear, GitHub
  issues, task files)", `gh issue` triage instead of "on Linear: team DEV",
  and "mutate Linear" / "Linear comments" (thegrid). Also "dev-linear MCP
  tools apply only in thegrid" (grove-repo), "provider is markdown, ignore
  every Linear instruction" (deanbook), and "GitHub issues provider"
  (waterhouse).
- **O: rule override.** unbrewed: issue comments are free (the operator's
  standing OK, 2026-07-05), and `gh issue list/view` is read-only. deanbook:
  no PRs, a local `git merge --no-ff` on the operator's yes, a per-ticket
  review gate, and Anthropic models only. thegrid: "never open CDP
  unprompted" added to Guardrails, and the close clause reworded to "never
  move any ticket to Done".
- **A: added section or paragraph.** deanbook `## SECRETARY MODE`.
  thegrid: `## Browser (CDP)`, a duty 1 addendum (review handoff package)
  and its own duty 10 "Review feedback loop". unbrewed: "GLM means Flash",
  a merge → issue-close check in duty 6, `## Local pair testing`, and a
  model-flip warning in the tools block. grove-repo: the "merge train / rote
  routing" conventions.
- **N (new): renumbering.** thegrid's duty 10 pushes Land to 11 and
  Learnings to 12. Three cross-references follow it: the tools block's
  "(duty 11)" and both Guardrails "(duty 10)" references. Any seed duty
  added later collides with it.
- **C (new): workspace context / bootstrap pointer.** "Read `LEARNINGS.md`
  next to this file at session start" (unbrewed, tableplace, identical
  wording). Waterhouse's workspace map. unbrewed's warning about the
  global-path incident.
- **F (new): fossil.** Text the seed has since reworded, which the merge
  kept because it was "ours". grove-repo and unbrewed still name
  `dev-core:cleanup-local-state`, which the seed generalized in 9de3331
  (grove-189); that skill exists only in thegrid. grove-repo's rote routing
  pins `--model claude-sonnet-5`, which the seed's "effort first, lane
  second" (fe117c5) now contradicts. A merge cannot see this class at all.
- **H (new): host drift.** The same workspace differs by host:
  - unbrewed's "GLM means Flash" exists only on the Mac;
  - grove-repo is customized on the Mac and pristine on the remote;
  - tableplace has its LEARNINGS pointer on the Mac only.

  The cause is that 18 of 20 brains are not in git. 11 Mac and 3 remote
  brains are ignored by `.grove/.gitignore` (`orchestrator/`), and 4
  remote workspaces have no git at all. Only thegrid and waterhouse (mac)
  track their brain, force-added past that ignore rule.
- **R (new): redundant or host-wrong.** waterhouse says "Read
  `/Users/grins/git/waterhouse/CLAUDE.md` first". On the Mac that file is an
  ancestor, so Claude Code already loads it. On groveremote the path is
  wrong and the file doesn't exist.

**Takeaways.** 11 of 20 brains (55%) are byte-for-byte the seed, and so is a
12th apart from one stray line. Each of those got a `.new` file, a merge and
a backup today for nothing. The other 8 brains are 4 workspaces' worth of
real customization (tableplace's is one paragraph) plus host copies.
Classes I, B and N cause most of the conflicts, and they exist only because
the customization is edited inline.

## What Claude Code supports (verified 2026-10-19, v2.1.303)

Docs: https://code.claude.com/docs/en/memory (§Import additional files,
§scope table, §troubleshoot), /context-window, /hooks#instructionsloaded.
"Live" means a probe in a scratch workspace laid out like a real one
(`ws/` git repo → `.grove/orchestrator/`, gitignored, plus a
`zai-plan-glm-flash/` profile subdir). The loaded set was read from the
transcript's `instructions` attachment, which lists every file in context
order with its type.

1. **Load order is concatenation, root → cwd.** Docs: "All discovered
   files are concatenated into context rather than overriding each other",
   "instructions closer to where you launched Claude are read last", and
   "Within each directory, `CLAUDE.local.md` is appended after `CLAUDE.md`."
   Live, from `.grove/orchestrator/`: `ws/CLAUDE.md` → `ws/.claude/rules/*`
   → `orchestrator/CLAUDE.md` → its `@`-imports (depth-first) →
   `orchestrator/.claude/rules/*` → `orchestrator/CLAUDE.local.md` (type
   `Local`, always last).
2. **Precedence is not a mechanism.** Docs: "if two instructions
   contradict each other, Claude may pick one arbitrarily." No file
   overrides another. Position (last) and explicit wording are all there
   is.
3. **`@path` imports.** These resolve relative to the importing file
   (docs, and live: `sub/hop.md` → `@nested.md` loaded `sub/nested.md`).
   They nest at most four hops deep and are skipped inside code spans and
   fences (docs; live, a backticked `@never.md` did not load).
4. **External imports are gated, and `-p` drops them silently.** Docs:
   "An import … is external when its path resolves outside your working
   directory … the first time … it shows an approval dialog. If you
   decline, the imports stay disabled and the dialog doesn't appear again."
   Live under `-p`: an absolute path outside the project and an `@~/…` path
   were both silently not loaded. Approval is stored per project in
   `~/.claude.json` (`hasClaudeMdExternalIncludesApproved`, false for
   `/Users/grins/git/grove`).
5. **Imports in an ancestor CLAUDE.md are external from a subdirectory.**
   This was the decisive live finding. Launched from
   `.grove/orchestrator/zai-plan-glm-flash/`, the ancestor
   `orchestrator/CLAUDE.md` loaded, but **its `@grove-seed.md` import did
   not**. A `grove-seed.md` placed in the cwd did not load either. The
   workspace-root `CLAUDE.md`'s `@root-import.md` was dropped from both
   cwds. `CLAUDE.local.md` and `.claude/rules/*` of the ancestor **did**
   load from the subdir. Grove launches profile chats from exactly such
   subdirs (`cmd/gv/main.go` orchestrator pane cwd, `cmd/gv/chat.go`
   profile dir).
6. **`CLAUDE.local.md` is current, and it loads with or without git.**
   Docs scope table: "Local instructions | `./CLAUDE.local.md` | Personal
   project-specific preferences; add to `.gitignore`". It loads from the
   cwd and every ancestor. Live, it loaded from a gitignored dir. It also
   loaded from a profile subdir of a workspace with **no git at all**, the
   shape of 4 remote workspaces, including the customized unbrewed and
   waterhouse.
7. **Compaction re-injects the whole set, overlay included.** Docs: only
   "Project-root CLAUDE.md survives compaction" is promised. Live, an
   **interactive** session ran on an isolated tmux socket, in a non-git
   orchestrator dir: a turn, an in-session `/compact`, then another turn.
   The transcript has a fresh `instructions` attachment after the
   `compact_boundary`, listing `CLAUDE.md` and `CLAUDE.local.md`, and the
   next API request carried both codewords. A `-p --resume` probe agreed,
   with imports included.
8. **HTML comments are stripped** before injection (docs). So the
   `<!-- grove-seed: … -->` stamp, and any `<!-- grove:extend -->` anchor,
   costs no context and is invisible to the model.
9. **Observability:** the `InstructionsLoaded` hook fires per file with
   `memory_type` and `load_reason` (`session_start`, `include`, `compact`,
   …). It is observe-only. Nothing here needs it.

**Behavioural check (live).** The real seed was placed as `CLAUDE.md`, with
an unbrewed-style `CLAUDE.local.md` beside it holding one line:
"**Override — Guardrails › Never post ticket comments:** … allowed freely
… Closing an issue stays forbidden." Asked "may I comment without asking /
may I close?", Haiku 5.5 and Sonnet 5.5 answered as follows:

- Control (no overlay): NO / NO, 4 of 4 runs.
- Overlay present: YES / NO, 4 of 4.
- Overlay present, with the precedence paragraph removed from the seed:
  YES / NO, 6 of 6.

The override won, and the rule it did not touch held. **GLM, Kimi and Muse
lanes were not tested.** They are the likeliest to ignore an override, so
the operator step after migration includes one run on
`zai-plan-glm-flash`.

## Options compared

| | A. Seed + `@import` overlay | B. Seed + `CLAUDE.local.md` | B′. Seed + `.claude/rules/workspace.md` | C. Templated seed | D. Named extension points |
|---|---|---|---|---|---|
| Update = replace, no merge | yes | **yes** | yes | only if nobody edits the render | no, still one shared file |
| Works from profile subdirs | **no** (fact 5) | **yes** | yes | yes | yes |
| No approval dialog | only if `CLAUDE.md` is the overlay and imports the seed from the cwd | **yes**, no imports | yes | yes | yes |
| Overlay is read last | no, an import follows its importer | **yes** (`Local`, always last) | before `CLAUDE.local.md` | n/a | n/a |
| Removes class I/B diffs | via a glossary line | via a glossary line | via a glossary line | yes, literally | no |
| Removes class N | yes, overlay duties are named | yes | yes | no | yes |
| New machinery | import line, two grove files | none | a dot-dir inside a dot-dir | renderer, config keys, render-aware stamps | anchor grammar, splice engine |

- **A. `@import`.** The natural reading of #508. There are two
  directions. If the overlay is `CLAUDE.md` and it imports the seed, then
  fact 5 means a profile chat in `zai-plan-glm-flash/` silently loses the
  **entire seed** and runs on the overlay alone. That is the worst failure
  available, and it is silent. If the seed is `CLAUDE.md` and it imports
  the overlay, the same drop loses the overlay instead, so the
  workspace's overrides vanish in exactly the lanes that run cheaper
  models. Rejected.
- **B. `CLAUDE.local.md`.** This uses the one layering Claude Code does
  natively: the same directory, always last, loaded from every subdir, with
  no import, no dialog and no git needed. Grove owns `CLAUDE.md` outright
  and may replace it. The operator owns `CLAUDE.local.md`. **Recommended.**
- **B′. `.claude/rules/workspace.md`.** This also loads from every subdir
  (fact 1), and it is the "second CLAUDE.md" shape #508 asks about. It
  loses to B on two counts. It is read before `CLAUDE.local.md`, so if both
  ever exist the overlay is not last. And `.grove/orchestrator/.claude/`
  is easy to miss: a dot-dir inside a dot-dir, which `ls` and the
  operator's eye both skip. A parent `.grove/CLAUDE.md` would apply to
  every orchestrator dir under it, but it also sits above things that
  aren't the orchestrator, so it is the wrong scope.
- **C. Templated seed** (`{{operator}}`, `{{ticket}}`, `{{backend}}`). This
  would delete the class I/B noise from the diff, but it is the wrong
  tool here:
  - The rendered brain is no longer the embedded seed, so the pristine
    check has to re-render with whatever config held at write time.
  - An operator-name edit in config would churn every brain.
  - Config has no operator-name key today.
  - The substitutions are semantically trivial: a two-line glossary in
    the overlay does the same job, and the behavioural check shows models
    apply it.

  The useful core of C survives as seed hygiene (car 05): neutral
  placeholders instead of thegrid-isms.
- **D. Named extension points** (`<!-- grove:extend duties -->`). Anchors
  only matter when two authors still splice into one file, which B
  removes. The model never sees them anyway (fact 8). Renumbering (class N)
  is fixed by convention instead. Overlay duties are named, not numbered.
  An addendum to a seed duty cites it by number and name ("Addendum to
  duty 1 (Fleet summary)"), and the seed's duty names become a contract
  that `seed_test` guards.

**Recommendation: B, with C's placeholder hygiene and D's naming rule.**

## The design

### Files

```
.grove/orchestrator/            (and the global cfg.Orchestrator.Dir)
  CLAUDE.md          grove-owned. The stamped seed, byte-identical to what
                     the binary embeds. Replaced in place by `gv brains --refresh`.
  CLAUDE.local.md    operator-owned overlay. Optional. Grove writes it only
                     as the empty first-seed stub, or by a confirmed
                     `gv brains --migrate --apply`.
  LEARNINGS.md       unchanged (the workspace's own incident log)
  <profile>/         profile chat cwds. They inherit both files as ancestors.
```

A workspace with no overlay behaves exactly as today, so the 11 pristine
brains need nothing. A `CLAUDE.local.md` that holds only HTML comments
(the stub) counts as **absent** everywhere: in the report, in migration,
and in `--apply`'s refusal check.

### How an override is expressed, and how the orchestrator knows it wins

Two layers, because the docs promise no precedence (fact 2):

1. **The seed says so, and names a floor.** Car 05 adds a short section
   after the intro:

   > ## This seed and the workspace overlay
   > This file is grove's seed. It is replaced wholesale on every grove
   > update, so never edit it. `CLAUDE.local.md` beside it, if present, is
   > this workspace's overlay, written by the operator. Where the overlay
   > contradicts this file, the overlay wins: it is the operator's
   > deliberate rule for this workspace. A seed rule the overlay does not
   > name still applies in full. **One floor no overlay lowers:** an
   > irreversible or outward-facing action still needs the operator's
   > explicit yes in this chat, or a mandate or land order. Those actions
   > are merging a PR or a branch, closing an issue, `done`, `untrack`,
   > `adopt`, `sweep`, `handoff`, and any `grab` the operator did not ask
   > for. An overlay may add a standing pre-authorization for one of them
   > only by naming it explicitly as one.

   The floor is the repo's "propose, then dispose" hard rule. It is
   enforced only by brain text, so it has to be stated in the brain. Every
   live overlay fits under it. unbrewed's free comments are not in the
   floor. deanbook's local merge still waits for a yes, and its
   `review: auto` shortens that yes; it doesn't remove it. thegrid's CDP
   rule only adds a restriction.

2. **The overlay names what it overrides.** The convention, taught in that
   seed section and in `docs/orchestrator-overlay.md`:

   ```markdown
   # Workspace overlay: unbrewed

   ## Glossary
   - The operator is Dean. Task ids look like `unbrewed-pro-server-17`
     (repo + issue #); wherever the seed writes `<task>`, use that form.
   - Backend: GitHub issues on unbrewed-p2p and unbrewed-pro-server. No Linear.

   ## Overrides
   - **Override — Guardrails › Never post ticket comments:** posting
     investigation findings as comments on unbrewed issues is allowed
     without asking (Dean's standing OK, 2026-07-05). Closing an issue
     stays forbidden.

   ## Addenda
   - **Addendum to duty 6 (Cleanup & recovery):** merge → issue-close check …

   ## Workspace duties
   - **Workspace duty: Local pair testing** …
   ```

   An override quotes the seed rule by section and bullet title, states
   the replacement, and restates what stays forbidden. That last clause is
   what kept "close: NO" in the probe.

### What `gv update` and `gv brains` do afterwards

A brain is **pristine** when `SeedStamp(body) == BrainStamp(content)`. The
stamp is a hash of the body, so a stamped file proves by itself whether
anyone edited it. No new state file is needed.

- **`gv brains [--json]` stays a pure read.** That is the promise in its
  own doc comment and in `docs/plugins.md`. It gains the additive fields
  `pristine` (bool), `overlay` (bool) and `overlay_lines` (int). The
  existing `state` values keep their meaning; "edited" is
  `pristine: false`, never a new state value.
- **`gv brains --refresh` is the writing verb.** It walks the same rows
  (registry plus the global dir) and acts per row as the table says. Its
  `--json` fills `action`.
- **`gv update`** calls `gv brains --refresh` where it calls `gv brains`
  today (`reportBrainSweepAfterUpdate`), subject to open question 1.

| Brain on disk | Today (`gv init --only orchestrator-md`) | `gv brains --refresh` |
|---|---|---|
| absent | seed it | seed it, plus the empty overlay stub |
| stamped, current | nothing | nothing |
| stamped, stale, **pristine** | `.new` + hand merge | **replace in place.** No `.new`, no backup (the old bytes are reproducible from git by stamp). `action: refreshed` |
| stamped, stale, **edited**, no overlay | `.new` | `.new`, **unchanged from today**, plus "move your edits to CLAUDE.local.md: `gv brains --migrate`". `action: new` |
| stamped, stale, edited, overlay present | n/a | `.new` plus "CLAUDE.md was edited after migration; move the edit to the overlay". `action: new` |
| unstamped | report | report (unchanged). The Mac global brain is this case |

Keeping `.new` for edited brains means no state is a dead end. Even if the
seed moves before a workspace migrates, today's merge path still works for
it. A live orchestrator chat keeps its loaded copy until it restarts or
compacts (fact 7), the same as today.

**Leftovers are reported, never removed.** `--refresh` and `--migrate`
list each workspace's `CLAUDE.md.new`, `.bak-seed-*` and `.bak-pre-overlay`
files (grove's own dir holds 8 `.bak-seed-*` files, and so does thegrid's)
as "safe to delete once you're happy". Removing them is the operator's
call.

### Migration: `gv brains --migrate [--label L] [--apply]`

Dry run by default. It covers the registry plus the global dir. Per brain:

1. **Find the base.** It is the seed whose stamp the brain carries. The
   binary embeds an **archive of every historical seed**, keyed by stamp:
   a `go generate` step walks `git log -- orchestrator/CLAUDE.md` and
   computes each stamp with `SeedStamp`. That is 27 versions today,
   compressed into one embedded file. There is no runtime git dependency,
   so migration works on groveremote, in workspaces with no git, and for
   OSS users. A stamp that isn't in the archive gets "unknown base; run
   `gv init --only orchestrator-md` and merge by hand", which is today's
   path, still open.
2. **Pristine** (or stub-only overlay with a pristine brain): nothing to
   migrate. Report it, and let `--refresh` replace it.
3. **Edited:** diff against the base and sort each hunk:
   - **pure addition** (only `+` lines): copied verbatim into the overlay,
     under `## Addenda` or `## Workspace duties`, tagged with the seed
     heading it sat under;
   - **substitution-only** (each `−`/`+` pair differs only by a consistent
     token map, such as `DEV-X`→`grove-N` or `the operator`→`Dean`): the
     hunks are dropped, and the inferred map becomes `## Glossary` lines;
   - **anything else** (rewrites, fossils, renumbered refs): emitted under
     `## Review before applying`, with the seed text and the brain text
     side by side, for the operator to turn into an `Override —` line or
     delete.

   The draft goes to `CLAUDE.local.md.draft`, with a summary of N added,
   M substituted and K to review.
4. **Tracked brains.** If `git ls-files` shows `CLAUDE.md` tracked (thegrid
   and waterhouse on the Mac), the dry run prints the git commands
   `--apply` would leave pending, and runs none of them:
   `git add -f .grove/orchestrator/CLAUDE.local.md` and
   `git rm --cached .grove/orchestrator/CLAUDE.md`. The overlay stays
   versioned. The seed stops being versioned, so a grove release no longer
   dirties that repo (see open question 2).
5. **`--apply --label L`** (one workspace at a time, confirmed) does
   three things. It refuses while the draft still has a `## Review before
   applying` block, or while a non-stub `CLAUDE.local.md` exists. It moves
   `CLAUDE.md` to `CLAUDE.md.bak-pre-overlay`. Then it writes the stamped
   **current** seed as `CLAUDE.md` and renames the draft to
   `CLAUDE.local.md`. Nothing is deleted, and no operator text is
   overwritten.

**Expected on today's fleet:**

| Workspace | Hosts | Expected overlay |
|---|---|---|
| 11 pristine + 2 global | — | none |
| grove-repo | remote | none (the stray line is a substitution, so it gets a glossary line or is dropped) |
| tableplace | mac (remote pristine) | about 7 lines: the LEARNINGS pointer (folds into the seed in car 05, then none) |
| waterhouse | mac, remote | about 20 lines: the workspace map, minus the Mac-only path (class R) |
| deanbook | mac | about 45 lines: SECRETARY MODE with its rules restated as `Override —` lines |
| grove-repo | mac | about 15 lines: glossary, backend, conventions (the rote pin and the cleanup-skill fossil go to Review) |
| thegrid | mac | about 90 lines: CDP section, duty 1 addendum, duty 10 renamed to "Workspace duty: Review feedback loop" (the 10/11/12 renumbering disappears), plus a glossary line "the Linear tools = the dev-linear MCP tools" so car 05's neutral wording loses nothing |
| unbrewed | mac, remote | about 85 lines: glossary, backend, comment override, GLM, pair testing and close-check addenda; fossils go to Review |

### Seed freeze and the operator step

**Freeze:** no change to `orchestrator/CLAUDE.md` merges from car 01's
merge until both hosts have migrated. A seed move during that window would
fall back to today's hand merge for every edited brain. Nothing breaks, but
the round would be wasted.

Cars 01–04 add operator-facing flags (`--refresh`, `--migrate`), which the
shipping-gates "verb surface → seed" rule says must be taught in the seed.
The freeze defers that teaching to car 05, which lands right after
migration. This exemption is deliberate, and each car's PR says so.

**The operator step, on each host:**

1. `gv update --yes` with car 04 shipped.
2. Run `gv brains --migrate` and read the 5 drafts. Resolve the Review
   blocks; an orchestrator chat can draft them.
3. Reconcile host drift: unbrewed takes the union (the Mac has GLM),
   waterhouse drops the host-wrong path, and the operator decides whether
   grove-repo and tableplace on groveremote carry the Mac overlay.
4. Run `gv brains --migrate --apply --label X` per workspace, and run the
   printed git commands in thegrid and waterhouse.
5. Smoke test: one orchestrator chat on `zai-plan-glm-flash` in unbrewed
   asks the comment / close question from §Behavioural check.

Car 05 (the seed text) then ships, and its `gv update` is the first
zero-merge refresh.

## Rules this keeps

- **Grove never silently overwrites operator-authored text.** It
  replaces only a file it can prove it wrote unchanged (the pristine stamp
  check), and only from the writing verb. An edited `CLAUDE.md` gets a
  `.new` beside it, never an overwrite. `CLAUDE.local.md` is written only
  by a confirmed `--apply` or as the empty first-seed stub.
- **Additive-only for plugins:** plain `gv brains --json` stays pure read
  and only gains fields, and existing `state` values keep their meaning.
- **Propose, then dispose:** migration is dry-run by default, with
  per-workspace confirmation, and git commands are printed, never run. The
  seed's floor (above) carries the same rule into every overlay.

## Done when

The train is finished when every line below is checkable from
`feature/brain-overlay`'s tip, by a throwaway build (`go build -o
/tmp/gv-brain-overlay ./cmd/gv`) run against SCRATCH workspaces only
(docs/seed-manifest.md §Dummy-data E2E). Nothing here touches a real
brain, host, or config dir; the operator's own migration is §Operator step
below and is not part of done.

1. **All six cars landed.** `gv feature ls --json` shows `brain-overlay`
   with 6/6 cars `landed` (#511–#515 and #568), each PR squash-merged into
   `feature/brain-overlay` with a recorded review verdict of pass.
2. **Gate and e2e green on the tip.** `go build ./... && go vet ./... &&
   go test ./...` green, `gofmt -l .` empty, and `e2e/all.sh` red only on
   rows that are red on `main` the same day (the macOS /private rows are
   known; name any other red).
3. **Pristine brains refresh in place.** In a scratch workspace seeded by
   an older stamp, `gv brains --refresh` replaces `CLAUDE.md` with the
   embedded seed, updates the stamp, and `gv brains --json` reports that
   brain `refreshed`. Plain `gv brains` and `gv brains --json` write
   nothing (a before/after checksum of the dir is identical).
4. **Edited brains are never overwritten.** With one operator line added
   to a scratch brain, `--refresh` leaves `CLAUDE.md` byte-identical,
   writes `CLAUDE.md.new`, and reports the brain as edited. This holds for
   the global brain dir as well as a workspace's.
5. **The overlay exists and the stub counts as absent.** A first seed
   (`gv init`) writes a `CLAUDE.local.md` holding only HTML comments;
   `gv brains --json` reports the overlay absent for that workspace and
   present once one non-comment line is added. Profile subdirs inherit
   both files (the headless `-p` probe from §Behavioural check, run in
   scratch).
6. **Migration is dry-run and refusing by default.** `gv brains
   --migrate` on an edited scratch brain prints a draft `CLAUDE.local.md`
   with `Review` blocks and runs no git command; `--migrate --apply`
   without the per-workspace confirmation changes no file; with it, the
   seed becomes `CLAUDE.md`, the draft becomes `CLAUDE.local.md`, and the
   git commands are printed, never run. A stub-only overlay does not
   trigger `--apply`'s refusal.
7. **The seed archive covers every historical stamp** named in §Inventory
   (9afe65e → a6a0462), and the classifier places each of the 20
   inventoried brains in the class the inventory table gives it (a table
   test, not a live run).
8. **Seed text.** `grep -c 'DEV-' orchestrator/CLAUDE.md` prints 0;
   `seed_test` asserts the override section, the floor sentence, the
   LEARNINGS-at-start line, the `--refresh`/`--migrate` tools lines, and
   the numbered duty names; the two promoted rules from car 05(c) are
   present.
9. **Contracts additive.** The key set of `gv brains --json` on the tip is
   a superset of `main`'s, existing `state` values keep their meaning, and
   `events.jsonl` gains no new record type without a line in
   docs/plugins.md.
10. **Docs landed with the code.** claude-code-facts §Sessions carries
    facts 4–7, root `CLAUDE.md` §Layout has the overlay line, LEARNINGS.md
    has the dated entry, and this design's Status reads IMPLEMENTED with
    the measured round: the number of brains that refresh with zero
    merges on the scratch fixture.
11. **Kickoff overlay (car 06, #568).** With no `prompt_overlay:` every
    embedded kickoff template renders byte-identical to `main`'s (golden
    test); a scratch repo with `prompt_overlay:` renders the fragment at
    the slot once; `gv brains --json` carries the additive `kickoffs`
    rows (`default` / `overlay` / `override`) and `gv doctor` warns only
    on an `override`.
12. **Report.** The draft PR `feature/brain-overlay` → `main` carries the
    report in the shape of docs/briefs/feature-drive.md §Report, with §Open
    questions for the operator resolved or listed as still open, and the
    §Operator step as a post-merge checklist.

### Operator step (after merge, not part of done)

On each host: `gv update --yes`, `gv brains --migrate`, read the drafts,
reconcile host drift, `--migrate --apply --label X` per workspace, run the
printed git commands in thegrid and waterhouse, then the GLM smoke test.
`gv update --yes` afterwards reports `refreshed` for every brain and no
new `.new` appears.

## Tickets (filed 2026-10-19 as feature train `brain-overlay`)

Filed as #511 (01), #512 (02), #513 (03), #514 (04) and #515 (05), each
depending on the one before; #568 (06, kickoff overlay) was added
2026-10-24 from the train review and depends on #515. The operator chose a **feature train**
instead of the merge train argued below. That removes the seed freeze.
All five cars, the seed text in 05 included, reach main in one landing.
The archive (#513) holds every historical stamp, so after the release
each host runs `gv update` (pristine brains refresh) and then
`gv brains --migrate`; no window exists in which an edited brain is
stuck. The cost is #515's landing note: if main's seed moves meanwhile,
rebase 05's text and re-run the archive generator. The original reasoning
follows, kept for the record.

**Merge train, not a feature train.** Each car lands green on main by
itself and leaves every brain working. A brain with no overlay is
today's behaviour, and an edited brain keeps its `.new` path throughout.
So no half-migrated state breaks the gate, and nothing needs a long-lived
branch. Car 01 pays off alone: 11 brains, plus both global brains once
they are re-stamped, stop needing a merge.

The chain is linear. 02 → 01 is a choice for review simplicity, not a
technical need: 02 only adds the overlay to the report and the stub. 03
needs 02's stub-is-absent rule, and 04 needs 03's classifier. Car 05 must
land **after** the operator step, because it is itself a seed move.

### 01 brains: `gv brains --refresh`, replacing pristine brains in place

Body: New writing flag `gv brains --refresh`, with the per-row behaviour
of §What `gv update` and `gv brains` do afterwards: a pristine stale brain
is replaced in place, and an edited one gets `.new` as today. Plain
`gv brains [--json]` stays a pure read and gains the `pristine` field
(bool); `action` is filled only under `--refresh`. The sweep also covers
the global `cfg.Orchestrator.Dir`, as row label `(global)`.
`reportBrainSweepAfterUpdate` calls `--refresh` (open question 1 decides
whether that is automatic; if the operator says "explicit only", `gv
update` prints the command instead). The report also lists `.new` and
`.bak-*` leftovers without deleting them. Seed freeze in force: this PR
does not touch `orchestrator/CLAUDE.md`, and its body says so.
Files: `internal/bootstrap/orchestratormd.go`, `brainsweep.go` (+ tests),
`cmd/gv/brains.go`, `cmd/gv/main.go` (global dir row),
`internal/connections/core.go` (the doctor row's fix text names
`gv brains --refresh`), `e2e/brains.sh` (pure-read assertion kept for
plain `gv brains`, new leg for `--refresh`), `e2e/wizard.sh` (drift legs
around lines 112–145), `e2e/plugin.sh` (new fields),
`docs/remote-host-setup.md` (the update procedure),
`docs/plugins.md` (field list; the pure-read promise unchanged), TASKS.md.
Acceptance:
- Table tests: pristine-stale → replaced and stamped with the new seed;
  edited-stale → `.new` written and the brain byte-identical afterwards;
  current → untouched; unstamped → untouched; global dir row present.
- `gv brains --json` with no flag writes nothing (an e2e asserts mtimes
  are unchanged).
- `e2e/brains.sh`, `e2e/wizard.sh`, `e2e/plugin.sh` and `e2e/dummy.sh` are
  green.
- The gate (`go build ./... && go vet ./... && go test ./...`, `gofmt -l .`
  empty).

Labels: `rote` **once open question 1 is answered** (it is then the only
decision, and it gets written into the body). Until then, no label.
Suggested: `--effort medium`.

### 02 brains: recognize the overlay, write the stub (depends on 01)

Body: `gv brains` reports `overlay` (bool) and `overlay_lines` (int,
non-comment lines) for `CLAUDE.local.md`. A file that holds only block
HTML comments counts as no overlay. On first seed (`SeedBrain`, and the
absent case of `--refresh` and `RefreshBrain`), write an overlay stub when
none exists. The stub holds only HTML-comment headings (Glossary,
Overrides, Addenda, Workspace duties) and a comment with the GitHub URL of
`docs/orchestrator-overlay.md`. Never write the stub over an existing file.
Add `docs/orchestrator-overlay.md` with the convention from §How an
override is expressed, including the floor and the unbrewed example. The
edited-brain note names `gv brains --migrate`.
Files: `internal/bootstrap/orchestratormd.go`, `brainsweep.go` (+ tests),
`cmd/gv/brains.go`, `docs/orchestrator-overlay.md`, `e2e/brains.sh`,
`e2e/plugin.sh`, `e2e/wizard.sh` (the first-seed leg now sees the stub),
TASKS.md.
Acceptance:
- The stub is written on absent-seed and never over an existing
  `CLAUDE.local.md` (test).
- Stripping block HTML comments from the stub leaves only whitespace
  (test).
- A stub-only file reports `overlay: false` (test).
- JSON fields are present. The gate and the e2e suites named above are
  green.

Labels: `rote`.

### 03 brains: seed archive and the migration classifier, dry run only (depends on 02)

Body: A `go generate` step (`orchestrator/seeds/gen.go`) writes every
historical `orchestrator/CLAUDE.md` from git history into one embedded,
compressed archive keyed by `SeedStamp`. A test asserts that each entry's
key equals its computed stamp, and that a6a0462035c0 and 16a561b74957 are
present. A pure classifier in `internal/bootstrap/brainmigrate.go` takes
(brain, base seed) and returns hunks sorted into pure-addition /
substitution-only (with the inferred token map) / review, then renders
the draft overlay. `gv brains --migrate [--label L]` runs it as a dry run
over the registry plus the global dir. It writes `CLAUDE.local.md.draft`,
prints the counts, prints the git commands for a tracked brain, and lists
leftovers. It writes nothing else.
Files: `orchestrator/seeds/` (gen + archive), `orchestrator/embed.go`,
`internal/bootstrap/brainmigrate.go` (+ `_test.go`, `testdata/`),
`cmd/gv/brains.go`, `e2e/brains.sh`, TASKS.md.
Acceptance:
- Fixture tests built from anonymized shapes of today's fleet. A pristine
  brain → "nothing to migrate". A brain with only an appended section →
  that section and no Review block. A brain with a consistent
  `DEV-X`→`grove-N` / operator→Name substitution → Glossary lines only. A
  brain with a rewritten guardrail → a Review block. An unknown stamp →
  the "unknown base" message.
- A tracked-brain fixture prints both git commands.
- `--migrate` without `--apply` changes no file other than `*.draft` (an
  e2e asserts this).
- Gate green.

Not rote: what counts as a consistent substitution is real design
judgment. Default effort.

### 04 brains: `gv brains --migrate --apply` (depends on 03)

Body: `--apply --label L` does the following. It refuses if the draft is
missing, still has a `## Review before applying` block, or a non-stub
`CLAUDE.local.md` exists. Otherwise it confirms, moves `CLAUDE.md` to
`CLAUDE.md.bak-pre-overlay`, writes `StampSeed(current seed)` as
`CLAUDE.md`, and renames the draft to `CLAUDE.local.md`. For a tracked
brain it prints the git commands and runs none of them.
Files: `internal/bootstrap/brainmigrate.go` (+ tests), `cmd/gv/brains.go`,
`e2e/brains.sh` (three scratch workspaces: pristine, additive,
conflicting), TASKS.md.
Acceptance:
- After `--apply`, `CLAUDE.md` == `StampSeed(seed)`, the original is kept
  as `.bak-pre-overlay`, and a non-stub `CLAUDE.local.md` is never
  overwritten (tests).
- The e2e covers: pristine → nothing; additive → applies; conflicting →
  refuses, then applies once the Review block is removed. A following
  `gv brains --refresh` reports the migrated brain pristine.
- Gate green.

Labels: `rote`. The behaviour is fully specified here.

**Operator step between 04 and 05** (not a ticket): §Seed freeze and the
operator step.

### 05 seed: overlay section and floor, neutral placeholders, promotions (depends on 04 and the operator step)

Body: Seed text and tests only, plus docs.
(a) Add the §How an override is expressed seed section, including the
floor.
(b) Neutralize the thegrid-isms the seed still carries: `DEV-X`/`DEV-42`
→ `<task>`, drop "(on Linear: team DEV)", and "the dev-linear MCP tools" →
"the Linear tools".
(c) Promote two rules that overlays keep repeating: "if `LEARNINGS.md`
exists beside this file, read it at session start", and "workspace duties
in the overlay are named, not numbered".
(d) Teach `gv brains --refresh` and `--migrate` in the tools block. This
satisfies the verb-surface rule the freeze deferred.
(e) Make the duty names a contract: `seed_test` asserts the numbered duty
list and its names.
(f) Docs: claude-code-facts §Sessions gets facts 4–7, root `CLAUDE.md`
§Layout gets one line on the overlay, and a LEARNINGS.md entry is added
via `scripts/log-append.py learnings --section "Claude Code behavior"`.
Files: `orchestrator/CLAUDE.md`, `orchestrator/seed_test.go`,
`.claude/skills/claude-code-facts/SKILL.md`, `CLAUDE.md`, LEARNINGS.md,
TASKS.md.
Acceptance:
- `grep -c 'DEV-' orchestrator/CLAUDE.md` prints 0.
- `seed_test` covers the overlay section, the floor sentence, the
  LEARNINGS line, the new flags and the duty names.
- Gate green.

The operator's post-release check is not part of the worker's done-check:
`gv update --yes` on both hosts reports `refreshed` for every migrated
or pristine brain, and no new `.new` appears.

Labels: `rote`. The seed wording is fully specified in this design.

**Optional, not drafted:** a `gv doctor` row that runs a headless `-p`
probe to confirm that a profile subdir loads the overlay. Only worth it if
the Claude Code loader changes.

### 06 kickoff: overlay slot in the embedded templates, whole-file overrides reported (depends on 05) — #568

Body: the kickoff template has the brain's two-authors problem, unreported.
A repo's `prompt:` replaces the embedded template wholesale; thegrid's four
`kickoff*.tmpl` are frozen at Aug 2026 and missed three template moves
(feature-train PR base f564158, goals-not-choreography 276473f, auto-memory
3df30b9), so its workers are still told to PR against `main`.
(a) Every embedded template (`internal/kickoff/*.tmpl`, six) gets one
`{{template "overlay" .}}` line after the auto-memory paragraph; empty
unless an overlay is given.
(b) `Repo.PromptOverlay` (`prompt_overlay:`), a fragment rendered with the
same data; `prompt:` keeps its whole-file meaning and wins when both are set
(stderr note at grab).
(c) `gv brains --json` gains additive `kickoffs` rows per repo: `repo`,
`state` ∈ `default`|`overlay`|`override`, `path`, `note`; plain `gv brains`
prints them only when one is not `default`; `gv doctor` warns on an
`override`, silent at zero.
(d) Docs: plugins.md field, root `CLAUDE.md` §Layout line, TASKS.md.
Not in this car: migrating thegrid's templates (operator step after
landing) or a kickoff `--migrate` (possible car 07).
Files: `internal/kickoff/{kickoff.go,*.tmpl,kickoff_test.go}`,
`internal/config/config.go`, the `gv brains` package, doctor, `cmd/gv`
pass-through, docs/plugins.md, `CLAUDE.md`, TASKS.md.
Acceptance:
- Golden test: six templates byte-identical to `main` with no overlay.
- A `prompt_overlay:` fragment renders at the slot once with
  `{{.Identifier}}` substituted; with `prompt:` also set, the override
  renders and no overlay.
- Scratch workspace with default/overlay/override repos → three
  `kickoffs` rows; pre-existing key sets unchanged.
- `gv doctor` warn row only with an `override`.
- Gate green; `e2e/dummy.sh` green.

Not labelled rote: a new config key and a report shape.

## Open questions for the operator

1. **Should `gv update` run `gv brains --refresh` automatically, or print
   the command?** Recommended: automatic. A pristine brain is grove's own
   unchanged file, and the report line says what happened. This decides
   whether car 01 is rote.
2. **thegrid and waterhouse track their brain in git.** Recommended: track
   the overlay and untrack the seed (the two printed commands). The
   customization stays versioned, and grove releases stop dirtying those
   repos. The alternative keeps both tracked and accepts a seed diff in the
   work repo on every release.
3. **Overlays in the 18 untracked workspaces: per host, or synced?**
   Recommended for now: per host, as brains are today. Some overlay text
   is host-specific (waterhouse's path, Mac-only CDP). Migration
   reconciles today's drift once, and a small overlay makes later drift
   visible by eye.
4. **groveremote's grove-repo and tableplace:** should they carry the Mac
   overlay? Today both are pristine there.

## Review log

Design review 2026-10-19 (an independent agent, read-only, with
spot-checks against the scratch diffs and the stamp check):

- **B1:** an edited brain had no path if the seed moved before migration.
  Fixed: edited brains keep `.new`, the archive holds every historical
  seed, and a seed freeze plus a deferred verb-surface rule.
- **B2:** the refresh made a pure-read verb write, and the edited-brain
  behaviour was stated two ways. Fixed: a separate `--refresh` and one
  table.
- **M1:** thegrid and waterhouse track the brain. Fixed: migration step
  4 and open question 2.
- **M2:** the global brains were missing. Fixed: inventory and the
  sweep.
- **M3:** "the overlay cannot weaken the hard rules" was wrong. Fixed:
  the floor.
- **M4:** rote labels while a decision was still open. Fixed: car 01 is
  conditional.
- **M5:** incomplete file lists. Fixed.
- **M6:** compaction was untested in-session. Fixed by a live
  interactive probe (fact 7).
- **Minors:** tracking arithmetic (18 of 20 untracked), tableplace drift,
  the non-git probe (fact 6), GLM untested (smoke test added), the
  `.claude/rules` option (B′), stub-only treated as absent, the stub uses
  a URL, leftovers reported, car 03 split into 03 + 04, the operator check
  moved out of car 05, the dev-linear glossary line, and the train
  ordering rationale.

## Sources

- https://code.claude.com/docs/en/memory: §Import additional files;
  scope table and load order; §troubleshoot memory issues (compaction);
  HTML comment stripping. Fetched 2026-10-19.
- https://code.claude.com/docs/en/context-window: the compaction
  re-injection table.
- https://code.claude.com/docs/en/hooks#instructionsloaded
- Live probes, Claude Code 2.1.303, 2026-10-19. Load order and import
  resolution came from the transcript `instructions` attachment, in
  scratch workspaces with git and without it. The compaction check ran in
  an interactive session on an isolated tmux socket. The override check
  was 14 `-p` runs over Haiku 5.5 and Sonnet 5.5.
- Brain diffs: 20 live brains plus `.bak-seed-4cbfa51affd9` (deancafe-admin
  `.bak-seed-183eb05a1451`) against `699e937`, `ba47c51` and `d1cb8d3`
  (`origin/main`) of `orchestrator/CLAUDE.md`. The global brains were
  compared against `cbe990c` (Mac) and `96b2811` (remote).
