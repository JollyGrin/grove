# Grove — status board

> **TASKS.md** is the status board · [LEARNINGS.md](LEARNINGS.md) is the
> surprises · [docs/roadmap.md](docs/roadmap.md) is the open phases.
> Fresh pickup? Read [HANDOFF.md](HANDOFF.md) first.
>
> Grove dogfoods itself: the backlog is GitHub issues on this repo
> (`grove-N` = issue #N), worked by grove workers — issue → `gv grab
> grove-N --repo grove` → PR → merge → `gv done`.
>
> **Append target:** when you ship, add one `- [x]` row at the TOP of
> §Now (newest first, `(grove-N, YYYY-MM-DD)` in the first line). This
> file holds the open rows plus the current month; on the first row of
> a new month, move last month's shipped rows to
> `docs/archive/TASKS-YYYY-MM.md` (same format, newest first). Never
> delete a row — move it. Grep `TASKS.md docs/archive/TASKS-*.md` for
> the full log.

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
      provably redundant). PR left open for review.
- [x] Unattended 3/4: `gv supervise` as a user systemd unit on the remote
      host — docs + unit file (grove-272, 2026-09-05). Docs only, no Go.
      `docs/remote-host-setup.md` gains a **§Sidecars: user systemd units**
      between the workspace-twins and Mac-side sections, documenting both
      long-running host processes as `~/.config/systemd/user/` units:
      `gv-chat.service` (the phone UI, registry-driven, `WorkingDirectory=%h`,
      one per host) and `gv-supervise.service`
      (`ExecStart=%h/go/bin/gv supervise --interval 30s`,
      `WorkingDirectory=%h/git/grove`, `Restart=on-failure`/`RestartSec=10`,
      `EnvironmentFile=-%h/.config/grove/.env`). Four things the section
      pins down: the cwd IS the config — supervise is ambient-workspace
      scoped with no `--workspace`, so **one unit per supervised
      workspace**; ntfy is NOT env — `notify.Push` reads the `notify:` block
      from the **global** `~/.config/grove/config.yaml` via
      `config.NotifySettings` whatever workspace it stands in, so a
      workspace-only block is silently ignored (falsified at runtime, not
      just read: `config.Dir()` stays `~/.config/grove` from inside a
      workspace, and a workspace-only topic gets zero POSTs against a local
      sink while a global one fires) and the `.env` (leading `-`, optional)
      is only the model-profile secrets; the grove-253
      single-emitter `flock` means anything else that tries exits 1 with
      `gv: already supervised (pid N)` — verified live — which is the lock
      working, not a bug — and since grove-254 the cockpit arbitrates the
      same lock, so a cockpit opened over ssh under the unit renders
      `⟳ supervised by pid N ·` in its header and never appends
      (`systemctl --user stop` hands the emitter role back); and
      `systemd --user` never reads `~/.profile`, hence the absolute
      `%h/go/bin/gv` (its `gh`/`git`/`tmux` callees are all in `/usr/bin`).
      The after-every-update note restarts BOTH units in one line, since the
      `ExecStart` path is fixed and the running process holds the old code.
      Two more traps the live dry-run turned up and the section now names:
      a quiet fleet logs NOTHING (supervise prints only on a transition, so
      `journalctl` showing just systemd's `Started …` is healthy — the
      on-demand check is stop-unit → `gv supervise --once --json` →
      start-unit), and `loadCfg` runs BEFORE the lock, so a crash-loop right
      after `enable --now` is a config error, not a supervision one. Both
      unit files pass `systemd-analyze verify` clean. `enable-linger` bullet
      and the §Phone paragraph rewritten to point at it;
      `docs/GETTING-STARTED.md`'s remote aside gains the one-sentence
      pointer. Live install on groveremote + the ntfy round-trip are
      operator-side (no key to that host from the worker).
- [x] Supervisor train 4/4: the cockpit drives the engine on its existing
      beat (grove-254, 2026-09-05). While the cockpit is open it IS the
      supervisor: `refreshMsg` now carries each task's `detect.LiveInfo`
      (a third return from `liveStates`, consumed in Update and dropped)
      and `prsMsg` its PR poll, and both are fed to
      `internal/supervise.Transitions` — liveness on the 1s refresh,
      delivery on the 30s/`r` PR poll with `PRKnown` from `prsCmd`'s
      unknown map so a gh outage emits nothing — with the results
      `state.Append`ed for the folder to pick up next tick. Computed in
      Update on message arrival, never in View; no new goroutine, poll,
      timer, or cache (the only additions are the engine's `Memory` and the
      flock; `TestViewAllocsFlatUnderSupervision` pins the frame). Lock
      arbitration both ways: `Run` takes `<state>/supervise.lock` before
      the program starts and releases on `q` (and before the `X` park's
      kill, re-taking it if the kill never happened); a headless holder
      makes the cockpit render `⟳ supervised by pid N` in the header and
      emit nothing, and a `gv supervise` under an open cockpit gets the
      existing refusal. Pushes go through the table moved to
      `supervise.Push`/`PushClass` (shared with `gv supervise`); the
      footer flash gets `✓ grove-98 ready — 4 checks green`,
      `pr_ci_failed`, `pr_conflicting`, `worker_*`. The engine's `Memory`
      gained a shadow of what it last emitted so a stale in-flight
      refresh can never make the cockpit re-emit (LEARNINGS).
      `e2e/cockpit.sh` drives the live TUI with the fake `gh`: none →
      open → ready → merged lands each event exactly once, `gv supervise`
      alongside is refused naming the cockpit's pid, `q` frees the lock;
      both tmux-conf modes.
- [x] Unattended train 1/4: `gv orchestrator new --brief T` /
      `--brief-file F` — a standing brief as the chat's FIRST user message
      (grove-271, 2026-09-05). The goal the train serves: dispatch from
      the Mac, close the lid, and a remote orchestrator keeps watching N
      workers under a mandate — which needs a way to say what the mandate
      IS at spawn time, before anyone can type into the pane. The brief
      is handed over the way a worker's kickoff prompt is (`claude …
      "$(cat <path>)"`, main.go:1583): written to
      `<orchDir>/briefs/<session-id>.md` — named by the id, so it is
      still findable from the conversation long after it scrolls off —
      and appended to the BARE launch, ahead of `wrapOrchestratorLaunch`,
      because the profile wrap ends in `exec <cmd> )` and anything after
      that is the shell's argument, not claude's (the `--resume` rule,
      re-applied). All three spawn shapes carry it: the cockpit pane, the
      `--workspace` detached chat, and the `--host` hop, where only the
      TEXT travels (`--brief-file` is read on the calling side — a path
      is local knowledge) and `--brief` goes LAST in chatHopArgs /
      chatManualRetry, so a retry's argv stays byte-equal to the hop it
      repeats and the op-id receipt stays trustworthy. `remote.Quote`
      single-quotes it, so a three-line brief holding an apostrophe
      survives the ssh hop byte-for-byte (`e2e/chat.sh` asserts exactly
      that, against a claude stand-in that records its own argv). Refused
      rather than guessed: `--brief` + `--resume` (a revival already has
      a conversation — the brief would land as an unrelated turn),
      `--brief` + `--brief-file`, and an empty brief from either door.
      The phone UI's POST new is unchanged.
- [x] Supervisor train 3/4: `gv supervise` — the headless loop (grove-253,
      2026-09-05). Part 2 built the pure transition engine; nothing ran it
      yet. Each pass: `state.Peek` (read-only fold) → `state.Active` → one
      `tmux.SnapshotSession` per distinct session + one
      `detect.DetectLiveFrom` per task (the grove-149 shape, never the
      stateless per-task exec) → one `github.FetchAll` round-trip → one
      `internal/supervise.Transitions` per task → `state.Append` for
      whatever fired, printed exactly like `gv watch` (or the raw record
      with `--json`). A non-blocking `flock` on `<state>/supervise.lock`
      (new `supervise.Lock`) makes it single-emitter: a second `gv
      supervise` — or part 4's future cockpit driver — exits 1 naming the
      pid already holding it, so a cockpit and a headless loop can safely
      coexist without double-emitting (the cockpit does not emit yet).
      `--once` runs one pass and exits 0: hysteresis lives in-process, so a
      single pass can still emit delivery/`worker_errored` events but never
      `worker_waiting`/`worker_vanished` (those need a running loop to
      accumulate the debounce window) — documented in `--help`. `pushNtfy`/
      `notify`/`ntfySettings` moved out of `internal/hooks` into a new
      `internal/notify` (exported `Push`/`Desktop`, pure move — hooks'
      behavior and every `TestNtfy*` test are unchanged) so `gv supervise`
      can push the same way per docs/plugins.md's table (high·warning for
      worker_waiting/vanished/errored, high·x for ci_failed/conflicting,
      default·white_check_mark for pr_ready, default·tada for pr_merged,
      nothing for pr_opened/updated/closed/worker_recovered) — body is
      `watch.Detail` (newly exported), the same trailing tail `gv watch`
      prints. `e2e/supervise.sh` (added to `e2e/all.sh`) proves the full
      11-step contract against a fake `gh` (answer rewritten between
      steps) and a controllable claude-shaped stub pane. Orchestrator seed
      taught `gv supervise` + the "never write a monitor script" rule with
      all 11 types, one line each; `orchestrator/seed_test.go` gained a
      tripwire.
- [x] Supervisor train 2/4: the transition engine — delivery + liveness
      state machines, 11 event types, fold, `watch --until <type>`
      (grove-252, 2026-09-05). DESIGN.md principle 4 says the supervisor
      loop is "fixed and enumerable, so it is code"; grove only ever built
      the hook half. unbrewed alone carries **30** hand-rolled
      `monitor-*.sh` scripts polling `gv ls --json` + `gh pr view` + `tmux
      capture-pane` because nothing streamed PR state or the liveness a
      Stop-hook sentinel can't see — field incidents behind them: p2p#691
      sat on an AskUserQuestion menu 2h15m unseen, three silent worker
      deaths in 24h, a 429 plan cap with the fix uncommitted, a
      sleep-cut turn. New `internal/supervise` (pure, no poller, no
      goroutine — part 3 runs it): `Transitions(Observation, *Memory)
      []state.Event` derives delivery (`none → opened → ci_failed
      /conflicting/ready → merged/closed`, from the #251 PR facts) and
      liveness (`ok → waiting/vanished/errored`, from a tmux pane read)
      as **transitions, not observations** — an event fires only when the
      derived state differs from the task's folded state, so two pollers
      or a restart re-emit nothing. Hysteresis (10s waiting debounce, 60s
      vanished debounce + 120s boot grace) lives in the caller-owned,
      never-persisted `Memory`. `internal/state` gains `Task.Delivery`/
      `Task.Liveness` (additive, nil-means-none/ok) folded from the 11 new
      event types, plus an internal (`json:"-"`) `LiveSince` the liveness
      engine's boot grace keys off. `internal/detect` adds the
      AskUserQuestion menu markers (`enter to select`/`ready to submit`)
      the pre-existing waiting patterns missed — the exact shape behind
      the p2p#691 stall — via a new `WaitingMarker` helper (seed-manifest
      divergence). `gv watch`'s type vocabulary and default set gain all
      11 types with human-row rendering, and `--until` now accepts an
      event type as well as a sentinel (`--until pr_merged`, `--until
      worker_waiting`). Nothing in the fleet changes behavior yet — no
      poller runs until grove-253.
- [x] `e2e/cockpit.sh` + `e2e/brains.sh` green on macOS (grove-230,
      2026-09-05): `cockpit.sh`'s `R merge missing the @pc row` was a real
      `ssh` shelled out to — tmux spawns a pane's shell as a LOGIN shell by
      default, and macOS's `/etc/zprofile` → `path_helper` rebuilds `PATH`
      from `/etc/paths`, pushing the script's faked `ssh` behind the real
      one before the cockpit's typed `gv dash` ever runs. Fix is
      script-only: the isolated server now boots (both default and hostile
      conf modes) with `default-command "$SHELL"` so panes run non-login,
      plus a new early assertion that a throwaway pane resolves `ssh` to
      the scratch bin. `brains.sh` had the grove-228 chat.sh bug in
      miniature — asserted against the raw `mktemp` path instead of
      `pwd -P`'d — one-line fix. No `internal/`/`cmd/` changes; both
      suites plus `e2e/all.sh` verified green on the Mac.
- [x] Supervisor train 1/4: `github.PR` gains `draft`, `mergeable`,
      `merge_state`, `failing` (sorted check names) and `checks`
      (grove-251, 2026-09-04) — the transition engine (part 2) needs these
      facts to decide `pr_ready`/`pr_ci_failed`/`pr_conflicting`, and none
      of them were fetched before. `PRForBranch` pulls `isDraft,mergeable,
      mergeStateStatus` and the check `name`/`context` fields in the same
      `gh pr list` call; `TIMED_OUT`/`CANCELLED`/`ACTION_REQUIRED` now
      count as CI failures too (closes #124 item 1: a cancelled-only-check
      PR used to read `ci: pass`). `FetchAll` now returns
      `(prs, unknown map[string]error)` so a failed/timed-out lookup can
      never be read as "no PR" — `gv ls --json` rows carry the additive
      `pr_known` bool, and the human table + cockpit render `?` instead of
      a blank PR column when it's false.
- [x] Hooks gate stop/notification/session-end on the recorded session
      id (grove-250, 2026-09-04): the receiver attributed every hook by
      cwd alone, and Claude Code's hook `cwd` follows the Bash tool's
      persistent shell cwd — so one `cd <worktree> && …` in an
      orchestrator made its next Stop look like the worker's. Verified
      live on unbrewed 2026-09-02: a worker 30 minutes into a busy turn
      was stamped `idle` with the orchestrator's chat reply as its
      `last_message`, and a stall monitor fired on it; the same mechanism
      is #148's late SessionEnd stamping `dead` over a live successor.
      `hooks.Receive` now drops `stop`/`notification`/`session-end` when
      the task has a recorded `claude_session_id` and the payload's
      differs (zero writes, exit 0 — the receiver's silence contract);
      `session-start` stays exempt (it is how a worker registers, adopt's
      fresh pickup session included); a task with no recorded id keeps
      cwd-only attribution. Additive contract field: the three event types
      gain `data.session_id`. Five unit cases + a dummy.sh intruder leg.
- [x] `claude-opus-5` priced, `claude-sonnet-5` re-priced, fable-5.1/
      mythos-5.1 given exact keys (grove-249, 2026-09-04): every current
      Opus 5 worker read `cost_known: false` and `est_usd: 0` —
      `defaultRates` had no `claude-opus-5` key at all, `claude-sonnet-5`
      was priced $3/$15 against a live $2/$10, and `claude-fable-5-1`
      only resolved by accidentally riding `claude-fable-5`'s prefix
      match, so its 2.5%-of-input cache-read rate silently used the
      wrong (10%) formula. unbrewed's ledger read July $2,457 → August
      $13 on unchanged volume — that was the pricing gap, not savings.
      Fixed the table (prices + fetch-date comment), added a
      `currentGeneration` tripwire test that fails if any current model
      id loses its exact key, and made unpriced models loud: `gv cost` /
      `gv cost --analyze` print an `⚠ unpriced: <model> — N tickets, M
      turns` footer, and `--analyze --json` gains additive
      `unpriced_models`.
- [x] Relay verbs teach the `--host`-after-the-ticket trap in their local
      error (grove-242, 2026-09-01): `gv nudge grove-N --host H "..."` ran
      LOCALLY and failed with a bare `no active task grove-N — see gv ls`
      — the flag was silently swallowed into message text and nothing
      hinted why. The rule is deliberate (answer/nudge parse `--host` in
      leading-flag position only, so free text may contain `--host`;
      `grab` scans the whole argv) and the parse is NOT changed: when the
      local miss leaves a literal `--host` token in the payload,
      `remote.PostTicketHostHint` appends the position rule to the error
      (it must come BEFORE the ticket; everything after it is payload),
      the seed's tools block gains the same rule, and the #235 seed
      tripwire asserts the phrase stays.
- [x] release.yml releases on `orchestrator/**` (grove-241, 2026-09-01):
      the auto-release trigger only listed `cmd/**`, `internal/**`,
      `go.mod`, `go.sum` — but `orchestrator/CLAUDE.md` is embedded in the
      binary (`orchestrator/embed.go`, `//go:embed`), so a seed-only merge
      changed what every `gv init` ships and what `gv brains` compares
      against yet cut no release. Demonstrated on the #234 train: PR #238
      (seed content) merged 2026-09-01 12:10:51Z with no Release run; the
      train only shipped because #237/#239 touched `internal/` minutes
      later. One path line fixes it; `orchestrator/release_test.go`
      tripwires the line so it cannot be dropped again.
- [x] `gv update` sweeps every workspace's orchestrator brain (grove-236,
      2026-09-01): `gv update` swaps in a binary carrying a NEW embedded
      seed and then says nothing, so every workspace on the box silently
      keeps running an older brain — grove-190 built the refresh path but
      it is per-workspace, opt-in and manual, and nothing walks the
      registry. Measured on the Mac: of four brains, three UNSTAMPED
      (hand-managed, so the refresh will not even write a `CLAUDE.md.new`
      without a flag the operator has never typed) and one current. New
      **`gv brains [--json]`**: a pure read that walks
      `~/.config/grove/registry.yaml`, plans each root against the seed
      THIS binary embeds, prints only the workspaces that need attention
      (`cd <root> && gv init --only orchestrator-md[ --force-…]`) and
      collapses the rest to `✓ N workspaces current` — it has to stay
      readable at 11+ workspaces or it gets scrolled past, which is the
      failure. A registered root that has vanished is a `missing-root`
      ROW, never an error: one stale entry must not cost the other ten
      their sweep. `gv update` runs it **only after a real replace** (new
      `update.Options.Applied`), so a routine `gv update --yes` on a
      current box stays silent, and never fails the update. It shells out
      to the REPLACED binary rather than sweeping in-process — this
      process still holds the OLD seed, so an in-process sweep would
      cheerfully report every workspace current against the seed just
      superseded. Cross-machine falls out for free: `gv update --yes` over
      ssh reads that host's own registry. Report-only throughout —
      grove-190's invariant is untouched, grove never overwrites a brain.
      Planning is pure (`bootstrap.PlanSweep` over `BrainProbe`s, table
      tested); `e2e/brains.sh` byte-compares both brains before and after
      and fails on any `CLAUDE.md.new`.
- [x] Seed brain learned `--host` and billing lanes (grove-234,
      2026-09-01): the embedded orchestrator seed had **zero** occurrences
      of `--host` and said nothing about model-profile lanes, though
      grove-176 shipped `gv grab --host`, grove-191 extended it to nine
      verbs (+ `orchestrator new`), and grove-36 shipped `--profile`. A
      freshly-`gv init`'d workspace whose brain stamp matched the seed
      EXACTLY still dispatched by raw `ssh` (§9 presented `gv handoff` as
      the only route to another host, and handoff correctly refuses a task
      with no PR body) and grabbed on the pay-per-token
      `openrouter-glm-flash` lane while the flat-rate `zai-plan-glm-flash`
      lane was configured with its key present. Content-only fix to
      `orchestrator/CLAUDE.md`: the tools block gains `gv grab --host`
      (with the note that `--host` is intercepted before each verb's
      flagset, so it never appears in `-h` output) and `gv grab
      --profile`; duty 3 gains a **Remote dispatch** paragraph (grab
      `--host` starts fresh work; handoff MOVES running work; the remote
      resolves `--repo` against its OWN config) and a **Lanes cost
      different money** paragraph (`zai-plan-*` flat-rate vs
      `openrouter-*` per-token, same model under two prefixes, name the
      lane when proposing a grab); duty 9 is rescoped to moving running
      work. The stamp moves by design. Tripwire is #235; propagation to
      already-seeded workspaces is #236.
- [x] `gv update`'s dev-build refusal stops telling the operator to
      `go install ./cmd/gv` (grove-233, 2026-09-01): `ErrDevBuild` now
      names the escape hatch that actually works — `gv update --yes
      --force` — instead of the one thing (`go install`) that re-stamps
      the binary `dev` and guarantees the next refusal too. Doc comment
      rewritten to state the rule rather than the old inverted premise.
- [x] Tripwire: `remote.Supported` tied to the orchestrator seed
      (grove-235, 2026-09-01): `orchestrator/seed_test.go` fails the moment
      `internal/remote.Supported`'s verb set drifts from a golden list of
      verbs the seed is known to cover, naming the offending verb and what
      to edit. Two more tests guard the #234 content (`--host` taught >= 4
      times + the two key phrases; `zai-plan`/`openrouter-` each >= 2
      times) against silent deletion. `.claude/skills/shipping-gates/
      SKILL.md` gained a "Verb surface → the orchestrator seed" section:
      a new verb/flag/lane isn't shipped until the seed teaches it: sync
      with the seed is not the same as being correct.

## Open

Roadmap phases and parked ideas: [docs/roadmap.md](docs/roadmap.md)
(Phase 1 remainder: pack loading + drift detection · Phase 4 remainder:
hooks/inbox generalization + generic orchestrator brain with pack
overlay · Phase 5: learnings first cut · Phase 6: OSS polish → Grid pack
→ parity gate → ovs retirement). Parked-but-tracked side quests: mobile
cockpit v2 (issue #5), Obsidian live board (issue #9, design paused at
REVISE), remote overflow host (docs/remote-host-setup.md; train #176–#178).
