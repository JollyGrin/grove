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

## 3. Finding things: resolve, never assume

- Claude's pane is **resolved, never assumed** — windows lose splits,
  panes renumber, and Claude's process title is its bare version string.
  All relay/detector/editor-inject paths go through `tmux.ClaudePane`.
- Window names drift (trailing dashes, glyph changes) but `-t
  session:window` lookups still hit because tmux **prefix-matches**
  targets. Never rely on exact window-name equality; re-derive and
  re-store on adopt.
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
