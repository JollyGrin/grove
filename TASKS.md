# Grove — status board

> **TASKS.md** is the status board · [LEARNINGS.md](LEARNINGS.md) is the
> surprises · [docs/roadmap.md](docs/roadmap.md) is the open phases.
> Fresh pickup? Read [HANDOFF.md](HANDOFF.md) first.
>
> Grove dogfoods itself: the backlog is GitHub issues on this repo
> (`grove-N` = issue #N), worked by grove workers — issue → `gv grab
> grove-N --repo grove` → PR → merge → `gv done`.
>
> **Append target — never open this file to add a row.** When you ship:
>
>     scripts/log-append.py tasks <<'EOF'
>     - [x] <what shipped> (grove-N, YYYY-MM-DD): <one paragraph>
>     EOF
>
> That puts your row at the top of §Now (newest first) and, if the head
> is over its cap, moves the OLDEST shipped rows into
> `docs/archive/TASKS-YYYY-MM.md` (their own month; same format, newest
> first). Nothing is ever deleted. Looking for an old row? `grep -r <term>
> TASKS.md docs/archive/` — don't read the archive in.
> `internal/guidance` fails `go test ./...` when this head is over its cap.
> <!-- head-cap: 16384 -->

## Now

- [x] Guidance-surface diet (grove-275, 2026-09-05): measured what lands in
      every session and trimmed it without dropping a rule. Always-resident
      bytes (root CLAUDE.md + orchestrator seed) 24,474 → 17,340 (−29%):
      history narration, duplicated rules, and `-h`-restated flag prose
      removed; every load-bearing phrase still guarded by
      `orchestrator/seed_test.go`. TASKS.md/LEARNINGS.md became small
      heads (current month) with monthly archives under `docs/archive/`
      and the open phases in `docs/roadmap.md`; HANDOFF.md rewritten lean
      (original archived). `model-lanes` split into procedure (17.5k) +
      two on-demand `reference/` files; shipping-gates lost its copy of
      the CLAUDE.md hard rules. Kickoff templates untouched (nothing
      provably redundant). Enforcement: each head declares `<!-- head-cap:
      N -->`; `internal/guidance.TestRepoHeadsUnderCap` fails the gate when
      a head is over it, and `scripts/log-append.py` is the append path —
      a session adds a row/entry without reading the file, and the script
      archives the oldest rows past the cap. PR left open for review.
- [x] feature trains: landed cars from GitHub + collapsed rail count
      (grove-397, 2026-09-27). On a GitHub provider a car is landed when
      its labelled issue is CLOSED and a PR from its `<ticket>-…` branch
      is MERGED into the feature branch (`landed_at` = mergedAt, `pr` =
      that PR) — two `gh` calls riding the queued pass
      (`StatusInput.ClosedIssues`/`MergedPRs`); events stay the fallback
      and the union, GitHub wins a ticket both know, a ticket tracked on the
      feature stays active. The rail collapses landed cars into one leading
      `⬢N ▸` token, labelled `361-363` or `3 landed`; the scene and lens
      keep every car. `e2e/github.sh` covers the lookup.
- [x] feature-trains 12: cutover (grove-383, 2026-09-27, on
      `feature/feature-trains`). New `e2e/feature.sh` (wired into
      `e2e/all.sh`) walks one train end to end in a single workspace:
      `feature new` on a scratch bare origin, `--adopt` (pushes nothing),
      grab by label (forks from the feature tip, kickoff says
      `--base feature/<slug>`, `gv ls --json` carries `feature`/`base`),
      `feature ls --json` status (cars, `behind_base`, `mergeable`),
      `land --json` dry run, `land --yes` (backend task files byte-identical
      after), `close` (branch left, label stops inferring). Grove's own
      `.grove/run.sh`: throwaway build to `/tmp/gv-$GROVE_FEATURE`, prints
      `GROVE_READY <path>`. `e2e/plugin.sh` teardown race fixed (wait for
      the isolated socket, retry the rm). Merge to main PROPOSED in the PR,
      not performed.
- [x] feature-trains 10: serve in the cockpit — `s`, review modal,
      `gv serve init` (grove-381, 2026-09-27, on `feature/feature-trains`).
      `s` on the selected FEATURES row: trusted run.sh → `startServe`
      (09's path, split out of `cmdServe`, no TTY, no stdout) and the
      READY value lands in the status line; untrusted/changed → review
      modal (script scrollable with ESC/bidi/control bytes rendered
      visibly, sha256, `y` appends `run_script_trusted` for the reviewed
      sha then serves; `esc` cancels, no event); a live `▶ <slug>` window
      → stop confirm. Rail title shows `serve ▶ <url> (behind tip)` /
      `stopped` / `untrusted` / `–`, fed by the 30s feature pass plus one
      pass after each start/stop — no new poll. `gv serve init` seeds a
      fresh cockpit orchestrator pane (`spawnOrchestratorBrief`, the
      `gv orchestrator new --brief` launcher) with `serve.InitPrompt`;
      refuses over an existing run.sh; records nothing. The lens (07)
      takes `s` too (modals return to it) and its SERVE line reads the
      same status. e2e/serve.sh drives the live cockpit, rail and lens.
- [x] feature-trains 07: feature lens + cockpit `l` land modal + `m`
      feature PR (grove-378, 2026-09-27, on `feature/feature-trains`).
      `enter` on a focused feature opens `internal/tui/lens.go`'s
      full-screen lens: TRAIN (every car, landed dimmed, est per car),
      BRANCH (tip, behind base, feature PR, closes = landed cars), SERVE
      (status `serve` or `no run.sh`), NEXT (pure `nextActions`: answer,
      land, review, grab when `after` landed, rebase, feature PR). Row
      keys hand off to the AGENTS handler with the cursor on the car's
      task; modals return to the lens. `l` builds the land plan from the
      fold + last PR poll (no network) and confirms before `feature.Land`
      × `FinishTask`. All lens strings built in `assemble()`.
- [x] feature-trains 11: docs + brains (grove-382, 2026-09-27, on
      `feature/feature-trains`). Orchestrator seed teaches `gv feature
      new/ls/close/land` and `gv serve` (tools block), a train ticket
      grabbed with `--feature` or by label (duty 3 Dispatch — the
      hand-written `--brief` train block is retired), and duty 10: `land
      <slug>` (`gv feature land <slug> --yes` then `gh issue close` +
      `Closes #N` on the feature PR) and standing `keep <slug> landed`
      (same, on each `pr_merged` for that feature, until its PR merges or
      the operator says stop) — the guardrail "never close any issue"
      gains exactly this exception, scoped to a ticket's PR `MERGED` into
      the feature's own branch. `.grove/orchestrator/CLAUDE.md` refreshed
      via `gv init --only orchestrator-md`. `.claude/skills/ticket-writing`
      "feature branch, consciously" bullet updated for the working
      `gv feature`/`gv grab --feature` machinery; root CLAUDE.md gains a
      "Feature trains" section. docs/plugins.md contract rows from 01/02/
      04/05/09 verified present, no gap found.
- [x] feature-trains 08: trellis in the scene (grove-379, 2026-09-27,
      on `feature/feature-trains`). At fx ≥ calm a feature's plants stand
      together, in rail order, under a `▁ <slug> landed/total ▁▁` bracket
      (just above the marker row; shares the ambient/marker row at
      compact/strip, markers + fairy win, ambient yields). Landed-and-done
      cars leave the orchard for their trellis as ♠; queued cars are `.`
      seeds. Glyphs only from the locked set (soil rule + grass tuft);
      the label degrades to the tally, then a bare rule — never `…`.
      Reads `featRow` (now carrying `trellis` + car tickets) built in
      `assemble()`; no features / fx=off stay byte-identical.
- [x] feature-trains 06: cockpit FEATURES rail panel + `TRAIN` column +
      `tab` focus (grove-377, 2026-09-27, on `feature/feature-trains`).
      Panel between header and AGENTS only while a feature is open
      (title `landed/total ↓N <base> serve – est`, rail `·●◆✓⬢ ▷ base`,
      issue labels, amber hint on the selected one); ≤3 expanded +
      `+N more`, collapses to title+strip under 12 spare rows. Status
      is computed in `assemble()` (live car states each refresh) merged
      with a `featuresCmd` pass that rides the 30s PR beat / `r` / a
      changed feature set — no new poll. Wiring shared with `gv feature
      ls` as `feature.LiveInput`. No-feature frames pinned by goldens
      (`internal/tui/testdata`). `enter/l/m` on a feature: 07; `s`: 10.

Grove is the operator's live daily driver and dogfoods itself: the real
backlog is **GitHub issues on this repo** (`grove-N` = issue #N), worked
by grove workers. Live tests + hooks install happened long ago; day-to-day
flow is issue → `gv grab grove-N --repo grove` → PR → merge → `gv done`.

- [ ] Phase 1 remainder: 1b pack loading, 1c drift detection (below)
- [ ] Phase 4 remainder: hooks/inbox generalization, generic orchestrator
      CLAUDE.md + pack overlay (below)
- [ ] Phase 5: learnings system first cut (below)
- [ ] Phase 6: OSS polish → Grid pack → parity gate → ovs retirement
- [ ] Parked-but-tracked side quests: mobile cockpit v2 (issue #5, planned),
      Obsidian live board (issue #9, design paused at REVISE), remote
      overflow host (docs/remote-host-setup.md; train #176–#178)

## Phase 0 — extraction proven (skeleton + local-md) ✅ 2026-07-04

Plan: [docs/plans/2026-07-04-phase-0.md](docs/plans/2026-07-04-phase-0.md)
(plan-reviewer approved). Divergences logged in docs/seed-manifest.md.

- [x] Seed: copy ovs tree byte-identical, module path rewrite, build/vet/
      test green (2026-07-03, see docs/seed-manifest.md)
- [x] **P0.0 namespace rename** (2026-07-04): config `~/.config/grove/`,
      state `~/.local/state/grove/` + `GROVE_STATE_DIR`, `gv hook` +
      basename-matched installer predicate (never claims ovs entries),
      `grove`/`grove-mobile` cockpit sessions, notifier group/titles
- [x] `markdown` TaskProvider (frontmatter schema; backlog = todo/backlog;
      event-state-authoritative in-flight exclusion; no-remote degraded
      grab/done paths per DESIGN §5.2)
- [x] `TaskProvider` interface extraction (P0 read subset of DESIGN §5.1);
      `linear` behind it; kickoff render byte-identical (golden-tested)
- [x] `gv init` P0 scaffold (register repo + `.grove/tasks/` + sample —
      probe/wizard stays Phase 1)
- [x] `gv grab/ls/done` E2E on a dummy repo (`e2e/dummy.sh`, remote-less,
      worker = `echo`) — also covers hooks, untrack, re-grab, audit
- [x] Dual-hook coexistence smoke test (scratch env over a copy of the
      real settings.json: ovs entries byte-identical, gv added once,
      `gv hook` no-ops on a live ovs worktree cwd). Live install = the operator's
      morning step.

## Phase 1 — bootstrap (drop-in-to-any-repo)

Plan: [docs/plans/2026-07-04-phase-1a-wizard.md](docs/plans/2026-07-04-phase-1a-wizard.md)
(plan-reviewer approved; re-scoped 1a to absorb most of 1b — the summary
board IS the manifest rendered).

- [x] 1a+ (2026-07-04): probe (stack/shape/context, `internal/probe`) +
      connections manifest (`internal/connections`, core kinds +
      grid-interim tagged for pack lift) + doctor = manifest renderer
      (✓/!/✗, fixes, --json, errors-only exit) + `gv init` wizard
      (detect-then-confirm huh forms, flag twins, `--yes` fills-empty-only,
      `--only <step>`, re-run = reconfigure, comment-preserving field-merge
      writer) + AGENTS.md bootstrap agent (templated one-shot, never
      overwrites, off under --yes) — e2e/wizard.sh covers it all
- [ ] 1b (remainder): pack loading (local path, slot merge)
- [ ] 1c: drift detection — TTL-cached lazy checks, failure-signal
      degradation via hook classifier, seeded-file hash drift +
      `gv sync --diff`; verb-boundary connection gating
- [x] Workspace registry + `gv switch` + ambient walk-up (2026-07-05,
      plan docs/plans/2026-07-05-workspaces.md, two review rounds):
      per-root `.grove/` config+state+orchestrator, yaml-merge over the
      global config, `grove-<label>` cockpits with the label in the TUI
      header (the visible-focus driver), read-only multi-fleet hook
      ownership, `gv switch`/`gv workspaces`, parent-scope init,
      e2e/workspace.sh. Legacy no-marker path preserved.
- [ ] Measure: is the Aider-style repo map needed at target repo sizes?
      (deferred decision, DESIGN.md OQ4)

## Phase 2 — routing (the smarter swarm) — deferred 2026-07-08

Unbuilt and moved to Parked. Routing/tiering likely isn't worth it for a solo
operator; the cost ledger these tasks would measure against already shipped in
grove-8, so this can be revisited when fleet size makes tier routing pay off.
See Parked / someday.

## Phase 3 — second provider (seam stress test)

- [x] `github-issues` adapter via `gh` (2026-07-05, pulled forward for
      unbrewed — plan docs/plans/2026-07-05-github-issues.md, two review
      rounds). OQ3 resolved → labels; ids `<repo>-<n>` fleet-unique;
      short refs (`gv done 7`) via numeric-suffix arbitration; list cap
      surfaced; e2e/github.sh with a stub gh. The seam held: zero
      changes to the linear/markdown providers.

## Phase 4 — relay + brain + cockpit (generic ovs parity)

- [ ] Hooks/inbox generalization (mail model lives in `state`)
- [ ] Generic orchestrator CLAUDE.md (de-Gridded duties text — does not
      exist yet, flagged by design review) + pack overlay rendering +
      seed-hash tracking
- [x] **Pulled forward 2026-07-04 (the operator's first-test feedback):** cockpit
      main-vertical (bare `gv` opens it; TUI-only = `gv dash`), MAIL/
      REVIEW panels → ACTIVITY feed, `gv orchestrator new` / `O` keybind,
      orchestrator default `claude --dangerously-skip-permissions`
      (e2e/cockpit.sh smoke-tests the layout)
- [x] **grove-8 (2026-07-07):** cockpit costs page (`$`/`c`, esc back) +
      persistent local spend ledger (`<state>/ledger.csv`, O_APPEND+flock;
      toggle in `<state>/cost-recording`, config `cost.record` seeds the
      default; `gv done` writes the final row so history survives
      transcript pruning) + ledger-only history section + hourly/daily/
      weekly spend bars (`internal/ledger`, `cost.Points/Buckets/Bar`,
      `gv cost --ledger|--record on|off`; e2e/dummy.sh proves durability)
- [ ] Cockpit remainder: workspace-labelled sessions (`grove-<label>`)
      + workspace-aware `orchestrator new` (§4.6 — needs Phase 1 ambient
      walk-up), expandable feed entries, lossless-clear drafts (§4.5)

## Phase 5 — learnings, first cut (lean)

- [ ] L0–L2 scopes + `gv learn` + `LEARNING:` sentinel harvest + curation
      inbox with human gate (docs/grove-learnings-design.md)
- [ ] Deferred until corpus size hurts: activation filtering, promotion
      automation, lint, counters (designed, not built)

## Phase 6 — OSS polish + the Grid pack + retirement

- [ ] goreleaser + tagged releases (decision: from day one — set up when
      first useful, no later than first share)
- [ ] Wizard hardening, config.example.yaml refresh, docs for strangers
- [ ] Architect/editor split (config flag, off by default)
- [ ] **Capability-surface audit of the live ccwork machine** → author
      the Grid pack in the workspace marketplace repo
- [ ] **Parity acceptance test** (docs/grove-connections-design.md §8) →
      ovs retirement + team onboarding

## Parked / someday

- Revisit-before-public: trust gate (accepted Critical, connections §9
  row 1) + worker-autonomy core safety guard (connections §6.4)
- Shared fleet visibility across teammates (explicitly out of v1)
- Router/tiers + escalate-on-failed-gate cascade (was Phase 2, deferred
  2026-07-08 — unbuilt; the cost ledger it would measure against shipped in
  grove-8, but there's no case for tier routing at solo scale yet)
- Learned router classifier (needs ledger history; likely never for solo)
- Public learnings commons (scope creep — parked)
