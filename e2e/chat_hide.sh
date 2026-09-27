#!/usr/bin/env bash
# grove-401 e2e: `gv chat hide` / `gv chat show` against scratch everything
# (dummy-data pattern, docs/seed-manifest.md): scratch HOME, a registered
# scratch workspace, an isolated tmux server, a fake agent. The repo's real
# state and the machine's real tmux server are never touched.
#
# tmux: isolated — unsets TMUX/TMUX_PANE, scratch TMUX_TMPDIR, scoped
# kill-server, real-server canary before/after (tmux-discipline).
#
# Proves, on a fake cockpit (dashboard + two chat panes):
#   - the train's invariant: until something is hidden, both chats are
#     `kind: cockpit` and no chat session exists;
#   - hide refuses with nothing touched: no argument outside tmux, a bare
#     cockpit name holding two chats, the dashboard pane;
#   - hide → the pane IS grove-chat-<label>-1: one window named `chat`, no
#     placeholder, same %pane, same pid, stamp intact, event logged;
#   - `gv chat ls --json` reports it kind chat / writable, with exactly the
#     row fields it had before (no contract change);
#   - `gv chat send` reaches the hidden chat;
#   - "this pane" ($TMUX_PANE) hides the caller;
#   - show → back in the cockpit window, the session gone, kind cockpit;
#   - `gv chat close` ends a hidden chat; a plain park leaves one running;
#     show refuses while the cockpit is down; `gv park --chats` reaps it.
# GROVE_E2E_TMUX_CONF=hostile runs it all under base-index 1 +
# pane-base-index 1 (grove-168).
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(cd "$(mktemp -d /tmp/grove-hide.XXXXXX)" && pwd -P)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

real_tmux() { env -u TMUX -u TMUX_PANE -u TMUX_TMPDIR tmux list-sessions -F '#{session_name}' 2>/dev/null | sort; true; }
REAL_TMUX_BEFORE="$(real_tmux)"

export HOME="$SCRATCH/home"
export HISTFILE=/dev/null
export LESSHISTFILE=-
# $TMUX beats TMUX_TMPDIR — unset or every tmux call hits the REAL server.
unset TMUX TMUX_PANE
unset GROVE_STATE_DIR || true
export TMUX_TMPDIR="$SCRATCH/tmux"
export GV_CLAUDE_CONFIG_DIR="$SCRATCH/claude"
mkdir -p "$HOME" "$TMUX_TMPDIR" "$SCRATCH/bin"

cleanup() {
  env -u TMUX TMUX_TMPDIR="$TMUX_TMPDIR" tmux kill-server 2>/dev/null || true   # isolated server only
  # kill-server returns before the panes are gone (grove-383).
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
  mkdir -p "$1"
  git -C "$1" init -qb main
  git -C "$1" config user.email e2e@grove.test && git -C "$1" config user.name "grove e2e"
  ( cd "$1" && echo x > README.md && git add -A && git commit -qm init )
}

say "workspace: hidews (registered)"
WS="$SCRATCH/hidews"
mkrepo "$WS"
( cd "$WS" && "$GV" init --yes --label hidews > /dev/null )
"$GV" workspaces > "$SCRATCH/workspaces.out" 2>&1
grep -q hidews "$SCRATCH/workspaces.out" || fail "hidews not registered"
ORCH="$WS/.grove/orchestrator"
EVENTS="$WS/.grove/state/events.jsonl"
mkdir -p "$ORCH"

# The fake agent: long-lived, carries the session id in its argv (what
# `gv chat ls` reads as ground truth, grove-222) and appends every line it
# is sent to a log named after that id.
cat > "$SCRATCH/bin/agent" <<EOF
#!/usr/bin/env bash
id=unknown
while [ \$# -gt 0 ]; do
  case "\$1" in
    --session-id) id="\$2"; shift 2 ;;
    *) shift ;;
  esac
done
while IFS= read -r line; do
  printf '%s\n' "\$line" >> "$SCRATCH/agent-\$id.log"
done
EOF
chmod +x "$SCRATCH/bin/agent"

gv() { ( cd "$WS" && "$GV" "$@" ); }
# row_field <file> <anchor field> <anchor value> <field>
row_field() { grep -B 14 -A 14 "\"$2\": \"$3\"" "$1" | grep -m1 "\"$4\":" | sed 's/.*: //; s/[",]//g'; }
pane_of()   { tmux display-message -p -t "$1" -F "$2"; }
cockpit_panes() { tmux list-panes -t '=grove-hidews:cockpit' -F '#{pane_id}' | tr '\n' ' '; }
chat_sessions() { tmux list-sessions -F '#{session_name}' 2>/dev/null | grep -c '^grove-chat-hidews-' || true; }
# Everything a refusal must leave untouched.
server_state() { tmux list-panes -a -F '#{session_name} #{window_name} #{pane_id} #{pane_pid}' | sort; }

build_cockpit() {
  tmux new-session -d -s grove-hidews -n cockpit -x 200 -y 50 -c "$WS" 'sleep 600'
  tmux set-window-option -t '=grove-hidews:cockpit' automatic-rename off
  DASH="$(tmux list-panes -t '=grove-hidews:cockpit' -F '#{pane_id}')"
}

say "fake cockpit: dashboard + two chat panes, a worker window in front"
build_cockpit
ID_A=aaaa1111-hide-e2e
ID_B=bbbb2222-hide-e2e
CHAT_A="$(tmux split-window -h -t '=grove-hidews:cockpit' -c "$ORCH" -P -F '#{pane_id}' "$SCRATCH/bin/agent --session-id $ID_A")"
CHAT_B="$(tmux split-window -h -t '=grove-hidews:cockpit' -c "$ORCH" -P -F '#{pane_id}' "$SCRATCH/bin/agent --session-id $ID_B")"
tmux select-layout -t '=grove-hidews:cockpit' even-horizontal
tmux set-option -p -t "$CHAT_A" @grove_chat_session "$ID_A"
tmux set-option -p -t "$CHAT_B" @grove_chat_session "$ID_B"
tmux set-option -p -t "$CHAT_A" @grove_model opus
# The operator is looking at a worker: the re-tile must not land here.
tmux new-window -t '=grove-hidews' -n 'repo · grove-1' -c "$WS" 'sleep 600'
tmux split-window -h -t '=grove-hidews:' 'sleep 600'
WORKER_LAYOUT="$(tmux display-message -p -t '=grove-hidews:' -F '#{window_layout}')"
PID_A="$(pane_of "$CHAT_A" '#{pane_pid}')"
PID_B="$(pane_of "$CHAT_B" '#{pane_pid}')"

say "invariant: nothing hides by itself — both chats are cockpit panes"
gv chat ls --json > "$SCRATCH/ls0.json" 2>&1 || { cat "$SCRATCH/ls0.json"; fail "gv chat ls failed"; }
[ "$(row_field "$SCRATCH/ls0.json" session_id "$ID_A" kind)" = "cockpit" ] || { cat "$SCRATCH/ls0.json"; fail "chat A must be kind cockpit before any hide"; }
[ "$(row_field "$SCRATCH/ls0.json" session_id "$ID_B" kind)" = "cockpit" ] || fail "chat B must be kind cockpit before any hide"
[ "$(chat_sessions)" -eq 0 ] || fail "no chat session may exist before a hide"
grep -o '"[a-z_]*":' "$SCRATCH/ls0.json" | sort -u > "$SCRATCH/keys0.txt"

say "hide with no argument and no \$TMUX_PANE: usage error, nothing touched"
BEFORE="$(server_state)"
rc=0; gv chat hide > "$SCRATCH/hide-noarg.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "hide with nothing to aim at must exit non-zero"
grep -q 'usage: gv chat hide' "$SCRATCH/hide-noarg.out" || { cat "$SCRATCH/hide-noarg.out"; fail "want the usage line"; }
[ "$(server_state)" = "$BEFORE" ] || fail "a usage error touched the server"

say "a bare cockpit name holding two chats is an error listing both"
rc=0; gv chat hide grove-hidews > "$SCRATCH/hide-amb.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "an ambiguous cockpit name must exit non-zero"
grep -q 'holds 2 chat panes' "$SCRATCH/hide-amb.out" || { cat "$SCRATCH/hide-amb.out"; fail "want the candidates listed"; }
grep -q "$ID_A" "$SCRATCH/hide-amb.out" || fail "candidate A missing"
grep -q "$ID_B" "$SCRATCH/hide-amb.out" || fail "candidate B missing"
[ "$(server_state)" = "$BEFORE" ] || fail "an ambiguous hide touched the server"

say "the dashboard and a worker pane are refused"
rc=0; gv chat hide "$DASH" > "$SCRATCH/hide-dash.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "hiding the dashboard must exit non-zero"
grep -q 'the dashboard' "$SCRATCH/hide-dash.out" || { cat "$SCRATCH/hide-dash.out"; fail "want the dashboard refusal"; }
WORKER_PANE="$(tmux display-message -p -t '=grove-hidews:' -F '#{pane_id}')"
rc=0; gv chat hide "$WORKER_PANE" > "$SCRATCH/hide-worker.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "hiding a worker pane must exit non-zero"
grep -q 'worker windows are never hidden' "$SCRATCH/hide-worker.out" || { cat "$SCRATCH/hide-worker.out"; fail "want the worker refusal"; }
[ "$(server_state)" = "$BEFORE" ] || fail "a refused hide touched the server"
[ ! -e "$EVENTS" ] || ! grep -q '"type":"chat_hidden"' "$EVENTS" || fail "a refused hide logged an event"

say "hide chat A by session-id prefix"
gv chat hide aaaa1111 > "$SCRATCH/hide-a.out" 2>&1 || { cat "$SCRATCH/hide-a.out"; fail "hide must succeed"; }
cat "$SCRATCH/hide-a.out"
grep -q '✓ hidden as grove-chat-hidews-1' "$SCRATCH/hide-a.out" || fail "hide must print one ✓ line naming the session"
tmux has-session -t '=grove-chat-hidews-1' 2>/dev/null || fail "grove-chat-hidews-1 does not exist"
[ "$(tmux list-windows -t '=grove-chat-hidews-1' -F '#{window_name}')" = "chat" ] \
  || { tmux list-windows -t '=grove-chat-hidews-1'; fail "want exactly one window, named chat — no placeholder"; }
[ "$(tmux list-panes -t '=grove-chat-hidews-1:chat' -F '#{pane_id}')" = "$CHAT_A" ] || fail "the pane id changed across hide"
[ "$(pane_of "$CHAT_A" '#{pane_pid}')" = "$PID_A" ] || fail "the pid changed across hide"
kill -0 "$PID_A" 2>/dev/null || fail "the hidden chat's process died"
[ "$(pane_of "$CHAT_A" '#{@grove_chat_session}')" = "$ID_A" ] || fail "the @grove_chat_session stamp did not travel"
[ "$(pane_of "$CHAT_A" '#{@grove_model}')" = "opus" ] || fail "the @grove_model stamp did not travel"
[ "$(cockpit_panes)" = "$DASH $CHAT_B " ] || fail "cockpit panes after hide = $(cockpit_panes), want $DASH $CHAT_B"
[ "$(tmux display-message -p -t '=grove-hidews:' -F '#{window_layout}')" = "$WORKER_LAYOUT" ] || fail "the re-tile landed on the worker window"
grep '"type":"chat_hidden"' "$EVENTS" > "$SCRATCH/ev-hidden.txt" || fail "no chat_hidden event"
grep -q '"session":"grove-chat-hidews-1"' "$SCRATCH/ev-hidden.txt" || fail "chat_hidden lacks the session"
grep -q "\"pane\":\"$CHAT_A\"" "$SCRATCH/ev-hidden.txt" || fail "chat_hidden lacks the pane"
grep -q '"workspace":"hidews"' "$SCRATCH/ev-hidden.txt" || fail "chat_hidden lacks the workspace"
grep -q "\"session_id\":\"$ID_A\"" "$SCRATCH/ev-hidden.txt" || fail "chat_hidden lacks the session id"

say "gv chat ls --json: the hidden chat is kind chat, writable — same fields"
gv chat ls --json > "$SCRATCH/ls1.json" 2>&1
[ "$(row_field "$SCRATCH/ls1.json" session_id "$ID_A" kind)" = "chat" ] || { cat "$SCRATCH/ls1.json"; fail "a hidden chat must be kind chat"; }
[ "$(row_field "$SCRATCH/ls1.json" session_id "$ID_A" writable)" = "true" ] || fail "a hidden chat must be writable"
[ "$(row_field "$SCRATCH/ls1.json" session_id "$ID_A" session)" = "grove-chat-hidews-1" ] || fail "a hidden chat's session is its chat session"
[ "$(row_field "$SCRATCH/ls1.json" session_id "$ID_B" kind)" = "cockpit" ] || fail "the chat nobody hid must still be kind cockpit"
grep -o '"[a-z_]*":' "$SCRATCH/ls1.json" | sort -u > "$SCRATCH/keys1.txt"
cmp -s "$SCRATCH/keys0.txt" "$SCRATCH/keys1.txt" || { diff "$SCRATCH/keys0.txt" "$SCRATCH/keys1.txt" || true; fail "hide changed the --json field set"; }

say "hiding it again is a friendly refusal"
rc=0; gv chat hide aaaa1111 > "$SCRATCH/hide-again.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "hiding a hidden chat must exit non-zero"
grep -q 'already hidden' "$SCRATCH/hide-again.out" || { cat "$SCRATCH/hide-again.out"; fail "want 'already hidden'"; }
grep -q 'gv chat show grove-chat-hidews-1' "$SCRATCH/hide-again.out" || fail "the refusal must name the show verb"
[ "$(chat_sessions)" -eq 1 ] || fail "a refused hide made a session"

say "gv chat send reaches the hidden chat"
gv chat send grove-chat-hidews-1 "still there" > "$SCRATCH/send.out" 2>&1 || { cat "$SCRATCH/send.out"; fail "send to a hidden chat must succeed"; }
grep -q '✓ sent to grove-chat-hidews-1' "$SCRATCH/send.out" || fail "send must confirm the chat it reached"
for _ in $(seq 1 30); do grep -q 'still there' "$SCRATCH/agent-$ID_A.log" 2>/dev/null && break; sleep 0.1; done
grep -q 'still there' "$SCRATCH/agent-$ID_A.log" || fail "the message never reached the hidden agent"

say "no argument inside a pane hides THAT pane (\$TMUX_PANE)"
( cd "$WS" && TMUX_PANE="$CHAT_B" "$GV" chat hide ) > "$SCRATCH/hide-b.out" 2>&1 || { cat "$SCRATCH/hide-b.out"; fail "hide of the calling pane must succeed"; }
grep -q '✓ hidden as grove-chat-hidews-2' "$SCRATCH/hide-b.out" || { cat "$SCRATCH/hide-b.out"; fail "the second hidden chat is number 2"; }
[ "$(tmux list-panes -t '=grove-chat-hidews-2:chat' -F '#{pane_id}')" = "$CHAT_B" ] || fail "chat B is not in its session"
[ "$(cockpit_panes)" = "$DASH " ] || fail "all hidden: the cockpit must hold the dashboard alone, got $(cockpit_panes)"

say "show chat A by its session name"
gv chat show grove-chat-hidews-1 > "$SCRATCH/show-a.out" 2>&1 || { cat "$SCRATCH/show-a.out"; fail "show must succeed"; }
cat "$SCRATCH/show-a.out"
grep -q '✓ shown grove-chat-hidews-1' "$SCRATCH/show-a.out" || fail "show must print one ✓ line naming the session"
[ "$(cockpit_panes)" = "$DASH $CHAT_A " ] || fail "cockpit panes after show = $(cockpit_panes), want $DASH $CHAT_A"
if tmux has-session -t '=grove-chat-hidews-1' 2>/dev/null; then fail "the emptied chat session must disappear"; fi
[ "$(pane_of "$CHAT_A" '#{pane_pid}')" = "$PID_A" ] || fail "the pid changed across show"
[ "$(pane_of "$CHAT_A" '#{@grove_chat_session}')" = "$ID_A" ] || fail "the stamp did not survive show"
[ "$(pane_of "$CHAT_A" '#{window_name}')" = "cockpit" ] || fail "show joined the wrong window"
grep '"type":"chat_shown"' "$EVENTS" > "$SCRATCH/ev-shown.txt" || fail "no chat_shown event"
grep -q '"session":"grove-chat-hidews-1"' "$SCRATCH/ev-shown.txt" || fail "chat_shown lacks the session"
grep -q "\"pane\":\"$CHAT_A\"" "$SCRATCH/ev-shown.txt" || fail "chat_shown lacks the pane"
gv chat ls --json > "$SCRATCH/ls2.json" 2>&1
[ "$(row_field "$SCRATCH/ls2.json" session_id "$ID_A" kind)" = "cockpit" ] || { cat "$SCRATCH/ls2.json"; fail "a shown chat must be kind cockpit"; }
[ "$(row_field "$SCRATCH/ls2.json" session_id "$ID_B" kind)" = "chat" ] || fail "chat B is still hidden: kind chat"
grep -o '"[a-z_]*":' "$SCRATCH/ls2.json" | sort -u > "$SCRATCH/keys2.txt"
cmp -s "$SCRATCH/keys0.txt" "$SCRATCH/keys2.txt" || fail "show changed the --json field set"

say "showing a shown chat is a friendly refusal"
rc=0; gv chat show "$ID_A" > "$SCRATCH/show-again.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "showing a shown chat must exit non-zero"
grep -q 'already shown' "$SCRATCH/show-again.out" || { cat "$SCRATCH/show-again.out"; fail "want 'already shown'"; }

say "a chat session the operator split is refused, not guessed at"
tmux split-window -d -t '=grove-chat-hidews-2:chat' 'sleep 600'
BEFORE="$(server_state)"
rc=0; gv chat show grove-chat-hidews-2 > "$SCRATCH/show-split.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "showing a split chat must exit non-zero"
grep -q 'holds 2 panes' "$SCRATCH/show-split.out" || { cat "$SCRATCH/show-split.out"; fail "want the split refusal"; }
[ "$(server_state)" = "$BEFORE" ] || fail "a refused show touched the server"
tmux kill-pane -t "$(tmux list-panes -t '=grove-chat-hidews-2:chat' -F '#{pane_id}' | grep -v "^$CHAT_B\$")"

say "gv chat close ends a hidden chat"
gv chat hide "$CHAT_A" > "$SCRATCH/hide-a2.out" 2>&1 || { cat "$SCRATCH/hide-a2.out"; fail "hide by %pane must succeed"; }
grep -q '✓ hidden as grove-chat-hidews-1' "$SCRATCH/hide-a2.out" || { cat "$SCRATCH/hide-a2.out"; fail "the freed number is reused"; }
gv chat close grove-chat-hidews-1 > "$SCRATCH/close.out" 2>&1 || { cat "$SCRATCH/close.out"; fail "close of a hidden chat must succeed"; }
if tmux has-session -t '=grove-chat-hidews-1' 2>/dev/null; then fail "close left the hidden chat running"; fi
for _ in $(seq 1 20); do kill -0 "$PID_A" 2>/dev/null || break; sleep 0.1; done
if kill -0 "$PID_A" 2>/dev/null; then fail "close left the hidden chat's process alive"; fi

say "a plain park leaves the hidden chat running"
gv park > "$SCRATCH/park.out" 2>&1 || { cat "$SCRATCH/park.out"; fail "park failed"; }
grep -q 'chat grove-chat-hidews-2 still running' "$SCRATCH/park.out" || { cat "$SCRATCH/park.out"; fail "park must name the hidden chat it leaves behind"; }
if tmux has-session -t '=grove-hidews' 2>/dev/null; then fail "park did not kill the cockpit session"; fi
tmux has-session -t '=grove-chat-hidews-2' 2>/dev/null || fail "a plain park killed a hidden chat"
kill -0 "$PID_B" 2>/dev/null || fail "a plain park killed the hidden chat's process"

say "show refuses while the cockpit is not running"
rc=0; gv chat show grove-chat-hidews-2 > "$SCRATCH/show-down.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "show with no cockpit must exit non-zero"
grep -q "open the cockpit with \`gv\`, or attach with tmux attach -t '=grove-chat-hidews-2'" "$SCRATCH/show-down.out" \
  || { cat "$SCRATCH/show-down.out"; fail "want the cockpit-not-running refusal"; }
tmux has-session -t '=grove-chat-hidews-2' 2>/dev/null || fail "a refused show killed the chat"

say "gv park --chats reaps the hidden chat"
build_cockpit
gv park --chats > "$SCRATCH/park2.out" 2>&1 || { cat "$SCRATCH/park2.out"; fail "park --chats failed"; }
grep -q 'chat grove-chat-hidews-2 (pid [0-9]*) — killed' "$SCRATCH/park2.out" || { cat "$SCRATCH/park2.out"; fail "--chats must name what it killed"; }
[ "$(chat_sessions)" -eq 0 ] || fail "--chats left $(chat_sessions) chat(s)"

say "real-server canary"
[ "$(real_tmux)" = "$REAL_TMUX_BEFORE" ] || fail "the machine's REAL tmux session list changed during this suite"

printf '\n\033[32mchat_hide e2e green\033[0m\n'
