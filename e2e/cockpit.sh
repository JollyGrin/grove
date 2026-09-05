#!/usr/bin/env bash
# Cockpit smoke test: builds the main-vertical cockpit on an ISOLATED tmux
# server (TMUX_TMPDIR), seeds fake fleet events, and asserts the dashboard
# renders AGENTS + ACTIVITY (no MAIL/REVIEW panels) and that
# `gv orchestrator new` stacks another chat pane. grove-185 adds the
# @host-row keys: driving the live TUI with send-keys against a fake ssh
# (handoff.sh pattern), R merges an @pc row, then a/n/d/enter must hit
# the host with the right argv while v stays blocked and tombstones stay
# read-only.
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
# Hermetic scratch HOME: the pane shells and the grove-185 diff pager
# otherwise drop .bash_history/.lesshst into it as they DIE — i.e. during
# cleanup's rm -rf, which then fails "Directory not empty" and (as the
# EXIT trap's last command) becomes the suite's exit status. Green run,
# red script.
export HISTFILE=/dev/null
export LESSHISTFILE=-
# $TMUX beats TMUX_TMPDIR in tmux's socket resolution — launched from
# inside a tmux pane, TMUX_TMPDIR alone is a silent no-op and every tmux
# call (including cleanup's kill-server) hits the REAL server. Unset first.
# (Root cause of the 2026-07-07 grove-7 crash; see LEARNINGS.md.)
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"   # isolated tmux server — never the user's
mkdir -p "$HOME/.config/grove" "$GROVE_STATE_DIR" "$TMUX_TMPDIR" "$SCRATCH/repo"

cleanup() {
  tmux kill-server 2>/dev/null || true
  # Let the panes actually die before removing the tree: kill-server only
  # signals, and a shell still flushing its history recreates a directory
  # rm has already emptied (see the HISTFILE note above). Poll the socket,
  # then retry the remove once.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -S "$TMUX_TMPDIR/tmux-$(id -u)/default" ] || break
    sleep 0.2
  done
  # Retry once, and never let the remove decide the exit status: as the
  # EXIT trap's last command it would turn a fully-green run red.
  rm -rf "$SCRATCH" 2>/dev/null || { sleep 0.5; rm -rf "$SCRATCH" 2>/dev/null || true; }
}
trap cleanup EXIT

say "seed config + fleet events"
cat > "$HOME/.config/grove/config.yaml" <<EOF
provider: {kind: markdown}
repos:
  demo: {path: $SCRATCH/repo, claude: echo}
orchestrator: {claude: echo}
hosts:
  pc: {ssh: localhost, gv: $GV}
EOF
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
cat > "$GROVE_STATE_DIR/events.jsonl" <<EOF
{"time":"$NOW","type":"task_created","ticket":"task-001","data":{"title":"Demo task","repo":"demo","branch":"task-001-demo","worktree":"$SCRATCH/repo","tmux_session":"pr-demo","tmux_window":"task-001-demo"}}
{"time":"$NOW","type":"agent_status","ticket":"task-001","data":{"status":"waiting","sentinel":"question","question":"tabs or spaces?","message":"STATUS: QUESTION — tabs or spaces?"}}
{"time":"$NOW","type":"task_created","ticket":"task-002","data":{"title":"Gone task","repo":"demo","branch":"task-002-demo","worktree":"$SCRATCH/repo","tmux_session":"pr-demo","tmux_window":"task-002-demo"}}
{"time":"$NOW","type":"task_handed_off","ticket":"task-002","data":{"host":"pc","branch":"task-002-demo"}}
EOF

say "fake ssh on PATH (grove-185 remote-row keys)"
# handoff.sh's fake-ssh pattern, cockpit flavor: parse like real ssh — skip
# options, first bare word = TARGET (must be hosts.pc.ssh), everything after
# -- = the command. Every call's raw argv lands in ssh.log; the R fetch
# (" ls ") gets a canned one-task gv ls --json envelope so the merge builds
# an @pc row; every other verb prints a marker line (gives the diff pager
# something to show) and succeeds.
mkdir -p "$SCRATCH/bin"
SSH_LOG="$SCRATCH/ssh.log"
touch "$SSH_LOG"
cat > "$SCRATCH/bin/ssh" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >> "$SSH_LOG"
target=""
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) shift 2 ;;
    --) shift; break ;;
    -*) shift ;;
    *) if [ -z "\$target" ]; then target="\$1"; shift; else break; fi ;;
  esac
done
[ "\$target" = "localhost" ] || { echo "fake ssh: target '\$target' is not hosts.pc.ssh (localhost)" >&2; exit 42; }
cmd="\$*"
case " \$cmd " in
  *" ls "*)
    printf '{"schema_version":1,"tasks":[{"ticket":"pc-9","title":"remote row","repo":"demo","agent":"working","live":"waiting","created":"$NOW"}]}' ;;
  *" orchestrator "*)
    # grove-199: the host half of the @-armed spawn. A profile the host does
    # not have is the error path — a non-zero exit whose stderr line is the
    # whole diagnosis, and the cockpit must spawn NO pane for it.
    case " \$cmd " in
      *" --profile boom "*)
        echo "gv: unknown model profile \"boom\" on @pc" >&2
        exit 1 ;;
    esac
    n=\$(cat "$SCRATCH/chat-n" 2>/dev/null || echo 0); n=\$((n+1)); echo \$n > "$SCRATCH/chat-n"
    echo "✓ orchestrator chat grove-chat-rws-\$n — workspace rws"
    echo "attach: tmux attach -t '=grove-chat-rws-\$n'" ;;
  *)
    echo "[fake ssh] ran: \$cmd" ;;
esac
EOF
chmod +x "$SCRATCH/bin/ssh"
export PATH="$SCRATCH/bin:$PATH"

# Start the isolated server explicitly, with PATH already scratch'd, so the
# global environment every later session/pane inherits carries the fake ssh.
# default-command non-empty means new panes run the shell DIRECTLY instead
# of as a login shell — on macOS a login shell runs /etc/zprofile, which
# runs /usr/libexec/path_helper and rebuilds PATH from /etc/paths, silently
# pushing the scratch bin (and its fake ssh) behind the real one (grove-230:
# this is why `R` merged no @pc row — the cockpit's pane shelled out to the
# REAL ssh and hit host-key verification, not the fixture). Linux has no
# path_helper, which is why this class was invisible on groveremote.
# exit-empty off keeps the server alive with no sessions until gv creates one.
PANE_SHELL="${SHELL:-/bin/sh}"
if [ "${GROVE_E2E_TMUX_CONF:-}" = "hostile" ]; then
  say "start isolated tmux server (non-login panes; hostile conf: base-index 1, pane-base-index 1)"
  cat > "$SCRATCH/tmux.conf" <<EOF
set -g default-command "$PANE_SHELL"
set -g exit-empty off
set -g base-index 1
set -g pane-base-index 1
set -g renumber-windows on
set -g allow-rename on
EOF
else
  say "start isolated tmux server (non-login panes)"
  cat > "$SCRATCH/tmux.conf" <<EOF
set -g default-command "$PANE_SHELL"
set -g exit-empty off
EOF
fi
tmux -f "$SCRATCH/tmux.conf" start-server

say "sanity: a cockpit-style pane resolves the fake ssh, not the real one"
tmux new-session -d -s ssh-check -x 80 -y 24
CHECKPANE="$(tmux list-panes -t '=ssh-check:' -F '#{pane_id}')"
tmux send-keys -t "$CHECKPANE" 'command -v ssh' Enter
CHECKCAP=""
for _ in 1 2 3 4 5 6 7 8 9 10; do
  CHECKCAP="$(tmux capture-pane -p -S -50 -t "$CHECKPANE")"
  echo "$CHECKCAP" | grep -q "$SCRATCH/bin/ssh" && break
  sleep 0.2
done
echo "$CHECKCAP" | grep -q "$SCRATCH/bin/ssh" || fail "pane shell resolved ssh to the wrong binary (path_helper?):
$CHECKCAP"
tmux kill-session -t '=ssh-check'

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

say "widen the cockpit pane — the @host-row flash assertions need room"
# A detached session sizes to the last client (80x24 default) and the
# stacked orchestrator panes squeeze the main-vertical main pane to ~25
# cols — the footer flash truncates long before "was handed off". Pin the
# window big, then the pane: resize-pane -x on the main pane sets
# main-pane-width, so 120 cols is deterministic, not a rebalance race.
tmux resize-window -t "$PANE0" -x 200 -y 50
tmux resize-pane -t "$PANE0" -x 120
sleep 1

# --- grove-185: act on @host rows (remote answer/diff/attach) ---
# Drive the live cockpit through send-keys: single keys and one short word
# into the detail input — never prose (tmux-discipline §2). Polls capture
# the pane with scrollback (-S -300) so a panic keeps its reason line.

# wait_grep <pattern> — poll the cockpit pane until the pattern renders;
# leaves the last capture in CAP. wait_ssh <pattern> — same for ssh.log.
wait_grep() {
  local pat="$1" i
  for i in $(seq 1 50); do
    CAP="$(tmux capture-pane -p -S -300 -t "$PANE0")"
    echo "$CAP" | grep -q -- "$pat" && return 0
    sleep 0.2
  done
  return 1
}
wait_ssh() {
  local pat="$1" i
  for i in $(seq 1 50); do
    grep -q -- "$pat" "$SSH_LOG" && return 0
    sleep 0.2
  done
  return 1
}

say "R folds the fake host's fleet in — one @pc row, one ssh ls"
EV_LINES=$(wc -l < "$GROVE_STATE_DIR/events.jsonl")
tmux send-keys -t "$PANE0" R
wait_grep 'pc-9' || fail "R merge missing the @pc row:
$CAP"
wait_ssh ' ls --json --no-pr --no-cost' || fail "R fetch did not go through ssh:
$(cat "$SSH_LOG")"

say "a on the @pc row: remote-bound detail (no pane scrape, ssh hint)"
tmux send-keys -t "$PANE0" j
wait_grep '▸.*pc-9' || fail "cursor not on the @pc row:
$CAP"
tmux send-keys -t "$PANE0" a
wait_grep 'remote worker on @pc' || fail "remote detail hint missing where the pane tail goes:
$CAP"
echo "$CAP" | grep -q 'ANSWER' || fail "detail input missing:
$CAP"

say "typing hello + enter relays gv answer over ssh — no local event"
tmux send-keys -t "$PANE0" hello Enter
wait_ssh ' answer pc-9 hello' || fail "ssh answer argv wrong (want '… -- <gv> answer pc-9 hello'):
$(cat "$SSH_LOG")"
sleep 1
[ "$(wc -l < "$GROVE_STATE_DIR/events.jsonl")" -eq "$EV_LINES" ] || fail "remote answer appended a LOCAL event — the remote host records its own"
grep -q '"type":"answered"' "$GROVE_STATE_DIR/events.jsonl" && fail "a local answered event was written" || true

say "n on the @pc row relays gv nudge — same input, nudge flavor"
wait_grep '▸.*pc-9' || fail "cursor left the @pc row after the answer:
$CAP"
tmux send-keys -t "$PANE0" n
wait_grep 'NUDGE' || fail "n did not open the detail input in nudge flavor:
$CAP"
echo "$CAP" | grep -q 'remote worker on @pc' || fail "nudge detail must carry the remote hint too:
$CAP"
tmux send-keys -t "$PANE0" wake Enter
wait_ssh ' nudge pc-9 wake' || fail "ssh nudge argv wrong (want '… -- <gv> nudge pc-9 wake'):
$(cat "$SSH_LOG")"

say "v stays blocked on a live remote row (review state is local)"
tmux send-keys -t "$PANE0" v
wait_grep 'review state is local' || fail "v on a remote row must flash the local-review refusal:
$CAP"
grep -q ' review pc-9' "$SSH_LOG" && fail "v must not reach the host" || true

say "d on the @pc row pages the remote diff; q restores the cockpit"
tmux send-keys -t "$PANE0" d
wait_ssh ' -- .* diff pc-9' || fail "ssh diff argv wrong (want '… -- <gv> diff pc-9'):
$(cat "$SSH_LOG")"
# The pager holds the pane until q — the shim's marker line is its content.
wait_grep 'fake ssh.* ran:' || fail "pager never showed the remote diff output:
$CAP"
tmux send-keys -t "$PANE0" q
wait_grep 'AGENTS' || fail "cockpit did not repaint after the pager exited:
$CAP"

say "enter on the @pc row: ssh <host> -t <gv> attach, then the cockpit is back"
tmux send-keys -t "$PANE0" Enter
wait_ssh 'localhost -t .* attach pc-9' || fail "ssh attach argv wrong (want '<hosts.pc.ssh> -t <hosts.pc.gv> attach pc-9'):
$(cat "$SSH_LOG")"
wait_grep 'AGENTS' || fail "cockpit did not come back after the remote attach:
$CAP"

say "tombstone rows stay read-only bookmarks"
tmux send-keys -t "$PANE0" j
wait_grep '▸.*task-002' || fail "cursor not on the tombstone row:
$CAP"
tmux send-keys -t "$PANE0" a
wait_grep 'was handed off to pc' || fail "a on a tombstone must flash read-only, not relay:
$CAP"

# --- grove-199: the `@`-armed remote spawn ---
# A WORKSPACE cockpit, because a remote chat spawns into the HOST's twin of
# the workspace the operator is standing in — the label is what travels, so
# there is nothing to send from the global layer.

say "grove-199: a workspace cockpit (rws) for the @ spawn"
WS="$SCRATCH/rws"
mkdir -p "$WS"
git -C "$WS" init -qb main
git -C "$WS" config user.email e2e@grove.test && git -C "$WS" config user.name "grove e2e"
( cd "$WS" && echo x > README.md && git add -A && git commit -qm init )
( cd "$WS" && "$GV" init --yes --label rws > /dev/null )
cat >> "$WS/.grove/config.yaml" <<EOF
model_profiles:
  e2e-glm:
    base_url: https://openrouter.ai/api
    auth_token_env: OPENROUTER_API_KEY
    opus: z-ai/glm-5.2
  boom:
    base_url: https://openrouter.ai/api
    auth_token_env: OPENROUTER_API_KEY
    opus: nope/nope
orchestrator:
  claude: echo
  hotkeys:
    "1": e2e-glm
    "2": boom
hosts:
  pc:
    ssh: localhost
    gv: $GV
EOF
( cd "$WS" && "$GV" >/dev/null 2>&1 ) || true
tmux has-session -t '=grove-rws' 2>/dev/null || fail "workspace cockpit session grove-rws not created"

# The polling helpers key off PANE0 — point them at the workspace cockpit's
# dashboard pane for the rest of the suite.
PANE0="$(tmux list-panes -t '=grove-rws:cockpit' -F '#{pane_id}' | head -1)"
tmux resize-window -t "$PANE0" -x 200 -y 50
tmux resize-pane -t "$PANE0" -x 140
sleep 2
wait_grep 'AGENTS' || fail "workspace cockpit never rendered:
$CAP"
PANES_BEFORE=$(tmux list-panes -t '=grove-rws:cockpit' | wc -l)

say "@ arms the spawn: the footer swaps to the armed prompt"
tmux send-keys -t "$PANE0" -l '@'
wait_grep 'esc cancel' || fail "@ did not swap the footer to the armed prompt:
$CAP"
echo "$CAP" | grep -q '@pc' || fail "the armed prompt must name the host:
$CAP"

say "@ then 1 relays grove-198's verb — op id, --as, --workspace, profile name only"
tmux send-keys -t "$PANE0" -l '1'
wait_ssh 'orchestrator new --op-id' || fail "@ + digit did not relay the spawn:
$(cat "$SSH_LOG")"
grep -Eq "orchestrator new --op-id [0-9a-f]{32} --as pc --workspace rws --profile e2e-glm\$" "$SSH_LOG" \
  || fail "relayed argv wrong (want 'orchestrator new --op-id <32-hex> --as pc --workspace rws --profile e2e-glm'):
$(cat "$SSH_LOG")"

say "…then a LOCAL pane attaches over ssh to the session the host named"
# ssh.log holds the pane shell's POST-parse argv, so the grove-207 quotes
# around the exact-match target are gone by here — that they are absent is
# the proof the quoting is transparent to ssh and tmux.
wait_ssh '-t localhost tmux attach -t =grove-chat-rws-1' \
  || fail "no local ssh-attach pane for the host's chat session:
$(cat "$SSH_LOG")"
PANES_AFTER=$(tmux list-panes -t '=grove-rws:cockpit' | wc -l)
[ "$PANES_AFTER" -eq "$((PANES_BEFORE + 1))" ] || fail "want exactly one new pane ($PANES_BEFORE → $PANES_AFTER)"

say "the remote pane wears its identity: @host · profile, in its own border color"
NEWPANE="$(tmux list-panes -t '=grove-rws:cockpit' -F '#{pane_id}' | tail -1)"
[ "$(tmux show-options -pqv -t "$NEWPANE" @grove_remote)" = "pc" ] || fail "remote pane not tagged with its host"
[ "$(tmux show-options -pqv -t "$NEWPANE" @grove_profile)" = "e2e-glm" ] || fail "remote pane not tagged with its profile"
tmux show-options -pqv -t "$NEWPANE" pane-border-style | grep -q 'fg=' || fail "remote pane has no distinct border color"
FMT="$(tmux show-options -wqv -t '=grove-rws:cockpit' pane-border-format)"
[ -n "$FMT" ] || fail "cockpit pane borders are off — the remote tag would be invisible"
TITLE="$(tmux display-message -p -t "$NEWPANE" -F "$FMT")"
echo "$TITLE" | grep -q '@pc · e2e-glm' || fail "remote pane title = '$TITLE', want '@pc · e2e-glm'"
DASHTITLE="$(tmux display-message -p -t "$PANE0" -F "$FMT")"
echo "$DASHTITLE" | grep -q '@pc' && fail "the local dashboard pane must not read as remote: $DASHTITLE" || true

say "error path: the remote's error line becomes the flash, and NO pane is spawned"
tmux send-keys -t "$PANE0" -l '@'
wait_grep 'esc cancel' || fail "@ did not re-arm:
$CAP"
tmux send-keys -t "$PANE0" -l '2'
wait_grep 'unknown model profile' || fail "the remote's error line never reached the flash:
$CAP"
sleep 1
[ "$(tmux list-panes -t '=grove-rws:cockpit' | wc -l)" -eq "$PANES_AFTER" ] \
  || fail "a failed remote spawn must leave no pane behind"

say "esc disarms: the ordinary legend is back and nothing was sent"
SSH_LINES=$(wc -l < "$SSH_LOG")
tmux send-keys -t "$PANE0" -l '@'
wait_grep 'esc cancel' || fail "@ did not arm a third time:
$CAP"
tmux send-keys -t "$PANE0" Escape
wait_grep 'remote spawn cancelled' || fail "esc did not cancel the arming:
$CAP"
[ "$(wc -l < "$SSH_LOG")" -eq "$SSH_LINES" ] || fail "a cancelled arming still reached the host"

say "PASS — cockpit: AGENTS+ACTIVITY left, stacked chats right, O/new works, @pc rows act over ssh, @ spawns on the host"
