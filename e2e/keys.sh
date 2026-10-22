#!/usr/bin/env bash
# gv keys exec E2E (design §9/§10): scratch HOME + GROVE_STATE_DIR, no tmux
# server touched. Asserts the value reaches only the child and appears
# nowhere gv writes.
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-e2e-keys.XXXXXX)"
trap 'chmod -R u+w "$SCRATCH" 2>/dev/null; rm -rf "$SCRATCH"' EXIT

# Build before HOME moves (the Go module cache lives under the real HOME).
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

export HOME="$SCRATCH/home"
export GROVE_STATE_DIR="$SCRATCH/state"
export HISTFILE="$SCRATCH/histfile"
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"
mkdir -p "$HOME" "$GROVE_STATE_DIR" "$TMUX_TMPDIR" "$SCRATCH/work"
cd "$SCRATCH/work"

say "init + set"
"$GV" keys init >/dev/null
printf hunter2 | "$GV" keys set DUMMY_KEY >/dev/null
printf other | "$GV" keys set DUMMY_OTHER >/dev/null

say "exec: value reaches the child"
"$GV" keys exec DUMMY_KEY -- sh -c 'printf %s "$DUMMY_KEY" > seen-exec'
[ "$(cat seen-exec)" = hunter2 ] || fail "seen-exec = $(cat seen-exec)"
out="$("$GV" keys exec DUMMY_KEY -- printenv DUMMY_OTHER || true)"
[ -z "$out" ] || fail "unnamed secret leaked: $out"

say "exec: child's exit code"
"$GV" keys exec DUMMY_KEY -- true || fail "true should exit 0"
rc=0; "$GV" keys exec DUMMY_KEY -- false || rc=$?
[ "$rc" = 1 ] || fail "false exit = $rc"

say "exec: usage error and unknown name never run the command"
rc=0; "$GV" keys exec DUMMY_KEY true 2>/dev/null || rc=$?
[ "$rc" != 0 ] || fail "no -- should fail"
rc=0; "$GV" keys exec NOPE_KEY -- touch fired 2>/dev/null || rc=$?
[ "$rc" != 0 ] || fail "unknown name should fail"
[ ! -e fired ] || fail "command ran despite unknown name"

say "exec: empty-arg warning"
"$GV" keys exec DUMMY_KEY -- sh -c 'echo ""' 2>err >/dev/null
[ ! -s err ] || fail "warned on a script-internal empty string"
"$GV" keys exec DUMMY_KEY -- echo "" 2>err >/dev/null
grep -q "sh -c" err || fail "no warning for an empty argument"

say "hunter2 appears nowhere gv writes"
if grep -rl hunter2 "$GROVE_STATE_DIR" "$HOME" "$HISTFILE" 2>/dev/null; then
  fail "plaintext found on disk"
fi
"$GV" ls --json 2>/dev/null | grep -q hunter2 && fail "gv ls --json leaked the value"

say "PASS"
