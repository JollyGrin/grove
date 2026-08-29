#!/usr/bin/env bash
# grove-205 E2E: `gv watch` — the transition stream monitors can trust.
#
# The ticket was filed by two false DONEs inside a minute: a detector grepped
# a worker's tmux pane for `STATUS: DONE`, and that line is in the KICKOFF
# PROMPT itself, so it is in every pane from second zero. This suite proves
# the trap is real (it greps the live pane and finds all three sentinels) and
# that `gv watch` is immune to it, plus the rest of the contract:
#
#   · from-now baseline — an ALREADY-done task never fires `--until done`
#   · fires exactly once, exit 0, when a NEW done lands
#   · coverage — an idle stop with no STATUS line, a notification and a
#     session_ended each emit a line (silence must not look like success)
#   · line-flushed through a pipe (the Monitor-tool contract)
#   · --replay / --since over a fixture; torn line skipped; missing log waits
#   · `gv ls --json` carries the additive sentinel_at
#
# Dummy-data pattern: scratch HOME, ISOLATED tmux server, worker stubbed to
# `echo`. Pure read throughout — watch never appends.
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-watch.XXXXXX)"

# Build with the real environment BEFORE pointing HOME at the scratch dir.
say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

export HOME="$SCRATCH/home"
# $TMUX beats TMUX_TMPDIR — without the unset, a run from inside a tmux pane
# puts every tmux call (incl. cleanup's kill-server) on the REAL server.
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"
mkdir -p "$HOME" "$TMUX_TMPDIR"
unset GROVE_STATE_DIR || true   # per-workspace state is the subject
cleanup() {
  tmux kill-server 2>/dev/null || true   # isolated server only (TMUX_TMPDIR)
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH"
}
trap cleanup EXIT

say "scratch workspace with an echo-stubbed worker"
DUMMY="$SCRATCH/repos/dummy"
mkdir -p "$DUMMY" && cd "$DUMMY"
git init -q -b main
git config user.email e2e@grove.test && git config user.name "grove e2e"
echo "# dummy" > README.md
git add -A && git commit -qm "init"
"$GV" init --yes > "$SCRATCH/init.out"
WCFG="$DUMMY/.grove/config.yaml"
perl -pi -e 's/^(\s*)base: main$/$1base: main\n$1claude: echo/' "$WCFG"
grep -q 'claude: echo' "$WCFG" || fail "claude stub not written"
"$GV" grab task-001 > "$SCRATCH/grab.out"
WTDIR="$(ls -d "$SCRATCH"/repos/.worktrees/dummy/task-001-*)"
STATE="$DUMMY/.grove/state"
EVENTS="$STATE/events.jsonl"
[ -f "$EVENTS" ] || fail "no events.jsonl after grab"

hook() { # <event> <json-payload-body>
  printf '{"session_id":"s-watch","cwd":"%s","hook_event_name":"%s"%s}' \
    "$WTDIR" "$1" "$2" | "$GV" hook "$1"
}

# --- 1. the booby trap is real ------------------------------------------
say "the worker pane contains ALL THREE kickoff STATUS sentinels"
tmux list-windows -a -F '#S:#W' > "$SCRATCH/wins.txt"
WIN="$(grep task-001 "$SCRATCH/wins.txt" | head -1)"
[ -n "$WIN" ] || fail "worker window not found on the isolated server"
tmux list-panes -t "$WIN" -F '#{pane_id}' > "$SCRATCH/panes.txt"
: > "$SCRATCH/pane.txt"
# -S -: full scrollback (the prompt scrolls off an 80x24 pane).
# -J:   join wrapped lines, so a sentinel split at the pane edge still greps.
while read -r p; do tmux capture-pane -p -S - -J -t "$p" >> "$SCRATCH/pane.txt"; done < "$SCRATCH/panes.txt"
for s in 'STATUS: QUESTION' 'STATUS: BLOCKED' 'STATUS: DONE'; do
  grep -qF "$s" "$SCRATCH/pane.txt" || fail "expected the kickoff placeholder $s in the pane"
done

say "…yet the worker has emitted NO sentinel: gv watch stays silent"
# The regression test for the false positive that filed grove-205: a pane
# grep would fire here, on every task, forever. --replay is deliberate —
# even the WHOLE log holds nothing that looks like a transition.
timeout 2 "$GV" watch --replay --ticket task-001 > "$SCRATCH/silent.out" 2>&1 || true
[ ! -s "$SCRATCH/silent.out" ] || fail "watch fired on kickoff prompt text: $(cat "$SCRATCH/silent.out")"

# --- 2. sentinel_at: absent, then present -------------------------------
say "gv ls --json omits sentinel_at while there is no sentinel"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-before.json"
python3 - "$SCRATCH/ls-before.json" <<'PY' || fail "sentinel_at present before any sentinel"
import json, sys
row = [t for t in json.load(open(sys.argv[1]))["tasks"] if t["ticket"] == "task-001"][0]
sys.exit(1 if "sentinel_at" in row else 0)
PY

say "an agent_status DONE lands (the authoritative signal: the Stop hook)"
hook stop ',"last_assistant_message":"STATUS: DONE — first pass, merged nothing"'
grep -q '"sentinel":"done"' "$EVENTS" || fail "stop hook did not append a done sentinel"

say "gv ls --json now carries sentinel_at, and it dates the sentinel"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-after.json"
python3 - "$SCRATCH/ls-after.json" <<'PY' || fail "sentinel_at missing or not the sentinel's own time"
import json, sys
row = [t for t in json.load(open(sys.argv[1]))["tasks"] if t["ticket"] == "task-001"][0]
assert row.get("sentinel") == "done", row
assert row.get("sentinel_at"), row
assert row["sentinel_at"] <= row["updated"], row
PY

# --- 3. the headline acceptance -----------------------------------------
say "--until done does NOT fire on a task that is ALREADY done (from-now)"
( "$GV" watch --ticket task-001 --until done > "$SCRATCH/until.out" 2>&1; \
  echo "exit=$?" > "$SCRATCH/until.rc" ) &
WATCH_PID=$!
sleep 1.5
kill -0 "$WATCH_PID" 2>/dev/null || fail "watch exited on the pre-existing done: $(cat "$SCRATCH/until.out")"
[ ! -s "$SCRATCH/until.out" ] || fail "watch replayed history: $(cat "$SCRATCH/until.out")"

say "…and fires exactly once, exit 0, when a NEW done is appended"
hook stop ',"last_assistant_message":"STATUS: DONE — second pass, this is the real one"'
for _ in $(seq 1 40); do
  [ -f "$SCRATCH/until.rc" ] && break
  sleep 0.25
done
wait "$WATCH_PID" 2>/dev/null || true
grep -q 'exit=0' "$SCRATCH/until.rc" || fail "watch --until done exited non-zero: $(cat "$SCRATCH/until.rc")"
[ "$(wc -l < "$SCRATCH/until.out")" -eq 1 ] || fail "expected exactly one line, got: $(cat "$SCRATCH/until.out")"
grep -q 'task-001' "$SCRATCH/until.out" || fail "row missing the ticket: $(cat "$SCRATCH/until.out")"
grep -q 'the real one' "$SCRATCH/until.out" || fail "row missing the message head: $(cat "$SCRATCH/until.out")"

# --- 4. coverage: a crashed or wandered-off worker is never silent -------
say "coverage: idle stop (no STATUS line), notification and session_ended"
timeout 6 "$GV" watch --ticket task-001 > "$SCRATCH/cov.out" 2>&1 &
COV_PID=$!
sleep 1
hook stop ',"last_assistant_message":"I refactored a few files and then just... stopped."'
hook notification ',"message":"Claude needs your permission to use Bash"'
hook session-end ''
sleep 1.5
for want in idle notification session_ended; do
  grep -q "$want" "$SCRATCH/cov.out" || fail "default stream dropped $want: $(cat "$SCRATCH/cov.out")"
done
[ "$(wc -l < "$SCRATCH/cov.out")" -eq 3 ] || fail "expected 3 rows, got: $(cat "$SCRATCH/cov.out")"

say "line-flushed through a pipe: each event lands before the process exits"
# The Monitor-tool contract — one stdout LINE per notification. A buffered
# writer would hold everything to exit, and the event would never be seen.
# `| cat` is the assertion: it makes stdout a pipe, not a tty.
( timeout 5 "$GV" watch --ticket task-001 --type all | cat > "$SCRATCH/flush.out" ) &
FLUSH_PID=$!
sleep 1
hook stop ',"last_assistant_message":"STATUS: QUESTION — flushed while still running?"'
sleep 1.5
kill -0 "$FLUSH_PID" 2>/dev/null || fail "flush probe exited early — cannot prove per-line flushing"
grep -q 'flushed while still running' "$SCRATCH/flush.out" \
  || fail "piped output was buffered: nothing visible while the watcher still runs"
wait "$COV_PID" 2>/dev/null || true
wait "$FLUSH_PID" 2>/dev/null || true

# --- 5. fixture replay: --replay / --since / torn line / missing log -----
say "--replay and --since reproduce a known range from a fixture log"
FIX="$SCRATCH/fixture"
mkdir -p "$FIX"
cat > "$FIX/events.jsonl" <<'FIXTURE'
{"time":"2026-08-29T10:00:00Z","type":"task_created","ticket":"fx-1","data":{"title":"fixture"},"v":1}
{"time":"2026-08-29T10:01:00Z","type":"agent_status","ticket":"fx-1","data":{"status":"waiting","sentinel":"question","question":"tabs or spaces?"},"v":1}
{"time":"2026-08-29T10:02:00Z","type":"agent_status","ticket":"fx-1","data":{"status":"idle","sentinel":"done","message":"shipped it"},"v":1}
{"time":"2026-08-29T10:03:00Z","type":"session_ended","ticket":"fx-1","v":1}
FIXTURE
# A torn trailing append — the writer was interrupted mid-record.
printf '{"time":"2026-08-29T10:04:00Z","type":"agent_st' >> "$FIX/events.jsonl"

timeout 2 env GROVE_STATE_DIR="$FIX" "$GV" watch --replay --type all --json > "$SCRATCH/replay.out" 2>&1 || true
[ "$(wc -l < "$SCRATCH/replay.out")" -eq 4 ] \
  || fail "--replay: expected the 4 complete records, got: $(cat "$SCRATCH/replay.out")"
head -1 "$SCRATCH/replay.out" > "$SCRATCH/replay.first"
grep -q '"v":1' "$SCRATCH/replay.first" || fail "--json dropped the record's v stamp"
grep -q 'agent_st"' "$SCRATCH/replay.out" && fail "torn trailing line was emitted" || true

timeout 2 env GROVE_STATE_DIR="$FIX" "$GV" watch --since 2026-08-29T10:02:00Z --type all > "$SCRATCH/since.out" 2>&1 || true
[ "$(wc -l < "$SCRATCH/since.out")" -eq 2 ] \
  || fail "--since: expected the 2 records at/after the cutoff, got: $(cat "$SCRATCH/since.out")"

say "a missing events.jsonl waits rather than erroring"
EMPTY="$SCRATCH/empty-state"
mkdir -p "$EMPTY"
rc=0
timeout 2 env GROVE_STATE_DIR="$EMPTY" "$GV" watch --replay > "$SCRATCH/empty.out" 2>&1 || rc=$?
[ "$rc" -eq 124 ] || fail "a missing log must keep waiting (timeout 124), got rc=$rc: $(cat "$SCRATCH/empty.out")"

say "a mistyped filter is an error, never an empty stream"
rc=0
"$GV" watch --sentinel finished > "$SCRATCH/typo.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "a mistyped --sentinel was accepted"
grep -q 'unknown sentinel' "$SCRATCH/typo.out" || fail "typo error unhelpful: $(cat "$SCRATCH/typo.out")"

# --- 6. hard rule: pure read --------------------------------------------
say "gv watch never wrote: events.jsonl untouched by every run above"
BEFORE=$(wc -l < "$EVENTS")
timeout 2 "$GV" watch --replay --type all > /dev/null 2>&1 || true
[ "$(wc -l < "$EVENTS")" -eq "$BEFORE" ] || fail "gv watch appended to events.jsonl"

say "PASS — gv watch: prompt-proof, from-now, line-flushed, pure read"
