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

- [x] claude-accounts 08 — brains (grove-475, 2026-10-16): orchestrator seed tools block gains `gv account ls --usage --json` and `gv grab … --account A`; duty 3 gains the accounts-are-a-lane paragraph (propose `--account` with the reason, never pick unasked, never `gv account use`, token-account caveats: no connectors / Remote Control, never `/login` in a pinned session). model-lanes Step 1 reads the Claude sub from `gv account ls --usage --json` instead of "no API". CLAUDE.md §Layout documents `<state>/accounts/` and that accounts never apply under a non-default `claude_config_dir`. gv-keys 07 (#367) import note drafted in the PR body for the operator to post.
- [x] grove-471 · claude-accounts 04: launch II (2026-10-11). Cockpit first pane (both `--continue`/fresh limbs), `gv orchestrator new [--account]` (refused beside a non-Anthropic `--profile`), detached + web chats resolve via `resolveChatAccount` and wrap via `wrapWorker`; a token-account chat says it has no claude.ai connectors/Remote Control (pane, CLI, web sheet `account`). `orchestrator_spawned` carries `account` when pinned; `@grove_account` pane tag lets `gv account rm` refuse on live chats; handoff plan prints the account. `gv sub --agentic` untouched. log-append `row_date` IndexError fixed.
- [x] claude-accounts 07 — `$` → CLAUDE tab (grove-474, 2026-10-11): third tab on the costs page (SPEND / ACCOUNT / CLAUDE; tab cycles three, `$` still lands on SPEND). One row per account, login first: name, active ●, pinned count, "as of 4m", EXPIRED, login's email; under it a 5h and a 7d fuel gauge (the kimi `fuelLine`, factored out of `viewFuelRows`) reading `% left · resets 14:10`. Branches on each window's state from `Store.Usage`: live → severity-colored gauge; reset → `100% left · reset since last seen`; stale → grayed gauge; unknown or no usage file → "no data" + the store's hint, never 0%. Reads local usage files only, on tab open and `r` — never on the 1s tick (`newClaudeCmd` counted in tests). `u` arms a footer y/N to make the selected account active (in-process `Store.Use`, the same call `gv account use` makes; anything but y cancels), then rereads. `account.ResetWhen`/`UsageAge` exported for the tab. Tests: three-way cycle, no read on tick/refresh, no-data hint, stale resets_at → reset, two-account golden (`testdata/claude-tab-two-accounts.golden`), width clamp, use confirm/cancel.
- [x] claude-accounts 06 — usage reader + `gv account ls --usage [--json]` (grove-473, 2026-10-11): `internal/account` now owns the usage record (`UsageRecord`, `ReadUsageRecord`, `Store.UsageDir`) that `gv statusline` writes and `ls --usage` reads — one schema, no drift. Each window carries an explicit `state`: `live` (observed, reset ahead), `reset` (resets_at passed → 0%), `unknown` (no percentage recorded — never 0); a live window with no reset time is `stale` once older than the window. Missing file → "no data — no session on this account yet"; corrupt/out-of-range → a hint, never an error. Text: `5h 42% (resets 14:10) · 7d 61% (resets Tue 22:00) · as of 3m`. `gv account rm` deletes the usage record so a re-added name starts empty. JSON documented in docs/plugins.md, asserted in e2e/plugin.sh; ZZZSENTINEL covers `--usage`.
- [x] grove-470 · claude-accounts 03: launch I — resolver, `WrapAccount`, grab/adopt, state (2026-10-11). `account.Store.Resolve` (flag → recorded pin → active → login; explicit/recorded + non-Anthropic profile, workspace `claude_config_dir`, or a claude cmd naming `CLAUDE_CONFIG_DIR` or running `ccwork` by any path/after `env`/VAR=val → error, the same silently skip the active account; a missing or expired pin refuses, never falls back to login) + `Store.Preflight` (exists, non-empty, 0600, inside its one-year life). `config.WrapAccount` renders `( t="$(cat '<path>')" && [ -n "$t" ] && export CLAUDE_CODE_OAUTH_TOKEN="$t" GROVE_ACCOUNT='<name>' && exec <cmd> )`, printing `gv: account <name> token unreadable` on stderr when the read fails; cmd/gv `wrapWorker` applies the profile wrap or the account wrap, never both; adopt wraps resume and fresh limbs separately. `--account` on grab and adopt (`login` strips an adopt pin; in `grabValueFlags`); `task_created`/`task_adopted` carry `account` only when pinned (adopt writes "" on a strip); `gv ls --json` `account` documented in docs/plugins.md and asserted in e2e/plugin.sh. Doctor: `account-pin-missing:<name>` (fail; tasks folded only while accounts exist). `rm` names done/untracked tasks still pinning the account; a refused pin's error names `gv adopt <t> --account <name|login>`. The no-secrets hook patch is not grove's to ship (LEARNINGS). Tests: resolver table (27 rows), WrapAccount golden + run-in-sh, no-accounts launch strings golden, ZZZSENTINEL absent from the send-keys line; e2e/dummy.sh accounts leg (fake claude echoing `${CLAUDE_CODE_OAUTH_TOKEN:+set}`: grab → set, adopt keeps, `--account login` strips).
- [x] grove-472 · claude-accounts 05: `gv statusline` pass-through shim recording per-account 5h/weekly usage (2026-10-11). `internal/statusline`: `Run` reads stdin once, runs `statusline.wrap` (global config) via `sh -c` with the same bytes, passes stdout/stderr/exit status through, and meanwhile writes `<state>/accounts/usage/<GROVE_ACCOUNT|login>.json` (`{five_hour, seven_day, seen_at}`, temp+rename, 0600, only when a window changed; invalid account names write nothing; every capture error swallowed). `gv statusline install|uninstall` edits `~/.claude/settings.json` only (generic-map + json.Number, other keys kept), records the prior command (or "" = none → uninstall drops `statusLine`), idempotent; `IsShim` also matches `gv-*` throwaway builds and `Run` refuses a self-wrap, so the shim can never wrap itself. `statusline` dispatches before the ambient workspace resolves. Conditional doctor row `account-statusline` (accounts exist, shim absent). `config.editDoc` factored out of `SaveHotkey` for the new yaml writer. Tests over the docs' full-schema fixture; real-binary overhead ≈12ms (mostly Go start-up), in-process ≈2ms. Review round: `IsShim` shell-word-parses (quoted paths, `exec`, `VAR=` prefixes, `;`/`|` chains) + `GROVE_STATUSLINE_DEPTH` runtime re-entry guard; shell-init exports `GROVE_ACCOUNT=<name>` beside the token (login paths strip it); quoted `ShimCommand`; `config.WriteFileAtomic` (symlink target, perms kept) for settings.json/config.yaml/usage; shim-without-wrap = install error + `statusline-wrap-missing` doctor row; seen_at = last sighting (≤1min stale), resets recorded; overhead test behind `GROVE_TIMING_TESTS=1`.
- [x] grove-469 · claude-accounts 02: `internal/account` store + `gv account add/ls/use/rm/token-path/shell-init` + doctor rows (2026-10-08). Store under `<state dir>/accounts/` (dir 0700, `<name>.token` 0600 written atomically, `<name>.minted` = add date, `active` = bare name; names `^[a-z][a-z0-9-]{0,31}$`, `login` reserved); every token reader goes through `Store.TokenSource` (the gv-keys 07 seam). `add` reads stdin only (echo off at a TTY); `ls --json` = `accounts` envelope documented in docs/plugins.md + asserted in e2e/plugin.sh; `rm` refuses while a non-done task's new read-side `Task.Account` names it (`--force`); `account` is exempt from the ambient-label check so shell `claude` never flips to login on a broken label. Conditional doctor rows (token not 0600, active missing, shell-init absent, token ≥330d). Tests: ZZZSENTINEL absent from every output/error; the shell-init function run under bash with the test binary as `gv` and a fake `claude`. No launch path changes (car 03).

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
