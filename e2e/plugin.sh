#!/usr/bin/env bash
# grove-75 E2E: the surface-plugin contract (docs/plugins.md).
#
# Proves an EXTERNAL script — the "plugin", written against the contract
# alone and handed nothing but the gv binary path — can:
#   1. enumerate workspaces (`gv workspaces --json` → registry → .grove/state)
#   2. poll the fleet (`gv ls --json`, schema_version envelope)
#   3. react (tail events.jsonl read-only; records carry the v stamp)
#   4. steer (`gv nudge` — the only sanctioned mutation path)
# without touching grove internals: it never writes events.jsonl or
# tasks.json, never calls tmux. Dummy-data pattern: scratch HOME, ISOLATED
# tmux server, worker command stubbed to `echo`.
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-plugin.XXXXXX)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

export HOME="$SCRATCH/home"
# $TMUX beats TMUX_TMPDIR — without the unset, a run from inside a tmux
# pane puts every tmux call (incl. cleanup's kill-server) on the REAL
# server. See LEARNINGS.md (2026-07-07 grove-7 crash).
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"
mkdir -p "$HOME" "$TMUX_TMPDIR"
unset GROVE_STATE_DIR || true   # registry → per-workspace state is the subject
cleanup() {
  tmux kill-server 2>/dev/null || true   # isolated server only (TMUX_TMPDIR)
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH"
}
trap cleanup EXIT

say "scratch workspace with an echo-stubbed worker"
DUMMY="$SCRATCH/repos/dummy"
mkdir -p "$DUMMY" && cd "$DUMMY"
git init -q -b main
git config user.email e2e@grove.test && git config user.name "grove e2e"
echo "# dummy" > README.md
git add -A && git commit -qm "init"
"$GV" init --yes > "$SCRATCH/init.out"
WCFG="$DUMMY/.grove/config.yaml"
perl -pi -e 's/^(\s*)base: main$/$1base: main\n$1claude: echo/' "$WCFG"
grep -q 'claude: echo' "$WCFG" || fail "claude stub not written"
"$GV" grab task-001 > "$SCRATCH/grab.out"

say "feature trains (grove-372): open one, adopt one, close one — the operator's side"
# A bare origin so feature new can push; added after the grab so the grab
# path is unchanged.
git init -q --bare -b main "$SCRATCH/origin.git"
git remote add origin "$SCRATCH/origin.git"
git push -q origin main
MAIN_SHA="$(git rev-parse main)"
"$GV" feature new trains --repo dummy > "$SCRATCH/fnew.out" || fail "feature new failed"
[ "$(git ls-remote origin refs/heads/feature/trains | cut -f1)" = "$MAIN_SHA" ] || fail "feature/trains not pushed at origin/main"
if "$GV" feature new trains --repo dummy > "$SCRATCH/fdup.out" 2>&1; then fail "second new of an open slug succeeded"; fi
git push -q origin main:refs/heads/feature/live-train
if "$GV" feature new live --branch feature/live-train --repo dummy > "$SCRATCH/fexist.out" 2>&1; then
  fail "new on an existing remote branch succeeded without --adopt"
fi
grep -q -- '--adopt' "$SCRATCH/fexist.out" || fail "refusal does not name --adopt"
if "$GV" feature new ghost --adopt --repo dummy > /dev/null 2>&1; then fail "--adopt of a missing branch succeeded"; fi
if "$GV" feature new Bad_Slug --repo dummy > /dev/null 2>&1; then fail "bad slug accepted"; fi
REFS_BEFORE="$(git ls-remote origin)"
"$GV" feature new live --branch feature/live-train --label live-train --adopt --repo dummy > /dev/null || fail "--adopt failed"
[ "$(git ls-remote origin)" = "$REFS_BEFORE" ] || fail "--adopt pushed something"
"$GV" feature new gone --repo dummy > /dev/null && "$GV" feature close gone --reason abandoned > /dev/null || fail "feature close failed"
git ls-remote --exit-code origin refs/heads/feature/gone > /dev/null || fail "feature close deleted the branch"

say "feature grabs (grove-373): label inference, --feature none, refusals"
# feature/trains moves one commit past main on origin, so a car's fork
# point is observable.
git push -q origin "$(git commit-tree -p "$MAIN_SHA" -m 'train car base' "$(git rev-parse "$MAIN_SHA^{tree}")"):refs/heads/feature/trains"
TRAIN_SHA="$(git ls-remote origin refs/heads/feature/trains | cut -f1)"
[ "$TRAIN_SHA" != "$MAIN_SHA" ] || fail "feature/trains did not move"
mdtask() { printf -- '---\nid: %s\ntitle: %s\nstatus: todo\nlabels: [%s]\n---\n\n%s\n' "$1" "$2" "$3" "$2" > "$DUMMY/.grove/tasks/$1.md"; }
mdtask task-002 "train car" "trains, ui"
mdtask task-003 "two trains" "trains, live-train"
if "$GV" grab task-002 --feature trains --host pc > "$SCRATCH/fhost.out" 2>&1; then fail "--host with --feature was not refused"; fi
grep -q -- '--host' "$SCRATCH/fhost.out" || fail "--host refusal does not name --host"
if "$GV" grab task-002 --host pc > "$SCRATCH/fhost2.out" 2>&1; then fail "--host with an inferred feature was not refused"; fi
grep -q 'feature trains' "$SCRATCH/fhost2.out" || fail "inferred --host refusal does not name the feature"
if "$GV" grab task-003 > "$SCRATCH/ftwo.out" 2>&1; then fail "grab matching two features succeeded"; fi
grep -q 'trains (label trains)' "$SCRATCH/ftwo.out" && grep -q 'live (label live-train)' "$SCRATCH/ftwo.out" || fail "two-match refusal does not name both"
if "$GV" grab task-003 --feature gone > "$SCRATCH/fgone.out" 2>&1; then fail "grab onto a closed feature succeeded"; fi
grep -q 'gv feature ls' "$SCRATCH/fgone.out" || fail "closed-feature refusal does not name gv feature ls"
"$GV" grab task-002 > "$SCRATCH/fgrab.out" || fail "feature grab failed"
grep -qx 'base: feature/trains (feature trains, from label trains)' "$SCRATCH/fgrab.out" || fail "grab did not name its inferred base"
CAR_WT="$(sed -n 's/^→ worktree //p' "$SCRATCH/fgrab.out")"
[ "$(git -C "$CAR_WT" rev-parse HEAD)" = "$TRAIN_SHA" ] || fail "car worktree did not fork from origin/feature/trains"
"$GV" grab task-003 --feature none > "$SCRATCH/fnone.out" || fail "--feature none grab failed"
grep -qx 'base: main (--feature none)' "$SCRATCH/fnone.out" || fail "--feature none did not name the repo base"

say "feature status (grove-375): a landed car, an active car, a queued car"
# task-004 rides trains and lands (done --force: no PR to check here);
# task-005 is an ungrabbed trains issue that depends on #2 — queued.
mdtask task-004 "landed car" "trains"
"$GV" grab task-004 > /dev/null || fail "task-004 grab failed"
"$GV" done task-004 --force > "$SCRATCH/fdone.out" 2>&1 || { cat "$SCRATCH/fdone.out"; fail "done task-004 failed"; }
printf -- '---\nid: task-005\ntitle: queued car\nstatus: todo\nlabels: [trains]\n---\n\nDepends on #2\n' > "$DUMMY/.grove/tasks/task-005.md"
git fetch -q origin
"$GV" feature ls --no-pr > "$SCRATCH/fls.out" || fail "feature ls failed"
grep -E '^trains .* 1/3 +↓0 main ' "$SCRATCH/fls.out" > /dev/null || { cat "$SCRATCH/fls.out"; fail "feature ls does not show 1/3 and ↓0 main for trains"; }

# --- the plugin: knows ONLY the contract + the gv path -------------------
# Everything below the line is what a gv-<surface> sidecar would do.
cat > "$SCRATCH/plugin.sh" <<'PLUGIN'
#!/usr/bin/env bash
set -euo pipefail
GV="$1"; OUT="$2"

# 1. READ: enumerate workspaces from the registry.
"$GV" workspaces --json > "$OUT/workspaces.json"
ROOT="$(python3 -c "
import json
data = json.load(open('$OUT/workspaces.json'))
assert data['schema_version'] == 1, data
print(data['workspaces'][0]['root'])
")"

# 2. READ: poll the fleet from inside the workspace (ambient scoping).
( cd "$ROOT" && "$GV" ls --json --no-pr --no-cost > "$OUT/ls.json" )
TICKET="$(python3 -c "
import json
data = json.load(open('$OUT/ls.json'))
assert data['schema_version'] == 1, data
rows = {t['ticket']: t for t in data['tasks']}
car = rows['task-002']
assert (car['feature'], car['base']) == ('trains', 'feature/trains'), car
for off in ('task-001', 'task-003'):
    assert 'feature' not in rows[off] and 'base' not in rows[off], rows[off]
print(data['tasks'][0]['ticket'])
")"

# 2b. READ: open feature trains (grove-372).
( cd "$ROOT" && "$GV" feature ls --json --no-pr > "$OUT/features.json" && "$GV" feature ls --all --json --no-pr > "$OUT/features-all.json" )
python3 -c "
import json
data = json.load(open('$OUT/features.json'))
assert data['schema_version'] == 1, data
fs = {f['slug']: f for f in data['features']}
assert set(fs) == {'trains', 'live'}, fs
t = fs['trains']
assert (t['repo'], t['branch'], t['base'], t['label']) == ('dummy', 'feature/trains', 'main', 'trains'), t
assert t['created_at'] and 'closed' not in t, t
assert t['serve'] == {'state': 'none', 'behind': False}, t  # grove-380: no run.sh yet
assert fs['live']['branch'] == 'feature/live-train' and fs['live']['label'] == 'live-train', fs
alld = {f['slug']: f for f in json.load(open('$OUT/features-all.json'))['features']}
assert alld['gone']['closed']['reason'] == 'abandoned' and alld['gone']['closed']['at'], alld
# status fields (grove-375): open rows only.
assert 'cars' not in alld['gone'] and 'landed' not in alld['gone'], alld['gone']
cars = [(c['ticket'], c['state']) for c in t['cars']]
assert cars == [('task-004', 'landed'), ('task-002', 'working'), ('task-005', 'queued')], cars
assert (t['landed'], t['total']) == (1, 3), t
landed, active, queued = t['cars']
assert landed['landed_at'] and landed['number'] == 4 and landed['title'] == 'landed car', landed
assert 'landed_at' not in active and 'after' not in active, active
assert queued['after'] == [2] and queued['number'] == 5, queued
assert all(isinstance(c['est_usd'], (int, float)) for c in t['cars']), t['cars']
assert t['behind_base'] == 0 and t['mergeable'] is True and 'pr' not in t, t
assert isinstance(t['est_usd'], (int, float)), t
assert 'serve' not in alld['gone'], alld  # serve rides on open rows only
"

# 3. REACT: tail events.jsonl — read-only, never written by a plugin.
tail -n 50 "$ROOT/.grove/state/events.jsonl" > "$OUT/events.tail"
python3 -c "
import json
evs = [json.loads(l) for l in open('$OUT/events.tail')]
created = [e for e in evs if e['type'] == 'task_created']
assert created, 'no task_created in the tail'
assert all(e.get('v', 1) == 1 for e in evs), 'unexpected record version'
fc = [e for e in evs if e['type'] == 'feature_created']
assert len(fc) == 3 and all(e['ticket'] == '' for e in fc), fc
assert set(fc[0]['data']) == {'slug', 'repo', 'branch', 'base', 'label'}, fc[0]
cl = [e for e in evs if e['type'] == 'feature_closed']
assert len(cl) == 1 and cl[0]['data'] == {'slug': 'gone', 'reason': 'abandoned'}, cl
tc = {e['ticket']: e['data'] for e in created}
assert tc['task-002']['feature'] == 'trains' and tc['task-002']['base'] == 'feature/trains', tc['task-002']
for off in ('task-001', 'task-003'):
    assert 'feature' not in tc[off] and 'base' not in tc[off], tc[off]
"

# 4. STEER: mutations go through gv only.
( cd "$ROOT" && "$GV" nudge "$TICKET" "plugin-smoke-ping" > "$OUT/nudge.out" )
grep -q '✓ sent' "$OUT/nudge.out"
echo "$TICKET"
PLUGIN
chmod +x "$SCRATCH/plugin.sh"

say "plugin run: enumerate → poll → tail → nudge"
EVENTS="$DUMMY/.grove/state/events.jsonl"
EV_BEFORE=$(wc -l < "$EVENTS")
"$SCRATCH/plugin.sh" "$GV" "$SCRATCH" > "$SCRATCH/plugin.out" || fail "plugin script failed"
grep -q 'task-001' "$SCRATCH/plugin.out" || fail "plugin did not resolve the ticket"

say "steer landed as exactly one gv-appended, v-stamped answered event"
[ "$(wc -l < "$EVENTS")" -eq "$((EV_BEFORE + 1))" ] || fail "expected exactly one new event"
tail -n 1 "$EVENTS" > "$SCRATCH/last-event.json"
grep -q '"type":"answered"' "$SCRATCH/last-event.json" || fail "last event is not answered"
grep -q '"v":1' "$SCRATCH/last-event.json" || fail "new event missing the v stamp"

say "nudge text reached the worker pane (relay, not direct tmux)"
tmux list-windows -a -F '#S:#W' > "$SCRATCH/wins.txt"
WIN="$(grep task-001 "$SCRATCH/wins.txt" | head -1)"
[ -n "$WIN" ] || fail "worker window not found on the isolated server"
# Full scrollback of every pane, wraps flattened — the narrow default pane
# (80x24) both scrolls and wraps the echoed paste out of a bare capture.
tmux list-panes -t "$WIN" -F '#{pane_id}' > "$SCRATCH/panes.txt"
: > "$SCRATCH/pane.txt"
while read -r p; do tmux capture-pane -p -S - -t "$p" >> "$SCRATCH/pane.txt"; done < "$SCRATCH/panes.txt"
tr -d '\n' < "$SCRATCH/pane.txt" > "$SCRATCH/pane.flat"
grep -q 'plugin-smoke-ping' "$SCRATCH/pane.flat" || fail "nudge text not delivered to the pane"

say "PASS — external plugin drove read/react/steer through the contract alone"
