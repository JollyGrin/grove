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

- [x] model-fit 08: compaction over discard (grove-440, 2026-10-07).
      Per-repo `autocompact: auto|100000–1000000` beside `claude:` →
      `claude --autocompact N` on grab/adopt via `config.WithAutocompact`
      (strip-then-inject, the grove-142 shape; default unset = no flag,
      events untouched); `config.example.yaml` recommends 150000 on
      Claude lanes, unset on 1M third-party lanes. The SessionStart
      receiver prints a ground-truth re-orientation on `source: compact`
      only (ticket + acceptance-criteria section from the provider with a
      5s fetch timeout, `git log --oneline -8` + `git status --short`,
      PR number/URL/body via 5s-bounded `gh pr view` with a git-only
      upstream fallback, the STATUS contract) — never from the summary,
      never non-zero, degrades per block on an unreadable worktree; the
      grove-289 `compaction` event is reused unchanged. One "when
      compacting, preserve …" line in root CLAUDE.md and the AGENTS.md
      bootstrap template. `e2e/dummy.sh` asserts the flag exactly once /
      absent / rejected; `e2e/plugin.sh` asserts the re-orientation.
- [x] model-fit 09: Stop hook evidence gate + last-match sentinel
      (grove-441, 2026-10-07, PR #446). `hooks.ParseSentinel` is the one
      parser (hooks + TUI): LAST `STATUS:` line wins, tolerates
      bold/underscore wrap, `—`/`–`/`-`/`:` separators and a bare
      `STATUS: DONE`, skips the kickoff's own `<placeholder>` echo. The
      Stop receiver checks a DONE claim against the worktree (`git
      status`, upstream/ahead, `gh pr list` with a 5s timeout, skipped
      without a remote): per-repo `done_gate: off|warn|block`, default
      **warn** for one release (verdict as additive
      `gate`/`gate_reason`/`gate_decision` data + notify, never blocks);
      `block` prints `{"decision":"block",…}` and records sentinel
      `done_unverified` (`gv ls` label `done?`), never on
      `stop_hook_active`, capped at 2 consecutive per session
      (`<state>/done-gate/<ticket>`), never when grove itself errors.
      `e2e/dummy.sh` covers block/warn/last-match. Flip to `block` once a
      release of warn verdicts looks right.
- [x] model-fit 10: price table + tier classifier for the Claude 5.5
      family and Fable ids (grove-442, 2026-10-07). Explicit
      `claude-opus-5-5` ($4/$20, cache reads $0.20) and `claude-sonnet-5-5`
      ($2/$10, cache reads $0.20) rows — Opus 5.5 had been riding Opus 5's
      prefix with derived cache reads at $0.50, 2.5× over. A Fable `--model`
      pin on a profile lane now takes the lane's opus slot (was: sonnet);
      `TierForModel` maps a Fable id to a configured "fable" tier and
      otherwise stays on the host default rather than downgrading a revive
      to opus (documented in `internal/config/models.go`). Status-bar
      detection knows "fable". Prices verified against the claude-api skill
      model table (cached 2026-09-25).
- [x] model-fit 03: `--effort` as a first-class dial (grove-435,
      2026-10-07). `gv grab|adopt|orchestrator new --effort
      <low|medium|high|xhigh|max>` beside `--model`, a per-repo `effort:`
      key as the standing default (flag wins), `config.WithEffort`
      strip-then-inject (grove-142 lesson), the level validated before
      anything exists. Recorded on the task (`Task.Effort`, additive
      `effort` in `gv ls --json` and the `task_created`/`task_adopted`
      data, a 15th `effort` ledger column — the 13/14-column rows still
      read). Adopt keeps the grabbed pin unless told otherwise.
      `gv doctor` row `effort-override` warns on
      `CLAUDE_CODE_EFFORT_LEVEL` in the launching env and `maxEffortLevel`
      in any settings scope that reaches a worker. Seed teaches the flag
      (`orchestrator/seed_test.go` tripwire); `e2e/dummy.sh` asserts the
      launch line carries it exactly once, `e2e/plugin.sh` the row field.
      No automatic routing — the operator pins.
- [x] model-fit 01: kickoff templates — goals and constraints, not
      choreography (grove-433, 2026-10-07, PR #444). All four autonomous
      templates (`md_default`, `md_pickup`, `default`, `pickup`) drop the
      1–7 step script, the ask/do-not-ask hedge (Claude Code 2.1.292
      injects the Fable 5.1 autonomy block itself — verified in
      `prompt_snapshot`, see claude-code-facts), the ~3-files subagent
      heuristic, the duplicate "never push to base" and `ALWAYS`; they
      state the task, the contract (start verb, commit prefix, `gh pr
      create --base`, feature paragraph), what done means, and add
      evidence / scope / delegation / wrap-up re-grounding sentences from
      the migration guide. STATUS lines byte-identical and last, pinned by
      `TestRenderEndsWithStatusSentinels`; linear goldens regenerated
      (seed-manifest row). Manual templates untouched. A/B is #434.
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
