#!/usr/bin/env bash
# demos/lib/tape.sh — tape DSL for automated terminal recordings.
#
# Adapted from the global `asciinema` skill's lib/tape.sh. Differences:
#   * honors an already-set TMUX_TMPDIR (the dummy sandbox's isolated tmux
#     server), so the recording session and any tmux the recorded command
#     spawns all live off the user's real tmux;
#   * `tape_send` runs a command WITHOUT echoing an Enter-delay, and
#     `tape_type` types char-by-char for a natural cadence.
#
# A tape script sources dummy-env.sh + this file, calls tape_init, drives
# `gv`, then tape_finish. Output: a v3 .cast file.
set -euo pipefail

TAPE_SESSION=""
CAST_FILE=""

# ── tape_init <name> [cols] [rows] [cast_dir] ────────────────
tape_init() {
  local name="${1:?tape_init requires a name}"
  local cols="${2:-100}"
  local rows="${3:-28}"
  local cast_dir="${4:-$(pwd)/casts}"
  TAPE_SESSION="tape-${name}-$$"
  CAST_FILE="${cast_dir}/${name}.cast"
  mkdir -p "$cast_dir"

  for cmd in tmux asciinema; do
    command -v "$cmd" &>/dev/null || { echo "ERROR: $cmd not found" >&2; exit 1; }
  done

  tmux new-session -d -s "$TAPE_SESSION" -x "$cols" -y "$rows"
  # Record a CLEAN bash (no rc/profile → no user prompt, no autosuggest
  # doubling). HOME is the scratch dir so there's no ~/.bashrc to read.
  tmux send-keys -t "$TAPE_SESSION" \
    "SHELL=$(command -v bash) asciinema rec --cols ${cols} --rows ${rows} --overwrite '${CAST_FILE}' -c 'bash --norc --noprofile'" Enter
  sleep 1.4   # let asciinema + the inner bash come up
  # Minimal prompt, quiet the terminal.
  tmux send-keys -t "$TAPE_SESSION" "PS1='\$ ' PROMPT_COMMAND='' TERM=xterm-256color; clear" Enter
  sleep 0.4
}

# ── tape_run <command> ───────────────────────────────────────
# Type a command char-by-char (visible), then press Enter.
tape_run() {
  tape_type "${1:?tape_run requires a command}" "${2:-0.035}"
  sleep 0.25
  tmux send-keys -t "$TAPE_SESSION" Enter
}

# ── tape_send <command> ──────────────────────────────────────
# Send a command instantly (no per-char typing) then Enter. Use for
# scaffolding you don't want on screen char-by-char.
tape_send() {
  tmux send-keys -t "$TAPE_SESSION" "${1:?tape_send requires a command}" Enter
}

# ── tape_type <text> [delay] ─────────────────────────────────
tape_type() {
  local text="${1:?tape_type requires text}" delay="${2:-0.05}"
  local i char
  for (( i=0; i<${#text}; i++ )); do
    char="${text:$i:1}"
    tmux send-keys -t "$TAPE_SESSION" -l "$char"
    sleep "$delay"
  done
}

# ── tape_key <keys...> ───────────────────────────────────────
tape_key() {
  local key
  for key in "$@"; do
    tmux send-keys -t "$TAPE_SESSION" "$key"
    sleep 0.06
  done
}

# ── tape_comment <text> ──────────────────────────────────────
# Echo a dimmed "# comment" line into the recording — narration in-terminal.
tape_comment() {
  tmux send-keys -t "$TAPE_SESSION" -l "printf '\\033[2m# ${1//\'/}\\033[0m\\n'"
  sleep 0.15
  tmux send-keys -t "$TAPE_SESSION" Enter
  sleep 0.4
}

# ── tape_sleep <seconds> ─────────────────────────────────────
tape_sleep() { sleep "${1:?tape_sleep requires seconds}"; }

# ── tape_wait_for <pattern> [timeout] ────────────────────────
tape_wait_for() {
  local pattern="${1:?pattern}" timeout="${2:-15}" elapsed=0
  while (( elapsed * 10 < timeout * 10 )); do
    if tmux capture-pane -t "$TAPE_SESSION" -p | grep -qE "$pattern"; then
      return 0
    fi
    sleep 0.3; elapsed=$(( elapsed + 1 ))
  done
  echo "WARN: tape_wait_for '$pattern' timed out after ${timeout}s" >&2
}

# ── tape_finish ──────────────────────────────────────────────
tape_finish() {
  sleep 0.5
  tmux send-keys -t "$TAPE_SESSION" "exit" Enter   # stop asciinema
  sleep 0.8
  tmux send-keys -t "$TAPE_SESSION" "exit" Enter   # exit the shell
  sleep 0.5
  tmux kill-session -t "$TAPE_SESSION" 2>/dev/null || true
  echo "Recording saved: $CAST_FILE"
}
