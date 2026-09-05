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
> **Append target:** this file holds the current month's entries. On
> the first entry of a new month, move last month's entries to
> `docs/archive/LEARNINGS-YYYY-MM.md` (same sections, newest first).
> Never delete an entry — move it. Grep `LEARNINGS.md
> docs/archive/LEARNINGS-*.md` for the full log (2026-06 holds the
> entries seeded from overstory-tui). Why-context (the narrative behind
> an era of work) lives in [docs/journal.md](docs/journal.md), which is
> never loaded into a session.

## Claude Code behavior (verified in ovs)

- **2026-09-04 · hook attribution is cwd + session id; a Bash `cd` moves
  the hook cwd** (grove-250; incident on unbrewed 2026-09-02). Hooks live
  in the profile's global `settings.json`, so EVERY session fires them and
  the receiver decides ownership — and it decided by cwd alone. Claude
  Code's hook `cwd` is the Bash tool's persistent shell cwd, not the
  directory the session launched in, so an orchestrator that ran one
  `cd <worktree> && …` had its next Stop attributed to that worker:
  `gv watch` emitted `idle` while the worker was 30 minutes into a busy
  turn, `gv ls` showed the orchestrator's own chat reply as the worker's
  `last_message`, and a stall monitor fired a false idle. The payload
  already carried `session_id` and the task already recorded its worker's
  (`claude_session_id`, folded from `session_started`); nothing compared
  them. Now `stop`/`notification`/`session-end` at a tracked cwd are
  dropped when the ids differ (silent, zero writes); `session-start` is
  exempt so an adopt's fresh pickup session can still register; a task
  with no recorded id keeps cwd-only attribution so an unknown id never
  makes a task unreachable. The brain rule "never `cd` into a tracked
  worktree from the orchestrator" stays as belt; this is the braces.

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
- **2026-09-01 · A parse-rule split between verbs is indistinguishable
  from breakage until it is taught** (grove-242). `gv nudge grove-236
  --host groveremote "rebase please..."` ran LOCALLY and failed
  `no active task grove-236 — see gv ls`: the flag was never parsed and
  nothing hinted why. The rule is deliberate, not a bug — relay verbs
  parse `--host` prefix-only by design (payload may legitimately contain
  `--host`, so `ExtractHostPrefix` stops at the first non-flag arg, the
  ticket) while `grab` scans the whole argv (`ExtractHost`, any position
  works). But the orchestrator had learned flag-order from the seed's own
  tools block — `gv grab grove-N --host H`, flag-after-ticket — and
  generalized it to a verb where the flag is silently swallowed into
  message text. Fix is teaching, not parsing: the local "no active task"
  error appends the position rule when the payload still carries a
  literal `--host` (pure argv inspection at the call site; the parse is
  untouched), and the seed's `--host` entry now says for answer/nudge the
  flag must come BEFORE the ticket. A verb-surface split like this must
  be taught in the seed or it reads as breakage.
- **2026-09-01 · The release path filter is part of the "shipped"
  definition** (grove-241). `release.yml` triggers on `cmd/**`,
  `internal/**`, `go.mod`, `go.sum` — and not on `orchestrator/**`, even
  though `orchestrator/CLAUDE.md` is embedded in the binary
  (`//go:embed`), so a seed-only merge changes what every `gv init`
  ships and what `gv brains` compares against yet cuts no release: the
  fixed seed exists on main and reaches no machine until an unrelated Go
  change happens to land. Field-proven 2026-09-01 on the #234 train —
  PR #238 (seed content only) merged 12:10:51Z with no Release run, and
  had it merged alone, `gv brains` everywhere would have reported every
  workspace current against a seed the binary never got. An embedded
  asset's directory missing from `on.push.paths` means
  merged-but-never-shipped, invisible until something else releases; the
  guard is a test (`orchestrator/release_test.go`) that reads the
  workflow file and fails without the line.
- **2026-09-01 · A workspace brain can be perfectly in sync with the seed
  and still be wrong** (grove-234). The field incident looked like brain
  drift and was not: the workspace's stamp (`6794db4eb15a`) matched the
  embedded seed byte for byte — the doc was current, the SEED was stale.
  `orchestrator/CLAUDE.md` is hand-maintained and nothing ties it to the
  verbs it documents, so `--host` (grove-176, nine verbs by grove-191) and
  `--profile` (grove-36) shipped without ever reaching it; grove-189 DID
  update the seed, which is the proof this is a remembered step, not an
  enforced one. Two failures fell out of one dispatch: the chat concluded
  "`gv grab` has no host flag" (it does — `internal/remote/remote.go`
  intercepts `--host` BEFORE the verb's flagset, so it is absent from
  every `-h` output, and a doc that isn't written is the only place to
  learn it) and dispatched by raw `ssh`; and it grabbed on a per-token
  `openrouter-*` lane while the flat-rate `zai-plan-*` twin was
  configured, because the lane distinction lived only in config comments
  and a repo-tracked skill marked "do NOT load for ordinary dispatch" —
  neither of which exists in another workspace. Rule: a flag that changes
  where work runs or who pays for it is not shipped until the seed says
  so; grove-190's stamp answers "is this brain behind the seed?", never
  "is the seed behind the binary?" (tripwire: #235).

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

