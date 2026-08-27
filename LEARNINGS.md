# Grove — learnings

> Working docs: [DESIGN.md](DESIGN.md) is the what/why ·
> [TASKS.md](TASKS.md) is the status board · **LEARNINGS.md** is the
> surprises — anything discovered the hard way or verified against source,
> so we never re-derive (or re-break) it.
>
> Entry format: date · context · the fact · what it changed. Newest first
> within each section. If a learning invalidates a DESIGN.md decision,
> update the doc and note it here.
>
> **Distilled 2026-07-12** into `.claude/skills/` (tmux-discipline ·
> shipping-gates · claude-code-facts) so workers load the rules
> automatically. This file stays the dated log of record; when adding an
> entry that generalizes into a rule, update the matching skill too.
>
> **Seeded 2026-07-03** from overstory-tui's LEARNINGS.md (@ `8c2f4f0`) —
> the *generic* entries only; each was verified live in ovs. Grid-specific
> entries (Linear pipeline rules, monorepo deploy semantics, worktree:setup
> codegen gap) stay with ovs and will move into the Grid pack's L5 layer.
>
> **Why-context lives in [docs/journal.md](docs/journal.md)** — the dated
> narrative of why an era of work happened (arcs, not facts). Not loaded
> into worker context; read it when an entry here or a TASKS.md row needs
> its backstory.

## Claude Code behavior (verified in ovs)

- **2026-07-09 · Claude Code clobbers tmux pane titles on boot** — it sets
  the terminal title via OSC ("✳ Claude Code" / its version string), so a
  `select-pane -T` tag survives only until boot. Durable per-pane tagging
  must live in a tmux pane **user option** (`set-option -p @grove_profile`),
  which foreground programs can't touch, rendered via a conditional
  `pane-border-format` (`#{?#{@grove_profile},⚡ #{@grove_profile},#{pane_title}}`).
  Field-hit: grove-36's first pane-title tag vanished the moment claude
  booted. Bonus verified the same day: Claude Code v2.1.205's statusline
  shows the real `ANTHROPIC_MODEL` slug (`z-ai/glm-5.2`), so in-app model
  visibility is real once the env override is set — only the model-class
  display name ("Sonnet 5") lies.
- **2026-07-09 · `--continue` chains key on cwd** — running a profiled
  orchestrator in a per-profile subdir (`.grove/orchestrator/<profile>/`)
  gives each backend its own conversation chain, and CLAUDE.md still
  applies (memory loads recurse up ancestor dirs). Without this, a fresh
  GLM pane `--continue`d the newest *Claude*-created conversation at ~100%
  context — wrong model resuming the wrong brain.
- **2026-07-02 · resume durability** — `claude --resume <id>` still works
  ≥6 days after the tmux window died, provided the transcript dir
  survives; the resumed session fires **SessionStart with the SAME
  session_id**, so hook re-capture needs no special-casing. Transcripts
  key on the encoded cwd (`<CLAUDE_CONFIG_DIR>/projects/<encoded-path>/`):
  reusing the same worktree path preserves resumability; re-creating the
  worktree at a new path orphans the transcript → pickup-prompt fallback.
  A resumed session opens idle awaiting input — it does not auto-continue.
- **2026-06-10 · hook payloads (verified live)** — Stop carries
  `session_id`, `cwd`, `transcript_path`, `permission_mode`, AND
  **`last_assistant_message`** — sentinel classification needs no
  transcript parsing (transcript stays the fallback for the full-message
  view). SessionStart carries `session_id` + `cwd` + `source`. Gotcha:
  `cwd` arrives realpath'd (`/tmp/x` → `/private/tmp/x`) — task matching
  must compare `filepath.EvalSymlinks` of both sides.
- **2026-06-10 · questions arrive via Stop, not Notification** — a
  plain-text question from an agent **ends the turn**. Notification only
  fires for permission prompts (mostly suppressed under
  `--dangerously-skip-permissions`) and the ~60s idle reminder. Question
  detection rides Stop + the `STATUS:` sentinel; Notification is re-pings.
- **2026-06-10 · profiles are separate worlds** — plugins, marketplaces,
  and MCP auth are **per-profile** (`~/.claude` vs any
  `CLAUDE_CONFIG_DIR`). A fresh worker profile has none of the user's
  plugins — workers run conventionless until the profile is wired. (This
  incident created ovs's doctor; grove's connections manifest exists
  because of it.)
- **2026-06-10 · plugins install at user scope** — `claude plugin install`
  from a directory-source marketplace lands at user scope, so skills load
  from any cwd under that profile; worktree placement doesn't matter.
  Directory-source marketplaces update via `git pull` in the source clone.
- **2026-06-10 · `claude -p` fires SessionEnd at exit** — a headless
  session ends as `dead` *after* Stop's classification (fold order is
  correct; the question survives on the task). Don't be confused by dead
  status in `-p` smoke tests; for real tmux workers, `dead` genuinely
  means crashed/exited.
- **2026-06-10 · sessions-index is unreliable for worktrees** — Claude's
  `sessions-index.json` points at the parent repo and silently misses
  sessions. Always resume by explicit `--resume <id>`, never path-based
  lookup. (Inherited stance from parkranger.)

## tmux / git / detector internals (verified against source)

- **2026-08-22 · hooks match on the DERIVED `tasks.json`, so a scripted
  hook right after `gv grab` is a silent no-op until something Loads**
  (grove-177 e2e): `hooks.Receive` finds the task by cwd via
  `state.ReadTasks` (tasks.json), and `grab` only appends to events.jsonl —
  the view is rebuilt by the next `state.Load` (any `gv ls`, the cockpit
  tick). `e2e/dummy.sh` happens to run `gv ls` before its first hook;
  `e2e/handoff.sh` didn't and its SessionStart vanished, which made the
  mid-turn guard pass and the suite hang in the idle wait. Rule for e2e
  scripts: `gv ls --json >/dev/null` before the first seeded hook.
- **2026-08-22 · a tmux socket path has a ~104-byte limit** (macOS
  `sun_path`): running an isolated-tmux suite from the session scratchpad
  dir (`/private/tmp/claude-501/-Users-…/<uuid>/scratchpad`) fails with
  "error connecting … (File name too long)" — every e2e suite keeps
  `mktemp -d /tmp/grove-*.XXXXXX` for that reason.

- **2026-08-20 · pane indices depend on `pane-base-index` — never write a
  literal `.0`/`.1` target** (grove-168, fresh-install cockpit failure):
  the common dotfiles pair `base-index 1` + `pane-base-index 1` makes the
  first pane `.1`, so every hardcoded `.0`/`.1` pane target either errors
  ("can't find pane: 0" — killed `gv grab` at the placeholder hint and the
  cockpit build at the dash launch) or silently hits the WRONG pane (grab's
  `.1` claude launch landed in the worktree shell; `closablePane`'s
  `index == 0` guard stopped protecting the dashboard, which now sat at
  index 1). Window targeting was already config-proof (`@N` ids,
  grove-116); panes now follow the same rule: resolve the `%N` id at
  creation (`split-window -P -F '#{pane_id}'` — `SplitVerticalWindow`,
  `SpawnPane`) or via list-panes (`FirstPaneID` for "the window's first
  pane", `ClaudePaneTarget` for claude); "first pane" checks compare
  against the window's lowest live index, never `== 0`. The e2e suites
  structurally could not catch this — scratch `HOME` means the isolated
  servers never load a user's tmux.conf — so `GROVE_E2E_TMUX_CONF=hostile`
  boots them with the hostile pair (`tmux -f`, applied at server start
  only) and `e2e/all.sh` runs cockpit.sh + workspace.sh in both modes.
  Non-goal, on purpose: grove never normalizes the user's numbering
  (`set-option base-index 0`) — it works under the user's config, not
  against it.
- **2026-08-17 · `tmux kill-window` does not kill the pane's process
  tree** (grove-156, found 2026-08-16 as 3 cores pegged for ~4 days):
  killing a window SIGHUPs the pane's foreground process group only.
  Build/test children that daemonize or double-fork (jest-worker's
  `processChild.js`) survive, reparent to launchd (ppid 1), and — in
  jest's case — spin-wait forever on a parent that no longer exists,
  invisibly to `gv ls` (task DONE, worktree even removed, ~2.4GB RSS
  held). Signature-based orphan detection can't see them (no claude/mcp
  in argv). The safe join is the one fact grove owns: **the worktree path
  is unique per task and grove created it**, so any argv referencing it
  belongs to that task by construction. Teardown now SIGTERMs argv
  matches before removing the worktree; audit/sweep report/offer the rest
  (done task or dir gone). Never pattern-match a generic `.worktrees/`
  string — foreign worktrees (ovs, editors) are not grove's business.
  Known gap: argv-less ownership (claude launched by cwd) needs an
  lsof/pane join — split to #157.

- **2026-07-29 · `paste-buffer` then `send-keys Enter` back-to-back loses
  the Enter — and "delivered" is not "submitted"** (grove-144, hit 3+
  times in one fresh-install session): the relay pasted with
  `paste-buffer -d` (no `-p`, despite a doc comment claiming bracketed
  paste) and pressed Enter with zero settle. Claude's TUI can still be
  ingesting the paste when the Enter arrives and swallows it into the
  input, leaving an unsent `[Pasted text]` in the box — while `gv nudge`
  printed ✓ and appended `EvAnswered`, so `gv ls` showed a stalled worker
  as `working`. The silent success was the expensive half: the operator
  only found out by attaching. Fixed by making the relay a
  **deliver-then-verify** operation, not fire-and-forget: bracketed paste
  (`-p`), a 250ms settle before Enter, then scrape the pane, retry Enter
  once, and error out (recording nothing) if the text is still in the
  input box. Two sub-traps found while building it: (1) `CapturePaneBottom`
  reads the pane's bottom N **rows**, which are blank whenever the app
  draws from the top — a verify built on it silently passed everything, so
  the scrape takes the whole visible pane and finds the box itself
  (bottom-most `╰`/`│` run, top border allowed to have scrolled off);
  (2) the box wraps text at spaces *and* mid-word, so the comparison drops
  every whitespace and box-drawing rune on both sides before matching a
  24-rune probe. The scrape is deliberately permissive — unreadable pane or
  no box ⇒ "landed" — because chrome changes under us and a false alarm
  would strand a delivered answer. Regression: `pasteLanded`/`verifySubmit`
  unit tests plus `e2e/relay.sh`, whose second leg runs a stub that
  swallows the Enter and redraws the box.
- **2026-07-18 · the WINDOW side of a `session:name` target
  prefix-matches too — and worker names are prefixes of each other**
  (grove-116, reproduced on an isolated `-L` server): `repo · grove-1` is
  a prefix of `repo · grove-10`. With both windows live, targeting the
  glyphed worker by its stored name erred "can't find window" (ambiguous
  over two prefix matches → a live worker read as dead, `gv adopt`
  offered a second claude on the same worktree); with grove-1's window
  gone, the same target **silently resolved to grove-10** — `WindowLive`
  lied true, so pause/untrack/done's `KillWindow` killed the sibling's
  live worker mid-turn, `ClaudePane` pasted `gv answer` text into the
  wrong agent, and a late hook re-badged the sibling's window. The
  grove-78-era stance "window side stays prefix-tolerant for glyphs" is
  retired: glyph tolerance now lives in `matchesWindowName`, and every
  worker-window target resolves through `tmux.WindowID` (list-windows +
  match → target by immutable `@N` id; `ClaudePaneTarget` returns the
  `%N` pane id for relay). No call site may build a `session:windowName`
  target from a task's stored window name. Regression: grove-1/grove-10
  scratch-server fixtures in `internal/tmux/window_id_test.go`.
- **2026-07-17 · `=`-anchoring is only valid where `-t` is a
  target-session** (grove-99, live repro on tmux 3.6a) — grove-78's blanket
  `Exact()` broke every cockpit build: commands whose `-t` is a
  target-pane/target-window (`set-option`, `show-options`, `select-layout`,
  `split-window`) reject a bare `=name` with "no such session" / "can't
  find pane", while `has-session`/`kill-session`/`new-window`/
  `list-windows` accept it. The universal safe form for the pane/window
  class is `=name:` (trailing colon: exact session, its active window) —
  `tmux.ExactActive`. Symptom chain: `CockpitReady`'s `show-options`
  always-false → forced rebuild → death at `SetCockpitLayout`, so `gv`
  could never open a cockpit. `e2e/cockpit.sh` catches it (it was red on
  main — grove-78 merged without running it, the grove-79 lesson again);
  regression test `TestPaneTargetHelpersExactSession` now runs every
  affected helper against a scratch server.
- **2026-07-13 · a bare `-t <session>` target matches window names across
  ALL sessions** (grove-78, live repro) — with a session literally named
  `grove` plus a worker window `grove · grove-75-…` in a *different*
  session, `new-window -d -t grove` resolved to the WINDOW and died on
  "create window failed: index 1 in use", even though session `grove` had
  only window 0. Verified in isolation; `-t '=grove'` (exact-match anchor)
  targets the session correctly and leaves `session:window` window-side
  prefix tolerance (glyph suffixes) intact. Every session-scoped `-t` in
  `internal/tmux` now goes through `tmux.Exact`; regression test
  `TestCreateWindowSessionExactCollision` runs the full collision against
  a scratch server.
- **2026-07-13 · a bare `capture-pane -p` can miss text that was really
  delivered** — it captures only the *visible* screen of the *active*
  pane, and a pasted line the shell then executed both scrolls away
  (command output pushes it off-screen) and hard-wraps at pane width
  (default 80 cols splits any longish token across lines). e2e assertions
  on pane content must capture every pane of the window with scrollback
  (`capture-pane -p -S -` per `list-panes` pane id) and flatten newlines
  (`tr -d '\n'`) before grepping. Field-hit: grove-75's plugin smoke test
  asserted a delivered nudge was "missing" until captured this way.
- **2026-07-07 · `$TMUX` beats `TMUX_TMPDIR`** — tmux resolves its socket
  as `-S`/`-L` > `$TMUX` > `TMUX_TMPDIR`, so every isolated-tmux-server
  script **must `unset TMUX` first** (or `env -u TMUX` each call), or when
  run from inside a worker pane the "isolation" is a silent no-op: sessions
  land on the REAL server, gv's attach takes the inside-tmux
  `switch-client` path and yanks the operator's terminal, and a cleanup
  `tmux kill-server` kills every session and worker on the machine (the
  grove-7 crash, 2026-07-07). `tapes/run.sh` now also snapshots the real
  server's session list before/after as a canary. Never run bare
  `kill-server` in any script; scope it with `env -u TMUX` + the scratch
  `TMUX_TMPDIR`.
- **`tmux.SendKeys` is single-line only** and tmux interprets key-name
  lookalikes in the text. Never use it for prose — relay replies via
  `load-buffer` + `paste-buffer` + a separate `send-keys Enter`. If a
  reply is a single character and the pane tail looks like an option
  picker, pass it through raw without Enter-wrapping.
- **Claude's pane is resolved, never assumed** (2026-07-02) — windows lose
  splits, panes renumber, and claude's process title is its bare version
  string; relay/detector/editor-inject all go through `tmux.ClaudePane`.
- **tmux window-name drift is survivable** (2026-07-02) — live windows
  can show name variants (trailing dash), but `-t session:window` lookups
  still hit because tmux prefix-matches targets. Don't rely on exact
  window-name equality; re-derive + re-store on adopt.
- **`worktree.Add` uses one string for branch and dir** — fine with the
  `<id>-<slug>` no-slash branch convention; a slash would force a two-arg
  fork.
- **Squash-merge defeats `git branch -d`** — a squash-merged branch is
  never ancestry-merged, so `-d` refuses every time. Verify merge via
  `gh pr view --json state,mergedAt`, then `branch -D` + remote delete.
- **Pane detector vs CC chrome (two incidents)** — the spinner glyph
  changed once (✢ → ✽), and current CC's bottom chrome pushes the live
  spinner >15 lines above the pane bottom. Spinner/activity checks must
  scan the full ~30-line capture, not a bottom window; both markers are
  transient so the wide scan is safe. Hooks were right both times — the
  scraper is liveness garnish, hooks are truth.
- **Detector reads `unknown` for a plain shell pane** — LIVE shows
  `unknown` until claude actually boots (e.g. during setup). Expected; the
  task status column carries the truth.
- **`session.EncodePath`** replaces `/` and `.` with `-`; transcripts live
  under `<CLAUDE_CONFIG_DIR>/projects/<encoded-cwd>/`.

## Go / CLI

- **2026-08-27 · `exec.CommandContext` kills the child, not its
  descendants — and pipe copy-goroutines wait for whoever holds the write
  end** (found pre-existing-red on main during grove-186's gate;
  `TestGHTimesOut`). The grove-164 test stubbed a wedged `gh` as
  `#!/bin/sh\nsleep 5`: the deadline's kill took down sh at 100ms, but the
  orphaned `sleep` had inherited the stdout/stderr pipes (non-*os.File
  writers ⇒ exec makes os.Pipes + copy goroutines), so `cmd.Wait` blocked
  until sleep exited at 5s — the timeout looked dead while actually
  firing. Minimal fix: model the wedge as one process (`exec sleep 5`),
  which is also what a real stalled-network `gh` is. Latent hole left
  open on purpose: a timed-out command whose CHILDREN hold the pipes
  still outlives the deadline — a true fix needs Setpgid + a
  process-group kill in `Cmd.Cancel`. Rule: a deadline test must stub the
  hung process itself, never a shell wrapping it; and never conclude a
  context timeout "isn't working" without checking which pid got the
  signal.
- **2026-07-18 · a single re-arming bubbletea timer is a pattern, not a
  one-off fix — it recurs per timer** (grove-118, farewell audit).
  grove-24 fixed the cockpit's 1s beat by making `tickMsg` the sole
  re-armer of `tickEvery`, so ad-hoc `refreshMsg` deliveries stay pure
  data application. The PR-poll 30s timer was never migrated to the same
  shape: its `prsMsg` handler unconditionally re-armed itself, so every
  ad-hoc `prsCmd` call — including the one manual `r` (refresh) fires —
  permanently added another self-perpetuating 30s poll loop, each fanning
  out one `gh` subprocess per task. Five `r` presses in a session left six
  concurrent loops running forever, unkillable without restarting the
  cockpit. A vestigial `prTickEvery()` one-shot timer sat next to it whose
  callback returned `nil` (bubbletea silently drops nil msgs) — dead code
  that read as if the PR loop were bounded, when it wasn't. Fix mirrors
  grove-24 exactly: a `prTickMsg` beat is the only re-armer; `prsMsg`
  (fired by both the beat and every ad-hoc refresh) is data-only. Rule:
  when introducing a second periodic `tea.Tick` loop in a bubbletea model,
  give its own message type the sole re-arm responsibility from day one —
  don't let a shared/generic message do double duty as both data payload
  and re-armer, and audit every existing `tea.Tick` call for the same
  shape before assuming the class is fixed once.
- **2026-07-17 · a default backend's storage dir must never double as a
  scope marker** (grove-100). The workspace marker was "any `.grove/`
  dir" — but the markdown backend (grove's default) stores its tasks in
  `.grove/tasks/`, so every `gv init`-scaffolded repo instantly became an
  unregistered "workspace" and grove-78's fail-closed grab guard broke
  the global-config grab path, i.e. the default onboarding flow.
  workspace.sh's legacy section had it red on main for days, masked by
  an earlier cockpit failure in the same suite. Fix: the marker now
  requires workspace substance — `config.yaml`, `state/`, or
  `orchestrator/` inside `.grove/`. The rule: when a scope marker and a
  default-on feature share a directory, the marker predicate must key on
  files only the scope's own init writes, or the default feature silently
  flips everyone into the scoped regime.
  every row budget to its data** (grove-79). Events load async, so
  `View()` always runs once against an empty feed; `viewActivity`'s
  `items[:avail]` used the rowBudgets leftover unclamped and panicked
  (`slice bounds out of range [:2] with capacity 0`) on any pane whose
  leftover rows fell below the scene floor — the e2e tape size, and any
  brand-new fleet. Live cockpits never hit it (feed populated by frame 2,
  tall panes give the scene its floor), so it shipped in grove-56 and sat
  red for a day. Three compounding lessons: (a) any `[:budget]` slice in a
  render path clamps to `len` — the budget and the data are computed from
  different inputs; (b) render tests must sweep SMALL heights with an
  empty model, not just narrow widths (`TestViewRendersNarrowPanesWithSparseFeed`
  sweeps 40×5..30 × 3 fx levels × empty/short feeds); (c) three TUI PRs
  merged while cockpit.sh + workspace.sh were red because nothing ran
  them — `e2e/all.sh` now runs all six suites, and TUI-touching merges run
  it. Bonus field-hit: the suites' bare `capture-pane -p` lost the panic
  reason (alt-screen closes, reason scrolls off) — they capture `-S -300`
  now, matching the tmux-discipline rule.
- **2026-07-04 · never pipe the test gate** — `go test ./... | tail` (or
  `| grep -c FAIL`) reports the PIPE's exit status, not the tests'; two
  red runs merged to main that way in one evening. Gates run bare and
  check $? — filtering happens on a saved log, never inline.
- **2026-07-04 · `/dev/null` IS a character device** — detecting "am I
  interactive?" via `os.Stdin.Stat()` + `ModeCharDevice` passes for
  `< /dev/null`, so the wizard tried to render forms headless and
  "aborted". Use `golang.org/x/term.IsTerminal(fd)`; a non-TTY `gv init`
  must silently become `--yes`, never hang or abort.
- **2026-07-04 · commands typed into tmux panes resolve via PATH, not via
  the invoking binary** — the cockpit's dashboard pane ran whatever `gv`
  was first on PATH (a stale installed build), not the binary that created
  the session. Any pane/hook command must use the absolute
  `os.Executable()` path (`buildCockpit` now does; hooks already did).
- **2026-07-04 · `cmd | grep -q` flakes under `set -o pipefail`** —
  grep -q exits at its first match, the producer SIGPIPEs writing the
  rest of its output, and pipefail reports the pipeline failed even
  though the assertion matched. E2E assertions capture to a file first,
  then grep.
- **2026-07-04 · shared tmux namespaces during ovs coexistence** — worker
  sessions keep ovs's `pr-<repo>` naming (byte-comparable `internal/tmux`),
  so a repo tracked by BOTH ovs and gv lands windows in the SAME tmux
  session. Window names differ per branch and each tool finds its own by
  stored names, so it works — but don't be surprised seeing gv windows
  inside an "ovs" session. Same class of clash made us rename the relay
  buffer (`ovs-relay` → `gv-relay`): tmux buffers are server-global, and
  a shared name would let one tool's relay clobber the other's mid-paste.
- **2026-07-04 · settings.json hook matching must be basename-precise** —
  the ovs-era installer predicate matched `"ovs"` as a substring anywhere
  in the command. The gv equivalent would have CLAIMED (and replaced) ovs
  entries in the shared `~/.cc-work/settings.json` on `gv hooks install`.
  Match on the hook command's binary basename (`gv`, `*grove*`) — never
  substring-across-the-path. Table-tested against a real transition-window
  settings fixture.
- **2026-07-04 · `yaml.Node` keeps flow style when you append** — seeding
  a config with `repos: {}` and appending via node surgery emits the whole
  map single-line (`repos: {r: {path: …}}`) because the `{}` scalar's
  flow style sticks. Set `node.Style = 0` after lookup/creation to force
  block style. (yaml.v3 round-trip via Node DOES preserve comments —
  that part worked as hoped in `gv init`.)
- **stdlib `flag` stops at the first positional** — `gv grab <url> --repo
  x` silently ignores `--repo` without a re-parse loop (`parseAnywhere`).
  Flags-after-positionals is table stakes; stdlib doesn't give it to you.
- **Hooks reference the absolute binary path** (`~/go/bin/<bin>`);
  `go install` refreshes that binary in place, so rebuilds don't require
  re-installing hooks (path unchanged).
- **Cost estimates: dedup + cache asymmetry** — transcript pricing follows
  ccusage's rules: dedup entries by `message.id`+`requestId`, price cache
  reads at 0.1×, 5-minute cache writes at 1.25×, 1-hour at 2×. Costs are
  ESTIMATES of relative effort, never billing.

## Remote / attach architecture (verified against t3code source)

- **2026-08-26 · live handoff shakedown (grove-187, Mac ↔ Frankfurt VPS):
  a remote host is GLOBAL-layer only — repos AND `provider:` must live in
  its `~/.config/grove/config.yaml`** — every `--host` verb runs over ssh
  at the login dir, where no workspace marker exists, so the remote gv
  resolves the global config and global state dir. Mirroring the Mac's
  workspace-config layout on the host breaks passthrough twice over:
  `adopt --host` died with `unknown repo "grove" (configured: )` (global
  config had no repos), and after adding repos it died again resolving
  the default **markdown** provider (`task grove-187 not found in
  .grove/tasks`) because `provider: kind: github` had also lived only in
  the workspace layer. The runbook's single-layer model is load-bearing,
  not a simplification. Bonus verified: the failed remote adopt left the
  task untracked with NO tombstone and a working retry hint — the
  #183-fix-round failure semantics observed live.
- **2026-08-26 · first-run Claude dialogs eat the pickup prompt on a
  fresh host** — the first adopt on a new `~/.claude` profile hit the
  folder-trust dialog AND the bypass-permissions acceptance before the
  pickup prompt's send-keys arrived; the prompt was consumed by the
  dialogs and the task sat in `setup` indefinitely with an empty input.
  Symptom signature: `agent=setup` stuck + pane showing "Do you trust
  this folder?". Recovery: answer both dialogs (trust defaults to Yes —
  Enter; bypass defaults to No — needs `2` then Enter), then re-send the
  instructions via `gv nudge`. Prevention (now in the runbook): burn the
  dialogs at provisioning time — after `claude` login, start claude once
  with the worker flags in the worktrees parent and accept both. Same
  family as the #186 delivery-window evidence: send-keys into a
  booting/compacting/dialog pane reports ✓ and delivers nothing.
- **2026-08-26 · pane-scrape verification needs `capture-pane -J` —
  narrow panes wrap text mid-word and defeat grep** — a 39-column worker
  pane wrapped every nudge across lines, so grepping the capture for a
  delivery marker returned 0 even though three nudges HAD landed (the
  worker deduped the retries gracefully: "nothing to redo"). `-J` joins
  wrapped lines before matching. Without it, delivery checks produce
  false negatives and cause exactly the duplicate sends #186's op-ids
  exist to make safe.
- **2026-08-26 · `gv update` is a GitHub-releases self-updater — a
  private source install updates via `git pull --ff-only && go install
  ./cmd/gv` over ssh** (no releases exist on a private repo; the verb
  errors). The runbook's update path for a VPS host is the git one.
- **2026-08-26 · t3code's "first-class remote" is attach, not sync — it
  synchronizes nothing between machines** — read during the #176–#179
  remote-train review: [pingdotgg/t3code](https://github.com/pingdotgg/t3code)
  has no CRDT, no cloud session DB, no event replication. One machine's
  server owns everything (agent processes, PTYs, an event-sourced SQLite
  store); every other device is a thin client on an authed WebSocket;
  host off = sessions unreachable. This validates grove's sync-free bet
  (per-host state, ssh passthrough, on-demand fleet merge) from the
  strongest possible reference — and t3code entirely punts on the one
  case grove covers: *moving* a task between machines (`gv handoff`).
  The transferable pieces, ranked with grove touchpoints, live in
  [docs/remote-attach-architecture.md](docs/remote-attach-architecture.md):
  client-minted command IDs + receipts (idempotent remote
  answer/nudge/grab), access-vs-launch orthogonality (ssh as a launch
  helper, never a special host type; never kill a server you didn't
  start), snapshot-then-stream terminal attach + pairing-token→short-
  lived-ticket auth (the `gv serve` blueprint for mobile cockpit v2),
  and cached-views-never-overwrite-live. What it changed: the remote
  train's architecture is confirmed, not revised; future remote work
  starts from that doc instead of re-deriving.

## Field notes (ovs, kept for judgment)

- **2026-07-29 · a polling TUI's perceived cost is `spawns/sec ×
  cost-per-spawn`, and the second factor varies ~50x by environment** — an
  external user's CPU pegged at 5–6 workers while Dean's larger fleet felt
  nothing: EDR/antivirus scans every exec, WSL1 forks are slow, so the same
  spawn rate reads "fine" on one machine and "pegged" on another. "Feels
  fine on my machine" is not evidence for a 1s-beat hot path. Fix arc
  (backstory: journal 2026-07-29): grove-149 batched the tick's tmux reads
  into one `SessionSnapshot` (6N+2 → ~N+3 execs/sec), grove-126 made state
  I/O O(new events) via the incremental `state.Folder`. What it changed:
  every per-tick exec or full-file scan in beat code must justify itself at
  write time — the beat multiplies it forever.
- **2026-07-29 · verifying hot-path fixes live: mtime freeze is strong
  evidence, ps-sampling is not** — a dirty-flagged derived file proves
  itself in production by its mtime staying frozen under a running dash
  (tasks.json sat untouched for the whole observation window; the old code
  rewrote it every second). But ms-lived execs are effectively invisible to
  `ps` sampling — 200 samples at 100ms caught zero transient tmux clients
  even while ticks ran — so exec *counts* are pinned by seam-counting unit
  tests, never claimed from sampling.
- **2026-07-09 · throwaway builds for operator testing** — when a change
  needs the operator's manual verification before merge, build the branch
  to a scratch path (`go build -o /tmp/gv-<ticket> ./cmd/gv`) and hand
  over that command — never `go install` from an unmerged branch. The
  installed `~/go/bin/gv` keeps running live sessions and hooks (hooks
  reference its absolute path); a temp binary tests the exact change,
  interrupts nothing, and is thrown away if it doesn't work. Used to
  verify grove-36's pane tagging live before merge. This is the DEFAULT
  handoff for "try it yourself" testing.
- **2026-07-09 · state never forgets a session id** — the events fold
  clears nothing on `untrack` (it only sets `Done`), so `gv adopt` of a
  previously-tracked task always `--resume`s the stored conversation.
  When the old conversation is the problem (corrupted/bloated context),
  `gv adopt --manual` is the guaranteed-fresh escape hatch — it skips the
  resume limb — then a `gv nudge` with the work order restores autonomy.
  Used to reset grove-36 onto a fresh Opus session mid-ticket.
- **2026-07-09 · prompt caching survives OpenRouter→Z.AI** — Claude Code's
  ~50k fixed prefix cached at 99.3% on the second GLM request (turn cost
  $0.065 → $0.014). Long profiled sessions are cheap; the floor cost is
  per-session, not per-turn. (Verified in the OpenRouter activity view.)
- **2026-07-09 · OpenRouter returns dated model slugs** — the API answers
  with `z-ai/glm-5.2-20260616` while humans configure `z-ai/glm-5.2`;
  any lookup keyed on the configured slug (cost pricing) needs
  exact-match-then-prefix-match at a `-` boundary or it silently reports
  unknown.
- **2026-07-10 · orchestrator cadence: one long-lived chat per sitting** —
  a fresh orchestrator spawn pays a ~50k-token initial prompt; only ~3.1k
  (~6%) is grove's own files (orchestrator brain + repo CLAUDE.md +
  memory index), the rest is harness + inherited MCP load. `gv` reads are
  stateless and live, so a long-lived chat never goes stale — `/clear`
  between unrelated batches instead of close-and-reopen, and reserve
  fresh spawns for genuinely parallel workloads (the `)` pane). The ~50k
  floor is paid per SESSION even where caching is good (Anthropic 5-min
  cache reads ~0.1×; GLM cached 99.3%, see the 2026-07-09 entry), so
  close-and-reopen re-pays the floor every cycle for nothing. Trimming
  inherited MCP servers shrinks the floor itself (2026-07-10: user scope
  emptied; posthog/grid moved to thegrid project scope). (grove-48.)

- **The full loop is proven** (2026-06-10, DEV-4556): grab → worktree +
  setup → autonomous work → PR + CI green + previews + self-review →
  ticket transitioned by the agent via MCP → clean `STATUS: DONE`
  sentinel. Sentinel compliance was 1/1 on day one; when an agent drops
  the sentinel, classification degrades to `stalled` — correct behavior.
- **Label→repo inference misses in practice** — real tickets carry
  type-labels (`[Feature]`), not surface-labels; treat `--repo` as the
  common path and inference as a bonus.
- **The `api_key_env` indirection earned its keep on day one** — users'
  keys live under personalized env names; never hardcode the env var.
- **When demoting a data source, wire the replacement into the same code
  path** — ovs's preview column went blind because the cheap poll dropped
  comments while only the on-demand path kept them.
- **2026-07-05 · a git-inited $HOME shadows parent-folder detection** —
  `git rev-parse --show-toplevel` from ~/git/unbrewed returned /Users/dev
  (dotfiles repo), so `gv init` made HOME the workspace. Parent-of-repos
  detection must test the cwd ITSELF (≥2 direct child repos AND cwd is not
  the git root) before trusting any enclosing repo root. Field-hit on
  the operator's machine within minutes of shipping.
- **2026-07-17 · audit called every live worker disconnected — glyphed
  window names vs exact compare** — grove-47's state glyphs append
  ` ⏸`/` ●` to live window names; `tmux.WindowExists` compared the
  listed names against the stored glyph-less name with `==`, so
  `WindowAlive` was always false and `gv audit` suggested `gv adopt`
  for healthy, minutes-old workers (caught live on grove-89/90 before
  any adopt fired). The rule already existed in tmux-discipline
  ("never rely on exact window-name equality") — the check predated
  the glyphs and nobody re-audited it when grove-47 shipped. When a
  feature decorates a shared identifier, grep for every consumer that
  compares it. Fixed in grove-94 (match exact or `stored + " "` prefix).
