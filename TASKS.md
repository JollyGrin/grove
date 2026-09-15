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
- [x] `gv sub` 1/2: read-only micro-task on a `model_profiles` lane —
      raw `/v1/messages` or agentic `claude -p --bare`, prints only the
      answer (grove-288, 2026-09-07). New `internal/sub/` package
      (`lane.go`, `prompt.go`, `raw.go`, `agentic.go`, `ledger.go`) +
      `cmd/gv/sub.go`; `config.Sub` (field-wise merge); doctor row
      `sub:lane`; `docs/plugins.md` + the plugin-authoring skill gain
      `sub.jsonl` + the three `--json` rows; `e2e/sub.sh` (15 steps).
      Ticket B (kickoff/skill text teaching workers to delegate) is
      separate (grove-290).
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
- [x] Unattended train 2/4: the **supervision mandate** — a standing,
      scoped pre-authorization in the orchestrator brain (grove-273,
      2026-09-05). Every steer was propose-then-yes, so an orchestrator
      woken by a worker's question at 03:00 drafted a proposal and stalled
      until morning; the only pre-authorization precedent
      (dispatch-and-dismiss) is scoped to one turn. New `## Supervision
      mandate` section in `orchestrator/CLAUDE.md`, 84 lines, between
      Monitoring and Duties: a mandate exists only when the operator's
      message (usually the #271 `--brief`) carries BOTH halves — scope and
      an end condition — so a plain "watch grove-41" stays duty 4. It
      watches with one `gv watch --json` over the scope **as a Monitor**,
      never `run_in_background` (that tool notifies on exit; the stream
      has none — see LEARNINGS), never a pane grep. In scope, act-then-
      report: `gv answer` when the answer is derivable from the ticket,
      the PR, or the mandate text, `gv nudge` on `pr_ci_failed`
      (`data.failing` names the check) and `pr_conflicting`, and duty 8's
      checkpoint nudge → `gv pause`. Out, always: `done`, `untrack`,
      `adopt` (it can resurrect the rotted context), `sweep`, `handoff`,
      merges, issue closes, ticket comments, un-named grabs, and any
      unsure answer — each becomes an ntfy push and the mandate KEEPS
      RUNNING. `internal/notify` is Go, so the seed teaches the equivalent
      `curl`, reading the topic from `notify.ntfy` in config.yaml — NOT
      `.env`, which holds API keys only (the ticket had this wrong;
      hardcoding or misreading it means a silently-never-sent push, since
      `notify.Push` is silent on every failure). Guardrails gained the
      one-sentence carve-out so the two sections cannot contradict, and
      Monitoring's `run_in_background`/Monitor mapping was corrected in
      place. Tripwire `TestSeedTeachesSupervisionMandate` asserts INSIDE
      the section (a whole-file substring search would stay green after
      deleting it) and splits in-scope from out- at the `**Out of scope`
      boundary, plus a ≤95-line budget — the seed is resident in every
      orchestrator turn. Falsified both ways before shipping.
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

## Open

Roadmap phases and parked ideas: [docs/roadmap.md](docs/roadmap.md)
(Phase 1 remainder: pack loading + drift detection · Phase 4 remainder:
hooks/inbox generalization + generic orchestrator brain with pack
overlay · Phase 5: learnings first cut · Phase 6: OSS polish → Grid pack
→ parity gate → ovs retirement). Parked-but-tracked side quests: mobile
cockpit v2 (issue #5), Obsidian live board (issue #9, design paused at
REVISE), remote overflow host (docs/remote-host-setup.md; train #176–#178).
