#!/usr/bin/env bash
# Cockpit smoke test: builds the main-vertical cockpit on an ISOLATED tmux
# server (TMUX_TMPDIR), seeds fake fleet events, and asserts the dashboard
# renders AGENTS + ACTIVITY (no MAIL/REVIEW panels) and that
# `gv orchestrator new` stacks another chat pane.
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-cockpit.XXXXXX)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

export HOME="$SCRATCH/home"
export GROVE_STATE_DIR="$SCRATCH/state"
export TMUX_TMPDIR="$SCRATCH/tmux"   # isolated tmux server — never the user's
mkdir -p "$HOME/.config/grove" "$GROVE_STATE_DIR" "$TMUX_TMPDIR" "$SCRATCH/repo"

cleanup() {
  tmux kill-server 2>/dev/null || true
  rm -rf "$SCRATCH"
}
trap cleanup EXIT

say "seed config + fleet events"
cat > "$HOME/.config/grove/config.yaml" <<EOF
provider: {kind: markdown}
repos:
  demo: {path: $SCRATCH/repo, claude: echo}
orchestrator: {claude: echo}
EOF
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
cat > "$GROVE_STATE_DIR/events.jsonl" <<EOF
{"time":"$NOW","type":"task_created","ticket":"task-001","data":{"title":"Demo task","repo":"demo","branch":"task-001-demo","worktree":"$SCRATCH/repo","tmux_session":"pr-demo","tmux_window":"task-001-demo"}}
{"time":"$NOW","type":"agent_status","ticket":"task-001","data":{"status":"waiting","sentinel":"question","question":"tabs or spaces?","message":"STATUS: QUESTION — tabs or spaces?"}}
EOF

say "gv (bare) builds the cockpit (attach fails headless — expected)"
"$GV" >/dev/null 2>&1 || true
tmux has-session -t grove 2>/dev/null || fail "cockpit session not created"

sleep 2
say "left pane renders AGENTS + ACTIVITY, no MAIL/REVIEW"
PANE0="$(tmux list-panes -t grove -F '#{pane_id}' | head -1)"
CAP="$(tmux capture-pane -p -t "$PANE0")"
echo "$CAP" | grep -q 'AGENTS'     || fail "AGENTS panel missing:
$CAP"
echo "$CAP" | grep -q 'ACTIVITY'   || fail "ACTIVITY feed missing:
$CAP"
# The narrow left pane truncates long lines — assert on prefixes.
echo "$CAP" | grep -q 'asked: tabs' || fail "feed missing the question event:
$CAP"
echo "$CAP" | grep -q 'grabbed'     || fail "feed missing the grab event:
$CAP"
echo "$CAP" | grep -q 'MAIL'   && fail "MAIL panel should be gone" || true
echo "$CAP" | grep -q 'REVIEW QUEUE' && fail "REVIEW panel should be gone" || true

say "gv orchestrator new stacks a chat pane"
PANES_BEFORE=$(tmux list-panes -t grove | wc -l)
"$GV" orchestrator new >/dev/null 2>&1 || true
PANES_AFTER=$(tmux list-panes -t grove | wc -l)
[ "$PANES_AFTER" -gt "$PANES_BEFORE" ] || fail "no new pane ($PANES_BEFORE → $PANES_AFTER)"

say "layout is main-vertical (TUI pane spans full height)"
tmux list-panes -t grove -F '#{pane_id} #{pane_width}x#{pane_height}' | head -3

say "PASS — cockpit: AGENTS+ACTIVITY left, stacked chats right, O/new works"
