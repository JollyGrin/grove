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

- [x] grove-368 gv-keys 08: `gv keys push --host H` / `receive` / `reseal` — ciphertext + sidecars to a remote over existing ssh (`remote.RunWithInput`, `keys` Supported but only recipient/receive relay), `keys:host:<H>` doctor row, e2e/keys.sh fake-ssh leg. Train PR → feature/gv-keys-secrets.
- [x] **grove-363** gv-keys 03: launch path — `env:` values `secret:NAME` (`literal:` escapes) validated at load; `WrapProfile(cmd, p, source)` now runs `eval "$(gv keys env --profile P --workspace-root R || echo false)"` instead of sourcing `.env`; internal `gv keys env` prints export lines for exactly the profile's names, refuses a TTY, and on a missing name exits non-zero naming it + the namespaces searched; `e2e/keys.sh` (grab half) in all.sh. PR → `feature/gv-keys-secrets` (grove-363, 2026-10-22).
- [x] grove-365: gv-keys 05 — `NAME.meta.yaml` sidecar (type/preview/reveal/set_at), `secrets.PreviewFor` rule, `gv keys set --type --reveal`, `ls` type+preview columns and `--json` fields; `rm` deletes both files.
- [x] **grove-364 gv-keys 04: `gv keys exec NAME[,NAME…] -- <cmd>`** — `secrets.ExecEnv` (named secrets only, dedup, name check before decrypt, missing name fails naming namespaces) + `syscall.Exec` verb (no shell; child's exit code); empty-arg stderr warning with the `sh -c '…'` right-shape; new `e2e/keys.sh` (added to all.sh). Not relayed over `--host`.
- [x] **grove-362 · gv-keys 02: `gv keys` CLI** — `init`/`recipient`/`set`/`ls [--json]`/`rm` over `internal/secrets` (value only from stdin or hidden prompt, positional value is a usage error; `ls` names/namespace/recipient count/shadowed, never a value); `workspace.label: global` rejected at parse (and as a wizard label); doctor rows `keys:identity` + `keys:unresolved` (conditional); wizard `keys` step (ON, parent-scope kept). PR → `feature/gv-keys-secrets`.
- [x] focus-swap train wrapped — measured before/after + docs (grove-533, 2026-10-19): last car of `focus-swap`; its PR into main is the operator's call. `e2e/focus-bench.sh` (not in all.sh) measures main vs tip on the dummy-data fixture with 3 real Claude Code chats, median of 9: start → target visually settled 480→107ms (`gv chat focus`), 488→90 (rail click), 472→112 (CHATS enter); tmux execs 52–62 → 10–11; the on-screen repaint is gone. Design doc Status → IMPLEMENTED with the table; CLAUDE.md §Layout; tmux-discipline gains never `set -g window-size`, `resize-window` pins `manual`, match PANE size, per-session hooks only; HANDOFF train entry. No event or `--json` change (the bench diffs event keys); orchestrator seed unchanged.
- [x] grove-531 focus-swap 03 — direct attach + phone captures with slot-pinned hidden chats: hidden chat sessions carry their own `client-attached`/`client-session-changed` (unpin → follow the client) and `client-detached` (`gv chat sync-sizes` re-pins) hooks; the planner leaves attached windows alone; `DecideSwap` falls back on `SwapAttached`. `internal/chatweb/width_test.go` + 30 live 2.1.303 fixtures pin 80- vs 139-col readings. Spike recorded in docs/plans/2026-10-19-focus-same-size-swap-design.md §Edge cases; e2e/focus.sh attach/two-client leg (default + hostile).
- [x] **grove-526** focus-swap 02 — a focus swap is one swap-pane (pure `DecideSwap`, fallback = break/join unchanged); `@grove_chat_home` keeps session names with chats; hidden sizing compares the PANE (global pane-border-status). PR #530 → `feature/focus-swap`.
- [x] grove-525: focus-swap car 01 — hidden chat windows track the focus slot (the shown chat pane's live size, read after the re-tile). `internal/tmux/slot.go`: pure `PlanHiddenSizes` over one `list-panes -a` (per-window `window-size manual` + `resize-window`, equal = no command, remote skipped, other workspaces untouched) + `SyncHiddenSizes`/`ResetHiddenSizes`. Triggers: end of `RetileFocus` (via injected `tmux.FocusWorkspace`), the cockpit window's `window-resized` hook (`gv chat sync-sizes`, hidden verb), `HideChatPane` in focus; leave-focus unpins before restore. `TestNoGlobalWindowSize` source tripwire; e2e/focus.sh legs + hostile pass (aggressive-resize + global window-size).
- [x] **grove-521** focus swaps: target lookup scoped to the cockpit's workspace, live rows only (`findLiveChat`; full report only on a miss, so archived refusals hold); rail viewer stamps `@grove_rail_pid` and redraws on SIGUSR1 after every write (2s tick kept as heartbeat). Fixture (12 ws, 260 archived): join 0.24→0.20s, rail redraw 1.29→0.30s.
- [x] grove-517: click a CHATS row to show/focus it — bubbletea cell-motion mouse + a filter (left press passes, wheel → ↑/↓, the rest dropped before Update); a click = enter on that row, on-screen chats only selected; hit-test from chatLayout (PR #520)
- [x] grove-510: focus rail — stable chat identity + every chat as a title chip (2026-10-19). Every move path (hide/show/focus/L/spawn-in-focus) first stamps each unstamped local chat pane with the name it wears (`@grove_chat_name`: `cockpit·N` / `chat-N`, rides break/join-pane); `nameChats` reads stamps first, positional otherwise, never reusing a stamped name — the original orchestrator stays `cockpit·1` when minimized. The rail is built from the CHATS box's own rows (`railChips`, wire `v2`): one chip per chat, shown chip bold `▣`, clicking it a no-op, remote chips unclickable; title = Claude's pane title (glyph stripped, `#{pane_title}` appended to the one `list-panes -a`) → ticket → first prompt. Every move force-writes the rail (Writer `force`, in-process `railWriteMu`). e2e/focus.sh: name stamps, chip counts, 6-move churn vs `gv chat ls`.
- [x] grove-503 (#503): rail writer race — chatrail.Writer.Write mutex-guarded, railTick uses TryLock on railBeat so an overlapping refresh skips its beat; race test in internal/chatrail
- [x] **grove-504** — focus rail never a CHATS row: `cockpitChatPanes` skips `p.Rail`; `ApplyLayout` starts the rail in `$HOME`, not the caller's cwd; unit test + `e2e/rail.sh` leg (build from inside the orchestrator dir).
- [x] grove-491: focus-mode 07 — docs wrap for the train: CLAUDE.md §Layout (two layouts, `L`, focus hides chats, `gv chat focus`), orchestrator seed (`gv chat focus` in tools, focus note under Dispatch-and-dismiss; brains go stale until `gv init --only orchestrator-md`), grove-cockpit-design Status 2026-10, HANDOFF train section. plugins.md already carries `reason:"focus"` (grove-487); no new tmux skill rule (no LEARNINGS entry on status/status-format).
- [x] grove-489: focus-mode 05 — click a rail box to focus that chat. The rail process (gv itself) turns on SGR mouse reporting, `chatrail.Hit` hit-tests from the same `layout()` the drawing uses, a left release calls `focusChat` in-process (box inverts for the tick; a chat closed since the draw flashes `gone`, nothing mutated). `@grove_rail_rows` rows gain a trailing session field. No tmux binding added. Rail RSS ≈ 14 MB. e2e/focus.sh drives the click by typing the SGR bytes into the rail pane.
- [x] grove-488 focus-mode 04: `gv chat focus <s>` swaps the shown chat in the focus layout (minimize current → `@grove_focus_hid`, show target, drop it from the option, rail rewritten; `chat_hidden` then `chat_shown`); the CHATS-row show keys call the same helper in focus, `gv chat show` semantics in horizontal. Refusals: shown chat (no-op), remote target, cockpit not running. e2e/focus.sh extended with a swap.
- [x] grove-487: focus-mode 03 — `focus` shows one chat, minimizes the rest. `L` into focus keeps the active chat (else the window's previous pane, else the last chat) and hides the others into `grove-chat-<label>-<n>` via chat-hide's primitives, remembering them in `@grove_focus_hid`; `L` back restores exactly those (hand-hidden chats stay hidden); `O`/`)`/digits/`gv orchestrator new` in focus minimize the shown chat first; rail rewritten after every move; `chat_hidden`/`chat_shown` carry additive `reason:"focus"`, appended after the move. Pure planner in `internal/tmux/focus.go` (table-tested), `e2e/focus.sh` (default + hostile) in `all.sh`.

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
