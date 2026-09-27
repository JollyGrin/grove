#!/usr/bin/env bash
# grove-383 E2E: one feature train, start to finish, in a single workspace —
# the cutover suite for feature/feature-trains. plugin.sh pins the JSON
# contract field by field and serve.sh owns `gv serve`; this suite walks the
# operator's lifecycle in order and pins the rules that span it:
#   new (pushes at origin/<base>) → --adopt (pushes nothing) → grab by
#   label (forks from the feature tip, kickoff names the PR base, ls --json
#   carries feature/base) → feature ls --json status (cars, behind_base,
#   mergeable) → land --json dry run → land --yes (never touches the
#   backend's task files) → close (branch left as is; the label stops
#   inferring).
# Dummy-data pattern: scratch HOME, markdown backend with labels, worker
# `echo`, a scratch bare origin, a stub `gh` for PR lookups, and an
# ISOLATED tmux server (scratch TMUX_TMPDIR + `unset TMUX`).
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-feature.XXXXXX)"
SCRATCH="$(cd "$SCRATCH" && pwd -P)"   # /tmp → /private/tmp on macOS

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

# Live-server canary: the REAL server's session list (read-only), taken
# with the caller's environment before isolation kicks in.
REAL_BEFORE="$(tmux list-sessions -F '#{session_name}' 2>/dev/null | sort || true)"
REAL_ENV_TMUX="${TMUX:-}"
REAL_ENV_TMPDIR="${TMUX_TMPDIR:-}"

export HOME="$SCRATCH/home"
export HISTFILE=/dev/null
# $TMUX beats TMUX_TMPDIR — without the unset every tmux call (incl.
# cleanup's kill-server) lands on the REAL server (2026-07-07 grove-7).
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"
mkdir -p "$HOME" "$TMUX_TMPDIR" "$SCRATCH/bin"
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

# Stub gh: PR lookups only. task-002's PR merged into the train, task-003's
# is open; everything else (incl. the feature's own PR) has none. Records
# argv so the no-mutation rule is checkable.
cat > "$SCRATCH/bin/gh" <<'EOF'
#!/bin/sh
echo "$*" >> "$GH_LOG"
pr() { printf '[{"number":%s,"url":"https://github.com/x/y/pull/%s","state":"%s","mergedAt":%s,"isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","statusCheckRollup":[],"comments":[]}]' "$1" "$1" "$2" "$3"; }
case "$*" in
  *"pr list"*"--head task-002"*) pr 42 MERGED '"2026-09-27T00:00:00Z"' ;;
  *"pr list"*"--head task-003"*) pr 43 OPEN null ;;
  *) printf '[]' ;;
esac
EOF
chmod +x "$SCRATCH/bin/gh"
export PATH="$SCRATCH/bin:$PATH"
export GH_LOG="$SCRATCH/gh.log"
: > "$GH_LOG"

py() { python3 -c "$1"; }

say "scratch workspace + bare origin"
DUMMY="$SCRATCH/repos/dummy"
mkdir -p "$DUMMY" && cd "$DUMMY"
git init -q -b main
git config user.email e2e@grove.test && git config user.name "grove e2e"
echo "# dummy" > README.md
git add -A && git commit -qm init
"$GV" init --yes > "$SCRATCH/init.out"
perl -pi -e 's/^(\s*)base: main$/$1base: main\n$1claude: echo/' "$DUMMY/.grove/config.yaml"
grep -q 'claude: echo' "$DUMMY/.grove/config.yaml" || fail "claude stub not written"
git init -q --bare -b main "$SCRATCH/origin.git"
git remote add origin "$SCRATCH/origin.git"
git push -q origin main
MAIN_SHA="$(git rev-parse main)"
remote_sha() { git ls-remote origin "refs/heads/$1" | cut -f1; }

say "feature new: pushes feature/<slug> at origin/main"
"$GV" feature new trains --repo dummy > "$SCRATCH/new.out" || fail "feature new: $(cat "$SCRATCH/new.out")"
grep -q "✓ feature trains: created and pushed feature/trains at ${MAIN_SHA:0:12} (base main, label trains)" "$SCRATCH/new.out" \
  || fail "feature new output: $(cat "$SCRATCH/new.out")"
[ "$(remote_sha feature/trains)" = "$MAIN_SHA" ] || fail "feature/trains not at origin/main"
git show-ref --verify -q refs/heads/feature/trains && fail "feature new created a local branch"

say "feature new --adopt: registers a pushed branch, pushes nothing"
git push -q origin main:refs/heads/feature/keys-secrets
REFS="$(git ls-remote origin)"
"$GV" feature new keys --adopt --branch feature/keys-secrets --label keys --repo dummy > "$SCRATCH/adopt.out" \
  || fail "adopt: $(cat "$SCRATCH/adopt.out")"
grep -q '✓ feature keys: adopted' "$SCRATCH/adopt.out" || fail "adopt output: $(cat "$SCRATCH/adopt.out")"
[ "$(git ls-remote origin)" = "$REFS" ] || fail "--adopt pushed something"

say "the train moves one commit past main"
git push -q origin "$(git commit-tree -p "$MAIN_SHA" -m 'train commit' "$(git rev-parse "$MAIN_SHA^{tree}")"):refs/heads/feature/trains"
TRAIN_SHA="$(remote_sha feature/trains)"
[ "$TRAIN_SHA" != "$MAIN_SHA" ] || fail "feature/trains did not move"

say "grab by label: forks from the feature tip, kickoff names the PR base"
mdtask() { printf -- '---\nid: %s\ntitle: %s\nstatus: todo\nlabels: [%s]\n---\n\n%s\n' "$1" "$2" "$3" "$2" > "$DUMMY/.grove/tasks/$1.md"; }
mdtask task-002 "merged car" "trains"
mdtask task-003 "open car" "trains, ui"
mdtask task-004 "queued car" "trains"
mdtask task-005 "keys car" "keys"
for t in task-002 task-003 task-005; do
  "$GV" grab "$t" > "$SCRATCH/grab-$t.out" || fail "grab $t: $(cat "$SCRATCH/grab-$t.out")"
done
grep -qx 'base: feature/trains (feature trains, from label trains)' "$SCRATCH/grab-task-002.out" || fail "grab did not name its inferred base"
grep -qx 'base: feature/keys-secrets (feature keys, from label keys)' "$SCRATCH/grab-task-005.out" || fail "adopted feature not inferred"
WT2="$(sed -n 's/^→ worktree //p' "$SCRATCH/grab-task-002.out")"
[ "$(git -C "$WT2" rev-parse HEAD)" = "$TRAIN_SHA" ] || fail "car did not fork from origin/feature/trains"
PROMPT="$DUMMY/.grove/state/prompts/task-002.txt"
grep -q 'gh pr create --base feature/trains' "$PROMPT" || fail "kickoff does not target feature/trains"
grep -q 'This task belongs to feature `trains`' "$PROMPT" || fail "kickoff does not name the feature"
grep -q -- '--base main' "$PROMPT" && fail "kickoff still names main as the PR base"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls.json"
py "
import json
rows = {t['ticket']: t for t in json.load(open('$SCRATCH/ls.json'))['tasks']}
for tk, f, b in (('task-002','trains','feature/trains'), ('task-003','trains','feature/trains'), ('task-005','keys','feature/keys-secrets')):
    assert (rows[tk].get('feature'), rows[tk].get('base')) == (f, b), rows[tk]
" || fail "gv ls --json feature/base wrong"

say "main moves two commits: feature ls --json status"
for n in 1 2; do
  git commit -q --allow-empty -m "main $n"
done
git push -q origin main
git fetch -q origin
"$GV" feature ls --json > "$SCRATCH/fls.json" 2> "$SCRATCH/fls.err" || fail "feature ls: $(cat "$SCRATCH/fls.err")"
py "
import json
data = json.load(open('$SCRATCH/fls.json'))
assert data['schema_version'] == 1, data
fs = {f['slug']: f for f in data['features']}
assert set(fs) == {'trains', 'keys'}, fs
t = fs['trains']
assert (t['branch'], t['base'], t['label']) == ('feature/trains', 'main', 'trains'), t
cars = {c['ticket']: c['state'] for c in t['cars']}
assert cars == {'task-002': 'working', 'task-003': 'working', 'task-004': 'queued'}, cars
assert (t['landed'], t['total']) == (0, 3), t
assert t['behind_base'] == 2 and t['mergeable'] is True, t
assert 'pr' not in t, t   # stub gh: no PR for the feature branch yet
assert [c['ticket'] for c in fs['keys']['cars']] == ['task-005'], fs['keys']
" || fail "feature ls --json status wrong: $(cat "$SCRATCH/fls.json")"

say "feature land --json: a dry run"
TASKS_BEFORE="$(cat "$DUMMY"/.grove/tasks/*.md | shasum)"
: > "$GH_LOG"
"$GV" feature land trains --json > "$SCRATCH/dry.json" || fail "land dry run failed"
py "
import json
d = json.load(open('$SCRATCH/dry.json'))
assert (d['schema_version'], d['feature']) == (1, 'trains'), d
assert [(r['ticket'], r['number'], r['pr']) for r in d['land']] == [('task-002', 2, 42)], d['land']
assert {r['ticket']: r['reason'] for r in d['skipped']} == {'task-003': 'PR open', 'task-004': 'queued'}, d['skipped']
assert 'landed' not in d and 'failed' not in d, d
" || fail "land dry run JSON wrong: $(cat "$SCRATCH/dry.json")"
[ -d "$WT2" ] || fail "dry run touched the worktree"

say "feature land --yes: lands the merged car, never touches the backend"
"$GV" feature land trains --yes > "$SCRATCH/land.out" 2>&1 || fail "land --yes: $(cat "$SCRATCH/land.out")"
grep -qx 'landed: #2' "$SCRATCH/land.out" || fail "land --yes did not land #2: $(cat "$SCRATCH/land.out")"
[ -d "$WT2" ] && fail "landed car's worktree left behind"
[ "$(cat "$DUMMY"/.grove/tasks/*.md | shasum)" = "$TASKS_BEFORE" ] || fail "feature land mutated the backend's task files"
grep -Ei 'issue (close|comment|edit)|pr (merge|close|comment|edit)' "$GH_LOG" > /dev/null && fail "gh ran a mutating verb: $(cat "$GH_LOG")"
"$GV" feature ls --json > "$SCRATCH/fls2.json" 2> /dev/null
py "
import json
t = {f['slug']: f for f in json.load(open('$SCRATCH/fls2.json'))['features']}['trains']
assert (t['landed'], t['total']) == (1, 3), t
assert {c['ticket']: c['state'] for c in t['cars']}['task-002'] == 'landed', t['cars']
" || fail "landed car not counted: $(cat "$SCRATCH/fls2.json")"

say "feature close: branch left as is, the label stops inferring"
"$GV" feature close trains > "$SCRATCH/close.out" || fail "close failed"
grep -q '✓ feature trains closed (merged) — branch feature/trains left as is' "$SCRATCH/close.out" || fail "close output: $(cat "$SCRATCH/close.out")"
[ "$(remote_sha feature/trains)" = "$TRAIN_SHA" ] || fail "close moved or deleted the branch"
if "$GV" feature close trains > /dev/null 2>&1; then fail "second close succeeded"; fi
"$GV" feature ls --json --no-pr --no-queued > "$SCRATCH/fls3.json"
"$GV" feature ls --all --json --no-pr --no-queued > "$SCRATCH/fls-all.json"
py "
import json
open_ = {f['slug'] for f in json.load(open('$SCRATCH/fls3.json'))['features']}
assert open_ == {'keys'}, open_
alld = {f['slug']: f for f in json.load(open('$SCRATCH/fls-all.json'))['features']}
assert alld['trains']['closed']['reason'] == 'merged', alld['trains']
" || fail "closed feature still listed as open"
"$GV" grab task-004 > "$SCRATCH/grab-after.out" || fail "grab after close: $(cat "$SCRATCH/grab-after.out")"
grep -q '^base: feature/' "$SCRATCH/grab-after.out" && fail "a closed feature's label still inferred a base"
WT4="$(sed -n 's/^→ worktree //p' "$SCRATCH/grab-after.out")"
[ "$(git -C "$WT4" rev-parse HEAD)" = "$(remote_sha main)" ] || fail "post-close grab did not fork from origin/main"
if "$GV" grab task-003 --feature trains > /dev/null 2>&1; then fail "--feature onto a closed train succeeded"; fi
"$GV" feature close keys --reason abandoned > /dev/null || fail "close keys failed"
events() { cat "$DUMMY/.grove/state/events.jsonl"; }
[ "$(events | grep -c '"type":"feature_created"')" = 2 ] || fail "want 2 feature_created events"
[ "$(events | grep -c '"type":"feature_closed"')" = 2 ] || fail "want 2 feature_closed events"

say "real tmux server untouched"
REAL_AFTER="$(env -u TMUX_TMPDIR ${REAL_ENV_TMPDIR:+TMUX_TMPDIR=$REAL_ENV_TMPDIR} ${REAL_ENV_TMUX:+TMUX=$REAL_ENV_TMUX} tmux list-sessions -F '#{session_name}' 2>/dev/null | sort || true)"
[ "$REAL_BEFORE" = "$REAL_AFTER" ] || fail "the real tmux server's sessions changed"

printf '\n\033[32mfeature lifecycle e2e green\033[0m\n'
