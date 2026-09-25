# Grove — learnings archive · 2026-08

> Rotated out of [LEARNINGS.md](../../LEARNINGS.md) on 2026-09-05 (grove-275).
> Same entry format and sections; newest first within each section.
> The rules these entries taught live in `.claude/skills/`; this is the
> dated record. Grep `LEARNINGS.md docs/archive/LEARNINGS-*.md` for the full log.

## Claude Code behavior (verified in ovs)

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

## Go / CLI

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
