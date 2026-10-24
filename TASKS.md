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

- [x] **grove-568 · brain-overlay 06: kickoff overlay slot** — every embedded kickoff template gains `{{template "overlay" .}}` (byte-identical render with no overlay, goldens from main); `prompt_overlay:` repo key fills it, `prompt:` still wins with a stderr note; `gv brains --json` gains top-level `kickoffs` rows (default/overlay/override), plain `gv brains` prints KICKOFFS when any repo isn't default; `gv doctor` warn row for wholesale overrides. PR into `feature/brain-overlay`.
- [x] brain-overlay 05: seed — overlay section + floor, neutral `<task>`, `gv brains` verbs (grove-515, 2026-10-24): `orchestrator/CLAUDE.md` gains `## This seed and the workspace overlay` right after the intro (overlay wins where it contradicts; the floor: merge / close / `done` / `untrack` / `adopt` / `sweep` / `handoff` / unasked `grab` still need the yes, a mandate or a land order; overlay duties named not numbered, addenda cite "duty N (Name)"; read `LEARNINGS.md` beside the seed at session start). Every `DEV-X`/`DEV-42` → `<task>`, "(on Linear: team DEV)" gone, "dev-linear MCP tools" → "the Linear tools". Tools block teaches `gv brains [--json]`, `--refresh`, `--migrate [--apply]`. `seed_test` asserts the section + floor + LEARNINGS line (scoped, wrap-insensitive), the three brains forms, the numbered duty list 1 Fleet summary … 11 Learnings review as a contract, and no `DEV-` anywhere. Seed archive regenerated (28). Fixtures that swapped `DEV-X` in the live seed (cmd/gv brains_test, e2e/brains.sh) swap `<task>`. Docs: claude-code-facts §Sessions facts 4-7, root CLAUDE.md §Layout overlay line, LEARNINGS entry. PR → feature/brain-overlay.
- [x] brain-overlay 04: `gv brains --migrate --apply --label L [--yes]` (grove-514, 2026-10-24): swaps one reviewed draft in — `CLAUDE.md` → `CLAUDE.md.bak-pre-overlay` (byte-identical, listed as a leftover, never deleted), `StampSeed(<embedded seed>)` → `CLAUDE.md`, `CLAUDE.local.md.draft` → `CLAUDE.local.md` (over a stub-only file, which counts as absent). Pure `bootstrap.PlanApply` refuses (`*ApplyRefusal`, non-zero, no file touched) on: root gone, no brain, pristine brain, missing draft, a `## Review before applying` line still in the draft, a real `CLAUDE.local.md`, an existing `.bak-pre-overlay`; `bootstrap.Apply` restores the brain if a later step fails. Confirm prompt `apply? [y/N]` (decline exits 0, nothing changed) unless `--yes`; a tracked brain's two git commands are printed after the swap and never run. Unit tests: swap bytes, stub replaced, each refusal changes nothing, stub-vs-real boundary. e2e brains.sh: refusals + declined prompt leave every byte, additive applies, tracked applies with commands printed not run, conflicting refuses then applies once the Review block is cut, `gv brains --json` reads the three as `current` / `pristine: true` / `overlay: true`. PR → feature/brain-overlay.
- [x] brain-overlay 03: seed archive + `gv brains --migrate` dry run (grove-513, 2026-10-24): `orchestrator/seeds` embeds every historical seed body keyed by `SeedStamp` (27; `go generate ./orchestrator/seeds`; tests: key == stamp, fleet stamps present, embedded seed archived). Pure classifier `internal/bootstrap/brainmigrate.go`: `+`-only hunks → Addenda / Workspace duties verbatim; pairs differing by a brain-wide consistent token map (≤4 tokens, never numeric, recurring or identifier-like) → Glossary; else → `## Review before applying`. `gv brains --migrate [--label L]` writes only `CLAUDE.local.md.draft`, prints N added / M substituted / K to review, reports unknown stamps ("merge by hand"), PRINTS a tracked brain's two git commands, lists leftovers. Fixture + real-seed tests, e2e brains.sh leg. PR → feature/brain-overlay; `--apply` is car 04.
- [x] brain-overlay 02: `gv brains` reports the `CLAUDE.local.md` overlay; first seed writes an empty stub (grove-512, 2026-10-24): every brain row (registry + `(global)`) now carries `overlay` (bool) and `overlay_lines` (int — non-blank lines once block HTML comments are stripped, so a comments-only file is no overlay). `SeedBrain`, `RefreshBrain`'s absent case and `gv brains --refresh`'s seed action drop `OverlayStub` beside a freshly seeded brain — one HTML comment per line (Glossary / Overrides / Addenda / Workspace duties + the convention URL), written with O_EXCL so it never lands over an existing file; a current or stale brain never gets a stub after the fact. Every edited-stale note ends "move your edits to CLAUDE.local.md: `gv brains --migrate`" (verb lands in car 03). New `docs/orchestrator-overlay.md` holds the convention: the floor no overlay lowers, the four sections, the `**Override — <section> › <rule>:**` / `**Addendum to duty N (Name):**` / `**Workspace duty:**` shapes and the unbrewed example. Tests: stub written on absent-seed and never over an existing overlay; stripping comments from the stub leaves whitespace; stub → `overlay: false`, one real line → `true`/1; text report shows `overlay: N lines` + a fleet count. e2e brains.sh (stub after init, real overlay on `edited`, pre-existing global overlay survives `--refresh`, no sweep/refresh adds or changes an overlay), wizard.sh (first-seed leg sees the stub; overlay survives every refresh) green; plugin.sh's 2e leg gained the two fields (suite itself still red on this Mac at the pre-brains `/private/tmp` learnings_file compare — leg verified in isolation). PR → feature/brain-overlay.
- [x] brain-overlay 01: `gv brains --refresh` replaces pristine brains in place (grove-511, 2026-10-24): a brain is **pristine** when its stamp is the hash of its own body (`bootstrap.Pristine`, no new state). New writing flag `gv brains --refresh` acts per row — absent → seed, stale+pristine → replaced in place (`action: refreshed`, no `.new`, no backup), stale+edited → `CLAUDE.md.new` as today (`action: new`), unstamped → report only. Plain `gv brains [--json]` stays a pure read (e2e asserts bytes AND mtimes) and gains additive fields `pristine`, `action` (empty unless `--refresh`) and `leftovers` (`CLAUDE.md.new` / `.bak-*` beside the brain, listed never deleted). The sweep now also covers the global `cfg.Orchestrator.Dir` as row `(global)`. Doctor's brain-row fix names `gv brains --refresh`; `gv update` runs `gv brains --refresh` itself after a real binary swap and prints the refresh report (design OQ1 decided 2026-10-24: automatic). Verified: unit tables in internal/bootstrap + cmd/gv, e2e brains.sh/wizard.sh/dummy.sh green; plugin.sh carries the new field leg but is red on macOS base before it (known /private/tmp red). Seed untouched (car 05 teaches the verb).
- [x] feature-drive docs: brief template, Done-when convention, scoping skill, reviewed design (2026-10-24): `docs/briefs/feature-drive.md` (one chat drives a train to a draft PR into base with a report; the feature branch is the trust boundary), `docs/briefs/brain-overlay-drive.md` (first dry run, 4/6 cars merged with zero operator turns), `.claude/skills/feature-scoping/SKILL.md` (seven required mechanics so a scoping session is not a yes-man ceremony), `docs/plans/2026-10-24-feature-drive-design.md` (8 drafted cars, independent review folded in: flat `pr_reviewed`, review tasks `grove-N.review`, supervise owns the timeout, `gv feature drive` asks before writing the grant, binary-appended journal). Cars not filed; train opens after brain-overlay merges.

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
- [x] `markdown` TaskProvider (frontmatter schema; backlog = todo/backlog; (2026-07-04)
      event-state-authoritative in-flight exclusion; no-remote degraded
      grab/done paths per DESIGN §5.2)
- [x] `TaskProvider` interface extraction (P0 read subset of DESIGN §5.1); (2026-07-03)
      `linear` behind it; kickoff render byte-identical (golden-tested)
- [x] `gv init` P0 scaffold (register repo + `.grove/tasks/` + sample — (2026-07-04)
      probe/wizard stays Phase 1)
- [x] `gv grab/ls/done` E2E on a dummy repo (`e2e/dummy.sh`, remote-less, (2026-07-03)
      worker = `echo`) — also covers hooks, untrack, re-grab, audit
- [x] Dual-hook coexistence smoke test (scratch env over a copy of the (2026-07-04)
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
