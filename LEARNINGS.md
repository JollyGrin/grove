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

- **2026-08-31 · `claude --session-id <uuid>` exists — mint identity, never
  infer it; and a claude holds NO fd on its transcript** (grove-222, fixing
  the grove-215 resolver). Two facts, both verified live. (1) The CLI takes
  `--session-id <uuid>` (`claude --help`; the value must be a real UUID),
  so the session id of a chat grove spawns is grove's to DECIDE — the whole
  class of "which transcript belongs to which pane" evaporates, exactly as
  `--resume` already made it evaporate for a revival. (2) The fallback
  everyone reaches for first does not work: on a machine running four live
  claude sessions, `ls -l /proc/<pid>/fd` showed **zero** open `.jsonl`
  handles — Claude Code opens, appends and closes, so there is no fd to
  correlate a pane with. What IS readable is the argv: an agent launched
  with `--session-id`/`--resume` carries its id there for its whole life,
  under a pane pid that is a SHELL (the launch is typed in), so the lookup
  is a descendant walk over `ps -Ao pid,ppid,args`, not a look at the pane
  process itself. Changed: grove mints and stamps at spawn, corrects a
  wrong stamp from the running argv, and refuses to pair rivals at all.

- **2026-08-31 · transcript mtime is LAST WRITE, so "newest pane owns
  newest transcript" is false — and a stable wrong answer is the worst
  kind** (grove-222; the grove-215 regression, found on groveremote by
  live verification, never by the suites). Two live chats in one project
  dir came back with each other's ids: chat-2 (younger pane) had gone idle
  at 02:41 while chat-1 (older, still working) wrote until 02:56, so the
  newest `.jsonl` was the OLDER pane's and the newest-pane-first sort
  handed it over. The ids were distinct and stable across repeated calls —
  which is all the acceptance criteria asked for, and is why unit tests,
  e2e and a human reading the output all passed it. Two rules out of it:
  an inferred identity must be checked against something the inference
  cannot see (here: grepping each transcript for text unique to the
  conversation in the pane), and where an id is DURABLE and steers writes,
  `null` beats a guess — a missing id costs a client a button, a wrong one
  delivers the operator's words to the wrong agent.

- **2026-08-31 · a transcript line is not a message — and three of its
  fields decide what a chat READS like** (grove-216, projecting `.jsonl` →
  `gv chat tail`). Verified against live transcripts: (1) one line holds
  MANY content blocks — an assistant turn routinely carries a `thinking`
  block plus two `tool_use` blocks — so "one line = one message" collapses a
  turn a phone needs separated; (2) a `tool_result` names only the opaque
  `tool_use_id` it answers, never the tool, so the NAME has to be
  remembered from the `tool_use` that came before it or every result
  renders anonymous; (3) `isSidechain: true` marks a SUBAGENT's private
  conversation — real entries, but not this chat's, and interleaving them
  makes a Task agent's tool spam read as the orchestrator talking to
  itself. Also: a `thinking` block can arrive with a signature and NO text
  (redacted), so an empty-text block is chrome to skip, not content to
  emit. Distilled into the claude-code-facts skill.

- **2026-08-27/29 · the cheap-lane probe results, and why the probe
  protocol is worth its cost** — four OpenRouter lanes dispatched on real
  grove tickets, ~91k resident context: `qwen/qwen3.7-flash` $0.056 —
  **PASS**, matched a verbatim spec character-for-character, gate green,
  TASKS.md row, clean sentinel (first choice for rote);
  `qwen/qwen3-coder-next` $0.566 — **PASS with caveat**, correct fix plus a
  real regression test but exceeded the enumerated surface and *invented*
  default config values instead of asking; `deepseek/deepseek-v4-flash-0731`
  $0.104 — **FAIL, loops**: produced the correct diff, then could not
  recognise it was finished (203 turns / 20 min / no commit, re-verifying
  the same file); undated `deepseek/deepseek-v4-flash` $0.139 — **FAIL at
  Gate 0**: reasoned about grove-115 correctly, then emitted DeepSeek's
  native `<｜DSML｜tool_calls>` markup inside a thinking block — 407 output
  tokens, zero `tool_use` blocks, zero text blocks, no error, 12 seconds.
  Three rules fell out: **pin dated slugs** (the `0731` snapshot clears
  Gate 0, the undated alias does not — same vendor, same family, same
  endpoint), **judge the model + endpoint pair** (z.ai's
  `api.z.ai/api/anthropic` is a real Anthropic-protocol surface;
  OpenRouter's generic endpoint passes some models' native tool syntax
  straight through), and **specification quality dominates model choice**
  in this tier — the $0.056 lane nailed the most tightly specified ticket
  while the 10×-dearer lane drifted on a looser one. Distilled into
  `.claude/skills/model-lanes` §Verified lanes + §The probe protocol.

- **2026-08-29 · every worked example in a skill is executable, so treat it
  like code (grove-202)** — the model-lanes skill instructed the
  orchestrator to run its snippets verbatim, and three of them were wrong
  in ways that fail *silently*: a calibration `jq` reading a
  `total_tokens` field `gv cost --analyze --json` has never emitted (yields
  `null`, so every downstream credit figure was null or skewed); a
  transcript path built with `sed 's#/#-#g'`, missing `EncodePath`'s second
  `.` → `-` rule, so the Gate 0 lane-killer read a directory that does not
  exist; and `sorted(glob(...))[-1]` over UUID filenames, which on a live
  7-file project dir picks a different transcript than mtime does. Plus a
  double-applied peak multiplier that put the z.ai windowed ceiling at
  40/80 turns when the file's own 18.6-credits/turn calibration gives
  ~80/~160. None of the four raises an error. **A skill snippet ships
  unreviewed and untested by default — run every one against live output
  before merging it, and quote the observed output beside it.**

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

- **2026-08-31 · `session_created` has one-second resolution, so two chats
  spawned back-to-back TIE** (grove-215): the lazy join between a live chat
  pane and its Claude session id resolves "newest unclaimed transcript
  first", which needs the panes ordered youngest-first — and `#{session_created}`
  is whole seconds, so `e2e/chat.sh` spawned three chats inside one second
  and the order collapsed to whatever the input order was, handing chat 1
  the transcript chat 3 had just written. Tie-break on the chat number
  (`grove-chat-<label>-<n>`): `NextChatSession` hands `<n>` out in order, so
  within one second the higher `<n>` is the younger. (A REUSED slot — n=1
  freed by a closed chat — breaks that rule, but a reused slot is minutes
  old and never ties.) The general lesson: any ordering read off a tmux
  timestamp needs a deterministic tie-break, because scripted spawns are
  always sub-second.

- **2026-08-31 · a pane user option is the only durable place for a chat's
  identity** (grove-215, confirming grove-36 T1 from the other side): the
  Claude session id is minted by claude on boot, so it cannot be passed at
  spawn — it has to be resolved from the transcript afterwards and then
  written somewhere that survives the agent. `set-option -p @grove_chat_session`
  survives claude's OSC title write, re-layouts, re-attaches and detaches;
  it is read back in the same `list-panes -F` call that reports the pane's
  cwd and command, so the join costs zero extra tmux round-trips. A pane
  TITLE would have been clobbered on boot, and a sidecar file would have to
  be reconciled against panes that die without notice.

- **2026-08-29 · `pane-border-style` is a PANE option since tmux 3.2, and
  `#{?…}` conditionals nest** (grove-199): `tmux set-option -p -t %N
  pane-border-style fg=colour110` is accepted and read back by
  `show-options -p` on 3.4 — so a single pane's border can be colored
  without touching the window-wide option every other pane shares. Verified
  live on an isolated server, together with a two-level
  `pane-border-format` (`#{?#{@grove_remote},@#{@grove_remote} · #{?#{@grove_profile},…}}`):
  tmux parses the commas inside the nested `#{ }` correctly, so a remote
  chat pane reads `@host · profile` while local panes are byte-identical to
  before. Both stay best-effort in the code — an older tmux rejects
  `set-option -p` and a cosmetic tag must never fail a spawn.

- **2026-08-29 · a pane grep for `STATUS: DONE` fires on every task, from
  second zero — the kickoff prompt contains all three sentinels verbatim**
  (grove-205; two false DONEs inside a minute on unbrewed-artgen #16 and
  #18/#19, both workers still `agent: working`). `md_default.tmpl:29` (and
  `md_pickup.tmpl:27`, `default.tmpl:26`, `pickup.tmpl:30`) ends every
  kickoff with the three `STATUS: …` placeholder lines, so the string a
  monitor greps for is planted in the pane by grove itself, before the
  agent has done anything. The same incident carried the mirror-image bug:
  a push probe whose "before" snapshot was taken AFTER the push had
  landed, so it could never fire. Both are the 2026-08-27 rule from the
  other side — **a marker's presence is not a transition, and a baseline
  sampled after the probe was armed is not a baseline**. This was grove's
  booby trap, not just a bad script: `orchestrator/CLAUDE.md` pointed
  monitor authors at `tmux capture-pane`, grove planted the placeholder,
  and grove offered nothing better. Fix: `gv watch` (grove-205) streams
  `events.jsonl` — the Stop hook's classification of the agent's OWN last
  message, which by construction never sees the prompt — one line per
  event, flushed, default from-now so the baseline cannot be sampled late,
  `--until done` for the one-notification shape. The templates were
  deliberately NOT changed: teaching the sentinel by literal example is
  why sentinel emission is reliable, and the hook regex parses that exact
  shape; the fix is to make the pane unnecessary. Rule folded into the
  tmux-discipline skill.
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

- **2026-08-31 · A zero `time.Time` crosses JSON as year 0001 — which is
  TRUTHY in JavaScript, so `a || b` is not a fallback** (grove-228). A
  Go time field with no value marshals to `"0001-01-01T00:00:00Z"`, a
  perfectly good non-empty string: the natural client fallback
  `c.last_active || c.created` therefore takes the zero and renders
  "739000d ago" rather than falling back at all. The fix is a VALUE check,
  not a truthiness one — `new Date(t).getTime() > 0`, since every real
  timestamp is after the epoch and year 1 is hugely negative. Applies to
  any additive optional time on the plugin contract: emitting `null`
  instead would have been a different contract shape (the field must be on
  every row), so the zero is deliberate and the client owes it a real
  guard. Verified against the live cockpit row.

- **2026-08-31 · `e2e/chat.sh` had never run past its first transcript on
  macOS** (grove-228). Two BSD/GNU splits, both silent until you look:
  `touch -d "@<epoch>"` is GNU-only (BSD touch wants `-t YYYYMMDDhhmm.SS`,
  and errors out), and `mktemp -d /tmp/...` returns a path that a tmux
  pane's `#{pane_current_path}` reports back as `/private/tmp/...`, so a
  cwd assertion against `$SCRATCH` fails on the symlink alone. The whole
  `gv chat ls` half of the suite — the join every chat ticket lands on —
  was dark on the operator's own machine while reading green on Linux.
  Fixed with a `set_mtime` helper that tries GNU then BSD, and
  `cd "$(mktemp -d …)" && pwd -P` for the scratch root. Lesson for any new
  suite: resolve the scratch dir with `pwd -P` and never reach for GNU
  flags in a shared e2e script.

- **2026-08-31 · `http.ServeFile`/`ServeFileFS` REDIRECT any request whose
  path ends in `/index.html`** (grove-218). Go's file helpers answer
  `/index.html` with a 301 to `./` — "to make the canonical path
  obvious". So the obvious way to serve an embedded shell (rewrite `/` to
  `/index.html`, hand it to the file server) is an infinite redirect loop,
  and serving `/index.html` directly is a 301 the service worker then
  caches as the shell. **What it changed:** `chatweb.serveAsset`
  normalises BOTH `/` and `/index.html` to `/` on a cloned request before
  calling `ServeFileFS(…, "index.html")`. Caught by a test asserting
  `GET /` is 200, which is the assertion worth writing — a redirect is not
  a failure to `curl -L`, so a hand-check would have passed.

- **2026-08-31 · a `Content-Type: application/json` requirement IS the
  CSRF defense for a localhost API** (grove-218, `gv chat serve`). A
  cross-origin request that skips CORS preflight can only send
  `text/plain`, `application/x-www-form-urlencoded` or
  `multipart/form-data` — so requiring `application/json` on every
  mutating route forces a preflight, which a server that sends no
  `Access-Control-Allow-Origin` never grants. That is the whole defense
  against a page in another tab typing into the operator's agent, it needs
  no configuration and no secret, and it costs a legitimate client one
  header. **What it changed:** `chatweb.GuardWrite` runs before any
  handler, and the two facts it leans on (no CORS headers anywhere, no
  preflight answered) are pinned by tests — either one appearing later
  would make the gate decorative without failing anything else. Note an
  Origin/Host check is NOT the missing piece: under DNS rebinding the
  browser sends the attacker's name in BOTH headers, so they agree.

- **2026-08-31 · marked does not sanitize (the option went away in v8) —
  the Content-Security-Policy is the sanitizer** (grove-218). Rendering an
  agent's markdown as HTML means rendering whatever the agent read out of
  a file, and on a page that can POST into a live pane that matters. A CSP
  of `default-src 'none'; script-src 'self'` (no `'unsafe-inline'`) makes
  an injected `<img onerror=…>` and a `javascript:` href inert, because
  the browser refuses to run any script the server did not serve as a
  file. **What it changed:** the page's logic lives in `app.js` rather
  than an inline `<script>` specifically so the policy can stay strict —
  an inline script would need `'unsafe-inline'` and take the whole net
  down with it. A test asserts `script-src` never gains it.

- **2026-08-31 · a model profile's launch is a SUBSHELL, so flags append
  to the wrong thing** (grove-217). `config.WrapProfile` renders
  `( . <secrets> && export ANTHROPIC_… && exec <cmd> )` — the profiled
  chat command every `--profile` spawn runs. Building a variant by
  appending to the finished string (`plan.Cmd + " --resume " + id`) puts
  the flag *outside* the closing paren, where the pane's shell reads it as
  a second command rather than claude reading it as an argument: no error,
  a chat that opens on an empty conversation, and an operator who believes
  they revived yesterday's. **Rule: compose the bare launch first, wrap
  LAST** — `chatSpawnPlan` builds `orchestratorLaunch(...) + " --resume
  <id>"` and hands that to `WrapProfile`. Pinned by a test asserting the
  profiled command ends in `--resume <id> )`, paren included; the same
  trap waits for any future flag (`--model`, `--append-system-prompt`).

- **2026-08-31 · a resumable conversation is (id, cwd), never id alone**
  (grove-217). Transcripts key on the encoded cwd, and grove keeps one cwd
  per orchestrator backend (grove-36 T4): a GLM chat's transcript lives
  under `<brain>/<profile>`, the default one's under `<brain>`. So
  `claude --resume <id>` launched from the wrong dir is looking in a
  project dir that does not hold that id — whatever it then does, it is
  not resuming the operator's conversation, and the pane is detached, so
  nobody reads the message either way. A revival therefore RESOLVES the id
  to the dir that holds it and infers the backend from that dir, before
  spawning; an id it cannot place is refused rather than launched
  hopefully, and `--resume` with `--profile` is a hard error because the
  conversation already carries its backend. Two refusals belong on the
  same path: a malformed id (it is interpolated into the pane's shell
  command) and an id a live pane already holds (two claude processes
  appending to one transcript).

- **2026-08-31 · following an append-only file means consuming COMPLETE
  lines only, with a Reader and never a Scanner** (grove-216): `gv chat
  tail --follow` advances a byte offset over lines that ended in `\n` and
  leaves a terminatorless trailing line for the next pass — a 15KB
  `tool_result` lands in two writes, and projecting the first half would
  emit a broken entry AND desync every later `seq`. `bufio.Scanner` is the
  other half of the trap: it silently DROPS a line past its buffer (64KB by
  default), which is the same silent-truncation class already logged
  against the transcript/event readers. The e2e proves it by appending half
  a JSON object, asserting nothing is emitted, then completing it.

- **2026-08-29 · a yaml.v3 "empty document" has two shapes, and only one
  has empty `Content`** (grove-201, the sibling case #195 left open).
  Zero-byte, whitespace-only and comment-only files parse to a document
  with `len(Content) == 0` — the shape every guard in this repo tested.
  But a file holding `null`, `~` or a bare `---` parses to a document with
  ONE child: a `!!null` ScalarNode. `len(Content) == 1`, so the guard is
  skipped, and appending key/value pairs into a *scalar's* `Content` is
  accepted by the API and silently DISCARDED by the emitter — the write
  returns nil and the data is gone. `gv init` emitted `null\n` with
  `WroteConfig` true; a wizard `Save()` dropped every confirmed setting
  and reported success. **Rule: an empty-doc guard tests the root's
  `Kind`, never just the length of `Content`** — and the three outcomes
  are distinct: content-free (no Content, or a lone `!!null` scalar) →
  replace with a mapping; already a mapping → use it; anything else (a
  top-level scalar or sequence) → ERROR, because replacing it would
  clobber real data. The same trap one level down was already known and
  handled (`config.ensureMap` coerces a bare `orchestrator:` key, which
  parses as a null scalar) — nobody thought to apply it to the ROOT.

- **2026-08-27 · an empty input box is not proof of delivery — and a long
  paste hides its own echo** (grove-186, closing the three swallowed
  nudges of 2026-08-26). `pasteLanded` deliberately reads an empty input
  box as "submitted", which is right for a live pane and exactly wrong for
  one that is booting or mid-`/compact`: the paste is swallowed, the box
  is empty for the opposite reason, and `gv nudge` printed ✓ three times
  for text no agent ever saw. Two-sided fix: refuse to send while the pane
  shows `Compacting conversation` (an error, so no event is recorded), and
  after a verified submit scrape for POSITIVE uptake — the probe echoed
  outside the input box, or `esc to interrupt`. Second surprise while
  building it: the uptake scrape must read SCROLLBACK, not the visible
  screen. A long relayed prompt (the handoff checkpoint template) pushes
  its own head off-screen, so the visible pane holds no trace of the text
  that was plainly consumed — `e2e/handoff.sh` failed with a spurious
  warning until the scrape switched to a bounded `capture-pane -S -200`.
  Bounded, not `-S -`: unbounded history matches an older identical relay
  and reports uptake that never happened. Rule: "the text is no longer in
  the input box" is absence-of-evidence; only a running turn or an echo in
  history is evidence-of-presence.
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

- **2026-08-29 · zsh equals-expands a word that STARTS with `=`, so every
  unquoted `tmux attach -t =<session>` is a broken paste on macOS**
  (grove-207): zsh's `EQUALS` option (on by default, not interactive-only)
  replaces `=foo` with the path of the command `foo`, and aborts the line
  when there is none — `zsh: grove-chat-unbrewed-1 not found`, which is NOT
  command-not-found (`zsh: command not found: X`) and never reaches ssh.
  bash has no such expansion, which is why the Linux side never saw it. The
  same is true of a leading `~`. Both are word-INITIAL: `A=b` and `a~b` are
  inert. The `=` cannot be dropped (tmux's exact-match anchor, grove-99), so
  the target is single-quoted — literal in bash and zsh alike — everywhere a
  command is printed for a human OR handed to a pane's shell. `remote.Quote`
  had `=` in its leave-it-bare safe set, so it was returning the anchor
  unquoted; it now forces quotes on a leading `=` or `~`, which is what
  fixed the cockpit's own ssh-attach pane, not just the printed hints. Any
  "is this shell-safe?" set has to be judged for zsh, not just POSIX sh.

- **2026-08-29 · an ssh hop fired from inside the TUI must not inherit
  `os.Stdin`, and must not write to the real stderr** (grove-199):
  `remote.Run` hands ssh `os.Stdin` — fine for a CLI verb, but inside the
  bubbletea loop ssh and the cockpit's key reader then race for the
  operator's keystrokes, and anything ssh's stderr prints (the grove-186
  255-retry notice included) lands on top of the alt-screen. The `@` spawn
  therefore uses `remote.RunDetached` (stdin nil) with both streams
  captured into buffers, through `runRemoteIdempotentWith` — the same
  idempotent hop with its ssh call and notice stream injected. Same class
  as the existing `remoteSendCmd`, which had quietly sidestepped it by
  building its own argv.
- **2026-08-29 · the cockpit dashboard guard blocked a chat's
  self-close** (grove-199, found in the #200 review): `closablePane`
  refuses a window's FIRST pane as "the dashboard", but a detached
  `grove-chat-<label>-<n>` session (grove-198) is one window with one pane
  and that pane IS the orchestrator — whose seeded brain instructs
  `gv orchestrator close` for dispatch-and-dismiss. A fire-and-forget
  remote chat grabbed its ticket, failed to close with a message about
  protecting a dashboard that does not exist there, and left its claude
  process alive on the host indefinitely. **The name alone cannot decide
  it** (caught in review of the first fix): labels are
  `[a-z0-9][a-z0-9_-]*` with only `grove`/`mobile`/`dash` reserved, so a
  workspace labelled `chat-app` owns cockpit session `grove-chat-app` —
  the chat shape and a real cockpit are the same string, and no shape
  regex separates them (`chat-app-2` is both chat 2 of `chat-app` and the
  cockpit of `chat-app-2`). The guard now takes an injected
  `CockpitCheck` resolved from the workspace REGISTRY, and an ambiguous
  name collapses to COCKPIT: a chat the operator closes by hand costs
  less than a dead dashboard. Nil check = everything is a cockpit, so a
  caller that forgets to inject can only be over-protective.

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
- **2026-08-28 · "global-layer only" is about where passthrough LANDS,
  not about what a host may hold (grove-198)** — the shakedown rule above
  reads as "keep the host workspace-free", and that is one step too far: a
  host can register workspace twins and still serve `--host` passthrough
  from the global layer (grove-191 made a global-layer `gv ls` aggregate
  registered workspaces). `gv orchestrator new --host` needs exactly that:
  a twin registered under the SAME label, whose own `.grove/config.yaml`
  supplies the chat's brain. What must never happen is the opposite
  direction — resolving a missing twin by falling back to the global
  layer, which would run the chat on a machine-specific `claude` wrapper
  (the 2026-07-05 ccwork inheritance). Missing twin ⇒ hard error.
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
