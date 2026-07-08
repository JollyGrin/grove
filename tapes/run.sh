#!/usr/bin/env bash
# tapes/run.sh <tape> — record one vhs tape against a fully isolated grove.
#
# Wraps `vhs <tape>` with the dummy-data safety harness:
#   1. mktemp a scratch dir, export GROVE_TAPE_SCRATCH, pre-build gv into it
#      (so the tape's `source tapes/lib/env.sh` is instant on camera);
#   2. snapshot live grove/overstory state (the e2e/dummy.sh canary) AND the
#      real tmux server's session list;
#   3. run vhs from the repo root with $TMUX stripped;
#   4. kill the ISOLATED tmux server (env -u TMUX + scratch TMUX_TMPDIR —
#      never the real one), remove the scratch, re-snapshot and fail loudly
#      if anything live changed.
#
# $TMUX beats TMUX_TMPDIR in tmux's socket resolution, so every tmux (and
# vhs) invocation here runs under `env -u TMUX` — otherwise, launched from
# inside a worker pane, the "isolated" server is silently the real one and
# the cleanup kill-server takes down the whole machine (2026-07-07 crash).
#
# Usage:  tapes/run.sh tapes/cockpit-question.tape
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

TAPE="${1:?usage: tapes/run.sh <tape file>}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
command -v vhs  >/dev/null || fail "vhs not installed (brew install vhs)"
command -v tmux >/dev/null || fail "tmux not installed"

# $TMUX canary: if we're inside a tmux pane, say so loudly — every tmux/vhs
# call below MUST strip it (env -u TMUX), or the scratch server is silently
# the real one. The assertion at the end proves the handling worked.
if [ -n "${TMUX:-}" ]; then
  say "\$TMUX is set (running inside a tmux pane) — stripping it for every tmux/vhs call"
fi

# Live-state canary (same set as e2e/dummy.sh): append-only logs + configs,
# plus the REAL tmux server's session list (via the ambient $TMUX socket —
# deliberately NOT stripped here; this is the one place we want the real
# server). If the real server isn't running, the snapshot is empty.
REAL_HOME="$HOME"
snapshot_live() {
  for f in "$REAL_HOME/.local/state/overstory/events.jsonl" \
           "$REAL_HOME/.config/overstory/config.yaml" \
           "$REAL_HOME/.local/state/grove/events.jsonl" \
           "$REAL_HOME/.config/grove/config.yaml" \
           "$REAL_HOME/.cc-work/settings.json"; do
    [ -e "$f" ] && stat -f '%N %m %z' "$f"
  done
  true
}
snapshot_real_tmux() {
  tmux list-sessions -F '#{session_name}' 2>/dev/null | sort
  true
}
LIVE_BEFORE="$(snapshot_live)"
TMUX_BEFORE="$(snapshot_real_tmux)"

export GROVE_TAPE_SCRATCH="$(mktemp -d /tmp/grove-tape.XXXXXX)"
cleanup() {
  # kill-server is safe ONLY because both $TMUX is stripped and
  # TMUX_TMPDIR points at the scratch socket dir: this is the isolated
  # server the tape created, never the machine's.
  env -u TMUX TMUX_TMPDIR="$GROVE_TAPE_SCRATCH/tmux" tmux kill-server 2>/dev/null || true
  chmod -R u+w "$GROVE_TAPE_SCRATCH" 2>/dev/null || true
  rm -rf "$GROVE_TAPE_SCRATCH"
}
trap cleanup EXIT

say "pre-build gv → $GROVE_TAPE_SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GROVE_TAPE_SCRATCH/gv" ./cmd/gv)

say "vhs $TAPE"
mkdir -p "$REPO_ROOT/tapes/out"
(cd "$REPO_ROOT" && env -u TMUX -u TMUX_PANE vhs "$TAPE")

say "live state untouched?"
LIVE_AFTER="$(snapshot_live)"
[ "$LIVE_BEFORE" = "$LIVE_AFTER" ] || {
  printf '%s\n---\n%s\n' "$LIVE_BEFORE" "$LIVE_AFTER"
  fail "live grove/overstory state changed during the recording"
}
TMUX_AFTER="$(snapshot_real_tmux)"
# Sibling agents legitimately create sessions on the real server while we
# record, so whole-list equality false-positives on a fleet machine. The
# two signals that mean OUR isolation broke:
#   - a session that existed before is GONE (the kill-server catastrophe);
#   - a session with one of our fixture names APPEARED (pollution leak).
MISSING="$(comm -23 <(printf '%s\n' "$TMUX_BEFORE") <(printf '%s\n' "$TMUX_AFTER"))"
ADDED="$(comm -13 <(printf '%s\n' "$TMUX_BEFORE") <(printf '%s\n' "$TMUX_AFTER"))"
LEAKED="$(printf '%s\n' "$ADDED" | grep -Ex '(grove|pr-demo)' || true)"
[ -z "$MISSING" ] || {
  printf 'sessions gone from the REAL server:\n%s\n' "$MISSING"
  fail "real tmux sessions disappeared during the recording (isolation leak — \$TMUX handling broken?)"
}
[ -z "$LEAKED" ] || {
  printf 'fixture sessions on the REAL server:\n%s\n' "$LEAKED"
  fail "tape fixture sessions leaked onto the real tmux server (isolation leak — \$TMUX handling broken?)"
}
[ -z "$ADDED" ] || printf 'note: unrelated tmux sessions appeared during the recording (sibling activity, ignored):\n%s\n' "$ADDED"

say "outputs"
ls -lh "$REPO_ROOT"/tapes/out/ 2>/dev/null || true
say "PASS"
