# Grove — learnings

> [TASKS.md](TASKS.md) is the status board · **LEARNINGS.md** is the
> surprises — anything discovered the hard way or verified against
> source, so we never re-derive (or re-break) it. Every entry is
> verified fact, not opinion.
>
> Entry format: `- **YYYY-MM-DD · the fact** — context, what it changed.`
> Newest first within each section. If a learning invalidates a
> DESIGN.md decision, update the doc and note it here. When an entry
> generalizes into a rule, update the matching `.claude/skills/` skill
> too — the skills are the distillation, this file is the dated log.
>
> **Append target — never open this file to add an entry.** When surprised:
>
>     scripts/log-append.py learnings --section "Go / CLI" <<'EOF'
>     - **YYYY-MM-DD · the fact** — context, what it changed.
>     EOF
>
> (sections: `Claude Code behavior (verified in ovs)` · `tmux / git /
> detector internals (verified against source)` · `Go / CLI` · `Remote /
> attach architecture (verified against t3code source)` · `Field notes
> (ovs, kept for judgment)`). That puts the entry at the top of its section
> and, if the head is over its cap, moves the OLDEST entries into
> `docs/archive/LEARNINGS-YYYY-MM.md` (their own month; same sections).
> Nothing is ever deleted; 2026-06 holds the entries seeded from
> overstory-tui. Looking for one? `grep -r <term> LEARNINGS.md
> docs/archive/` — don't read the archive in. `internal/guidance` fails
> `go test ./...` when this head is over its cap. Why-context (the
> narrative behind an era) lives in [docs/journal.md](docs/journal.md),
> never loaded into a session.
> <!-- head-cap: 16384 -->

## Claude Code behavior (verified in ovs)

- **2026-09-06 · GLM 5.3 Flash via the Anthropic protocol returns zero
  text unless `thinking` is disabled** (grove-288, `gv sub` bake-off
  against api.z.ai). A `/v1/messages` call with no `thinking` field (or
  `thinking` enabled) came back with an empty `content` text block —
  `gv sub`'s raw mode sends `"thinking":{"type":"disabled"}` by default
  for exactly this reason (`sub.thinking: false`).
- **2026-09-06 · `claude -p --output-format json` may print
  `[claude-code:…]` warning lines before the JSON on a third-party lane**
  (grove-288). A model slug the lane doesn't recognize (
  `[claude-code:unrecognized_model]`) prints to stdout ahead of the JSON
  payload — a consumer must skip to the first `{` rather than
  `json.Unmarshal` stdout directly.
- **2026-09-06 · `claude -p --bare` works with `ANTHROPIC_BASE_URL` +
  `ANTHROPIC_AUTH_TOKEN` on any Anthropic-protocol endpoint, no OAuth
  needed** (grove-288, verified against api.z.ai). `--bare` skips hooks,
  CLAUDE.md, skills, and MCP, so a read-only agentic `gv sub --agentic`
  call never touches this workspace's own context or config.
- **2026-09-05 · `run_in_background` notifies on EXIT, so it can never
  watch an unbounded stream** (grove-273; verified against the Bash/Monitor
  tool contracts, not a live incident). `gv watch --json` with no
  `--until` never exits — under `run_in_background` the agent gets exactly
  zero notifications and simply waits, which looks identical to a quiet
  fleet. The tool split is by NOTIFICATION COUNT, not by duration: one
  notification when a command exits → `run_in_background` (the `--until`
  form, which does exit); one notification per stdout line, indefinitely →
  a Monitor. This is the whole reason an unattended supervision mandate
  says "as a Monitor" out loud — an orchestrator that reaches for the
  familiar background-Bash tool sleeps through the night it was woken to
  work. Fixed the Monitoring section's wording (it had them backwards) and
  spelled the mapping out in `## Supervision mandate`.
- **2026-09-05 · the ntfy topic is `notify.ntfy` in `config.yaml`, not a
  var in `.env`** (grove-273, correcting the ticket's own premise).
  `internal/config.Notify` is `{ntfy, ntfy_body}` read out of
  `~/.config/grove/config.yaml` — the GLOBAL file, always;
  `~/.config/grove/.env` holds provider API keys only. Not the workspace
  file, and that is the sharper trap: `notify` DOES merge field-wise
  through `LoadAt` (merge.go:20 names it), but `NotifySettings` never
  calls `LoadAt` — it is `NotifySettingsFrom(Dir()/config.yaml)`, and
  `Dir()` is `$HOME/.config/grove` unconditionally (config.go:183 — no
  env override, unlike `StateDir`). So the workspace layer governs
  everything EXCEPT the push path, and a `notify:` block in a workspace's
  `.grove/config.yaml` is read by nothing. A chat cannot call
  `internal/notify`, so the seed teaches the equivalent `curl` — and it
  has to read the topic from the right file or the push is silently never
  sent (`notify.Push` returns early on an empty topic; every failure path
  in it is silent by design). `ntfy_body: title-only` is the second var
  and it matters: it drops the body, so the title must carry the meaning.

## tmux / git / detector internals (verified against source)

- **2026-09-05 · macOS login-shell panes run `path_helper` and silently
  reorder PATH out from under a faked binary** (grove-230): `e2e/cockpit.sh`
  faked `ssh` on `$PATH` and typed `gv dash` into a tmux pane via
  `SendKeys`, but tmux spawns a pane's shell as a LOGIN shell whenever
  `default-command` is empty — on macOS that runs `/etc/zprofile`, which
  runs `/usr/libexec/path_helper` and rebuilds `PATH` from `/etc/paths`,
  pushing the scratch bin (and its fake ssh) behind the real one. The
  cockpit then shelled out to the REAL `ssh`, hit host-key verification,
  and the `R` merge never saw the `@pc` row — read as a fixture/timing bug
  for a week before the pane's own shell turned out to be the cause.
  Linux has no `path_helper`, so this class is invisible on groveremote.
  Fix is script-only: `tmux -f <conf> start-server` with
  `set -g default-command "$SHELL"` (a non-empty default-command runs the
  shell directly, skipping the login-shell flag and `/etc/zprofile`
  entirely) before anything sends keys into a pane. Any e2e that fakes a
  binary for a pane-launched process needs this — a session grep for
  `command -v <faked-binary>` in a throwaway pane is the tripwire to add
  alongside it.
- **2026-09-05 · `mktemp -d /tmp/...` is not the scratch root on macOS —
  `pwd -P` it** (grove-230, same class as grove-228's chat.sh bug):
  `e2e/brains.sh` asserted against the raw `mktemp` path, but `gv brains`
  prints the registry root realpath'd, and macOS's `/tmp` is a symlink to
  `/private/tmp`. Every e2e scratch root must be resolved with
  `SCRATCH="$(cd "$(mktemp -d ...)" && pwd -P)"` up front, not compared
  against post-hoc.

## Go / CLI

- **2026-09-05 · An unstreamed dimension gets re-derived once per
  orchestrator, badly** (grove-252). `gv watch` (grove-205) streamed hook
  events, but nothing streamed delivery (PR opened/CI failing/conflicting/
  ready/merged) or the liveness a Stop hook cannot see (an AskUserQuestion
  menu, a bare shell after a silent death, a 429 plan cap, a sleep-cut
  turn — each leaves `agent: working` with no sentinel). The gap didn't
  stay empty: `~/git/unbrewed/.grove/` grew **30** hand-rolled
  `monitor-*.sh` scripts, each polling `gv ls --json` + `gh pr view` +
  `tmux capture-pane` on its own 90–300s loop with its own hysteresis,
  each written after `gv watch` already existed — because the thing they
  needed to watch had no supported subscription. The sentinel histogram
  on unbrewed makes the shape of the miss visible: 754 `done`, 24
  `question`, 16 `blocked` — the states that actually need a human
  (a stuck menu, a dead worker, a capped plan) essentially never produce
  a hook sentinel at all. Fixed by building the missing half as a pure
  engine (`internal/supervise.Transitions`) rather than another poller:
  DESIGN.md principle 4 says the supervisor loop is "fixed and
  enumerable, so it is code", and a dimension nobody streams is exactly
  where ad-hoc bash accretes.
- **2026-09-05 · A transition engine must diff against folded state, not
  emit on every observation** (grove-252). The naive shape — "poll PR/pane,
  append what you see" — double-fires the instant two pollers exist (a
  cockpit driver and an operator's own `gv supervise` loop) or a poller
  restarts mid-fleet. `Transitions(Observation, *Memory)` instead compares
  the freshly derived delivery/liveness state against `Task.Delivery`/
  `Task.Liveness` (the fold of prior events) and emits only on an actual
  change — re-observing the same state, from any number of concurrent
  readers, emits nothing. The hysteresis timers (10s waiting-debounce, 60s
  vanished-debounce + 120s boot grace) live in a caller-owned `Memory`
  that is explicitly never persisted: a restart just re-arms the timers,
  delaying the next transition by one debounce window rather than ever
  fabricating or losing one. Generalizes past this ticket: any future
  poll-derived dimension should fold-and-diff the same way, not append
  raw snapshots.
- **2026-09-05 · An idempotent engine driven by a reader whose fold can
  lag its own appends still double-emits — unless it remembers what it
  emitted** (grove-254). `Transitions` diffs against the folded
  `Task.Delivery`/`Task.Liveness`, which is airtight for `gv supervise`
  (peek → append → peek, strictly serial). The cockpit is not serial: an
  ad-hoc `refreshCmd` (an answer, a review, a done) can be in flight —
  its fold read BEFORE the prsMsg handler appended `pr_opened` — and
  deliver a task that still says `none` a tick later, so the engine would
  derive and append the same `pr_opened` again. Rather than special-case
  the cockpit, `supervise.Memory` now shadows the state of the last event
  it emitted per ticket, stamped with the event time, and diffs against
  the shadow only while it is strictly newer than the fold's own `At`; the
  moment the fold carries the same stamp it is authoritative again (and a
  fold that is newer than the shadow wins outright, so a headless holder's
  appends are never masked). Two consequences worth keeping: the memory
  is still never persisted (a restart's first observation is against the
  real fold, which is correct), and `e2e/cockpit.sh` asserts "exactly
  once" by counting types in `events.jsonl` after an `r` re-poll, not by
  reading the screen. Generalizes: any bubbletea driver that appends from
  one message handler and folds from another must dedupe on its own
  emissions, because message arrival order is not read order.
- **2026-09-05 · A pure transition engine still needs exactly one
  single-emitter lock, not "trust the caller"** (grove-253). Part 2
  (`internal/supervise.Transitions`) is idempotent by construction — two
  readers observing the same state emit nothing twice — but a headless `gv
  supervise` loop and part 4's future cockpit driver are two *processes*
  that could both run `state.Append` in the same tick, which is fine for
  `events.jsonl` (flock-serialized) but means every consumer sees each
  transition's human row printed twice. Rather than special-case "am I the
  cockpit" logic, `gv supervise` takes a plain non-blocking
  `flock(LOCK_EX|LOCK_NB)` on `<state>/supervise.lock` at startup and
  writes its own pid into it; a second caller's `Flock` fails immediately
  (no polling, no timeout) and it reads the pid back out to name the
  holder in its error. The lock needs no cleanup path for a crashed
  holder — `flock` is released by the kernel the instant the holding
  process's last fd closes, crash or clean exit alike — so "stale lock
  from a dead pid" was never a case to handle. Generalizes: any feature
  that must run as at-most-one-instance-per-directory (this ticket, and
  probably part 4) is this same four-line pattern, not a pidfile-plus-
  liveness-check.
- **2026-09-05 · A stub tmux pane for a liveness e2e must be a redrawing
  loop, not an `echo`** (grove-253). Every prior scripted-tmux suite
  (`watch.sh`, `dummy.sh`, …) sets a repo's `claude:` to plain `echo` —
  it prints the kickoff prompt once and the pane goes back to a bare
  shell, which is fine when the suite never reads pane CONTENT. `gv
  supervise`'s liveness dimension (`detect.DetectLiveFrom`) reads exactly
  that content, so `e2e/supervise.sh` needed a persistent process that
  redraws a claude-shaped screen (idle prompt / an "Enter to select" menu
  / a `429`+`API Error:` line) on command from a control file the script
  rewrites mid-run — an infinite loop doing `printf '\033[H\033[2J'` +
  the current mode's lines + `sleep 0.3`, driven by a plain file the test
  writes between assertions. `pickPane`'s highest-index fallback resolves
  it as "the claude pane" without the script needing to look like a real
  `claude` process (`pane_current_command` reports `bash` either way,
  same as every other worker pane in this suite family).
- **2026-09-04 · A pricing table that silently prices a whole generation
  at $0 stays invisible for five weeks unless the $0 path is loud**
  (grove-249). `defaultRates` had no `claude-opus-5` key — `rateFor`'s
  prefix match only strips a trailing `-<suffix>` and `claude-opus-4` is
  not a `-`-boundary prefix of `claude-opus-5`, so every Opus 5 worker
  (213 unbrewed rows, 19 waterhouse, 2 grove) resolved `cost_known:
  false` and `est_usd: 0`. unbrewed's ledger read July $2,457 → August
  $13 on unchanged ticket volume (154 tasks in August) — that drop was
  the pricing gap, not savings, and nobody caught it because `fmtUSD`'s
  `~$` prefix on unknown-cost rows reads as "approximately", not "this is
  actually zero and wrong." Separately, `claude-fable-5-1` had no key
  either but *did* resolve — by accident, riding `claude-fable-5`'s
  prefix match — and silently used the wrong cache-read rate (10% of
  input instead of Fable 5.1's actual 2.5%). Fix: add the missing/wrong
  keys, and make unknowns loud instead of quiet — a `currentGeneration`
  tripwire test asserts each current model id has an EXACT table entry
  (not just a resolving `rateFor`), and `gv cost` / `gv cost --analyze`
  now print an `⚠ unpriced: <model> — N tickets, M turns` footer instead
  of leaving $0 rows to blend into the totals. Lesson: any code path that
  can legitimately return "zero, and I don't actually know" must render
  differently from "zero, confirmed" — a soft prefix marker is not
  enough to get a human to look.

## Remote / attach architecture (verified against t3code source)

## Field notes (ovs, kept for judgment)

- **2026-09-05 · "last 30 days" is not a small head — rotate the logs by
  month, and the brain seed is three quarters of an orchestrator's
  resident context** (grove-275). Measuring before restructuring:
  TASKS.md rows from the previous 30 days were ~62k bytes (August alone
  50k) and LEARNINGS.md ~42k, so a 30-day head would have been 3–4× the
  ~15k target — a calendar-month head + `docs/archive/<FILE>-YYYY-MM.md`
  is the unit that stays small and is trivially greppable. Of the 24.5k
  that lands in every orchestrator chat, 18.7k was the seed (the `gv`
  table restating `-h`, dated war stories, duty prose); only ~2.1k of a
  worker's resident context is skill frontmatter, so tightening
  descriptions buys little — the wins are in the two CLAUDE.md files and
  in never instructing a session to read a 70k log to append one line.

