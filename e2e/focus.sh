#!/usr/bin/env bash
# grove-487: focus mode shows ONE chat and minimizes the rest, on an ISOLATED
# tmux server. dash + 3 chats (chat 2 active): `L` -> focus leaves dash +
# chat 2 + the rail, chats 1 and 3 become grove-chat-alpha-N sessions (same
# %pane, same pid), @grove_focus_hid names both, `gv chat ls` still reports
# them kind chat / writable; `L` -> horizontal restores exactly those. A chat
# hidden by hand stays hidden. A spawn in focus minimizes the shown chat.
#
# tmux: isolated — unsets TMUX/TMUX_PANE, scratch TMUX_TMPDIR, scoped
# kill-server, real-server canary before/after (tmux-discipline).
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH=$(mktemp -d /tmp/grove-focus.XXXXXX) || exit 1
SCRATCH="$(cd "$SCRATCH" && pwd -P)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

real_tmux() { env -u TMUX -u TMUX_PANE -u TMUX_TMPDIR tmux list-sessions -F '#{session_name}' 2>/dev/null | sort; true; }
REAL_TMUX_BEFORE="$(real_tmux)"

export HOME="$SCRATCH/home"
export HISTFILE=/dev/null
export LESSHISTFILE=-
unset TMUX TMUX_PANE
unset GROVE_STATE_DIR || true
export TMUX_TMPDIR="$SCRATCH/tmux"
export GV_CLAUDE_CONFIG_DIR="$SCRATCH/claude"
mkdir -p "$HOME" "$TMUX_TMPDIR"

cleanup() {
  env -u TMUX TMUX_TMPDIR="$TMUX_TMPDIR" tmux kill-server 2>/dev/null || true   # isolated server only
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -S "$TMUX_TMPDIR/tmux-$(id -u)/default" ] || break
    sleep 0.2
  done
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  case "$SCRATCH" in /tmp/grove-focus.*|/private/tmp/grove-focus.*)
    rm -rf "$SCRATCH" 2>/dev/null || { sleep 0.5; rm -rf "$SCRATCH" 2>/dev/null || true; } ;;
  esac
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

WS="$SCRATCH/alpha"
mkdir -p "$WS"
git -C "$WS" init -q -b main
git -C "$WS" config user.email e2e@x && git -C "$WS" config user.name e2e
( cd "$WS" && echo x > README.md && git add -A && git commit -qm init )
( cd "$WS" && "$GV" init --yes --label alpha > /dev/null )
cat >> "$WS/.grove/config.yaml" <<CFG
orchestrator: {claude: echo}
CFG

W='=grove-alpha:cockpit'
panes()  { tmux list-panes -t "$W" -F '#{pane_id}'; }
npanes() { panes | wc -l | tr -d ' '; }
npanes_is() { [ "$(npanes)" = "$1" ]; }
hid_count_is() { [ "$(hid_opt | wc -w | tr -d ' ')" = "$1" ]; }
waitfor() { # <tries> <cmd...>
  local n="$1"; shift
  for _ in $(seq 1 "$n"); do "$@" && return 0; sleep 0.3; done
  return 1
}
hid_opt() { tmux show-options -t '=grove-alpha:' -v @grove_focus_hid 2>/dev/null || true; }
chat_sessions() { tmux list-sessions -F '#{session_name}' | grep '^grove-chat-alpha-' | sort || true; }
EVENTS="$WS/.grove/state/events.jsonl"
# `orchestrator new` attaches when it runs outside tmux; run-shell gives it
# the server's $TMUX like a real chat spawn from inside the cockpit.
spawn() {
  tmux run-shell "cd '$WS' && '$GV' orchestrator new > '$SCRATCH/spawn.out' 2>&1" || true
  grep -q 'new orchestrator chat pane' "$SCRATCH/spawn.out" || { cat "$SCRATCH/spawn.out"; return 1; }
}

say "cockpit (horizontal) + two more chats = dash + 3 chats"
( cd "$WS" && "$GV" >/dev/null 2>&1 || true )
tmux has-session -t '=grove-alpha' 2>/dev/null || fail "grove-alpha missing"
tmux resize-window -t "$W" -x 200 -y 50
sleep 1
spawn || fail "spawn 2"
spawn || fail "spawn 3"
[ "$(npanes)" = 4 ] || fail "want dash + 3 chats, got $(npanes)"
DASH="$(panes | head -1)"; C1="$(panes | sed -n 2p)"; C2="$(panes | sed -n 3p)"; C3="$(panes | sed -n 4p)"
P1="$(tmux display-message -p -t "$C1" '#{pane_pid}')"; P3="$(tmux display-message -p -t "$C3" '#{pane_pid}')"
BYHAND_BASE="$(npanes)"
tmux select-pane -t "$C2"

say "L -> focus: dash + chat 2 + rail; chats 1 and 3 minimized"
sleep 1   # let the previous toggle finish its tail re-tile
tmux send-keys -t "$DASH" L
waitfor 40 npanes_is 3 || fail "focus: want 3 panes (dash, chat 2, rail), got $(npanes)"
waitfor 40 hid_count_is 2 || fail "focus: want two minimized chats, got '$(hid_opt)'"
panes | grep -qx "$C2" || fail "the active chat 2 must stay shown"
panes | grep -qx "$C1" && fail "chat 1 still on screen"
panes | grep -qx "$C3" && fail "chat 3 still on screen"
[ "$(chat_sessions | wc -l | tr -d ' ')" = 2 ] || fail "want two chat sessions, got: $(chat_sessions)"
S1="$(tmux display-message -p -t "$C1" '#{session_name}')"; S3="$(tmux display-message -p -t "$C3" '#{session_name}')"
case "$S1$S3" in *grove-chat-alpha-*grove-chat-alpha-*) ;; *) fail "chats not in chat sessions: $S1 $S3" ;; esac
[ "$(tmux display-message -p -t "$C1" '#{pane_pid}')" = "$P1" ] || fail "chat 1 pid changed"
[ "$(tmux display-message -p -t "$C3" '#{pane_pid}')" = "$P3" ] || fail "chat 3 pid changed"
HID="$(hid_opt)"
echo "$HID" | grep -qw "$S1" || fail "@grove_focus_hid ($HID) missing $S1"
echo "$HID" | grep -qw "$S3" || fail "@grove_focus_hid ($HID) missing $S3"
grep -q '"reason":"focus"' "$EVENTS" || fail "no chat_hidden with reason focus in events"
RAILN=""
for _ in $(seq 1 30); do
  RAILN="$(tmux show-options -t '=grove-alpha:' -v @grove_rail_rows 2>/dev/null | sed 1d | grep -c . || true)"
  [ "$RAILN" = 2 ] && break
  sleep 0.3
done
[ "$RAILN" = 2 ] || fail "rail must list chats 1 and 3 (2 rows), got $RAILN"

say "gv chat ls --json: minimized chats are kind chat, writable"
( cd "$WS" && "$GV" chat ls --json ) > "$SCRATCH/ls.json"
for s in "$S1" "$S3"; do
  python3 - "$SCRATCH/ls.json" "$s" <<'PY' || fail "ls row for $s"
import json, sys
rows = json.load(open(sys.argv[1]))
rows = rows.get("chats", rows) if isinstance(rows, dict) else rows
r = [r for r in rows if r["session"] == sys.argv[2]]
assert r and r[0]["kind"] == "chat" and r[0]["writable"] is True, r
PY
done

say "L -> horizontal: all four panes back, option cleared"
sleep 1   # let the previous toggle finish its tail re-tile
tmux send-keys -t "$DASH" L
waitfor 40 npanes_is 4 || fail "horizontal: want 4 panes, got $(npanes)"
for p in "$C1" "$C2" "$C3"; do panes | grep -qx "$p" || fail "$p not restored"; done
[ -z "$(hid_opt)" ] || fail "@grove_focus_hid not cleared: $(hid_opt)"
[ -z "$(chat_sessions)" ] || fail "chat sessions linger: $(chat_sessions)"
[ "$(tmux list-panes -t "$W" -F '#{pane_top}' | sort -u | wc -l | tr -d ' ')" = 1 ] || fail "not even-horizontal (panes at different tops)"
grep -q '"type":"chat_shown".*"reason":"focus"\|"reason":"focus".*"type":"chat_shown"' "$EVENTS" || true

say "a chat hidden by hand stays hidden through a focus round trip"
( cd "$WS" && "$GV" chat hide "$C1" >/dev/null ) || fail "hand hide"
HAND="$(tmux display-message -p -t "$C1" '#{session_name}')"
tmux select-pane -t "$C2"
sleep 1   # let the previous toggle finish its tail re-tile
tmux send-keys -t "$DASH" L
waitfor 40 npanes_is 3 || fail "focus 2: want 3 panes, got $(npanes)"
waitfor 40 hid_count_is 1 || fail "focus 2: want one minimized chat, got '$(hid_opt)'"
echo "$(hid_opt)" | grep -qw "$HAND" && fail "the hand-hidden chat must not be in @grove_focus_hid"
sleep 1   # let the previous toggle finish its tail re-tile
tmux send-keys -t "$DASH" L
waitfor 40 npanes_is 3 || { fail "horizontal 2: want dash + 2 chats, got $(npanes)"; }
[ "$(tmux display-message -p -t "$C1" '#{session_name}')" = "$HAND" ] || fail "hand-hidden chat was restored"

say "spawn in focus minimizes the shown chat"
sleep 1   # let the previous toggle finish its tail re-tile
tmux send-keys -t "$DASH" L
waitfor 40 npanes_is 3 || { tmux list-panes -a -F "#{session_name} #{pane_id} #{@grove_rail}"; echo "hid=$(hid_opt) layout=$(tmux show-options -t =grove-alpha: -v @grove_layout)"; fail "focus 3: got $(npanes)"; }
waitfor 40 hid_count_is 1 || fail "focus 3: want one minimized chat, got '$(hid_opt)'"
BEFORE="$(panes | tr '\n' ' ')"
SHOWN_BEFORE="$(panes | sed -n 2p)"   # the dash is first, the rail last: the chat in between
spawn || fail "spawn in focus"
waitfor 40 hid_count_is 2 || fail "spawn in focus: want two minimized chats (the old one + the shown one), got '$(hid_opt)'"
waitfor 40 npanes_is 3 || fail "spawn in focus: want dash + new chat + rail, got $(npanes)"
panes | grep -qx "$SHOWN_BEFORE" && fail "the previously shown chat is still on screen"
echo "$(hid_opt)" | grep -qw "$(tmux display-message -p -t "$SHOWN_BEFORE" '#{session_name}')" || fail "the previously shown chat is not in @grove_focus_hid ($(hid_opt))"
NEW="$(panes | while read -r p; do echo "$BEFORE" | grep -qw "$p" || echo "$p"; done)"
[ -n "$NEW" ] || fail "no new pane after the spawn"
RAILN=""
for _ in $(seq 1 30); do
  RAILN="$(tmux show-options -t '=grove-alpha:' -v @grove_rail_rows 2>/dev/null | sed 1d | grep -c . || true)"
  [ "$RAILN" = 3 ] && break
  sleep 0.3
done
[ "$RAILN" = 3 ] || fail "rail must hold three boxes (hand-hidden + two minimized), got $RAILN"

say "gv chat focus: swap the shown chat (grove-488)"
SHOWN_NOW="$(panes | sed -n 2p)"
SN="$(tmux display-message -p -t "$SHOWN_NOW" '#{session_name}')"   # still the cockpit session
HIDLIST="$(hid_opt)"
T1="$(echo "$HIDLIST" | awk '{print $1}')"
T1PANE="$(tmux list-panes -s -t "=$T1" -F '#{pane_id}' | head -1)"
T1PID="$(tmux display-message -p -t "$T1PANE" '#{pane_pid}')"
SHOWN_PID="$(tmux display-message -p -t "$SHOWN_NOW" '#{pane_pid}')"
( cd "$WS" && "$GV" chat focus "$T1" ) > "$SCRATCH/focus.out" 2>&1 || { cat "$SCRATCH/focus.out"; fail "chat focus $T1"; }
waitfor 40 npanes_is 3 || fail "swap: want dash + chat + rail, got $(npanes)"
panes | grep -qx "$T1PANE" || fail "swap: $T1PANE not on screen"
panes | grep -qx "$SHOWN_NOW" && fail "swap: the previous chat is still on screen"
[ "$(tmux display-message -p -t "$T1PANE" '#{pane_pid}')" = "$T1PID" ] || fail "swap: target pid changed"
[ "$(tmux display-message -p -t "$SHOWN_NOW" '#{pane_pid}')" = "$SHOWN_PID" ] || fail "swap: minimized pid changed"
PREV="$(tmux display-message -p -t "$SHOWN_NOW" '#{session_name}')"
HID="$(hid_opt)"
echo "$HID" | grep -qw "$PREV" || fail "swap: @grove_focus_hid ($HID) missing $PREV"
echo "$HID" | grep -qw "$T1" && fail "swap: target still in @grove_focus_hid ($HID)"
LAST2="$(tail -2 "$EVENTS")"
echo "$LAST2" | sed -n 1p | grep -q '"type":"chat_hidden"' || fail "swap: want chat_hidden then chat_shown, got: $LAST2"
echo "$LAST2" | sed -n 2p | grep -q '"type":"chat_shown"' || fail "swap: want chat_hidden then chat_shown, got: $LAST2"

say "gv chat focus: the shown chat is a friendly no-op; a remote target is refused"
EV_BEFORE="$(wc -l < "$EVENTS")"
T1ID="$(tmux display-message -p -t "$T1PANE" '#{@grove_chat_session}')"
[ -n "$T1ID" ] || fail "the shown chat has no session id stamp"
( cd "$WS" && "$GV" chat focus "$T1ID" ) > "$SCRATCH/focus.out" 2>&1 || { cat "$SCRATCH/focus.out"; fail "focus on the shown chat must exit 0"; }
grep -q 'already the shown chat' "$SCRATCH/focus.out" || fail "no-op message missing: $(cat "$SCRATCH/focus.out")"
[ "$(wc -l < "$EVENTS")" = "$EV_BEFORE" ] || fail "the no-op wrote events"
if ( cd "$WS" && "$GV" chat focus @somehost/grove-chat-alpha-9 ) > "$SCRATCH/focus.out" 2>&1; then fail "remote target must be refused"; fi
grep -q 'gv chat show' "$SCRATCH/focus.out" || fail "remote refusal must name gv chat show: $(cat "$SCRATCH/focus.out")"
npanes_is 3 || fail "refusals moved panes"

say "gv chat focus in horizontal is gv chat show"
sleep 1   # let the previous toggle finish its tail re-tile
tmux send-keys -t "$DASH" L
waitfor 40 npanes_is 4 || fail "horizontal 3: want dash + 3 chats, got $(npanes)"
( cd "$WS" && "$GV" chat hide "$T1PANE" >/dev/null ) || fail "hide for horizontal check"
waitfor 40 npanes_is 3 || fail "horizontal 3: want 3 panes after hide, got $(npanes)"
( cd "$WS" && "$GV" chat focus "$T1ID" ) > "$SCRATCH/focus.out" 2>&1 || { cat "$SCRATCH/focus.out"; fail "horizontal focus"; }
grep -q 'horizontal' "$SCRATCH/focus.out" || fail "horizontal line must say so: $(cat "$SCRATCH/focus.out")"
waitfor 40 npanes_is 4 || fail "horizontal focus must show without minimizing, got $(npanes)"

say "real tmux server untouched"
[ "$(real_tmux)" = "$REAL_TMUX_BEFORE" ] || fail "real tmux session list changed"
echo "focus e2e ok"
