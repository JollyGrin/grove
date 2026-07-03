# Grove — status board

> Working docs: [DESIGN.md](DESIGN.md) is the what/why · **TASKS.md** is
> the status board · [LEARNINGS.md](LEARNINGS.md) is the surprises.
> Fresh pickup? Read [HANDOFF.md](HANDOFF.md) first.
>
> Phases mirror DESIGN.md §13 (redrawn 2026-07-03 per design review).
> Each phase gets a `docs/plans/` plan (plan-reviewer gated) before code.

## Now

- [ ] **Write the Phase 0 plan** (`docs/plans/2026-MM-DD-phase-0.md`):
      P0.0 + markdown provider + dummy-repo E2E + dual-hook smoke test.
      Run plan-reviewer before executing.

## Phase 0 — extraction proven (skeleton + local-md)

- [x] Seed: copy ovs tree byte-identical, module path rewrite, build/vet/
      test green (2026-07-03, see docs/seed-manifest.md)
- [ ] **P0.0 namespace rename — BLOCKS running the binary** (see
      CLAUDE.md warning + seed-manifest generalization map): config dir,
      state dir + env var, hook command names + binary path, tmux session
      names, notifier strings
- [ ] `markdown` TaskProvider (DESIGN.md §5.2 incl. the event-state-
      authoritative rule and the no-remote degraded path)
- [ ] `TaskProvider` interface extraction; `linear` moves behind it
      (read paths only for now)
- [ ] `gv grab/ls/done` E2E on a dummy repo (`.grove/tasks/` file, worker
      = `echo`) via the dummy-data pattern
- [ ] Dual-hook coexistence smoke test with live ovs (DESIGN.md §12:
      task-ownership no-op contract)

## Phase 1 — bootstrap (drop-in-to-any-repo)

- [ ] 1a: probe (stack/shape/scope) + `AGENTS.md` bootstrap agent +
      wizard core (detect-then-confirm, flag twins, re-runnable-as-repair)
- [ ] 1b: connections manifest + doctor derived from it + minimal pack
      loading (local path, slot merge)
- [ ] 1c: drift detection — TTL-cached lazy checks, failure-signal
      degradation via hook classifier, seeded-file hash drift
- [ ] Workspace registry + `gv switch` + ambient walk-up (DESIGN.md §6.5)
- [ ] Measure: is the Aider-style repo map needed at target repo sizes?
      (deferred decision, DESIGN.md OQ4)

## Phase 2 — routing (the smarter swarm)

- [ ] `Router` interface + `ClaudeTiers`; pre-dispatch classifier;
      `--tier` override; TIER column
- [ ] Escalate-on-failed-gate cascade; measure against the cost ledger

## Phase 3 — second provider (seam stress test)

- [ ] `github-issues` adapter via `gh` (decide transitions: labels vs
      Projects v2 — deferred to here on purpose)

## Phase 4 — relay + brain + cockpit (generic ovs parity)

- [ ] Hooks/inbox generalization (mail model lives in `state`)
- [ ] Generic orchestrator CLAUDE.md (de-Gridded duties text — does not
      exist yet, flagged by design review) + pack overlay rendering +
      seed-hash tracking
- [ ] `gv ui` cockpit: main-vertical, AGENTS + ACTIVITY feed,
      `gv orchestrator new` / `O` keybind (docs/grove-cockpit-design.md)

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
- Learned router classifier (needs ledger history; likely never for solo)
- Public learnings commons (scope creep — parked)
