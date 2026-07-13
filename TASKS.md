# Grove — status board

> Working docs: [DESIGN.md](DESIGN.md) is the what/why · **TASKS.md** is
> the status board · [LEARNINGS.md](LEARNINGS.md) is the surprises.
> Fresh pickup? Read [HANDOFF.md](HANDOFF.md) first.
>
> Phases mirror DESIGN.md §13 (redrawn 2026-07-03 per design review).
> Each phase gets a `docs/plans/` plan (plan-reviewer gated) before code.

## Now (2026-07-12)

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
      Obsidian live board (issue #9, design paused at REVISE), remote PC
      fleet host (docs/pc-remote-host-setup.md, blocked on BIOS
      virtualization)
- [x] Cockpit first-frame panic fix (grove-79, 2026-07-13): viewActivity's
      `items[:avail]` clamped (grove-56 regression — empty feed + small
      leftover panicked the render); narrow/short render-sweep test;
      e2e/all.sh runs all six suites; cockpit.sh/workspace.sh re-greened
      and capture with `-S -300`
- [x] Surface-plugin contract v1 (grove-75, 2026-07-13): docs/plugins.md +
      copyable plugin-authoring skill; `schema_version`/`v` stamps;
      e2e/plugin.sh tripwire. First consumer: gv-remarkable (issue #76,
      separate repo)

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
