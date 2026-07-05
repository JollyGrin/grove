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
export TMUX_TMPDIR="$SCRATCH/tmux"
mkdir -p "$HOME" "$TMUX_TMPDIR"
unset GROVE_STATE_DIR || true   # per-workspace state is the subject here
cleanup() {
  tmux kill-server 2>/dev/null || true
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH"
}
trap cleanup EXIT

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

say "dash pane shows the workspace label (the driver)"
( cd "$ALPHA" && "$GV" >/dev/null 2>&1 || true )   # headless attach fails after build — expected
tmux has-session -t grove-alpha 2>/dev/null || fail "grove-alpha session missing"
sleep 2
PANE0="$(tmux list-panes -t grove-alpha -F '#{pane_id}' | head -1)"
CAP="$(tmux capture-pane -p -t "$PANE0")"
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
EOF
mkdir -p "$LEG/.grove/tasks"
printf -- "---\nid: task-009\ntitle: legacy check\nstatus: todo\n---\nbody\n" > "$LEG/.grove/tasks/task-009.md"
( cd "$SCRATCH" && "$GV" grab task-009 --repo legacy-repo > "$SCRATCH/grab-leg.out" )
grep -q 'workspace:' "$SCRATCH/grab-leg.out" && fail "legacy grab must not claim a workspace" || true
[ -s "$HOME/.local/state/grove/events.jsonl" ] || fail "legacy grab must use the global state dir"

say "untrack cleanup"
( cd "$ALPHA" && "$GV" untrack task-001 --rm --force > /dev/null )

say "PASS — workspaces: isolated state, ambient scoping, visible focus, owned hooks, legacy intact"
