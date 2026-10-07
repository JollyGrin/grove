# Grove — learnings archive · 2026-06

> Rotated out of [LEARNINGS.md](../../LEARNINGS.md) by
> `scripts/log-append.py` (grove-275). Same entry format and sections;
> newest first within each section. The rules these entries taught live
> in `.claude/skills/`; this is the dated record. Grep `LEARNINGS.md
> docs/archive/LEARNINGS-*.md` for the full log.

## Claude Code behavior (verified in ovs)

- **2026-06-10 · sessions-index is unreliable for worktrees** — Claude's
  `sessions-index.json` points at the parent repo and silently misses
  sessions. Always resume by explicit `--resume <id>`, never path-based
  lookup. (Inherited stance from parkranger.)
- **2026-06-10 · `claude -p` fires SessionEnd at exit** — a headless
  session ends as `dead` *after* Stop's classification (fold order is
  correct; the question survives on the task). Don't be confused by dead
  status in `-p` smoke tests; for real tmux workers, `dead` genuinely
  means crashed/exited.
- **2026-06-10 · plugins install at user scope** — `claude plugin install`
  from a directory-source marketplace lands at user scope, so skills load
  from any cwd under that profile; worktree placement doesn't matter.
  Directory-source marketplaces update via `git pull` in the source clone.
- **2026-06-10 · profiles are separate worlds** — plugins, marketplaces,
  and MCP auth are **per-profile** (`~/.claude` vs any
  `CLAUDE_CONFIG_DIR`). A fresh worker profile has none of the user's
  plugins — workers run conventionless until the profile is wired. (This
  incident created ovs's doctor; grove's connections manifest exists
  because of it.)
- **2026-06-10 · questions arrive via Stop, not Notification** — a
  plain-text question from an agent **ends the turn**. Notification only
  fires for permission prompts (mostly suppressed under
  `--dangerously-skip-permissions`) and the ~60s idle reminder. Question
  detection rides Stop + the `STATUS:` sentinel; Notification is re-pings.
- **2026-06-10 · hook payloads (verified live)** — Stop carries
  `session_id`, `cwd`, `transcript_path`, `permission_mode`, AND
  **`last_assistant_message`** — sentinel classification needs no
  transcript parsing (transcript stays the fallback for the full-message
  view). SessionStart carries `session_id` + `cwd` + `source`. Gotcha:
  `cwd` arrives realpath'd (`/tmp/x` → `/private/tmp/x`) — task matching
  must compare `filepath.EvalSymlinks` of both sides.

## Field notes (ovs, kept for judgment)

- **The full loop is proven** (2026-06-10, DEV-4556): grab → worktree +
  setup → autonomous work → PR + CI green + previews + self-review →
  ticket transitioned by the agent via MCP → clean `STATUS: DONE`
  sentinel. Sentinel compliance was 1/1 on day one; when an agent drops
  the sentinel, classification degrades to `stalled` — correct behavior.
