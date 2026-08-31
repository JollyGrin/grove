---
name: claude-code-facts
description: Use when working on anything that integrates with the Claude Code CLI — hook capture/classification, session resume and gv adopt, transcripts and cost estimation, model profiles / CLAUDE_CONFIG_DIR, or kickoff/relay behavior. Verified facts about how Claude Code actually behaves, so you don't re-derive them.
---

# Claude Code integration facts

Everything below was verified live (dates + incident details in
[LEARNINGS.md](../../../LEARNINGS.md) §"Claude Code behavior"). Trust
these over intuition; re-verify only if a Claude Code major version
changes the behavior.

## Hooks (the source of truth for worker state)

- **Stop** carries `session_id`, `cwd`, `transcript_path`,
  `permission_mode`, and `last_assistant_message` — sentinel
  classification needs no transcript parsing (transcript is fallback for
  the full-message view).
- **Questions arrive via Stop, not Notification** — a plain-text question
  ends the turn. Notification only fires for permission prompts (mostly
  suppressed under `--dangerously-skip-permissions`) and the ~60s idle
  reminder. Question detection = Stop + the `STATUS:` sentinel.
- Hook `cwd` arrives **realpath'd** (`/tmp/x` → `/private/tmp/x`); task
  matching must compare `filepath.EvalSymlinks` of both sides.
- `claude -p` fires SessionEnd at exit, so headless smoke tests end
  `dead` — that's fold order working, not a bug. For real tmux workers,
  `dead` genuinely means crashed/exited.
- When an agent drops the `STATUS:` sentinel, classification degrades to
  `stalled` — correct behavior, not a defect.

## Sessions, resume, transcripts

- Transcripts key on the **encoded cwd**:
  `<CLAUDE_CONFIG_DIR>/projects/<encoded-path>/` where
  `session.EncodePath` replaces `/` and `.` with `-`. Reuse the same
  worktree path to preserve resumability; re-creating a worktree at a new
  path orphans the transcript → pickup-prompt fallback.
- The `.` rule is the one people drop. In shell the encoding is **two**
  substitutions — `sed -e 's#/#-#g' -e 's#\.#-#g'` — and a dotted path
  segment doubles the dash: `/home/dean/git/grove/.grove/orchestrator` →
  `-home-dean-git-grove--grove-orchestrator`, never
  `…-grove-.grove-orchestrator`. A one-rule sed points at a directory that
  does not exist, and every consumer then reports "no transcript" rather
  than an error (grove-202).
- Transcript **filenames are session UUIDs**, so a directory listing has no
  chronological meaning. Pick the current transcript by mtime
  (`max(files, key=os.path.getmtime)`); `sorted(glob(...))[-1]` returns a
  stale session — verified on a 7-file project dir where the two disagree.
- `claude --resume <id>` works ≥6 days after the tmux window died, and a
  resumed session fires SessionStart with the **same** session_id — hook
  re-capture needs no special-casing. It opens idle awaiting input; it
  does not auto-continue.
- **A transcript LINE is not a message.** One line holds many content
  blocks — an assistant turn routinely carries `thinking` plus two
  `tool_use` blocks — so project one entry per BLOCK, not per line. A
  `tool_result` names only the opaque `tool_use_id` it answers, never the
  tool, so pair it back to the `tool_use` that came before it or every
  result renders anonymous. `isSidechain: true` is a SUBAGENT's private
  conversation (skip it in a chat view); `isMeta: true` is injected
  context, not something anyone said. A `thinking` block can arrive with a
  signature and NO text (redacted) — an empty-text block is chrome.
  `internal/chat` (grove-216) is the worked implementation.
- **Following a transcript is a byte offset, not a diff** — it is
  append-only. Consume COMPLETE lines only (a terminatorless trailing line
  is the writer mid-append), with `bufio.Reader`; `bufio.Scanner` silently
  drops a line past its buffer and a `tool_result` routinely runs past
  64KB.
- Never resume via `sessions-index.json` (it points at the parent repo
  and silently misses worktree sessions) — always explicit
  `--resume <id>`.
- State never forgets a session id: `untrack` clears nothing, so
  `gv adopt` always resumes the stored conversation. When the OLD
  conversation is the problem, `gv adopt --manual` is the guaranteed-fresh
  escape hatch, then `gv nudge` restores autonomy.
- `--continue` chains key on **cwd** — per-profile subdirs
  (`.grove/orchestrator/<profile>/`) give each backend its own chain, and
  CLAUDE.md still applies (memory loads recurse up ancestor dirs).

## Profiles and config dirs

- Claude profiles are **separate worlds**: plugins, marketplaces, and MCP
  auth are per-`CLAUDE_CONFIG_DIR`. A fresh worker profile has none of
  the user's plugins — this incident is why the connections manifest
  exists.
- Plugins install at user scope, so skills load from any cwd under that
  profile; worktree placement doesn't matter.
- Claude Code clobbers tmux pane titles on boot — see
  [tmux-discipline](../tmux-discipline/SKILL.md) §4 for the durable-tag
  pattern.

## Costs

- Transcript pricing follows ccusage's rules: dedup by
  `message.id`+`requestId`; cache reads 0.1×, 5-min cache writes 1.25×,
  1-hour 2×. All numbers are ESTIMATES of relative effort, never billing.
- OpenRouter answers with **dated** model slugs (`z-ai/glm-5.2-20260616`)
  while config holds `z-ai/glm-5.2` — lookups need
  exact-match-then-prefix-match at a `-` boundary.
- Prompt caching survives OpenRouter→Z.AI (~99.3% hit on turn 2), so the
  ~50k kickoff floor is per-SESSION, not per-turn — prefer one long-lived
  orchestrator chat with `/clear` between topics over close-and-reopen.
