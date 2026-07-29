---
name: tmux-discipline
description: Use when writing or reviewing ANY code, script, or e2e test that touches tmux — sessions, windows, panes, send-keys, kill-server, isolation, window names, or pane titles. Every rule here was learned via a live incident; violating them can kill the operator's real fleet.
---

# tmux discipline

Grove's workers and cockpit live on the operator's REAL tmux server. The
rules below are ordered by blast radius. War stories + dates:
[LEARNINGS.md](../../../LEARNINGS.md) §"tmux / git / detector internals".

## 1. Isolation: `$TMUX` beats `TMUX_TMPDIR` (the grove-7 crash)

tmux resolves its socket as `-S`/`-L` > `$TMUX` > `TMUX_TMPDIR`. Any
"isolated tmux server" script run from inside a tmux pane (i.e. from any
grove worker) silently targets the **real server** unless it clears
`$TMUX` first.

- Every isolation script must `unset TMUX` up front, or wrap every tmux
  call in `env -u TMUX`.
- **Never run bare `tmux kill-server` in any script.** Always scope it:
  `env -u TMUX TMUX_TMPDIR=<scratch> tmux kill-server`. A bare one killed
  every session and worker on the machine (2026-07-07).
- `tapes/run.sh` snapshots the real server's session list before/after as
  a canary — copy that pattern for new scripted-tmux suites.

## 2. Sending text to panes

- `tmux send-keys` is **single-line only**, and tmux interprets key-name
  lookalikes (`Enter`, `Space`, …) inside the text. Never use it for
  prose. Relay replies via `load-buffer` + `paste-buffer` + a separate
  `send-keys Enter` — in Go, use the existing relay path in
  `internal/tmux`.
- Exception: a single character aimed at an option picker goes through
  raw, without Enter-wrapping.
- tmux buffers are **server-global**. Use the `gv-relay` buffer name;
  never invent a generic name another tool could clobber mid-paste.
- **Delivered is not submitted** (grove-144). `paste-buffer` immediately
  followed by `send-keys Enter` loses the Enter: the receiving TUI is
  still ingesting the paste and swallows it into the input, leaving an
  unsent `[Pasted text]`. Paste **bracketed** (`-p`), settle ~250ms, press
  Enter, then *verify* by scraping the pane's input box — retry Enter
  once, then fail loudly. Never append an "answered"-class event off a
  paste whose submit wasn't verified; a silent success costs far more
  than a loud failure.
- Scraping to verify reads the **whole visible pane**, never
  `CapturePaneBottom`: that helper takes the pane's bottom N *rows*, which
  are blank whenever the app draws from the top (it silently passed every
  relay until `e2e/relay.sh` caught it). Keep the check permissive —
  unreadable pane or no recognizable box counts as landed.

## 3. Finding things: resolve, never assume

- Claude's pane is **resolved, never assumed** — windows lose splits,
  panes renumber, and Claude's process title is its bare version string.
  All relay/detector/editor-inject paths go through `tmux.ClaudePane`.
- Window names drift (trailing dashes, glyph changes), so never rely on
  exact window-name equality; re-derive and re-store on adopt. But tmux's
  window-side **prefix matching is a trap, not the answer** (grove-116):
  worker names are prefixes of each other (`repo · grove-1` vs
  `repo · grove-10`), so a `session:name` target is ambiguous while both
  live (a live glyphed worker reads as dead) and **silently resolves to
  the sibling** once the task's own window dies — kill/rename/paste then
  hits the wrong worker. NEVER build a tmux target from a stored window
  name: resolve through `tmux.WindowID` (list-windows +
  `matchesWindowName`, glyph-tolerant) and target the immutable `@N` id;
  relay text via `tmux.ClaudePaneTarget`'s `%N` pane id.
- The session-side twin of that rule (grove-78): a BARE `-t <session>`
  target resolves against **window names across all sessions** too — a
  session literally named `grove` collides with every `grove · <ticket>`
  worker window (`new-window -t grove` died on "index 1 in use" in a
  different session). Every session-scoped `-t` must be `=`-anchored; the
  window side is never name-targeted at all since grove-116 — windows
  resolve to `@N` ids via `tmux.WindowID` (glyph tolerance lives in
  `matchesWindowName`, not in tmux's matcher).
- But the anchor form depends on the command's target KIND (grove-99,
  tmux 3.6a): `tmux.Exact` (`-t '=grove'`) is only valid where `-t` is a
  *target-session* (`has-session`, `kill-session`, `new-window`,
  `list-windows`, `switch-client`, `attach-session`). Commands whose `-t`
  is a *target-pane/window* (`set-option`, `show-options`,
  `select-layout`, `split-window`, `display-message`) reject bare `=name`
  ("no such session") and need `tmux.ExactActive` (`-t '=grove:'` — exact
  session, active window). Getting this wrong broke every cockpit build;
  `e2e/cockpit.sh` is the tripwire — actually run it.
- Commands typed into panes resolve via `PATH`, not via the binary that
  created the session. Any pane/hook command must embed the absolute
  `os.Executable()` path.

## 4. Titles and detection

- Claude Code clobbers pane titles on boot (OSC "✳ Claude Code").
  Durable per-pane tags live in a tmux pane **user option**
  (`set-option -p @grove_…`) rendered via a conditional
  `pane-border-format` — foreground programs can't touch those.
- Pane-scraping is liveness garnish; **hooks are truth**. Spinner glyphs
  and chrome layout have both changed under us — activity checks scan the
  full ~30-line capture, never a bottom window.
- The detector reads `unknown` for a plain shell pane (before Claude
  boots). Expected; the task status column carries the truth.
- e2e assertions on pane content: a bare `capture-pane -p` sees only the
  visible screen of the active pane — delivered text scrolls off and
  hard-wraps at pane width. Capture every pane with `-S -` (scrollback)
  and `tr -d '\n'` before grepping (grove-75 field-hit).
