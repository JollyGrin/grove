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

- **2026-10-07 · Auto memory is keyed on the git REPO, not the cwd, so
  every worktree shares `<config dir>/projects/<encoded main checkout>/
  memory/` — and the one settings scope that is per-repo and private,
  `.claude/settings.local.json`, never reaches a fresh worktree** (grove-437).
  Verified on this machine: a worker whose cwd was a worktree was handed
  `~/.claude/projects/-home-dean-git-grove/memory/`, none of 24 worktree
  project dirs has a `memory/` subdir, and `.claude/` in main and in a
  worktree hold only `skills/`. `autoMemoryDirectory` is read from any
  scope but must be absolute or `~/` (no relative path), so relocating
  the surface beside `.grove/` means a committed absolute path (project
  scope) or one dir for every repo (user scope) — hence the doctor row
  reads the default location instead. A headless `claude -p` session
  DOES carry the auto-memory system prompt (`prompt_snapshot` has
  "persistent file-based memory"), so headless dry runs are a valid
  probe of whether a kickoff makes workers write memory.
- **2026-10-07 · Claude Code 2.1.292 ships Anthropic's Fable 5.1 autonomy
  block in the system prompt, so the kickoff's "ask — otherwise do not ask
  for confirmation" line was a duplicate** (grove-433). Checked on three
  real worker transcripts (grove-435/441/442): the `prompt_snapshot`
  attachment carries "You are operating autonomously … asking 'Shall
  I…?' will block the work", the "check your last paragraph" rule and the
  "Delivering work" scope paragraphs verbatim from the migration guide.
  The kickoff templates now state goals, constraints and verification
  (no numbered steps, no hedge); the autonomy paragraph is NOT added to
  the template — re-verify via the transcript before ever adding it back.
  Rule distilled into `.claude/skills/claude-code-facts`.
- **A session restart after compaction is a SessionStart with
  `source: "compact"`, not a new session** (grove-289). Distinguishing it
  matters for two reasons: folding it as `session_started` would flip a
  worker's glyph and inflate its session count for something that isn't a
  fresh pickup, and `gv cost --context`'s compaction count depends on
  seeing it as its own event (`state.EvCompaction`) rather than losing it
  inside `session_started`. The ticket's research draft additionally
  claims sessions pinned to the `[1m]` cache tier never hit Claude Code's
  auto-compact threshold in practice — plausible (a 1h cache write keeps
  far more of the transcript "hot" before the context window fills), but
  this session had no live long-running `[1m]`-pinned transcript to
  independently reproduce that against; treat it as a hypothesis to watch
  `gv cost --context`'s `compactions`/`NeverCompacted` flag for, not yet
  independently confirmed here.

## tmux / git / detector internals (verified against source)

- **2026-09-27 · `tmux kill-server` returns before the server's panes
  are gone, so an e2e `rm -rf "$SCRATCH"` right after it can race**
  (grove-377 saw it once in `e2e/plugin.sh`, grove-383 fixed it): every
  assertion passed, then cleanup died on "rm: Directory not empty" because
  a pane process was still writing under the scratch tree, and the suite
  read red. Wait for the isolated socket
  (`$TMUX_TMPDIR/tmux-$(id -u)/default`) to vanish, then retry the rm
  once — `e2e/serve.sh`'s `cleanup()` already did; plugin.sh now does too.

- **2026-09-27 · macOS `script(1)` drops a piped answer that arrives
  before the child prompts** (grove-380): `e2e/serve.sh` drives the TTY
  trust prompt with `printf 'y\n' | script -q /dev/null gv serve …`, and
  gv read a bare EOF (the pane echoed `^Dy`) — so "y" became "no". Hold
  the answer back (`{ sleep 1; printf 'y\n'; sleep 1; } | script …`) so
  it lands after the prompt. Related, verified on tmux 3.6a: `new-window
  -n <name> <argv…>` with more than one command argument execs argv
  directly (no user shell), `-e K=V` sets the env for just that window,
  and `-P -F '#{window_id}'` hands back the `@N` id — the serve window
  needs none of SendKeys' quoting. Serve windows are matched by EXACT
  name (`tmux.WindowIDExact`), not `matchesWindowName`: its " <glyph>"
  tolerance would let `▶ keys` hit a `▶ keys 2`.

- **`tmux.SendKeys` is single-line only** and tmux interprets key-name
  lookalikes in the text. Never use it for prose — relay replies via
  `load-buffer` + `paste-buffer` + a separate `send-keys Enter`. If a
  reply is a single character and the pane tail looks like an option
  picker, pass it through raw without Enter-wrapping.
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
- **A local editor alias fools `gv editor`'s "already running?" check**
  (grove-359, 2026-09-27) — detection matches the pane's foreground
  command against `editor.command`'s binary, so a shell alias (`vi` →
  nvim) reads as not-running and a second editor opens. Fix the alias
  (or set `editor.command: nvim`) in your own config, not in gv.
- **Detector reads `unknown` for a plain shell pane** — LIVE shows
  `unknown` until claude actually boots (e.g. during setup). Expected; the
  task status column carries the truth.
- **`session.EncodePath`** replaces `/` and `.` with `-`; transcripts live
  under `<CLAUDE_CONFIG_DIR>/projects/<encoded-cwd>/`.

## Go / CLI

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
- **2026-09-27 · `gv feature new --adopt` defaults the label to the SLUG,
  not the branch or the issues' label** (grove-383): the gv-keys train's
  issues carry the label `gv-keys`, so `gv feature new keys --adopt
  --branch feature/gv-keys-secrets` registers label `keys`, infers
  nothing on grab, and every car silently forks from main. Pass
  `--label <the issues' label>` whenever the slug differs from it; check
  with `gh issue view <n> --json labels` first.

- **2026-09-26 · A shared-repo host turns "orphan" into "every sibling
  workspace's live worker"** (grove-350, seen on groveremote). `gv audit`'s
  `scanOrphans` walked every repo in `cfg.Repos` and subtracted only the
  CALLING workspace's own tasks — fine on the Mac, where repos are
  per-workspace, but wrong on a host like groveremote where the repo table
  lives in the GLOBAL `~/.config/grove/config.yaml` and every workspace
  deep-merge-inherits all of it (waterhouse/unbrewed/deanlol, see
  `waterhouse-on-groveremote` memory). A worktree another workspace is
  actively running is then "untracked" from the auditor's point of view:
  32 of 36 reported orphans in the `runelite` workspace were live,
  DIRTY, worker-attached trees. Fixed by unioning the tracked set with
  every OTHER registered+alive workspace's tasks (`workspace.ActiveWorktrees`,
  read via `state.Peek` — same fresh-not-derived read `FindTicket` already
  used for ticket routing) and the legacy global state dir. `scanProcesses`
  turned out NOT to share the bug: its `reapable` set only ever keys off
  tickets already present in the calling workspace's own tasks map, and a
  ticket has exactly one owning workspace (grove-191 routing), so a
  sibling's worktree can never appear there. The registry read itself
  stays at the `cmd/gv` call site, not inside `internal/audit` — mirrors
  `FindTicket`'s shape (registry loaded by the caller, matching logic takes
  a plain `[]workspace.Workspace`) so the audit package stays unit-testable
  without a real `$HOME/.config/grove/registry.yaml`.

- **2026-09-26 · a flag injected after the command head loses to the same
  flag already in the configured command** (grove-142). `config.WithModel`
  built the launch command as `head + " --model 'sonnet' " + rest`, so a
  repo whose `claude:` line already carried its own `--model` (e.g.
  `claude --dangerously-skip-permissions --model opus`) produced `claude
  --model 'sonnet' --dangerously-skip-permissions --model opus` — the
  claude CLI resolves repeated `--model` flags last-wins, so the
  pre-existing config flag silently beat the pin every time, on both
  `gv grab --model` and `gv adopt --model` (same `WithModel` call, two
  call sites). `gv` still reported "model pinned" — nothing checked what
  actually ran; only the ledger's `models:` field on a finished session
  told the truth. Fixed in PR #295: `WithModel` now strips any existing
  `--model`/`--model=<value>` out of the configured command before
  injecting the pin, so the pin always wins regardless of what the
  configured command already contains. Generalizes: when a function's job
  is "make sure X wins," appending X is not enough if the base string can
  already contain X — strip-then-inject, or inject last, never assume the
  base is flag-free. Add a test for the exact repro shape
  (`WithModel("claude --flag --model opus", "sonnet")` → sonnet wins)
  whenever this pattern shows up elsewhere.

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
