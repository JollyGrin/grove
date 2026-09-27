#!/usr/bin/env bash
# grove-398 E2E: a feature train across hosts.
#
# Dummy-data pattern with a fake second host (handoff.sh's pattern): the
# "remote" is this machine behind a fake `ssh` on PATH that re-runs the
# command with a second GROVE_STATE_DIR and a second ISOLATED tmux server.
# The remote state dir has NO features — the forwarded grab must not need
# them. Proves:
#   1. `gv grab --host pc --feature keys` forwards `--feature keys
#      --feature-branch feature/keys-secrets`; the remote forks from
#      origin/feature/keys-secrets and records feature/base
#   2. label inference with --host forwards the same flags
#   3. a branch missing on origin refuses on the receiving side, and
#      --feature-branch without --feature refuses
#   4. `gv feature ls --json` shows the remote cars with host "pc" and
#      their real state; `--no-remote` never dials the host
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-feathost.XXXXXX)"
SCRATCH="$(cd "$SCRATCH" && pwd -P)"   # /tmp → /private/tmp on macOS

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

# Real-server canary (tmux-discipline): the REAL session list, read-only,
# taken before isolation kicks in.
real_tmux() { env -u TMUX -u TMUX_PANE -u TMUX_TMPDIR tmux list-sessions -F '#{session_name}' 2>/dev/null | sort; true; }
REAL_TMUX_BEFORE="$(real_tmux)"

export HOME="$SCRATCH/home"
export HISTFILE=/dev/null
export GROVE_STATE_DIR="$SCRATCH/state-local"
REMOTE_STATE="$SCRATCH/state-remote"
# $TMUX beats TMUX_TMPDIR — unset or every tmux call hits the REAL server.
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux-local"
REMOTE_TMUX="$SCRATCH/tmux-remote"
mkdir -p "$HOME" "$GROVE_STATE_DIR" "$REMOTE_STATE" "$TMUX_TMPDIR" "$REMOTE_TMUX" "$SCRATCH/bin"
cleanup() {
  env -u TMUX TMUX_TMPDIR="$TMUX_TMPDIR" tmux kill-server 2>/dev/null || true   # isolated servers only
  env -u TMUX TMUX_TMPDIR="$REMOTE_TMUX" tmux kill-server 2>/dev/null || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -S "$TMUX_TMPDIR/tmux-$(id -u)/default" ] || [ -S "$REMOTE_TMUX/tmux-$(id -u)/default" ] || break
    sleep 0.2
  done
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH" 2>/dev/null || { sleep 0.5; rm -rf "$SCRATCH" 2>/dev/null || true; }
}
trap cleanup EXIT

say "fake ssh (logs every hop) + gh on PATH"
SSH_LOG="$SCRATCH/ssh.log"
: > "$SSH_LOG"
cat > "$SCRATCH/bin/ssh" <<EOF
#!/usr/bin/env bash
target=""
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) shift 2 ;;
    --) shift; break ;;
    -*) shift ;;
    *) if [ -z "\$target" ]; then target="\$1"; shift; else break; fi ;;
  esac
done
[ "\$target" = "localhost" ] || { echo "fake ssh: target '\$target' is not hosts.pc.ssh (localhost)" >&2; exit 42; }
cmd="\$*"
echo "\$cmd" >> "$SSH_LOG"
exec env GROVE_STATE_DIR="$REMOTE_STATE" TMUX_TMPDIR="$REMOTE_TMUX" sh -c "\$cmd"
EOF
cat > "$SCRATCH/bin/gh" <<'EOF'
#!/bin/sh
printf '[]'
EOF
chmod +x "$SCRATCH/bin/ssh" "$SCRATCH/bin/gh"
export PATH="$SCRATCH/bin:$PATH"

say "scratch workspace + bare origin + host pc"
DUMMY="$SCRATCH/repos/dummy"
mkdir -p "$DUMMY" && cd "$DUMMY"
git init -q -b main
git config user.email e2e@grove.test && git config user.name "grove e2e"
echo "# dummy" > README.md
git add -A && git commit -qm init
"$GV" init --yes > "$SCRATCH/init.out"
WCFG="$DUMMY/.grove/config.yaml"
perl -pi -e 's/^(\s*)base: main$/$1base: main\n$1claude: echo/' "$WCFG"
grep -q 'claude: echo' "$WCFG" || fail "claude stub not written"
printf 'hosts:\n  pc:\n    ssh: localhost\n    gv: %s\n' "$GV" >> "$WCFG"
git init -q --bare -b main "$SCRATCH/origin.git"
git remote add origin "$SCRATCH/origin.git"
git push -q origin main
MAIN_SHA="$(git rev-parse main)"

say "feature keys, one commit past main (registered on THIS host only)"
git push -q origin "$(git commit-tree -p "$MAIN_SHA" -m 'keys commit' "$(git rev-parse "$MAIN_SHA^{tree}")"):refs/heads/feature/keys-secrets"
KEYS_SHA="$(git ls-remote origin refs/heads/feature/keys-secrets | cut -f1)"
"$GV" feature new keys --adopt --branch feature/keys-secrets --label keys --repo dummy > "$SCRATCH/adopt.out" \
  || fail "adopt: $(cat "$SCRATCH/adopt.out")"
[ ! -s "$REMOTE_STATE/events.jsonl" ] || fail "remote state should start empty"

mdtask() { printf -- '---\nid: %s\ntitle: %s\nstatus: todo\nlabels: [%s]\n---\n\n%s\n' "$1" "$2" "$3" "$2" > "$DUMMY/.grove/tasks/$1.md"; }
mdtask task-010 "explicit car" "ui"
mdtask task-011 "label car" "keys"
mdtask task-012 "queued car" "keys"

say "1. grab --host pc --feature keys forwards the resolved branch"
"$GV" grab task-010 --host pc --feature keys > "$SCRATCH/grab-010.out" 2>&1 || fail "explicit forward: $(cat "$SCRATCH/grab-010.out")"
grep -Fxq "$GV grab task-010 --feature keys --feature-branch feature/keys-secrets" "$SSH_LOG" \
  || { cat "$SSH_LOG"; fail "remote argv lacks --feature keys --feature-branch feature/keys-secrets"; }
grep -qx 'base: feature/keys-secrets (feature keys, forwarded from the registering host)' "$SCRATCH/grab-010.out" \
  || fail "remote grab did not name the forwarded base: $(cat "$SCRATCH/grab-010.out")"
WT10="$(sed -n 's/^→ worktree //p' "$SCRATCH/grab-010.out")"
[ "$(git -C "$WT10" rev-parse HEAD)" = "$KEYS_SHA" ] || fail "remote car did not fork from origin/feature/keys-secrets"
python3 -c "
import json
evs = [json.loads(l) for l in open('$REMOTE_STATE/events.jsonl')]
tc = [e for e in evs if e['type'] == 'task_created' and e['ticket'] == 'task-010']
assert tc and tc[-1]['data']['feature'] == 'keys' and tc[-1]['data']['base'] == 'feature/keys-secrets', tc
assert not any(e['type'].startswith('feature_') for e in evs), 'remote registry touched'
"
[ -z "$(grep task-010 "$GROVE_STATE_DIR/events.jsonl" 2>/dev/null || true)" ] || fail "forwarded grab wrote local events"

say "2. label inference with --host forwards the same flags"
"$GV" grab task-011 --host pc > "$SCRATCH/grab-011.out" 2>&1 || fail "label forward: $(cat "$SCRATCH/grab-011.out")"
grep -Fxq "$GV grab task-011 --feature keys --feature-branch feature/keys-secrets" "$SSH_LOG" \
  || { cat "$SSH_LOG"; fail "label inference did not forward the feature"; }

say "3. receiving side refuses a missing branch and a bare --feature-branch"
if GROVE_STATE_DIR="$REMOTE_STATE" TMUX_TMPDIR="$REMOTE_TMUX" "$GV" grab task-012 --feature keys --feature-branch feature/missing > "$SCRATCH/missing.out" 2>&1; then
  fail "a branch missing on origin was accepted"
fi
if GROVE_STATE_DIR="$REMOTE_STATE" TMUX_TMPDIR="$REMOTE_TMUX" "$GV" grab task-012 --feature-branch feature/keys-secrets > "$SCRATCH/bare.out" 2>&1; then
  fail "--feature-branch without --feature was accepted"
fi
grep -q -- '--feature-branch needs --feature' "$SCRATCH/bare.out" || fail "bare --feature-branch refusal: $(cat "$SCRATCH/bare.out")"
grep -q "feature/missing" "$SCRATCH/missing.out" || fail "missing-branch refusal: $(cat "$SCRATCH/missing.out")"
"$GV" grab -h > "$SCRATCH/help.out" 2>&1 || true
grep -q 'feature-branch' "$SCRATCH/help.out" && fail "--feature-branch is advertised in grab -h"

say "4. feature ls shows the remote cars with their host"
"$GV" feature ls --json --no-pr > "$SCRATCH/ls.json" || fail "feature ls failed"
python3 -c "
import json
t = {f['slug']: f for f in json.load(open('$SCRATCH/ls.json'))['features']}['keys']
cars = {c['ticket']: c for c in t['cars']}
for k in ('task-010', 'task-011'):
    assert cars[k]['host'] == 'pc' and cars[k]['state'] == 'working', cars[k]
assert cars['task-012']['state'] == 'queued' and 'host' not in cars['task-012'], cars['task-012']
"
: > "$SSH_LOG"
"$GV" feature ls --json --no-pr --no-remote > "$SCRATCH/ls-noremote.json" || fail "feature ls --no-remote failed"
[ ! -s "$SSH_LOG" ] || { cat "$SSH_LOG"; fail "--no-remote dialed the host"; }
python3 -c "
import json
t = {f['slug']: f for f in json.load(open('$SCRATCH/ls-noremote.json'))['features']}['keys']
assert all('host' not in c for c in t['cars']), t['cars']
"

say "real tmux server untouched (canary)"
[ "$(real_tmux)" = "$REAL_TMUX_BEFORE" ] || fail "the REAL tmux server's session list changed — the suite leaked out of isolation"

printf '\n\033[32mfeature_host e2e green\033[0m\n'
