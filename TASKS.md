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
- [x] PR-poll timer multiplication fix (grove-118, 2026-07-18): the
      `prsMsg` handler unconditionally re-armed its own 30s tick, so every
      ad-hoc `prsCmd` delivery — including manual `r` refresh — added
      another self-perpetuating poll loop (grove-24's `refreshMsg` bug,
      reintroduced for PRs); split into a `prTickMsg` beat that alone
      re-arms, `prsMsg` now pure data application; vestigial one-shot
      `prTickEvery()` (callback returned nil, dropped by bubbletea) removed
- [x] Cost cache eviction (grove-119, 2026-07-18): `cost.Cache.entriesFor`
      kept every `(path, mtime, size)` generation forever — with the
      costs page open, a live worker's continuously-mutating transcript
      inserted a new full-parse entry every 1s refresh and never freed the
      old ones, growing cockpit RAM (reserved for workers) unbounded. Added
      a `path -> newest fileKey` index; on insert, the prior generation for
      that path is evicted, so the cache holds exactly one entry per path.
- [x] Kimi Code plan fuel gauges (grove-133, 2026-07-18): ACCOUNT tab
      rows whose profile `base_url` targets `https://api.kimi.com/` show
      per-window quota gauges under the key line
      (`5h  ▓▓▓░░  62% left · resets in 2h 20m`) — new read-only
      `internal/kimi` client (`GET /v1/usages`, schema per kimi-cli's
      `_parse_usage_payload`, tolerant parsing: garbage → empty, non-200 →
      dash + hint), fetched only inside the one-shot `accountCmd`; unset
      key or failed fetch renders a dash fuel line, never an error state
- [x] Window-side tmux target hardening (grove-116, 2026-07-18): worker
      windows resolve by immutable `@N` id via `tmux.WindowID` — the old
      `session:name` targets prefix-matched, so `repo · grove-1` could
      silently hit sibling `repo · grove-10` (pause/untrack/done killed a
      live worker mid-turn, `gv answer` steered the wrong agent, late
      hooks re-badged the sibling's window); KillWindow/RenameWorker/
      ClaudePane refuse missing windows, relay goes through
      `ClaudePaneTarget` (`%N` pane id), grove-1/grove-10 scratch-server
      regression fixtures in `internal/tmux/window_id_test.go`
- [x] Orchestrator hotkeys (grove-105, 2026-07-18): `)` always opens the
      profile picker (default_profile dropped, lingering yaml key ignored);
      digits 1–8 spawn their bound profile directly, bound/unbound from the
      picker and persisted to `orchestrator.hotkeys` in the workspace (or
      global) config.yaml, comments preserved
- [x] ACCOUNT tab → per-profile key manager (grove-104, 2026-07-18):
      one selectable KEYS row per distinct `auth_token_env` across
      configured model_profiles (shared vars merge, profile names on the
      row) — masked value when the key resolves, an explicit "not set —
      enter to paste" state when it doesn't; enter (or p) opens the paste
      flow for the selected row's var; `openrouter.Key`/`SaveKey` are
      var-agnostic (same 0600 replace-in-place contract, other lines
      byte-for-byte); OpenRouter row keeps balance/runway/top-up extras,
      other rows are stars-only; zero profiles → grove-87's standalone
      OpenRouter view unchanged
- [x] Model profile per-profile env map (grove-103, 2026-07-18): `env:`
      map on `ModelProfile` for backend-specific vars beyond the six
      built-ins (Kimi Code's K3 endpoint needs
      `CLAUDE_CODE_AUTO_COMPACT_WINDOW`/`ENABLE_TOOL_SEARCH`/etc.) —
      exported sorted, before the built-in six, which win on collision so
      `env:` can't redirect `base_url`/`auth_token_env`; keys validated at
      config load (`^[A-Za-z_][A-Za-z0-9_]*$`) since they're interpolated
      unquoted into the wrap's shell line. No `env:` → byte-identical
      `WrapProfile` output.
- [x] Workspace marker narrowed (grove-100, 2026-07-17): a `.grove/` is a
      workspace marker only when it holds substance — `config.yaml`,
      `state/`, or `orchestrator/`; a `.grove/` with only the markdown
      backend's `tasks/` is NOT a workspace, so grove-78's fail-closed
      grab guard no longer traps `gv init`-scaffolded repos on the
      legacy global-config path. workspace.sh (red since the bare-dir
      marker landed, unmasked by grove-99) back to green unmodified;
      e2e/all.sh fully green.
- [x] Cockpit build restored on tmux 3.6a (grove-99, 2026-07-17):
      grove-78's blanket `=`-anchor broke every pane/window-target command
      (`set-option`/`show-options`/`select-layout`/`split-window` reject
      bare `=name` → `gv` died at SetCockpitLayout, CockpitReady always
      false); those helpers now anchor via `tmux.ExactActive` (`=name:`),
      Exact stays for true session-target commands; regression test vs
      scratch server + e2e/cockpit.sh back to green. Leftover: workspace.sh
      still red — legacy-path grab vs `.grove/tasks` workspace marker
      (issue #100)
- [x] Sweep offers: orphan kill + idle pause (grove-92, 2026-07-17):
      `gv sweep` gains two per-row-confirmed offer types — orphan process
      → plain-SIGTERM `kill <pid>` (a survivor is reported, never
      SIGKILLed) and idle worker → `gv pause`; offer-building extracted to
      pure `audit.SweepOffers`, which drops paused rows on the paused FACT
      (not class — Merged outranks Paused in Classify), so a paused task
      yields ZERO offers of any kind; `sweep --json` adds additive
      `orphan_processes`; e2e stubs `ps` via PATH so a piped `y` can never
      reach a real process
- [x] Audit idle class (grove-91, 2026-07-17): flag finished-but-burning
      workers — window alive + agent done (idle with STATUS done sentinel)
      or waiting + quiet past `audit.idle_after` (default 30m,
      zero/invalid tolerant) classify `idle`, suggestion `gv pause`; ranks
      below merged/drifted/paused/abandoned/disconnected, working agents
      never idle (stuck stays the cost flag's job); additive `--json`
      class + `facts.sentinel`
- [x] Audit orphan-process report (grove-89, 2026-07-17): `gv audit`
      flags claude/mcp descendants reparented to launchd (ppid==1,
      not in any live tracked pane's ancestry) — pure detection fn
      over injected ps/tmux text, additive `orphan_processes` in
      `--json`, human output prints a suggested `kill <pid>`;
      report-only, audit stays pure read
- [x] WindowExists glyph fix (grove-94, 2026-07-17): live window names
      carry grove-47 state glyphs (`… ⏸`), audit's exact-equality
      check classified every live worker `disconnected`; now matches
      stored name exactly or as `stored + " "` prefix; kill/target
      paths untouched
- [x] `gv pause` (grove-90, 2026-07-17): park one worker — kill its window
      (worktree/branch/transcript untouched), `task_paused` event +
      `Task.Paused` fold, audit class `paused` (never falls through to
      disconnected/abandoned; suggestion `gv adopt`), ⏸ in ls + cockpit,
      paused detail skips the pane scrape and hints `gv adopt`; mid-turn
      guard behind `--force`; dummy.sh pause→adopt loop asserts
      `--resume <sessionID>`
- [x] Cockpit first-frame panic fix (grove-79, 2026-07-13): viewActivity's
      `items[:avail]` clamped (grove-56 regression — empty feed + small
      leftover panicked the render); narrow/short render-sweep test;
      e2e/all.sh runs all six suites; cockpit.sh/workspace.sh re-greened
      and capture with `-S -300`
- [x] Surface-plugin contract v1 (grove-75, 2026-07-13): docs/plugins.md +
      copyable plugin-authoring skill; `schema_version`/`v` stamps;
      e2e/plugin.sh tripwire. First consumer: gv-remarkable (issue #76,
      separate repo)
- [x] grab fail-closed + exact tmux session targets (grove-78,
      2026-07-13): grab errors when the repo's workspace isn't the ambient
      one (no legacy-session escape) and rolls back worktree/local
      branch/prompt/window on failure (remote branch kept); every
      session-scoped `-t` in internal/tmux `=`-anchored via `tmux.Exact`
      (session `grove` vs `grove · <ticket>` window collision, live)

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
