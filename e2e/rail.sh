#!/usr/bin/env bash
# grove-486: the focus-mode chat rail on an ISOLATED tmux server. A workspace
# cockpit built with cockpit.layout: focus gets exactly one rail pane under
# the chat; a chat hidden by hand (break-pane) gets a box in it, showing the
# chat's label (read from its transcript); `L` back to horizontal removes the
# rail and only the rail. Also: the rail is invisible to `gv chat ls`, and a
# chat verb aimed at it refuses.
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(cd "$(mktemp -d /tmp/grove-rail.XXXXXX)" && pwd -P)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

export HOME="$SCRATCH/home"
export HISTFILE=/dev/null
export LESSHISTFILE=-
# $TMUX beats TMUX_TMPDIR (grove-7 crash): unset before anything touches tmux.
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"
export GV_CLAUDE_CONFIG_DIR="$SCRATCH/claude"
mkdir -p "$HOME" "$TMUX_TMPDIR"
unset GROVE_STATE_DIR || true
cleanup() {
  tmux kill-server 2>/dev/null || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -S "$TMUX_TMPDIR/tmux-$(id -u)/default" ] || break
    sleep 0.2
  done
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH" 2>/dev/null || { sleep 0.5; rm -rf "$SCRATCH" 2>/dev/null || true; }
}
trap cleanup EXIT

if [ "${GROVE_E2E_TMUX_CONF:-}" = "hostile" ]; then
  say "hostile tmux conf (base-index 1, pane-base-index 1)"
  cat > "$SCRATCH/hostile.conf" <<'CONF'
set -g base-index 1
set -g pane-base-index 1
set -g renumber-windows on
set -g allow-rename on
set -g exit-empty off
CONF
  tmux -f "$SCRATCH/hostile.conf" start-server
else
  printf 'set -g exit-empty off\n' > "$SCRATCH/tmux.conf"
  tmux -f "$SCRATCH/tmux.conf" start-server
fi

say "workspace alpha with cockpit.layout: focus"
WS="$SCRATCH/alpha"
mkdir -p "$WS"
git -C "$WS" init -q -b main
git -C "$WS" config user.email e2e@x && git -C "$WS" config user.name e2e
( cd "$WS" && echo x > README.md && git add -A && git commit -qm init )
( cd "$WS" && "$GV" init --yes --label alpha > /dev/null )
cat >> "$WS/.grove/config.yaml" <<CFG
orchestrator: {claude: echo}
cockpit:
  layout: focus
CFG

say "gv builds the cockpit (headless attach fails — expected)"
( cd "$WS" && "$GV" >/dev/null 2>&1 || true )
tmux has-session -t grove-alpha 2>/dev/null || fail "grove-alpha session missing"
tmux resize-window -t '=grove-alpha:cockpit' -x 200 -y 50
sleep 1

rails() { tmux list-panes -t '=grove-alpha:cockpit' -F '#{pane_id} #{@grove_rail}' | awk 'NF==2 {print $1}'; }
[ "$(rails | wc -l | tr -d ' ')" = 1 ] || fail "want exactly one rail pane, got: $(rails)"
RAIL="$(rails)"
[ "$(tmux list-panes -t '=grove-alpha:cockpit' | wc -l | tr -d ' ')" = 3 ] || fail "want dash + chat + rail"
H="$(tmux display-message -p -t "$RAIL" '#{pane_height}')"
[ "$H" = 4 ] || fail "rail height = $H, want 4"
LAST="$(tmux list-panes -t '=grove-alpha:cockpit' -F '#{pane_id}' | tail -1)"
[ "$LAST" = "$RAIL" ] || fail "rail must be the bottom pane of the right column"

say "a second build reuses the rail"
( cd "$WS" && "$GV" >/dev/null 2>&1 || true )
[ "$(rails | wc -l | tr -d ' ')" = 1 ] || fail "second build duplicated the rail: $(rails)"
[ "$(rails)" = "$RAIL" ] || fail "second build replaced the rail pane"

say "empty rail reads 'no minimized chats'"
CAP=""
for _ in $(seq 1 20); do
  CAP="$(tmux capture-pane -p -S - -t "$RAIL" | tr -d '\n')"
  echo "$CAP" | grep -q 'no minimized chats' && break
  sleep 0.5
done
echo "$CAP" | grep -q 'no minimized chats' || fail "rail did not render:
$CAP"

say "hide the chat by hand; seed its transcript so the box has a label"
ORCH="$WS/.grove/orchestrator"
CHAT="$(tmux list-panes -t '=grove-alpha:cockpit' -F '#{pane_id} #{pane_current_path} #{@grove_rail}' | awk -v o="$ORCH" 'index($2, o) == 1 && NF == 2 {print $1}' | head -1)"
[ -n "$CHAT" ] || fail "no orchestrator chat pane found"
ID="11111111-2222-3333-4444-555555555555"
PD="$GV_CLAUDE_CONFIG_DIR/projects/$(printf '%s' "$ORCH" | sed 's#[/.]#-#g')"
mkdir -p "$PD"
printf '{"type":"user","cwd":"%s","gitBranch":"main","message":{"role":"user","content":"polish the rail boxes"}}\n' "$ORCH" > "$PD/$ID.jsonl"
tmux set-option -p -t "$CHAT" @grove_chat_session "$ID"
tmux break-pane -d -s "$CHAT"
CHAT_PID="$(tmux display-message -p -t "$CHAT" '#{pane_pid}')"

say "the rail shows the hidden chat's label (cockpit writes on its ~5s beat)"
CAP=""
for _ in $(seq 1 40); do
  CAP="$(tmux capture-pane -p -S - -t "$RAIL" | tr -d '\n')"
  echo "$CAP" | grep -q 'polish the rail' && break
  sleep 0.5
done
echo "$CAP" | grep -q 'polish the rail' || fail "rail box missing the chat label:
$CAP
$(tmux show-options -t '=grove-alpha:' -v @grove_rail_rows 2>&1)"

say "the rail is not a chat: ls skips it, verbs refuse it"
( cd "$WS" && "$GV" chat ls --json ) > "$SCRATCH/ls.json"
if grep -q "$RAIL" "$SCRATCH/ls.json"; then fail "gv chat ls listed the rail"; fi
rc=0; ( cd "$WS" && "$GV" chat close "$RAIL" ) > "$SCRATCH/close.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "chat close of the rail must fail"
grep -q 'that is the rail, not a chat' "$SCRATCH/close.out" || fail "wrong refusal: $(cat "$SCRATCH/close.out")"
tmux list-panes -t '=grove-alpha:cockpit' -F '#{pane_id}' | grep -qx "$RAIL" || fail "refused close still killed the rail"

say "layout -> horizontal kills the rail and only the rail"
DASH="$(tmux list-panes -t '=grove-alpha:cockpit' -F '#{pane_id}' | head -1)"
tmux send-keys -t "$DASH" L   # the dash's own layout toggle: focus -> horizontal
for _ in $(seq 1 20); do
  tmux list-panes -t '=grove-alpha:cockpit' -F '#{pane_id}' | grep -qx "$RAIL" || break
  sleep 0.3
done
tmux list-panes -t '=grove-alpha:cockpit' -F '#{pane_id}' | grep -qx "$RAIL" && fail "rail pane survived a horizontal build"
[ "$(tmux display-message -p -t "$CHAT" '#{pane_pid}')" = "$CHAT_PID" ] || fail "the hidden chat's pid changed"
echo ok

say "L again respawns exactly one rail"
tmux send-keys -t "$DASH" L
for _ in $(seq 1 20); do
  [ "$(rails | wc -l | tr -d ' ')" = 1 ] && break
  sleep 0.3
done
[ "$(rails | wc -l | tr -d ' ')" = 1 ] || fail "L back to focus must leave one rail, got: $(rails)"

say "rail process RSS at rest (KB)"
RPID="$(tmux display-message -p -t "$(rails)" '#{pane_pid}')"
sleep 3
ps -o rss= -p "$RPID" | tr -d ' ' | sed 's/^/RSS_KB=/'
