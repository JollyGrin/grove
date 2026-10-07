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
  # kill-server returns before the server and its panes are gone; a pane
  # process still writing under $SCRATCH made rm -rf fail with "Directory
  # not empty" after every assertion had passed (grove-377, grove-383).
  # Wait for the socket to go, then retry the rm once.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -S "$TMUX_TMPDIR/tmux-$(id -u)/default" ] || break
    sleep 0.2
  done
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH" 2>/dev/null || { sleep 0.5; chmod -R u+w "$SCRATCH" 2>/dev/null || true; rm -rf "$SCRATCH"; }
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
# grove-435: task-003 is grabbed later with an --effort pin; its `effort`
# row field is asserted below, and its absence on the unpinned rows.

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
"$GV" grab task-003 --feature none --effort low > "$SCRATCH/fnone.out" || fail "--feature none grab failed"
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

say "learnings fixture (grove-439): a scratch auto memory, a LEARNINGS.md, a skill that cites nothing"
# autoMemoryDirectory in the repo's local settings scope relocates the
# memory dir the verb reads — the same key and scope order as the doctor
# row (docs/plugins.md). Two notes in the window (one feedback, one
# nested-metadata reference), one old note, one malformed note.
MEM="$SCRATCH/mem"
mkdir -p "$MEM" "$DUMMY/.claude/skills/dummy-skill"
printf '{"autoMemoryDirectory":"%s"}\n' "$MEM" > "$DUMMY/.claude/settings.local.json"
NOW_ISO="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
printf -- '- [Routing](no-routing.md) — never route unasked\n- [Watch](gv-watch.md) — the detector\n' > "$MEM/MEMORY.md"
printf -- '---\nname: no-routing\ndescription: never route unasked\ntype: feedback\nmodified: %s\n---\n\nbody\n' "$NOW_ISO" > "$MEM/no-routing.md"
printf -- '---\nname: gv-watch\ndescription: the detector\nmetadata:\n  type: reference\nmodified: %s\n---\n\nbody\n' "$NOW_ISO" > "$MEM/gv-watch.md"
printf -- '---\ntype: user\nmodified: 2020-01-01\n---\nold\n' > "$MEM/old.md"
printf -- '---\nname: broken\ntype: feedback\n\nnever closed\n' > "$MEM/broken.md"
printf '# dummy skill\n\nnothing cited here\n' > "$DUMMY/.claude/skills/dummy-skill/SKILL.md"
TODAY="$(date -u +%Y-%m-%d)"
printf -- '# learnings\n\n## Go / CLI\n\n- **%s · A fresh fact.** Found in task-9; rule for the dummy-skill skill.\n- **2020-01-01 · An old fact.** Nothing named.\n' "$TODAY" > "$DUMMY/LEARNINGS.md"
LEARN_BEFORE="$(cat "$DUMMY/LEARNINGS.md" "$DUMMY/.claude/skills/dummy-skill/SKILL.md" | cksum)"
MEM_BEFORE="$(cd "$MEM" && cat MEMORY.md no-routing.md gv-watch.md old.md broken.md | cksum; ls -la "$MEM")"
# The plugin below reads these only to assert the fixture round-trips;
# the contract itself needs none of them.
export MEM DUMMY TODAY

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
# grove-435: effort is additive — present only on the pinned worker.
assert rows['task-003'].get('effort') == 'low', rows['task-003']
for unpinned in ('task-001', 'task-002'):
    assert 'effort' not in rows[unpinned], rows[unpinned]
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

# 2c. READ: gv cost --context --all --json (grove-289) — contract envelope.
( cd "$ROOT" && "$GV" cost --context --all --json > "$OUT/cost-context.json" )
python3 -c "
import json
data = json.load(open('$OUT/cost-context.json'))
assert data['schema_version'] == 1, data
"

# 2d. READ: gv learnings --json (grove-439) — the promotion loop's read side.
( cd "$ROOT" && "$GV" learnings --json --since 7d > "$OUT/learnings.json" )
python3 -c "
import json
data = json.load(open('$OUT/learnings.json'))
assert data['schema_version'] == 1, data
rep = data['report']
assert rep['since'].endswith('Z'), rep['since']
repos = {r['repo']: r for r in rep['repos']}
r = repos['dummy']
m = r['memory']
assert m['dir'] == '$MEM' and m['source'].endswith('/.claude/settings.local.json') and 'disabled' not in m, m
assert m['notes_total'] == 4, m
assert [i['file'] for i in m['index']] == ['no-routing.md', 'gv-watch.md'], m['index']
notes = [n['file'] for n in m['notes']]
assert notes[0] == 'no-routing.md' and set(notes) == {'no-routing.md', 'gv-watch.md', 'broken.md'}, notes
by = {n['file']: n for n in m['notes']}
assert by['no-routing.md']['type'] == 'feedback' and by['no-routing.md']['indexed'] is True and by['no-routing.md']['modified_by'] == 'frontmatter', by
assert by['gv-watch.md']['type'] == 'reference', by['gv-watch.md']
assert by['broken.md']['error'] and 'type' not in by['broken.md'], by['broken.md']
assert any('broken.md' in w for w in m['warnings']), m
assert r['learnings_file'] == '$DUMMY/LEARNINGS.md', r
assert [e['date'] for e in r['entries']] == ['$TODAY'], r['entries']
e = r['entries'][0]
assert (e['section'], e['fact'], e['skills']) == ('Go / CLI', 'A fresh fact.', ['dummy-skill']), e
assert r['skills'] == ['dummy-skill'], r['skills']
assert [(c['skill'], c['date']) for c in r['candidates']] == [('dummy-skill', '$TODAY')], r['candidates']
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

say "gv learnings mutated nothing: memory dir, LEARNINGS.md and the skill are byte-identical"
LEARN_AFTER="$(cat "$DUMMY/LEARNINGS.md" "$DUMMY/.claude/skills/dummy-skill/SKILL.md" | cksum)"
MEM_AFTER="$(cd "$MEM" && cat MEMORY.md no-routing.md gv-watch.md old.md broken.md | cksum; ls -la "$MEM")"
[ "$LEARN_BEFORE" = "$LEARN_AFTER" ] || fail "gv learnings changed LEARNINGS.md or the skill"
[ "$MEM_BEFORE" = "$MEM_AFTER" ] || fail "gv learnings changed the memory dir"

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

say "hook: SessionStart source=compact records exactly one compaction event, no session_started (grove-289)"
WTPATH="$(python3 -c "
import json
data = json.load(open('$SCRATCH/ls.json'))
print(data['tasks'][0]['worktree'])
")"
EV_BEFORE_COMPACT=$(wc -l < "$EVENTS")
SS_BEFORE_COMPACT=$(grep -c '"type":"session_started"' "$EVENTS" || true)
python3 -c "
import json
print(json.dumps({'session_id': 's-compact-1', 'cwd': '$WTPATH', 'hook_event_name': 'SessionStart', 'source': 'compact'}))
" | "$GV" hook session-start > "$SCRATCH/compact-stdout.txt"
[ "$(wc -l < "$EVENTS")" -eq "$((EV_BEFORE_COMPACT + 1))" ] || fail "compact session-start did not append exactly one event"
tail -n 1 "$EVENTS" > "$SCRATCH/last-compact-event.json"
grep -q '"type":"compaction"' "$SCRATCH/last-compact-event.json" || fail "compact session-start did not append a compaction event"
SS_AFTER_COMPACT=$(grep -c '"type":"session_started"' "$EVENTS" || true)
[ "$SS_AFTER_COMPACT" -eq "$SS_BEFORE_COMPACT" ] || fail "compact session-start incorrectly appended session_started"

say "hook: the compact SessionStart prints a ground-truth re-orientation (grove-440)"
[ -s "$SCRATCH/compact-stdout.txt" ] || fail "compact session-start printed nothing to stdout"
grep -q 'Task: task-001' "$SCRATCH/compact-stdout.txt" || { cat "$SCRATCH/compact-stdout.txt"; fail "re-orientation missing the ticket id"; }
grep -q 'git log --oneline -8' "$SCRATCH/compact-stdout.txt" || { cat "$SCRATCH/compact-stdout.txt"; fail "re-orientation missing the git log block"; }
grep -q 'git status --short' "$SCRATCH/compact-stdout.txt" || fail "re-orientation missing the git status block"
grep -q 'STATUS contract' "$SCRATCH/compact-stdout.txt" || fail "re-orientation missing the STATUS contract line"

say "gv ls --json: the task's compactions field is now 1"
( cd "$DUMMY" && "$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-compacted.json" )
grep -q '"compactions": 1' "$SCRATCH/ls-compacted.json" || fail "gv ls --json missing compactions: 1 after the compact restart"

say "gv watch --replay --type compaction --json prints the compaction event"
( cd "$DUMMY" && "$GV" watch --replay --type compaction --until compaction --json > "$SCRATCH/watch-compaction.json" )
grep -q '"type":"compaction"' "$SCRATCH/watch-compaction.json" || fail "gv watch did not print the compaction event"

say "feature land (grove-376): plan, dry run, execute, skip reasons"
# task-002 is the only active (not-done) car on trains; a stub gh answers
# its PR lookup MERGED, so it lands. task-005 is still queued (no task at
# all — no gh call, the queued lookup is the local markdown backend).
# task-004 already landed via `gv done`, so it's absent from both lists.
# Runs LAST: it finishes task-002, and everything above depends on it
# still reading "working".
mkdir -p "$SCRATCH/bin"
cat > "$SCRATCH/bin/gh" <<'EOF'
#!/bin/sh
echo "$*" >> "$GH_LOG"
case "$*" in
  *"pr list"*) printf '[{"number":42,"url":"https://github.com/x/y/pull/42","state":"MERGED","mergedAt":"2026-09-27T00:00:00Z","isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","statusCheckRollup":[],"comments":[]}]' ;;
  *) printf '[]' ;;
esac
EOF
chmod +x "$SCRATCH/bin/gh"
export PATH="$SCRATCH/bin:$PATH"
export GH_LOG="$SCRATCH/gh.log"

( cd "$DUMMY" && "$GV" feature land trains --json > "$SCRATCH/fland-dry.json" ) || fail "feature land dry run failed"
python3 -c "
import json
data = json.load(open('$SCRATCH/fland-dry.json'))
assert data['schema_version'] == 1, data
assert data['feature'] == 'trains', data
land = {r['ticket']: r for r in data['land']}
assert set(land) == {'task-002'}, land
assert (land['task-002']['pr'], land['task-002']['number']) == (42, 2), land['task-002']
skipped = {r['ticket']: r['reason'] for r in data['skipped']}
assert skipped == {'task-005': 'queued'}, skipped
assert 'landed' not in data and 'failed' not in data, data
"
[ -d "$CAR_WT" ] || fail "feature land dry run must not touch the worktree"
grep -Ei 'issue (close|comment|edit)|pr (merge|close|comment)' "$SCRATCH/gh.log" > /dev/null && fail "gh invoked with a mutating verb during the dry run"

( cd "$DUMMY" && "$GV" feature land trains --yes > "$SCRATCH/fland.out" ) || fail "feature land --yes failed"
tail -n1 "$SCRATCH/fland.out" > "$SCRATCH/fland-last.out"
grep -qx 'landed: #2' "$SCRATCH/fland-last.out" || { cat "$SCRATCH/fland.out"; fail "feature land --yes did not report landed: #2"; }
[ -d "$CAR_WT" ] && fail "feature land --yes left the landed worktree behind"
( cd "$DUMMY" && "$GV" ls --json --no-pr --no-cost > "$SCRATCH/fland-ls.json" )
python3 -c "
import json
data = json.load(open('$SCRATCH/fland-ls.json'))
assert 'task-002' not in {t['ticket'] for t in data['tasks']}, data
"
grep -Ei 'issue (close|comment|edit)|pr (merge|close|comment)' "$SCRATCH/gh.log" > /dev/null && fail "gh invoked with a mutating verb during feature land --yes"

say "PASS — external plugin drove read/react/steer through the contract alone"
