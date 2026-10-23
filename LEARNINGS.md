# Grove — learnings

> [TASKS.md](TASKS.md) is the status board · **LEARNINGS.md** is the
> surprises — anything discovered the hard way or verified against
> source, so we never re-derive (or re-break) it. Every entry is
> verified fact, not opinion.
>
> This is the middle of three surfaces: a correction or a confirmed
> approach that only this machine needs stays in Claude's auto memory
> (`~/.claude/projects/<repo>/memory/`, shared by the repo's worktrees); a
> surprise about the harness or tooling that every machine must know lands
> here, dated and verified; a rule that generalized graduates to a skill.
>
> Entry format: `- **YYYY-MM-DD · the fact** — context, what it changed.`
> Newest first within each section. An entry is fact + rule + ticket; the
> incident narrative behind a reshaped entry lives in the archive under
> the same date (grove-438). An entry a later one contradicts is rewritten
> to the current fact with a pointer, never left standing. If a learning invalidates a
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

## tmux / git / detector internals (verified against source)

- **2026-10-23 · A train's "depends on" chain can skip a ticket that never merged** (grove-370). gv-keys 07 (#367, `gv keys import`, the legacy `.env` fallback, doctor `keys:legacy-env`) is OPEN with no PR and no commits, yet 08/09 merged on top and the cutover ticket was written assuming it. Before a cutover/deletion ticket, `git log` the train branch for each earlier ticket's feature, not just the one named as its dependency. Also: under load `e2e/all.sh` flaked `supervise.sh` ("gv learnings changed the memory dir" — a concurrent session writing the shared auto memory) and `focus.sh` (a repaint-on-swap leg); both passed alone, and the other 22 legs passed.
- 2026-10-19 (grove-531, tmux 3.6a): **A window with `window-size manual` ignores a client that attaches to it** — a 100x30 pty client attached straight to a slot-pinned 139x50 hidden chat sees a 139x50 window clipped to its screen; `set -wu window-size` makes it follow the client at once (100x29). A SESSION hook (`set-hook -t <@win> client-attached …`, no `-w`) fires for a direct attach; `client-session-changed` fires on attach AND on `switch-client` INTO the session, but nothing fires on the session a client switches AWAY from; `client-detached` fires on detach, and a `run-shell -b gv …` from it already reads `#{session_attached}` = 0. Hook values read as formats like any option (`#{m:*sync-sizes*,#{client-detached}}` tells grove's hook from an operator's global one), and a session-level hook shadows a global hook of the same name for that session. `show-hooks -g` lists every hook NAME, set or not — grep for a value, not the name. `set-hook <name> …` without an `[index]` replaces the whole array (a `[1]` set earlier is gone). Promoted: tmux-discipline §3 (per-session hooks).
- **2026-10-19 · A global `pane-border-status` makes a lone pane a row shorter than its window — size the PANE, not the window** — under the operator's own `set -g pane-border-status top` (tmux 3.6a) a one-pane hidden chat window pinned to the slot's W×H holds a W×(H-1) pane, so `swap-pane` into the slot still resizes it and sends SIGWINCH (Claude repaints on screen). Pin a one-pane window to slot + (window − pane) and compare PANE sizes; `swap-pane -d` keeps every pane option and sends no SIGWINCH when the two panes are the same size; tmux's `after-select-layout` hook fires for CLI-issued commands, a cheap e2e probe for "no re-tile ran". Tests on the scratch server read `~/.tmux.conf` (no `-f`), so a live test pins the option it depends on. grove-526. Promoted: tmux-discipline §3 (pane, not window, size).
- 2026-10-19 (grove-525, tmux 3.6a): **`resize-window -x/-y` silently sets that window's `window-size` to `manual`** — `show-options -w` reads `window-size manual` afterwards with no `set-option` of ours. So an e2e `tmux resize-window -t <cockpit>` pins the cockpit window too, and "unpin on leave" must be an explicit `set-option -wu -t <@win> window-size` (it falls back to the global/user value). Also verified: a `set-hook` body is parsed when it fires, so `cmd1 ; cmd2` in one hook string runs both, in order (the rail's `resize-pane` then `run-shell -b`); `run-shell` format-expands its argument, so a `#` in a path must be doubled. A user conf with `set -g window-size smallest` + `aggressive-resize on` does NOT crash new windows — only global `manual` does. Promoted: tmux-discipline §3 (`resize-window` pins).
- 2026-10-19 (focus swap spike, tmux 3.6a): **`set -g window-size manual` crashes the tmux server on the next new window** — `new-session` (with or without `-x/-y`) or `new-window` after it prints `server exited unexpectedly` and every session on that server is gone (reproduced 2× on an isolated `-L` server, no client attached). Per-window `set -w -t <win> window-size manual` + `resize-window -x -y` is safe. Rule: never set `window-size` globally — on the operator's real server that line is a kill-server. Same spike: a `swap-pane` between two panes of IDENTICAL size sends no SIGWINCH, so Claude Code does not repaint (swapped-in screen byte-identical, ~5ms), while today's break-pane/join-pane into a differently-sized slot costs one full Claude repaint (~270ms on a short transcript). Design: docs/plans/2026-10-19-focus-same-size-swap-design.md. Promoted: tmux-discipline §1 (never `set -g window-size`).
- 2026-10-19 (grove-487, tmux 3.6a): `#{pane_last}` is 1 for the window's previously active pane — the only pane recency tmux has (no per-pane activity time), and the right "which chat was I in" answer when `L` is typed in the dash (the dash is always the active pane then). `run()` trims its output, so a list-panes format whose LAST field is an empty `@option` loses its trailing tabs on the final line and a field-count check rejects that pane — end such a format with a literal `\t.`. Two quick `L` presses run two off-loop layout commands concurrently: an e2e that polls pane count and presses `L` again can race the first command's option write (`@grove_focus_hid`) — wait for the option, not the panes.

- **`tmux.SendKeys` is single-line only** and tmux interprets key-name
  lookalikes in the text. Rule: prose goes via `load-buffer` +
  `paste-buffer` + a separate `send-keys Enter`; a single picker character
  goes raw. tmux-discipline §2.
- **`worktree.Add` uses one string for branch and dir** — fine with the
  `<id>-<slug>` no-slash branch convention; a slash would force a two-arg
  fork.
- **Squash-merge defeats `git branch -d`** — a squash-merged branch is
  never ancestry-merged. Rule: verify via `gh pr view --json
  state,mergedAt`, then `branch -D` + remote delete. shipping-gates
  §Merging.
- **Pane scraping is liveness garnish; hooks are truth** — Claude Code's
  chrome has moved under grove three times (the spinner glyph CYCLES, a
  taller bottom chrome, the unboxed input box). Rule: scan the full
  capture and match a line's SHAPE (`detect.Spinning`), never one glyph or
  a bottom window (grove-300, grove-317). Current chrome: claude-code-facts
  §TUI chrome; rule: tmux-discipline §4.
- **Detector reads `unknown` for a plain shell pane** — LIVE shows
  `unknown` until claude actually boots (e.g. during setup). Expected; the
  task status column carries the truth.
- **`session.EncodePath`** replaces `/` and `.` with `-`; transcripts live
  under `<CLAUDE_CONFIG_DIR>/projects/<encoded-cwd>/`.

## Go / CLI

- **2026-10-23 · Provider API keys moved out of `.env` (successor to the 2026-09-05 "`~/.config/grove/.env` holds provider API keys only" note).**
  They live age-encrypted in `~/.config/grove/secrets/global/NAME.age`
  (`gv keys import` moves them; `.env` is read only as the last
  `secrets.Resolve` fallback for one release, gv-keys ticket 10 removes it).
  Account tokens move to `CLAUDE_ACCOUNT_<NAME>` in the same store; because
  `WrapAccount` `cat`s a path, `account.Store.TokenSource` decrypts a stored
  token to a 0600 file under `$XDG_RUNTIME_DIR/grove-accounts/` (tmpfs).
- **2026-10-22 — `eval "$(cmd)"` swallows cmd's failure.** `eval ""` exits 0, so a wrap `( eval "$(gv keys env …)" && … exec claude )` would launch with an empty token when `keys env` fails. The snippet is `eval "$(… || echo false)"`: the subshell stops before `exec` and the pane stays at a shell. Also: a worker's cwd (its worktree) may not walk up to the workspace, so the root travels in argv (`--workspace-root`), as with `run-setup` — found only because `e2e/keys.sh` ran a real pane.
- **`log-append.py rotate` half-commits when it hits an undated row** (2026-10-11, grove-474). With TASKS.md over its cap, rotate wrote the oldest row into `docs/archive/` and then `sys.exit`ed on the next, undated row, leaving TASKS.md unchanged. The rerun archived that first row again, so it appeared twice. Fix: give each undated row a date from `git log -S'<row text>' --format=%cs -- TASKS.md`, rerun rotate, then check the archive diff for duplicates. Its bare-date fallback also raised IndexError (no capture group) until grove-471 fixed `row_date`.
- **2026-10-11 · The no-secrets PreToolUse guard is not in the repo and is armed only on contributor lanes** (grove-470). `~/.config/grove/hooks/no-secrets-on-contrib.sh` lives in the operator's config dir, and its first line exits 0 unless `GROVE_DATA_LANE=contributor` — a value only a model profile's env map exports. Accounts never apply under a profile (claude-accounts design §Decision 1), so an account-pinned worker never runs with the guard armed: adding `CLAUDE_CODE_OAUTH_TOKEN` to its deny pattern (design §Decision 4) defends only a contributor-lane worker that inherited the token some other way. It is one operator's private file, so the patch is not grove's to ship (no doctor row either: a repo-agnostic binary must not hardcode it). Rule: before citing a hook as a guard, read its arming condition — a guard that exits early on your lane is not one.
- **2026-10-07 · App-token 403 cascade (grove-451).** One bundled `--json` field took down two unrelated features: a GitHub App token can't read `statusCheckRollup`, so the single `gh pr list` that served both the ls/TUI CI column and the `gv done` merge gate failed as a whole — CI columns went blank AND every `gv done` on a genuinely merged ticket reported "no PR found" and demanded `--force`. The gate never looked at CI; it just rode the same query. Rule: a verdict-bearing query asks only for the fields its verdict needs; display queries degrade field-by-field (retry without the privileged field, render unknown) rather than failing the fetch; and a command failure must never be reported with the words of a successful empty result.
- **2026-10-07 · `--model` was never recorded on the task; only
  `model_profile` was** (grove-435). `gv ls --json`, the `Task` struct and
  the `task_created`/`task_adopted` data know a worker's PROFILE, but a
  `--model` pin lived only in the tmux pane's argv and was gone with the
  window. `--effort` therefore could not "mirror `--model`" on the state
  side — it got its own `effort` event key, `Task.Effort`, and a ledger
  column. If `--model` ever needs to be read back (adopt keeping a pin,
  the ledger explaining a cost), it needs the same treatment; today
  `chat`'s `orchestrator_spawned.model` is the only recorded pin.
- **2026-10-07 · With `claude: echo`, every flag appears TWICE in the
  worker pane** (grove-435): once on the typed launch line
  (`echo --effort low "$(cat …)"`) and once in echo's output, which
  prints its args back. A "flag present exactly once" assertion must
  grep the launch line (`grep -o 'echo [^$]*\$(cat'`) and not the whole
  scrollback — the first cut of `e2e/dummy.sh`'s effort leg failed on a
  correct launch for exactly this reason.

- **stdlib `flag` stops at the first positional** — `gv grab <url> --repo
  x` silently ignores `--repo` without a re-parse loop (`parseAnywhere`).
  Flags-after-positionals is table stakes; stdlib doesn't give it to you.
- **Hooks pin the absolute binary path** (`os.Executable()` at `gv hooks
  install`), so they must point at the binary `gv update` writes
  (`~/go/bin/gv`) or no hook-side fix ever ships — grove-339 found both
  profiles pointing at a stale `~/.local/bin/gv`. Rule: refresh only via
  `gv update --yes`; never `go install` (shipping-gates §Handing a change).
- **Cost estimates: dedup + cache asymmetry** — transcript pricing follows
  ccusage's rules: dedup entries by `message.id`+`requestId`, price cache
  reads at 0.1×, 5-minute cache writes at 1.25×, 1-hour at 2×. Costs are
  ESTIMATES of relative effort, never billing.

## Remote / attach architecture (verified against t3code source)

## Field notes (ovs, kept for judgment)

- **2026-10-07 · Kickoff A/B (model-fit 02, #434): the de-choreographed
  template is cost-neutral and slightly more disciplined; `--effort
  medium` is the win on rote work.** Three rote tickets (#145 merge gate,
  #131 six paper cuts, #169 doctor tmux options), each run three ways on
  Fable 5.1 the same afternoon: A = pre-#444 template (served through a
  `grove-old` repo entry with `prompt:` →
  `docs/plans/2026-10-07-model-fit-train/kickoff-old.tmpl`), B = current
  template, C = current template + `--effort medium`; B and C on cloned
  issues (#450–#455, label `ab-test`). Every one of the nine opened a
  PR, passed the bare gate when re-run by the orchestrator, had its DONE
  verified by the Stop evidence gate, and took zero steers. Per ticket
  (est $ / API calls / minutes grab→done): #145 A 2.11/17/7.3, B
  2.45/22/5.8, C 1.70/15/5.8; #131 A 2.14/18/6.6, B 1.87/15/5.5, C
  1.82/18/5.8; #169 A 3.10/25/8.7, B 3.41/26/10.6, C 2.20/16/7.7. A vs B
  is a wash on cost (−13%, +15%, +10%) but B stayed inside #145's
  enumerated surface where A also edited `internal/tui/view.go`, B reused
  the existing test file on #131 where A added `cmd/gv/paper_cuts_test.go`,
  and B's PR bodies carried gate evidence in every case (A: 1 of 3 on
  #131/#169). C was the cheapest arm on all three tickets (−31%, −3%,
  −35% vs B) with the same test counts, the same surface discipline and
  the same gate outcomes. Decision: keep the current template; the rote
  test's first action is `--effort medium` (ticket-writing skill,
  #447); the lane is the second lever. Caveats: three tickets, one
  afternoon, one model; `last_message` is capped at 2000 chars so
  wrap-up evidence was read from PR bodies; the two generic
  "surface drift" hits (notify.go on #131, internal/connections on #169)
  were the same in all three arms — the tickets' line references had
  aged, not the workers.
- **Label→repo inference misses in practice** — real tickets carry
  type-labels (`[Feature]`), not surface-labels; treat `--repo` as the
  common path and inference as a bonus.
- **The `api_key_env` indirection earned its keep on day one** — users'
  keys live under personalized env names; never hardcode the env var.
- **When demoting a data source, wire the replacement into the same code
  path** — ovs's preview column went blind because the cheap poll dropped
  comments while only the on-demand path kept them.
