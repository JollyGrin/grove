# Grove — learnings archive · 2026-10

> Rotated out of [LEARNINGS.md](../../LEARNINGS.md) by
> `scripts/log-append.py` (grove-275). Same entry format and sections;
> newest first within each section. The rules these entries taught live
> in `.claude/skills/`; this is the dated record. Grep `LEARNINGS.md
> docs/archive/LEARNINGS-*.md` for the full log.

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

## tmux / git / detector internals (verified against source)

- **2026-10-07 · `show-options` takes ONE option name, and does not auto-start a server.** tmux 3.4: `tmux show-options -g a b c` fails with "too many arguments (need at most 1)", and on a machine with no server running `tmux show-options -g` answers "error connecting to …/default" instead of sourcing the config (only `start-server`/`new-session` boot one). The working read-only query is a single exec `tmux start-server \; show-options -g \; show-options -gw` — on a serverless machine the started server serves the query and exits (no session, `exit-empty` default); on a live one `start-server` is a no-op. `pane-base-index` and `allow-rename` live in the WINDOW table (`-gw`), `base-index`/`renumber-windows` in the session table — one table alone misses half of them. Found implementing grove-455/#169, whose ticket text assumed both the multi-name form and the auto-start.
