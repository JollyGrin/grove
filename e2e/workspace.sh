#!/usr/bin/env bash
# Workspaces E2E (plan 2026-07-05-workspaces): two scratch workspaces (one
# repo-scope, one parent-scope) + one legacy no-marker repo, on scratch
# HOME and an ISOLATED tmux server. Asserts per-workspace state isolation,
# ambient scoping of gv ls, the VISIBLE focus marker (the driver: dash pane
# renders GROVE · <label>), grove-<label> sessions, hook ownership landing
# in the right fleet, switch --print, and the legacy path staying alive.
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-ws.XXXXXX)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

export HOME="$SCRATCH/home"
# Hermetic scratch HOME: a pane shell flushes .bash_history into it as it
# DIES — i.e. during cleanup's rm -rf, which then fails "Directory not
# empty" and (as the EXIT trap's last command) becomes this suite's exit
# status. Every assertion passes and the script still reports red; it hit
# e2e/all.sh under load on 2026-08-27.
export HISTFILE=/dev/null
export LESSHISTFILE=-
# $TMUX beats TMUX_TMPDIR — without the unset, a run from inside a tmux
# pane puts every tmux call (incl. cleanup's kill-server) on the REAL
# server. See LEARNINGS.md (2026-07-07 grove-7 crash).
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"
mkdir -p "$HOME" "$TMUX_TMPDIR"
unset GROVE_STATE_DIR || true   # per-workspace state is the subject here
cleanup() {
  tmux kill-server 2>/dev/null || true
  # kill-server only signals: wait for the panes to actually go, or a
  # shell still flushing its history recreates a directory rm just
  # emptied (see the HISTFILE note above), then retry once.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -S "$TMUX_TMPDIR/tmux-$(id -u)/default" ] || break
    sleep 0.2
  done
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  # Retry once (the settle above is a poll, not a guarantee), and never
  # let the remove decide the suite's exit status — as the EXIT trap's
  # last command it would turn a fully-green run red (grove-191 reached
  # the same `|| true`; the settle + retry is so the tree actually goes,
  # instead of accumulating in /tmp).
  rm -rf "$SCRATCH" 2>/dev/null || { sleep 0.5; rm -rf "$SCRATCH" 2>/dev/null || true; }
}
trap cleanup EXIT

# Hostile tmux config mode (grove-168): GROVE_E2E_TMUX_CONF=hostile boots the
# isolated server with base-index 1 + pane-base-index 1 — the common dotfiles
# pair that turned literal ".0"/".1" pane targets into fresh-install failures.
# -f only applies at server boot, so start the server now (exit-empty off
# keeps it alive with no sessions); every later gv tmux call joins it with
# the hostile options already global.
if [ "${GROVE_E2E_TMUX_CONF:-}" = "hostile" ]; then
  say "hostile tmux conf (base-index 1, pane-base-index 1)"
  cat > "$SCRATCH/hostile.conf" <<'EOF'
set -g base-index 1
set -g pane-base-index 1
set -g renumber-windows on
set -g allow-rename on
set -g exit-empty off
EOF
  tmux -f "$SCRATCH/hostile.conf" start-server
fi

mkrepo() { # dir
  mkdir -p "$1" && git -C "$1" init -qb main . 2>/dev/null || git -C "$1" init -qb main
  git -C "$1" config user.email e2e@x && git -C "$1" config user.name e2e
  ( cd "$1" && echo x > README.md && git add -A && git commit -qm init )
}

say "repo-scope workspace: alpha"
ALPHA="$SCRATCH/alpha"
mkrepo "$ALPHA"
( cd "$ALPHA" && "$GV" init --yes --label alpha > /dev/null )
[ -f "$ALPHA/.grove/config.yaml" ] || fail "alpha workspace config missing"
grep -q 'label: alpha' "$ALPHA/.grove/config.yaml" || fail "alpha label missing"
[ -f "$ALPHA/.grove/.gitignore" ] || fail "alpha .grove/.gitignore missing"

say "parent-scope workspace: duo (two sibling repos)"
DUO="$SCRATCH/duo"
mkrepo "$DUO/svc-a"
mkrepo "$DUO/svc-b"
( cd "$DUO" && "$GV" init --yes --label duo > /dev/null )
grep -q 'scope: parent' "$DUO/.grove/config.yaml" || fail "duo scope missing"
grep -q 'svc-a' "$DUO/.grove/config.yaml" && grep -q 'svc-b' "$DUO/.grove/config.yaml" || fail "duo children not registered"

say "registry lists both"
"$GV" workspaces > "$SCRATCH/wslist.out"
grep -q alpha "$SCRATCH/wslist.out" && grep -q duo "$SCRATCH/wslist.out" || fail "registry incomplete:
$(cat "$SCRATCH/wslist.out")"

say "stub workers to echo"
perl -pi -e 's/^(\s*)base: main$/$1base: main\n$1claude: echo/' "$ALPHA/.grove/config.yaml" "$DUO/.grove/config.yaml"

say "grab in alpha uses alpha's state and echoes the workspace"
( cd "$ALPHA" && "$GV" grab task-001 > "$SCRATCH/grab-a.out" )
grep -q '→ workspace: alpha' "$SCRATCH/grab-a.out" || fail "grab must echo the resolved workspace"
[ -s "$ALPHA/.grove/state/events.jsonl" ] || fail "alpha state not written"
[ ! -e "$DUO/.grove/state/events.jsonl" ] || fail "duo state must be untouched by alpha's grab"
[ ! -e "$HOME/.local/state/grove/events.jsonl" ] || fail "legacy state must be untouched by a workspace grab"

say "gv ls is ambient-scoped"
( cd "$ALPHA" && "$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-a.json" )
grep -q 'task-001' "$SCRATCH/ls-a.json" || fail "alpha ls missing its task"
( cd "$DUO" && "$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-d.json" )
grep -q 'task-001' "$SCRATCH/ls-d.json" && fail "duo ls must not see alpha's task" || true

say "hook ownership lands in the owning fleet only"
WT="$(ls -d "$SCRATCH/.worktrees/alpha/task-001-"* 2>/dev/null || ls -d "$SCRATCH"/*/.worktrees/alpha/task-001-* 2>/dev/null | head -1)"
[ -n "$WT" ] || WT="$(find "$SCRATCH" -type d -name 'task-001-*' -path '*worktrees*' | head -1)"
[ -n "$WT" ] || fail "alpha worktree not found"
EV_A=$(wc -l < "$ALPHA/.grove/state/events.jsonl")
printf '{"session_id":"ws-e2e","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"STATUS: QUESTION — which fleet?"}' "$WT" \
  | ( cd "$SCRATCH" && "$GV" hook stop )   # note: run from OUTSIDE alpha — ownership, not cwd of gv
[ "$(wc -l < "$ALPHA/.grove/state/events.jsonl")" -gt "$EV_A" ] || fail "hook event did not land in alpha"
[ ! -e "$DUO/.grove/state/events.jsonl" ] || fail "hook wrote into a non-owning fleet"

say "grove-191: global-layer gv ls aggregates the registered workspaces"
( cd "$SCRATCH" && "$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-global.json" )
grep -q 'task-001' "$SCRATCH/ls-global.json" || fail "global ls must aggregate workspace tasks:
$(cat "$SCRATCH/ls-global.json")"
grep -q '"workspace": "alpha"' "$SCRATCH/ls-global.json" || fail "aggregated rows must carry the workspace label:
$(cat "$SCRATCH/ls-global.json")"
( cd "$SCRATCH" && "$GV" ls --no-pr --no-cost > "$SCRATCH/ls-global.out" )
grep -q 'WORKSPACE' "$SCRATCH/ls-global.out" || fail "human table must gain the WORKSPACE column:
$(cat "$SCRATCH/ls-global.out")"
grep -q '@alpha' "$SCRATCH/ls-global.out" || fail "human rows must tag the workspace:
$(cat "$SCRATCH/ls-global.out")"
# in-workspace output is byte-identical: the field never leaks in
! grep -q '"workspace"' "$SCRATCH/ls-a.json" || fail "in-workspace ls must not emit the workspace field"

say "grove-191: global-layer answer re-execs into the owning workspace"
EV_A=$(wc -l < "$ALPHA/.grove/state/events.jsonl")
( cd "$SCRATCH" && "$GV" answer task-001 "routed hello" > "$SCRATCH/answer-global.out" 2> "$SCRATCH/answer-global.err" )
grep -q '→ workspace alpha' "$SCRATCH/answer-global.out" || fail "answer must print the routing line:
$(cat "$SCRATCH/answer-global.out")
$(cat "$SCRATCH/answer-global.err")"
grep -q '✓ sent' "$SCRATCH/answer-global.out" || fail "routed answer must complete:
$(cat "$SCRATCH/answer-global.out")"
[ "$(wc -l < "$ALPHA/.grove/state/events.jsonl")" -eq "$((EV_A + 1))" ] || fail "answered event must land in alpha's state"
[ ! -e "$HOME/.local/state/grove/events.jsonl" ] || fail "routed answer must not touch global state"

say "grove-191: a ticket tracked in two workspaces is an honest error"
mkdir -p "$DUO/svc-a/.grove/tasks"
printf -- "---\nid: task-001\ntitle: duo duplicate\nstatus: todo\n---\nbody\n" > "$DUO/svc-a/.grove/tasks/task-001.md"
( cd "$DUO" && "$GV" grab task-001 --repo svc-a > "$SCRATCH/grab-duo.out" 2>&1 )
grep -q '✓ task-001 grabbed' "$SCRATCH/grab-duo.out" || fail "duo grab of the duplicate ticket failed:
$(cat "$SCRATCH/grab-duo.out")"
rc=0
( cd "$SCRATCH" && "$GV" answer task-001 "which one?" > "$SCRATCH/ambiguous.out" 2>&1 ) || rc=$?
[ "$rc" -ne 0 ] || fail "ambiguous ticket must exit non-zero"
grep -q 'alpha' "$SCRATCH/ambiguous.out" && grep -q 'duo' "$SCRATCH/ambiguous.out" \
  || fail "ambiguity error must name both workspaces:
$(cat "$SCRATCH/ambiguous.out")"

say "dash pane shows the workspace label (the driver)"
( cd "$ALPHA" && "$GV" >/dev/null 2>&1 || true )   # headless attach fails after build — expected
tmux has-session -t grove-alpha 2>/dev/null || fail "grove-alpha session missing"
sleep 2
PANE0="$(tmux list-panes -t grove-alpha -F '#{pane_id}' | head -1)"
# -S -300: include scrollback so a gv panic keeps its reason line (grove-79).
CAP="$(tmux capture-pane -p -S -300 -t "$PANE0")"
echo "$CAP" | grep -q 'GROVE' || fail "dash not rendering:
$CAP"
echo "$CAP" | grep -q 'alpha' || fail "dash must show the workspace label:
$CAP"

say "switch --print + non-TTY list"
[ "$("$GV" switch alpha --print)" = "$(cd "$ALPHA" && pwd -P)" ] || fail "switch --print wrong root"
"$GV" switch > "$SCRATCH/switch.out"
grep -q 'alpha' "$SCRATCH/switch.out" && grep -q 'duo' "$SCRATCH/switch.out" || fail "non-TTY switch must print the rollup list"

say "legacy path still alive (no marker, global config)"
LEG="$SCRATCH/legacy-repo"
mkrepo "$LEG"
mkdir -p "$HOME/.config/grove"
cat > "$HOME/.config/grove/config.yaml" <<EOF
provider: {kind: markdown}
repos:
  legacy-repo: {path: $LEG, base: main, claude: echo}
  alpha: {path: $ALPHA, base: main, claude: echo}
EOF
mkdir -p "$LEG/.grove/tasks"
printf -- "---\nid: task-009\ntitle: legacy check\nstatus: todo\n---\nbody\n" > "$LEG/.grove/tasks/task-009.md"
( cd "$SCRATCH" && "$GV" grab task-009 --repo legacy-repo > "$SCRATCH/grab-leg.out" )
grep -q 'workspace:' "$SCRATCH/grab-leg.out" && fail "legacy grab must not claim a workspace" || true
[ -s "$HOME/.local/state/grove/events.jsonl" ] || fail "legacy grab must use the global state dir"

say "grove-191: global-layer grab --repo <workspace repo> routes instead of refusing"
printf -- "---\nid: task-002\ntitle: routed grab\nstatus: todo\n---\nbody\n" > "$ALPHA/.grove/tasks/task-002.md"
EV_A=$(wc -l < "$ALPHA/.grove/state/events.jsonl")
( cd "$SCRATCH" && "$GV" grab task-002 --repo alpha > "$SCRATCH/grab-route.out" 2>&1 )
grep -q '→ workspace alpha' "$SCRATCH/grab-route.out" || fail "grab must print the routing line:
$(cat "$SCRATCH/grab-route.out")"
grep -q '✓ task-002 grabbed' "$SCRATCH/grab-route.out" || fail "routed grab must complete:
$(cat "$SCRATCH/grab-route.out")"
[ "$(wc -l < "$ALPHA/.grove/state/events.jsonl")" -gt "$EV_A" ] || fail "routed grab must grow the WORKSPACE state"
grep -q '"ticket":"task-002"' "$ALPHA/.grove/state/events.jsonl" || fail "task_created must land in alpha's events"
! grep -q '"ticket":"task-002"' "$HOME/.local/state/grove/events.jsonl" || fail "routed grab must not touch global state"

say "grove-191: routed verbs propagate the child's exit code"
rc=0
( cd "$SCRATCH" && "$GV" done task-002 > "$SCRATCH/done-route.out" 2>&1 ) || rc=$?
[ "$rc" -ne 0 ] || fail "an unmerged done must exit non-zero through the route"
grep -q '→ workspace alpha' "$SCRATCH/done-route.out" || fail "done must print the routing line"
grep -q 'has no remote' "$SCRATCH/done-route.out" || fail "the refusal must come from inside the workspace:
$(cat "$SCRATCH/done-route.out")"
grep -q '"ticket":"task-002"' "$ALPHA/.grove/state/events.jsonl" || fail "a refused done must leave the task tracked"

say "grove-191: routed untrack cleans up through the route"
( cd "$SCRATCH" && "$GV" untrack task-002 --rm --force > "$SCRATCH/untrack-route.out" 2>&1 )
grep -q '→ workspace alpha' "$SCRATCH/untrack-route.out" || fail "untrack must print the routing line:
$(cat "$SCRATCH/untrack-route.out")"
grep -q '✓ task-002 untracked' "$SCRATCH/untrack-route.out" || fail "routed untrack must complete:
$(cat "$SCRATCH/untrack-route.out")"

say "grove-191: a no-registry machine stays byte-identical"
mkdir -p "$SCRATCH/home2"
( cd "$SCRATCH" && HOME="$SCRATCH/home2" "$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-noreg.json" )
! grep -q '"workspace"' "$SCRATCH/ls-noreg.json" || fail "no-registry ls must not emit the workspace field:
$(cat "$SCRATCH/ls-noreg.json")"

say "grove-191: reserved ambient label fails loudly (the cloned-grove shape)"
mkrepo "$SCRATCH/grove"
mkdir -p "$SCRATCH/grove/.grove"
printf 'repos:\n  grove: {path: %s, base: main, claude: echo}\n' "$SCRATCH/grove" > "$SCRATCH/grove/.grove/config.yaml"
rc=0
( cd "$SCRATCH/grove" && "$GV" ls > "$SCRATCH/badlabel.out" 2>&1 ) || rc=$?
[ "$rc" -ne 0 ] || fail "a reserved ambient label must exit non-zero"
grep -q 'reserved' "$SCRATCH/badlabel.out" || fail "label error must say why:
$(cat "$SCRATCH/badlabel.out")"
grep -q 'workspace.label' "$SCRATCH/badlabel.out" || fail "label error must name workspace.label:
$(cat "$SCRATCH/badlabel.out")"
( cd "$SCRATCH/grove" && "$GV" > /dev/null 2>&1 ) || true   # bare gv dies at the same gate
! tmux has-session -t grove-grove 2>/dev/null || fail "must never build a grove-<reserved> session"
printf '{"session_id":"x","cwd":"/nonexistent","hook_event_name":"Stop"}' \
  | ( cd "$SCRATCH/grove" && "$GV" hook stop ) || fail "hook receiver must stay exit-0 inside a bad-label workspace"
printf 'workspace:\n  label: grove-repo\nrepos:\n  grove: {path: %s, base: main, claude: echo}\n' "$SCRATCH/grove" > "$SCRATCH/grove/.grove/config.yaml"
( cd "$SCRATCH/grove" && "$GV" ls > /dev/null 2>&1 ) || fail "a valid workspace.label must make the workspace usable"

say "untrack cleanup"
( cd "$ALPHA" && "$GV" untrack task-001 --rm --force > /dev/null )
( cd "$DUO" && "$GV" untrack task-001 --rm --force > /dev/null )

say "PASS — workspaces: isolated state, ambient scoping, visible focus, owned hooks, legacy intact"
