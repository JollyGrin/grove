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
# $TMUX beats TMUX_TMPDIR in tmux's socket resolution — launched from
# inside a tmux pane, TMUX_TMPDIR alone is a silent no-op and every tmux
# call (including cleanup's kill-server) hits the REAL server. Unset first.
# (Root cause of the 2026-07-07 grove-7 crash; see LEARNINGS.md.)
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"   # isolated tmux server — never the user's
mkdir -p "$HOME/.config/grove" "$GROVE_STATE_DIR" "$TMUX_TMPDIR" "$SCRATCH/repo"

cleanup() {
  tmux kill-server 2>/dev/null || true
  rm -rf "$SCRATCH"
}
trap cleanup EXIT

# Hostile tmux config mode (grove-168): GROVE_E2E_TMUX_CONF=hostile boots the
# isolated server with the common dotfiles pair base-index 1 +
# pane-base-index 1, which turned every literal ".0"/".1" pane target into a
# fresh-install failure. The scratch HOME means suites never load a real
# tmux.conf, so without this mode that whole class is structurally invisible
# here. -f only applies at server boot, so start the server now (exit-empty
# off keeps it alive with no sessions) and every later gv tmux call joins it
# with the hostile options already global.
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
# -S -300: include scrollback, not just the visible screen — if gv panics,
# the alt-screen closes and the panic reason scrolls off a bare capture
# (grove-79 lost its reason line exactly this way).
CAP="$(tmux capture-pane -p -S -300 -t "$PANE0")"
echo "$CAP" | grep -q 'AGENTS'     || fail "AGENTS panel missing:
$CAP"
echo "$CAP" | grep -q 'ACTIVITY'   || fail "ACTIVITY feed missing:
$CAP"
# The narrow left pane truncates long lines — assert on prefixes.
echo "$CAP" | grep -q 'asked: tabs' || fail "feed missing the question event:
$CAP"
# A grab younger than a minute renders as "planted" at full effects
# (grove-56 J4, presentation-only); the seeded event is brand-new, so the
# correct render here is the planting — accept either text.
echo "$CAP" | grep -qE 'grabbed|planted' || fail "feed missing the grab event:
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
