#!/usr/bin/env bash
# grove-380 E2E: `gv serve` — the .grove/run.sh contract, the sha256 trust
# gate, per-feature ports, the serve worktree, and the exact "▶ <slug>"
# window — on scratch HOME, a scratch bare origin and an ISOLATED tmux
# server (scratch TMUX_TMPDIR + `unset TMUX`; tmux-discipline rule 1).
# The TTY trust prompt is driven through script(1), the only way to hand
# gv a real terminal from a script.
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-serve.XXXXXX)"
SCRATCH="$(cd "$SCRATCH" && pwd -P)"   # /tmp → /private/tmp on macOS

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

# Live-server canary: the session list of the REAL server (read-only),
# taken with the caller's environment before isolation kicks in.
real_sessions() { tmux list-sessions -F '#{session_name}' 2>/dev/null | sort || true; }
REAL_BEFORE="$(real_sessions)"
REAL_ENV_TMUX="${TMUX:-}"
REAL_ENV_TMPDIR="${TMUX_TMPDIR:-}"

export HOME="$SCRATCH/home"
export HISTFILE=/dev/null
export LESSHISTFILE=-
# $TMUX beats TMUX_TMPDIR — without the unset every tmux call (incl.
# cleanup's kill-server) lands on the REAL server (2026-07-07 grove-7).
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"
mkdir -p "$HOME" "$TMUX_TMPDIR"
unset GROVE_STATE_DIR || true
cleanup() {
  env -u TMUX TMUX_TMPDIR="$SCRATCH/tmux" tmux kill-server 2>/dev/null || true   # isolated server only
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -S "$SCRATCH/tmux/tmux-$(id -u)/default" ] || break
    sleep 0.2
  done
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH" 2>/dev/null || { sleep 0.5; rm -rf "$SCRATCH" 2>/dev/null || true; }
}
trap cleanup EXIT

# tty <answer> <gv args…>: run gv on a pseudo-terminal, typing <answer>.
# The answer is held back a moment: script(1) forwards piped input as soon
# as it reads it, and a line that reaches the pty before gv prompts can be
# lost to the terminal setup (seen on macOS as a bare ^D, i.e. "no").
tty() {
  local answer="$1"; shift
  if [ "$(uname)" = Darwin ]; then
    { sleep 1; printf '%s\n' "$answer"; sleep 1; } | script -q /dev/null "$GV" "$@"
  else
    local cmd; cmd="$(printf '%q ' "$GV" "$@")"
    { sleep 1; printf '%s\n' "$answer"; sleep 1; } | script -qec "$cmd" /dev/null
  fi
}
windows() { tmux list-windows -a -F '#{session_name}|#{window_name}' 2>/dev/null || true; }
has_window() { windows | grep -Fxq "$SESSION|$1"; }
events() { cat "$DUMMY/.grove/state/events.jsonl"; }
serve_field() { # <slug> <python expr on s>
  "$GV" feature ls --json --no-pr --no-queued > "$SCRATCH/ls.json"
  python3 -c "
import json,sys
rows = {r['slug']: r for r in json.load(open('$SCRATCH/ls.json'))['features']}
s = rows['$1']['serve']
print($2)"
}

say "scratch workspace + origin + two features"
DUMMY="$SCRATCH/repos/dummy"
mkdir -p "$DUMMY" && cd "$DUMMY"
git init -q -b main
git config user.email e2e@grove.test && git config user.name "grove e2e"
echo "# dummy" > README.md
git add -A && git commit -qm init
"$GV" init --yes > "$SCRATCH/init.out"
git init -q --bare -b main "$SCRATCH/origin.git"
git remote add origin "$SCRATCH/origin.git"
git push -q origin main
"$GV" feature new trains --repo dummy > /dev/null
"$GV" feature new other --repo dummy > /dev/null
SESSION="grove-dummy"
WT="$SCRATCH/repos/.worktrees/dummy/trains-serve"

[ "$(serve_field trains "s['state']")" = none ] || fail "no run.sh: serve state not none"

cat > "$DUMMY/.grove/run.sh" <<'EOF'
#!/bin/sh
echo "wt=$GROVE_WORKTREE branch=$GROVE_BRANCH feature=$GROVE_FEATURE pwd=$(pwd -P)"
echo "GROVE_READY http://localhost:$GROVE_PORT"
exec sleep 600
EOF
SHA="$(shasum -a 256 "$DUMMY/.grove/run.sh" | cut -d' ' -f1)"
[ "$(serve_field trains "s['state']")" = untrusted ] || fail "fresh run.sh: serve state not untrusted"

say "untrusted + non-TTY: refused, nothing runs"
if "$GV" serve trains < /dev/null > "$SCRATCH/refuse.out" 2>&1; then fail "non-TTY serve of an untrusted script succeeded"; fi
grep -q 'not trusted' "$SCRATCH/refuse.out" || fail "refusal does not say untrusted: $(cat "$SCRATCH/refuse.out")"
grep -q "$SHA" "$SCRATCH/refuse.out" || fail "refusal does not name the sha"
has_window "▶ trains" && fail "a window opened for an untrusted script"
events | grep -q run_script_trusted && fail "trust recorded without a yes"

say "TTY: 'n' refuses"
tty n serve trains > "$SCRATCH/no.out" 2>&1 || true
grep -q 'trust and run?' "$SCRATCH/no.out" || fail "no trust prompt on a TTY"
grep -q "$SHA" "$SCRATCH/no.out" || fail "prompt does not show the sha"
events | grep -q run_script_trusted && fail "'n' recorded trust"

say "TTY: 'y' trusts, serves, prints the READY value"
tty y serve trains > "$SCRATCH/yes.out" 2>&1 || fail "serve failed: $(cat "$SCRATCH/yes.out")"
LAST="$(tr -d '\r' < "$SCRATCH/yes.out" | grep -v '^$' | tail -1)"
[ "$LAST" = "http://localhost:4100" ] || fail "READY value not printed last: got '$LAST'"
has_window "▶ trains" || fail "window '▶ trains' missing: $(windows)"
TIP="$(git rev-parse origin/feature/trains)"
[ "$(git -C "$WT" rev-parse HEAD)" = "$TIP" ] || fail "serve worktree not at the feature tip"
git -C "$WT" symbolic-ref -q HEAD > /dev/null && fail "serve worktree is on a branch, want detached"
LOG="$DUMMY/.grove/state/serve/trains.log"
grep -q "wt=$WT branch=feature/trains feature=trains pwd=$WT" "$LOG" || fail "env/cwd wrong: $(cat "$LOG")"
python3 -c "
import json
evs = [json.loads(l) for l in open('$DUMMY/.grove/state/events.jsonl')]
tr = [e for e in evs if e['type'] == 'run_script_trusted']
assert len(tr) == 1 and tr[0]['data'] == {'sha256': '$SHA'} and tr[0]['ticket'] == '', tr
sv = [e for e in evs if e['type'] == 'feature_served']
assert len(sv) == 1 and sv[0]['ticket'] == '', sv
assert sv[0]['data'] == {'slug': 'trains', 'port': '4100', 'tip': '$TIP', 'window': '▶ trains', 'url': 'http://localhost:4100'}, sv
"
[ "$(serve_field trains "s['state'], s['port'], s['url'], s['behind']")" = "running 4100 http://localhost:4100 False" ] \
  || fail "running status wrong: $(serve_field trains s)"

say "already running: refused"
if "$GV" serve trains < /dev/null > "$SCRATCH/again.out" 2>&1; then fail "second serve of a running feature succeeded"; fi

say "second feature gets the next port (trusted script, non-TTY is fine)"
"$GV" serve other < /dev/null > "$SCRATCH/other.out" 2>&1 || fail "serve other: $(cat "$SCRATCH/other.out")"
[ "$(tail -1 "$SCRATCH/other.out")" = "http://localhost:4101" ] || fail "second feature not on 4101"

say "stop kills only the exact window"
tmux new-window -d -t "=$SESSION" -n "▶ trains 2" "sleep 600"
tmux new-window -d -t "=$SESSION" -n "▶ trains-x" "sleep 600"
"$GV" serve stop trains > /dev/null
has_window "▶ trains" && fail "stop left '▶ trains'"
has_window "▶ trains 2" || fail "stop killed the '▶ trains 2' decoy"
has_window "▶ trains-x" || fail "stop killed the '▶ trains-x' decoy"
has_window "▶ other" || fail "stop killed another feature's serve"
events | grep -q '"type":"feature_serve_stopped","ticket":"","data":{"slug":"trains"}' || fail "no feature_serve_stopped"
[ "$(serve_field trains "s['state']")" = stopped ] || fail "stopped status wrong"
"$GV" serve stop trains | grep -q 'nothing to stop' || fail "stop of a stopped serve not a no-op"

say "branch moves: behind, then re-serve moves the worktree and reuses the port"
git commit -q --allow-empty -m two && git push -q origin HEAD:refs/heads/feature/trains
TIP2="$(git rev-parse origin/feature/trains)"
[ "$(serve_field trains "s['behind']")" = True ] || fail "not behind after the branch moved"
"$GV" serve trains < /dev/null > "$SCRATCH/re.out" 2>&1 || fail "re-serve: $(cat "$SCRATCH/re.out")"
[ "$(tail -1 "$SCRATCH/re.out")" = "http://localhost:4100" ] || fail "re-serve did not reuse 4100"
[ "$(git -C "$WT" rev-parse HEAD)" = "$TIP2" ] || fail "worktree not moved to the new tip"
[ "$(serve_field trains "s['behind']")" = False ] || fail "still behind after re-serve"
"$GV" serve stop trains > /dev/null

say "edited script: non-TTY refuses, TTY re-trust; no READY → timeout names the log"
printf '#!/bin/sh\necho starting\nexec sleep 600\n' > "$DUMMY/.grove/run.sh"
[ "$(serve_field trains "s['state']")" = untrusted ] || fail "edit did not make it untrusted"
if "$GV" serve trains < /dev/null > "$SCRATCH/edit.out" 2>&1; then fail "edited script ran without re-trust"; fi
grep -q 'not trusted' "$SCRATCH/edit.out" || fail "edit refusal wrong: $(cat "$SCRATCH/edit.out")"
has_window "▶ trains" && fail "a window opened for the edited script"
if tty y serve trains --timeout 2s > "$SCRATCH/to.out" 2>&1; then fail "serve without READY succeeded"; fi
grep -q "serve/trains.log" "$SCRATCH/to.out" || fail "timeout does not name the log: $(cat "$SCRATCH/to.out")"
has_window "▶ trains" || fail "timeout must leave the window for inspection"
[ "$(events | grep -c run_script_trusted)" = 2 ] || fail "re-trust not recorded"
"$GV" serve stop trains > /dev/null

say "feature close removes the serve worktree gv made — and nothing else"
MINE="$SCRATCH/repos/.worktrees/dummy/other-serve"
"$GV" feature close trains > "$SCRATCH/close.out"
[ -e "$WT" ] && fail "serve worktree survived feature close"
[ -d "$MINE" ] || fail "close removed another feature's serve worktree"
git ls-remote --exit-code origin refs/heads/feature/trains > /dev/null || fail "close deleted the branch"
"$GV" serve stop other > /dev/null
git -C "$MINE" checkout -q -b hand-made
"$GV" feature close other > /dev/null
[ -d "$MINE" ] || fail "close removed a worktree on a branch (not gv's shape)"

say "real tmux server untouched"
REAL_AFTER="$(env -u TMUX_TMPDIR ${REAL_ENV_TMPDIR:+TMUX_TMPDIR=$REAL_ENV_TMPDIR} ${REAL_ENV_TMUX:+TMUX=$REAL_ENV_TMUX} tmux list-sessions -F '#{session_name}' 2>/dev/null | sort || true)"
[ "$REAL_BEFORE" = "$REAL_AFTER" ] || fail "the real tmux server's sessions changed"

printf '\n\033[32mserve e2e green\033[0m\n'
