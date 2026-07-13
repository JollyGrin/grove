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
print(data['tasks'][0]['ticket'])
")"

# 3. REACT: tail events.jsonl — read-only, never written by a plugin.
tail -n 50 "$ROOT/.grove/state/events.jsonl" > "$OUT/events.tail"
python3 -c "
import json
evs = [json.loads(l) for l in open('$OUT/events.tail')]
created = [e for e in evs if e['type'] == 'task_created']
assert created, 'no task_created in the tail'
assert all(e.get('v', 1) == 1 for e in evs), 'unexpected record version'
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
