#!/usr/bin/env bash
# grove-487: focus mode shows ONE chat and minimizes the rest, on an ISOLATED
# tmux server. dash + 3 chats (chat 2 active): `L` -> focus leaves dash +
# chat 2 + the rail, chats 1 and 3 become grove-chat-alpha-N sessions (same
# %pane, same pid), @grove_focus_hid names both, `gv chat ls` still reports
# them kind chat / writable; `L` -> horizontal restores exactly those. A chat
# hidden by hand stays hidden. A spawn in focus minimizes the shown chat.
# grove-510: every chat keeps its cockpit·N name through the moves, the rail
# has a chip per chat, and after a burst of focus moves it matches `gv chat ls`.
# grove-525: in focus every hidden chat window has exactly the slot's size —
# after entering focus, a cockpit resize, a dash-width change + `L` round
# trip, a spawn and a hand `gv chat hide`; leaving focus unpins every one.
# grove-526: in focus a swap is ONE swap-pane — from `gv chat focus`, a rail
# click and the CHATS box's enter alike — so the swapped-in pane's screen is
# byte-identical before and after (no SIGWINCH reaches it) and no
# select-layout runs; across 6 swaps among 3 chats every chat keeps its
# session name (@grove_chat_home) and its #510 name; a window knocked off
# the slot's size falls back to break/join and is re-synced.
# grove-531: a pty client attached straight to a hidden chat (or
# switch-client'd into it) sees it at its own size; on detach the window is
# re-pinned before the next swap, which stays fast; a second cockpit client
# of another size re-sizes the slot and the hidden chats follow; focusing a
# chat someone is attached to takes the slow path and lets the client go.
# GROVE_E2E_TMUX_CONF=hostile adds base-index 1 / pane-base-index 1, a
# user-level aggressive-resize + global window-size, and a global
# pane-border-status (a lone pane a row shorter than its window).
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
set -gw aggressive-resize on
set -g window-size smallest # grove-e2e: user-conf fixture (an operator's own global value)
set -g pane-border-status top
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
# grove-525: the slot is the shown chat's pane — not the dash (first), not
# the rail (its @grove_rail adds a field); a hidden chat's PANE must match it
# (grove-526: under a global pane-border-status its window is a row taller).
slot_dims() { tmux list-panes -t "$W" -F '#{pane_id} #{@grove_rail} #{pane_width}x#{pane_height}' | sed 1d | awk 'NF == 2 {print $2}'; }
hidden_dims() { tmux list-panes -a -F '#{session_name} #{pane_width}x#{pane_height}' | awk '$1 ~ /^grove-chat-alpha-/ {print $2}' | sort -u; }
hidden_at_slot() { # every hidden chat window == the one slot
  local sl; sl="$(slot_dims)"
  [ -n "$sl" ] && [ "$(echo "$sl" | wc -l | tr -d ' ')" = 1 ] && [ -n "$(hidden_dims)" ] && [ "$(hidden_dims)" = "$sl" ]
}
size_report() { echo "slot=[$(slot_dims | tr '\n' ' ')] hidden=[$(tmux list-panes -a -F '#{session_name} #{pane_width}x#{pane_height} (window #{window_width}x#{window_height})' | grep '^grove-chat-alpha-' | tr '\n' ' ')]"; }
pinned() { # window-level window-size of any hidden chat window
  tmux list-windows -a -F '#{session_name} #{window_id}' | awk '$1 ~ /^grove-chat-alpha-/ {print $2}' | while read -r w; do
    tmux show-options -w -t "$w" window-size; done
}
# `orchestrator new` attaches when it runs outside tmux; run-shell gives it
# the server's $TMUX like a real chat spawn from inside the cockpit.
spawn() {
  tmux run-shell "cd '$WS' && '$GV' orchestrator new > '$SCRATCH/spawn.out' 2>&1" || true
  grep -q 'new orchestrator chat pane' "$SCRATCH/spawn.out" || { cat "$SCRATCH/spawn.out"; return 1; }
}

KEYS_BEFORE="$(tmux list-keys -T root 2>/dev/null || true)"   # grove-489: no root binding is ever added

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
  [ "$RAILN" = 3 ] && break
  sleep 0.3
done
[ "$RAILN" = 3 ] || fail "rail must list every chat — 1, 2 (shown) and 3 (3 rows), got $RAILN"

say "grove-525: entering focus sizes the minimized chats to the slot"
waitfor 20 hidden_at_slot || fail "enter focus: hidden chats not at the slot: $(size_report)"
[ -n "$(pinned)" ] || fail "enter focus: hidden windows not pinned per window"
tmux show-options -g window-size | grep -q manual && fail "window-size went global"
SLOT_ENTER="$(slot_dims)"

say "grove-525: a resize of the cockpit window re-sizes them (window-resized hook)"
tmux resize-window -t "$W" -x 180 -y 44
waitfor 30 sh -c "[ \"\$(tmux list-panes -t '$W' -F '#{pane_id} #{@grove_rail} #{pane_width}x#{pane_height}' | sed 1d | awk 'NF == 2 {print \$2}')\" != '$SLOT_ENTER' ]" || fail "resize: the slot did not change: $(size_report)"
waitfor 30 hidden_at_slot || fail "resize: hidden chats did not follow the slot: $(size_report)"
tmux resize-window -t "$W" -x 200 -y 50
waitfor 30 hidden_at_slot || fail "resize back: hidden chats did not follow the slot: $(size_report)"
[ "$(slot_dims)" = "$SLOT_ENTER" ] || fail "resize back: slot $(slot_dims), want $SLOT_ENTER"

say "grove-510: minimized chats keep their cockpit name; the rail names them so"
for p in "$C1" "$C2" "$C3"; do
  NAME="$(tmux display-message -p -t "$p" '#{@grove_chat_name}')"
  case "$NAME" in cockpit·*) ;; *) fail "$p name stamp is '$NAME', want cockpit·N" ;; esac
  tmux show-options -t '=grove-alpha:' -v @grove_rail_rows | awk -F'\t' -v n="$NAME" 'NR > 1 && $4 == n {f=1} END {exit !f}' \
    || fail "rail has no chip named $NAME"
done
C1NAME="$(tmux display-message -p -t "$C1" '#{@grove_chat_name}')"
tmux show-options -t '=grove-alpha:' -v @grove_rail_rows | awk -F'\t' 'NR > 1 && $1 == 1 {n++} END {exit n != 1}' \
  || fail "want exactly one shown chip"

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
[ -z "$(pinned)" ] || fail "a chat hidden in horizontal must not be pinned: $(pinned)"
tmux select-pane -t "$C2"
# grove-525: a dash-width change (what cockpit.focus_dash_width seeds) lands
# on the next re-tile — this `L` round trip — and the hidden chats follow.
tmux set-option -t '=grove-alpha:' @grove_focus_dash_width 40
sleep 1   # let the previous toggle finish its tail re-tile
tmux send-keys -t "$DASH" L
waitfor 40 npanes_is 3 || fail "focus 2: want 3 panes, got $(npanes)"
waitfor 40 hid_count_is 1 || fail "focus 2: want one minimized chat, got '$(hid_opt)'"
echo "$(hid_opt)" | grep -qw "$HAND" && fail "the hand-hidden chat must not be in @grove_focus_hid"
waitfor 20 hidden_at_slot || fail "L round trip + dash width: hidden (minimized AND hand-hidden) not at the slot: $(size_report)"
[ "$(slot_dims)" != "$SLOT_ENTER" ] || fail "dash width 40: the slot did not change ($SLOT_ENTER)"
tmux set-option -t '=grove-alpha:' -u @grove_focus_dash_width
sleep 1   # let the previous toggle finish its tail re-tile
tmux send-keys -t "$DASH" L
waitfor 40 npanes_is 3 || { fail "horizontal 2: want dash + 2 chats, got $(npanes)"; }
[ "$(tmux display-message -p -t "$C1" '#{session_name}')" = "$HAND" ] || fail "hand-hidden chat was restored"
no_rail() { ! tmux list-panes -t "$W" -F '#{@grove_rail}' | grep -q .; }
waitfor 40 no_rail || fail "horizontal 2: the rail is still up"
waitfor 40 hid_count_is 0 || fail "horizontal 2: @grove_focus_hid not cleared: '$(hid_opt)'"
[ -z "$(pinned)" ] || fail "leave focus: hidden windows still carry a window-level window-size: $(pinned)"

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
  [ "$RAILN" = 4 ] && break
  sleep 0.3
done
[ "$RAILN" = 4 ] || fail "rail must hold four chips (hand-hidden + two minimized + the new shown chat), got $RAILN"
[ "$(tmux display-message -p -t "$C1" '#{@grove_chat_name}')" = "$C1NAME" ] || fail "chat 1 lost its name $C1NAME"
waitfor 20 hidden_at_slot || fail "spawn in focus: hidden chats not at the slot: $(size_report)"

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

say "click a rail box = gv chat focus (grove-489)"
# A real mouse click cannot be driven here; the same bytes a terminal sends
# for a left-button release (SGR, column 3 line 1) are typed into the rail
# pane's pty, which is exactly what its mouse reader parses.
RAILPANE="$(tmux list-panes -t "$W" -F '#{pane_id} #{@grove_rail}' | awk '$2 != "" {print $1}')"
[ -n "$RAILPANE" ] || fail "click: no rail pane"
sleep 3   # one viewer tick: the rail has drawn the post-swap picture
# grove-510: every chat has a chip, the shown one too (clicking it is a
# no-op) — click the first chip that is off screen, at its own column.
ROWS="$(tmux show-options -t '=grove-alpha:' -v @grove_rail_rows | sed 1d)"
NCHIPS="$(echo "$ROWS" | grep -c .)"
# The later legs read the clicked chat's session-id stamp, so pick a chip
# whose pane wears one (a spawned chat; the build's first pane has none).
IDX=""; BOX1=""; i=0
while IFS=$'\t' read -r SHOWN _ _ _ SESS _; do
  if [ "$SHOWN" = 0 ] && [ -n "$SESS" ]; then
    SP="$(tmux list-panes -s -t "=$SESS" -F '#{pane_id}' | head -1)"
    if [ -n "$(tmux display-message -p -t "$SP" '#{@grove_chat_session}')" ]; then IDX=$i; BOX1="$SESS"; break; fi
  fi
  i=$((i + 1))
done <<< "$ROWS"
[ -n "$BOX1" ] || fail "click: the rail option carries no off-screen chip with a stamped session"
RW="$(tmux display-message -p -t "$RAILPANE" '#{pane_width}')"
SLOTS="$NCHIPS"; [ $((RW / SLOTS)) -ge 16 ] || SLOTS=$((RW / 16))
CW=$((RW / SLOTS - 1))
COL=$((IDX * (CW + 1) + 3))
BOX1PANE="$(tmux list-panes -s -t "=$BOX1" -F '#{pane_id}' | head -1)"
CLICKED_FROM="$(panes | sed -n 2p)"
tmux send-keys -t "$RAILPANE" -l "$(printf '\033[<0;%d;1m' "$COL")"
waitfor 40 npanes_is 3 || fail "click: want dash + chat + rail, got $(npanes)"
waitfor 20 sh -c "tmux list-panes -t '$W' -F '#{pane_id}' | grep -qx '$BOX1PANE'" || fail "click: $BOX1PANE ($BOX1) did not take the screen"
panes | grep -qx "$CLICKED_FROM" && fail "click: the previously shown chat is still on screen"
active_is_box() { [ "$(tmux display-message -p -t "$BOX1PANE" '#{pane_active}')" = 1 ]; }
waitfor 20 active_is_box || fail "click: the clicked chat does not have focus"
KEYS_AFTER="$(tmux list-keys -T root 2>/dev/null || true)"
[ "$KEYS_BEFORE" = "$KEYS_AFTER" ] || fail "click: tmux list-keys -T root changed"
T1PANE="$BOX1PANE"   # the later legs treat the shown chat as T1

say "grove-510 churn: 6 focus moves inside 60s, then the rail matches gv chat ls"
START=$(date +%s)
for i in 1 2 3 4 5 6; do
  TGT="$(hid_opt | awk '{print $1}')"
  [ -n "$TGT" ] || TGT="$(chat_sessions | head -1)"
  ( cd "$WS" && "$GV" chat focus "$TGT" ) > "$SCRATCH/churn.out" 2>&1 || { cat "$SCRATCH/churn.out"; fail "churn move $i ($TGT)"; }
done
[ $(( $(date +%s) - START )) -le 60 ] || fail "churn took over 60s"
( cd "$WS" && "$GV" chat ls --json ) > "$SCRATCH/ls.json"
tmux show-options -t '=grove-alpha:' -v @grove_rail_rows > "$SCRATCH/rail.txt"
python3 - "$SCRATCH/ls.json" "$SCRATCH/rail.txt" <<'PY' || fail "rail does not match gv chat ls right after the last move: $(cat "$SCRATCH/rail.txt")"
import json, sys
rows = json.load(open(sys.argv[1]))
rows = rows.get("chats", rows) if isinstance(rows, dict) else rows
live = [r for r in rows if r["kind"] in ("chat", "cockpit")]
chips = [l.split("\t") for l in open(sys.argv[2]).read().splitlines()[1:] if l]
assert len(chips) == len(live), (len(chips), len(live))
hidden = sorted(r["session"] for r in live if r["kind"] == "chat")
off = sorted(c[4] for c in chips if c[0] == "0")
assert hidden == off, (hidden, off)
assert sum(c[0] == "1" for c in chips) == len(live) - len(hidden)
PY
if ! panes | grep -qx "$T1PANE"; then   # the later legs want the clicked chat shown again
  ( cd "$WS" && "$GV" chat focus "$(tmux display-message -p -t "$T1PANE" '#{session_name}')" ) > "$SCRATCH/churn.out" 2>&1 || { cat "$SCRATCH/churn.out"; fail "refocus $T1PANE"; }
fi

say "grove-526: fast swaps — no repaint, no re-tile, every chat keeps its session name"
# Three chats: the shown one and two focus-minimized ones. Each is respawned
# (same %pane, its stamps kept) as a program that prints WINCH on every
# SIGWINCH, so a resize — what makes Claude repaint — shows in its capture.
SA="$(panes | sed -n 2p)"
SB="$(tmux list-panes -s -t "=$(hid_opt | awk '{print $1}')" -F '#{pane_id}' | head -1)"
SC="$(tmux list-panes -s -t "=$(hid_opt | awk '{print $2}')" -F '#{pane_id}' | head -1)"
[ -n "$SA" ] && [ -n "$SB" ] && [ -n "$SC" ] && [ "$SB" != "$SC" ] || fail "fast swaps: want a shown chat and two minimized ones (shown=$SA hid=$(hid_opt))"
for p in "$SA" "$SB" "$SC"; do
  tmux respawn-pane -k -t "$p" "sh -c 'trap \"echo WINCH\" WINCH; echo ready $p; while :; do sleep 0.1; done'"
done
waitfor 20 hidden_at_slot || fail "fast swaps: hidden chats not at the slot: $(size_report)"
LAYOUTS="$SCRATCH/layouts.log"; : > "$LAYOUTS"
tmux set-hook -g after-select-layout "run-shell 'echo L >> $LAYOUTS'"
sleep 1   # every pane has drawn; any WINCH from the respawn has printed
# Per-pane bookkeeping without bash-4 arrays: NAME_<n> / CHOME_<n> for %<n>.
kv()     { eval "printf '%s' \"\${$1_${2#%}:-}\""; }
kv_set() { eval "$1_${2#%}=\$3"; }
for p in "$SA" "$SB" "$SC"; do kv_set NAME "$p" "$(tmux display-message -p -t "$p" '#{@grove_chat_name}')"; done
kv_set CHOME "$SB" "$(tmux display-message -p -t "$SB" '#{session_name}')"
kv_set CHOME "$SC" "$(tmux display-message -p -t "$SC" '#{session_name}')"
check_names() { # <step>
  local p s h
  for p in "$SA" "$SB" "$SC"; do
    [ "$(tmux display-message -p -t "$p" '#{@grove_chat_name}')" = "$(kv NAME "$p")" ] || fail "swap $1: $p lost its name $(kv NAME "$p")"
    s="$(tmux display-message -p -t "$p" '#{session_name}')"
    [ "$s" = grove-alpha ] && continue
    h="$(tmux display-message -p -t "$p" '#{@grove_chat_home}')"
    [ "$s" = "$h" ] || fail "swap $1: $p hidden in $s, its @grove_chat_home is '$h'"
    [ -z "$(kv CHOME "$p")" ] || [ "$(kv CHOME "$p")" = "$s" ] || fail "swap $1: $p moved from session $(kv CHOME "$p") to $s"
    kv_set CHOME "$p" "$s"
    echo " $(hid_opt) " | grep -q " $s " || fail "swap $1: $s ($p) is minimized but not in @grove_focus_hid ($(hid_opt))"
  done
  for s in $(hid_opt); do
    tmux has-session -t "=$s" 2>/dev/null || fail "swap $1: @grove_focus_hid names $s, which does not exist ($(hid_opt))"
  done
  [ "$(tmux display-message -p -t "$(panes | sed -n 2p)" '#{session_name}')" = grove-alpha ] || fail "swap $1: no chat shown"
}
rail_click() { # <session>: the chip's column, as the click leg computes it
  local rows n i idx rw slots cw shown sess rp
  rp="$(tmux list-panes -t "$W" -F '#{pane_id} #{@grove_rail}' | awk '$2 != "" {print $1}')"
  rows="$(tmux show-options -t '=grove-alpha:' -v @grove_rail_rows | sed 1d)"
  n="$(echo "$rows" | grep -c .)"; i=0; idx=""
  while IFS=$'\t' read -r shown _ _ _ sess _; do
    [ "$sess" = "$1" ] && idx=$i
    i=$((i + 1))
  done <<< "$rows"
  [ -n "$idx" ] || return 1
  rw="$(tmux display-message -p -t "$rp" '#{pane_width}')"
  slots="$n"; [ $((rw / slots)) -ge 16 ] || slots=$((rw / 16))
  cw=$((rw / slots - 1))
  tmux send-keys -t "$rp" -l "$(printf '\033[<0;%d;1m' $((idx * (cw + 1) + 3)))"
}
dash_has() { tmux capture-pane -p -t "$DASH" | grep -q -- "$1"; }
chats_enter() { # <chat name>: tab to CHATS, walk the cursor onto the row, enter
  local k
  tmux select-pane -t "$DASH"
  waitfor 10 dash_has '▸[▣○]' || { tmux send-keys -t "$DASH" Tab; waitfor 20 dash_has '▸[▣○]' || return 1; }
  for k in j j j j j j k k k k k k k k k k k k; do
    dash_has "▸. $1" && { tmux send-keys -t "$DASH" Enter; return 0; }
    tmux send-keys -t "$DASH" "$k"; sleep 0.3
  done
  return 1
}
in_slot() { [ "$(panes | sed -n 2p)" = "$1" ]; }
SHOWN="$SA"
STEP=0
for TGT in "$SB" "$SC" "$SA" "$SC" "$SB" "$SA"; do
  STEP=$((STEP + 1))
  TS="$(tmux display-message -p -t "$TGT" '#{session_name}')"
  BEFORE_CAP="$(tmux capture-pane -p -t "$TGT")"
  NL="$(wc -l < "$LAYOUTS" | tr -d ' ')"
  case $((STEP % 3)) in
    1) ( cd "$WS" && "$GV" chat focus "$TS" ) > "$SCRATCH/fast.out" 2>&1 || { cat "$SCRATCH/fast.out"; fail "swap $STEP: gv chat focus $TS"; }; HOW="gv chat focus" ;;
    2) rail_click "$TS" || fail "swap $STEP: no rail chip for $TS"; HOW="rail click" ;;
    0) chats_enter "$(kv NAME "$TGT")" || fail "swap $STEP: could not enter $(kv NAME "$TGT") on the CHATS box: $(tmux capture-pane -p -t "$DASH")"; HOW="CHATS enter" ;;
  esac
  waitfor 40 in_slot "$TGT" || fail "swap $STEP ($HOW): $TGT ($TS) did not take the slot"
  npanes_is 3 || fail "swap $STEP ($HOW): want dash + chat + rail, got $(npanes)"
  sleep 0.6   # a SIGWINCH would have printed by now
  [ "$(tmux capture-pane -p -t "$TGT")" = "$BEFORE_CAP" ] || fail "swap $STEP ($HOW): $TGT repainted:
$BEFORE_CAP
-->
$(tmux capture-pane -p -t "$TGT")"
  [ "$(wc -l < "$LAYOUTS" | tr -d ' ')" = "$NL" ] || fail "swap $STEP ($HOW): a select-layout ran — the slow path"
  echo "$(tail -2 "$EVENTS")" | sed -n 1p | grep -q '"type":"chat_hidden".*"reason":"focus"' || fail "swap $STEP: want chat_hidden (reason focus) then chat_shown, got: $(tail -2 "$EVENTS")"
  echo "$(tail -2 "$EVENTS")" | sed -n 2p | grep -q "\"type\":\"chat_shown\".*\"session\":\"$TS\"" || fail "swap $STEP: want chat_shown of $TS, got: $(tail -2 "$EVENTS")"
  check_names "$STEP"
  echo "  swap $STEP ($HOW): $SHOWN -> $TGT fast, no repaint"
  SHOWN="$TGT"
done
for p in "$SA" "$SB" "$SC"; do  # gv chat ls finds every one of them by its session name
  s="$(tmux display-message -p -t "$p" '#{session_name}')"
  [ "$s" = grove-alpha ] && continue
  ( cd "$WS" && "$GV" chat ls --json ) | python3 -c 'import json,sys; rows=json.load(sys.stdin); rows=rows.get("chats", rows) if isinstance(rows, dict) else rows; sys.exit(0 if any(r["session"] == sys.argv[1] and r["kind"] == "chat" for r in rows) else 1)' "$s" \
    || fail "gv chat ls has no chat row for $s after the swaps"
done

say "grove-526: a hidden window off the slot's size falls back to break/join, then is re-synced"
FB="$(tmux list-panes -s -t "=$(kv CHOME "$SB")" -F '#{pane_id}' | head -1)"
[ "$FB" = "$SB" ] || fail "fallback: $SB is not in $(kv CHOME "$SB")"
tmux resize-window -t "$(tmux display-message -p -t "$SB" '#{window_id}')" -x 80 -y 24
NL="$(wc -l < "$LAYOUTS" | tr -d ' ')"
( cd "$WS" && "$GV" chat focus "$(kv CHOME "$SB")" ) > "$SCRATCH/fast.out" 2>&1 || { cat "$SCRATCH/fast.out"; fail "fallback: gv chat focus $(kv CHOME "$SB")"; }
waitfor 40 in_slot "$SB" || fail "fallback: $SB did not take the slot"
npanes_is 3 || fail "fallback: want dash + chat + rail, got $(npanes)"
[ "$(wc -l < "$LAYOUTS" | tr -d ' ')" -gt "$NL" ] || fail "fallback: no select-layout — the fast path ran on a mis-sized window"
waitfor 20 hidden_at_slot || fail "fallback: hidden chats not re-synced to the slot: $(size_report)"
SHOWN="$SB"
check_names fallback
TS="$(tmux display-message -p -t "$SC" '#{session_name}')"
NL="$(wc -l < "$LAYOUTS" | tr -d ' ')"
( cd "$WS" && "$GV" chat focus "$TS" ) > "$SCRATCH/fast.out" 2>&1 || { cat "$SCRATCH/fast.out"; fail "after fallback: gv chat focus $TS"; }
waitfor 40 in_slot "$SC" || fail "after fallback: $SC did not take the slot"
[ "$(wc -l < "$LAYOUTS" | tr -d ' ')" = "$NL" ] || fail "after fallback: the next swap was not fast"
check_names after-fallback
tmux set-hook -gu after-select-layout
if ! panes | grep -qx "$T1PANE"; then   # the later legs want T1 shown
  ( cd "$WS" && "$GV" chat focus "$(tmux display-message -p -t "$T1PANE" '#{session_name}')" ) > "$SCRATCH/churn.out" 2>&1 || { cat "$SCRATCH/churn.out"; fail "refocus $T1PANE"; }
fi

say "grove-531: a direct attach follows the client; detach re-pins; a second cockpit client re-sizes the slot"
# A pty client (script(1): BSD and util-linux spell it differently) at a
# size that is not the slot's. Its tmux talks to this isolated server
# through the exported TMUX_TMPDIR; TMUX is unset above.
PTY_PIDS=""
pty_attach() { # <cols> <rows> <session>
  local cmd="stty cols $1 rows $2; exec tmux attach -t '=$3'"
  if script --version 2>/dev/null | grep -q util-linux; then
    script -qfc "$cmd" /dev/null > /dev/null 2>&1 &
  else
    script -q /dev/null sh -c "$cmd" > /dev/null 2>&1 &
  fi
  PTY_PIDS="$PTY_PIDS $!"
}
client_of() { tmux list-clients -F '#{client_name} #{client_session}' | awk -v s="$1" '$2 == s {print $1; exit}'; }
has_client() { [ -n "$(client_of "$1")" ]; }
win_of() { tmux display-message -p -t "$1" '#{window_id}'; }
win_is() { [ "$(tmux display-message -p -t "$1" '#{window_width}x#{window_height}')" = "$2" ]; }
unpinned() { [ -z "$(tmux show-options -w -t "$(win_of "$1")" window-size)" ]; }
hidden_at_slot_but() { # <session>: hidden_at_slot, minus one attached chat
  local sl; sl="$(slot_dims)"
  [ -n "$sl" ] && [ "$(tmux list-panes -a -F '#{session_name} #{pane_width}x#{pane_height}' | awk -v s="$1" '$1 ~ /^grove-chat-alpha-/ && $1 != s {print $2}' | sort -u)" = "$sl" ]
}
fast_focus() { # <pane>: gv chat focus its session; assert the fast path (no select-layout, no repaint)
  local ts nl cap
  ts="$(tmux display-message -p -t "$1" '#{session_name}')"
  cap="$(tmux capture-pane -p -t "$1")"
  nl="$(wc -l < "$LAYOUTS" | tr -d ' ')"
  ( cd "$WS" && "$GV" chat focus "$ts" ) > "$SCRATCH/fast.out" 2>&1 || { cat "$SCRATCH/fast.out"; fail "attach leg: gv chat focus $ts"; }
  waitfor 40 in_slot "$1" || fail "attach leg: $1 ($ts) did not take the slot"
  sleep 0.6
  [ "$(wc -l < "$LAYOUTS" | tr -d ' ')" = "$nl" ] || fail "attach leg: focusing $ts ran a select-layout — the slow path"
  [ "$(tmux capture-pane -p -t "$1")" = "$cap" ] || fail "attach leg: $1 repainted on its swap"
}
tmux set-hook -g after-select-layout "run-shell 'echo L >> $LAYOUTS'"
waitfor 20 hidden_at_slot || fail "attach leg: hidden chats not at the slot: $(size_report)"
AH="$(hid_opt | awk '{print $1}')"
AP="$(tmux list-panes -s -t "=$AH" -F '#{pane_id}' | head -1)"
SLOT0="$(slot_dims)"
tmux show-hooks -t "$AP" | grep -q 'client-attached.*window-size' || fail "attach leg: $AH has no client-attached hook: $(tmux show-hooks -t "$AP")"
tmux show-hooks -t "$AP" | grep -q 'client-detached.*sync-sizes' || fail "attach leg: $AH has no client-detached hook: $(tmux show-hooks -t "$AP")"
tmux show-hooks -g | grep -Eq '^client-(attached|detached|session-changed)(\[[0-9]+\])? ' && fail "attach leg: a client hook went global: $(tmux show-hooks -g)"
# 1. Direct attach at 100x30: the window follows the client, not the slot.
pty_attach 100 30 "$AH"
waitfor 30 has_client "$AH" || fail "attach leg: the pty client never attached to $AH"
waitfor 20 unpinned "$AP" || fail "attach leg: $AH still pinned while attached: $(tmux show-options -w -t "$(win_of "$AP")" window-size)"
waitfor 20 win_is "$AP" 100x29 || fail "attach leg: attached $AH is $(tmux display-message -p -t "$AP" '#{window_width}x#{window_height}'), want the client's 100x29"
# A sync while attached (a cockpit resize, a re-tile) leaves it with its client.
( cd "$WS" && "$GV" chat sync-sizes --session grove-alpha ) || fail "attach leg: sync-sizes"
win_is "$AP" 100x29 || fail "attach leg: a sync re-pinned the attached $AH: $(tmux display-message -p -t "$AP" '#{window_width}x#{window_height}')"
echo "  attached at 100x30: $AH follows the client"
# 2. Detach: the client-detached hook re-pins it, before any swap.
tmux detach-client -s "=$AH"
waitfor 30 hidden_at_slot || fail "attach leg: $AH not back at the slot after detach: $(size_report)"
[ "$(tmux show-options -wv -t "$(win_of "$AP")" window-size)" = manual ] || fail "attach leg: $AH not pinned after detach"
fast_focus "$AP"
echo "  detached: $AH back at the slot $SLOT0; the next swap is fast"
# 3. A second client of another size on the cockpit session (desk +
# laptop): the slot is whatever tmux gave the cockpit window; hidden chats
# follow it and swaps stay fast. Then the same client switch-clients into a
# hidden chat (gv's attach from inside tmux) and back out.
# The grove-525 leg's resize-window left the cockpit window manual; give it
# back to its clients first, as a real cockpit is.
tmux set-option -wu -t "$W" window-size
ncockpit() { tmux list-clients -F '#{client_session}' | grep -cx grove-alpha || true; }
ncockpit_is() { [ "$(ncockpit)" = "$1" ]; }
pty_attach 160 45 grove-alpha
waitfor 30 ncockpit_is 1 || fail "attach leg: the 160x45 client never attached to the cockpit"
pty_attach 120 40 grove-alpha
waitfor 30 ncockpit_is 2 || fail "attach leg: the 120x40 client never attached to the cockpit"
waitfor 30 hidden_at_slot || fail "attach leg: hidden chats did not follow the second client's slot: $(size_report)"
SLOT1="$(slot_dims)"
[ "$SLOT1" != "$SLOT0" ] || fail "attach leg: the 120x40 client did not re-size the cockpit's slot ($SLOT0)"
echo "  two cockpit clients (160x45, 120x40): cockpit $(tmux display-message -p -t "$W" '#{window_width}x#{window_height}'), slot $SLOT0 -> $SLOT1, hidden chats follow"
BH="$(hid_opt | awk '{print $1}')"
BP="$(tmux list-panes -s -t "=$BH" -F '#{pane_id}' | head -1)"
fast_focus "$BP"
CH="$(hid_opt | awk '{print $1}')"
CP="$(tmux list-panes -s -t "=$CH" -F '#{pane_id}' | head -1)"
SW="$(tmux list-clients -F '#{client_name} #{client_session} #{client_width}x#{client_height}' | awk '$2 == "grove-alpha" && $3 == "120x40" {print $1}')"
[ -n "$SW" ] || fail "attach leg: no 120x40 cockpit client: $(tmux list-clients -F '#{client_name} #{client_session} #{client_width}x#{client_height}')"
tmux switch-client -c "$SW" -t "=$CH"
waitfor 30 has_client "$CH" || fail "attach leg: switch-client into $CH did not land"
waitfor 20 unpinned "$CP" || fail "attach leg: $CH still pinned after a switch-client into it"
waitfor 30 hidden_at_slot_but "$CH" || fail "attach leg: with one cockpit client left, the other hidden chats did not follow its slot: $(size_report)"
waitfor 20 win_is "$CP" 120x39 || fail "attach leg: switched-into $CH is $(tmux display-message -p -t "$CP" '#{window_width}x#{window_height}'), want the client's 120x39"
echo "  switch-client into $CH: it follows that client (120x39); one cockpit client left: cockpit $(tmux display-message -p -t "$W" '#{window_width}x#{window_height}'), slot $(slot_dims), the other hidden chats follow"
# 4. The cockpit wins a show: focusing a chat someone is attached to takes
# the slow path (never a swap that would leave the client watching another
# chat); the join ends that session and its client.
NL="$(wc -l < "$LAYOUTS" | tr -d ' ')"
( cd "$WS" && "$GV" chat focus "$CH" ) > "$SCRATCH/fast.out" 2>&1 || { cat "$SCRATCH/fast.out"; fail "attach leg: gv chat focus attached $CH"; }
waitfor 40 in_slot "$CP" || fail "attach leg: attached $CP did not take the slot"
[ "$(wc -l < "$LAYOUTS" | tr -d ' ')" -gt "$NL" ] || fail "attach leg: an attached target took the fast swap"
has_client "$CH" && fail "attach leg: $CH still has a client after its chat was shown: $(tmux list-clients -F '#{client_name} #{client_session}')"
waitfor 30 hidden_at_slot || fail "attach leg: hidden chats not at the slot after the attached show: $(size_report)"
echo "  focusing an attached chat: slow path, the attach client is let go"
for pid in $PTY_PIDS; do kill "$pid" 2>/dev/null || true; done
tmux list-clients -F '#{client_name}' | while read -r c; do tmux detach-client -t "$c"; done
tmux set-hook -gu after-select-layout
if ! panes | grep -qx "$T1PANE"; then   # the later legs want T1 shown
  ( cd "$WS" && "$GV" chat focus "$(tmux display-message -p -t "$T1PANE" '#{session_name}')" ) > "$SCRATCH/churn.out" 2>&1 || { cat "$SCRATCH/churn.out"; fail "refocus $T1PANE"; }
fi

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

say "grove-525: a hand gv chat hide in focus is born at the slot's size"
SLOT_NOW="$(slot_dims)"
[ -n "$SLOT_NOW" ] || fail "hand hide in focus: no slot to start from"
( cd "$WS" && "$GV" chat hide "$T1PANE" >/dev/null ) || fail "hand hide in focus"
waitfor 40 npanes_is 2 || fail "hand hide in focus: want dash + rail, got $(npanes)"
T1HOME="$(tmux display-message -p -t "$T1PANE" '#{session_name}')"
[ "$(tmux display-message -p -t "$T1PANE" '#{pane_width}x#{pane_height}')" = "$SLOT_NOW" ] \
  || fail "hand hide in focus: $T1HOME is $(tmux display-message -p -t "$T1PANE" '#{pane_width}x#{pane_height}'), want the slot $SLOT_NOW"
[ "$(hidden_dims)" = "$SLOT_NOW" ] || fail "hand hide in focus: every hidden chat must stay at $SLOT_NOW: $(size_report)"
( cd "$WS" && "$GV" chat focus "$T1HOME" ) > "$SCRATCH/focus.out" 2>&1 || { cat "$SCRATCH/focus.out"; fail "focus back $T1HOME"; }
waitfor 40 npanes_is 3 || fail "focus back: want dash + chat + rail, got $(npanes)"
panes | grep -qx "$T1PANE" || fail "focus back: $T1PANE not on screen"
waitfor 20 hidden_at_slot || fail "focus back: hidden chats not at the slot: $(size_report)"

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
