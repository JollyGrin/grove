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

- **2026-10-07 · Auto memory is keyed on the git REPO, not the cwd — every
  worktree shares `<config dir>/projects/<encoded main checkout>/memory/`,
  and `.claude/settings.local.json` never reaches a fresh worktree**
  (grove-437). Rule: treat auto memory as one surface per repo;
  `autoMemoryDirectory` must be absolute or `~/`-rooted, so the doctor row
  reads the default location rather than relocating it. A headless
  `claude -p` run carries the auto-memory system prompt, so it is a valid
  probe of whether a kickoff makes workers write memory. Rule:
  claude-code-facts §Profiles.
- **2026-10-07 · Claude Code 2.1.292 injects Anthropic's Fable 5.1 autonomy
  block itself, so a kickoff's "do not ask for confirmation" line is a
  duplicate** (grove-433). Rule: templates state goals, constraints and
  verification only; before ever adding the paragraph back, grep a real
  worker transcript's `prompt_snapshot` for "operating autonomously".
  Rule: claude-code-facts §System prompt.
- **2026-09-07 · A restart after compaction is a SessionStart with
  `source: "compact"`, not a new session** (grove-289). Rule: never fold
  it as `session_started` (that would flip the worker's glyph and inflate
  its session count); it is its own event, `state.EvCompaction`, which
  `gv cost --context` counts. The claim that `[1m]`-pinned sessions never
  auto-compact is an unverified hypothesis — watch `compactions` /
  `NeverCompacted` before relying on it. Rule: claude-code-facts §Hooks.

## tmux / git / detector internals (verified against source)

- **2026-10-07 · `tmux show-options` takes ONE option name and never
  auto-starts a server** (grove-455). Rule: the read-only query is a single
  exec `tmux start-server \; show-options -g \; show-options -gw` (on a
  serverless machine the started server answers and exits; on a live one
  `start-server` is a no-op); `pane-base-index`/`allow-rename` live in the
  WINDOW table (`-gw`), `base-index`/`renumber-windows` in the session
  table — one table alone misses half. Rule: tmux-discipline §3.
- **2026-09-27 · `tmux kill-server` returns before its panes are gone, so
  an immediate `rm -rf "$SCRATCH"` can race** (grove-383). Rule: wait for
  the isolated socket to vanish, then retry the rm once — copy
  `cleanup()` from `e2e/serve.sh`. Rule: shipping-gates §The gate.
- **2026-09-27 · macOS `script(1)` drops a piped answer that arrives
  before the child prompts** (grove-380). Rule: hold the answer back —
  `{ sleep 1; printf 'y\n'; sleep 1; } | script -q /dev/null …`. Verified
  alongside (tmux 3.6a): `new-window -n <name> <argv…>` execs argv with no
  shell, `-e K=V` scopes env to that window, `-P -F '#{window_id}'`
  returns the `@N` id; serve windows match by EXACT name
  (`tmux.WindowIDExact`), never `matchesWindowName`. Rule: shipping-gates
  §The gate.
- **2026-09-27 · A shell alias fools `gv editor`'s already-running check**
  (grove-359). Rule: detection matches the pane's foreground command
  against `editor.command`'s binary, so an alias (`vi` → nvim) reads as
  not running and a second editor opens — set `editor.command: nvim` (or
  drop the alias) in your own config, not in gv.
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
- **2026-09-27 · `gv feature new --adopt` defaults the label to the SLUG,
  not the branch or the issues' label** (grove-383): the gv-keys train's
  issues carry the label `gv-keys`, so `gv feature new keys --adopt
  --branch feature/gv-keys-secrets` registers label `keys`, infers
  nothing on grab, and every car silently forks from main. Pass
  `--label <the issues' label>` whenever the slug differs from it; check
  with `gh issue view <n> --json labels` first.

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
