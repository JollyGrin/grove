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
  rm -rf "$SCRATCH" 2>/dev/null || { sleep 0.5; rm -rf "$SCRATCH"; }
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
  *)
    echo "[fake ssh] ran: \$cmd" ;;
esac
EOF
chmod +x "$SCRATCH/bin/ssh"
export PATH="$SCRATCH/bin:$PATH"

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

say "PASS — cockpit: AGENTS+ACTIVITY left, stacked chats right, O/new works, @pc rows act over ssh"
