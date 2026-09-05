#!/usr/bin/env bash
# grove-253 E2E: `gv supervise` — the headless supervisor loop.
#
# Part 2 (grove-252) built the pure transition engine; nothing ran it. This
# proves the driver: one poll loop reading tmux + a fake `gh`, feeding
# internal/supervise.Transitions, appending + printing whatever fires —
# plus the single-emitter lock that keeps a second `gv supervise` (or
# part 4's future cockpit driver) from double-emitting.
#
# Dummy-data pattern: scratch HOME, ISOLATED tmux server (`unset TMUX` —
# tmux-discipline), a fake `gh` whose answer is a file this script
# rewrites between steps, and a claude-shaped stub pane driven by a second
# control file (a plain `echo` worker never shows pane CONTENT, which the
# liveness dimension reads).
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(cd "$(mktemp -d /tmp/grove-supervise.XXXXXX)" && pwd -P)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

# Real-server canary (tmux-discipline): snapshot the machine's REAL tmux
# session list now, compare at the end.
real_tmux() { env -u TMUX -u TMUX_PANE -u TMUX_TMPDIR tmux list-sessions -F '#{session_name}' 2>/dev/null | sort; true; }
REAL_TMUX_BEFORE="$(real_tmux)"

export HOME="$SCRATCH/home"
# $TMUX beats TMUX_TMPDIR — without the unset, a run from inside a tmux pane
# puts every tmux call (incl. cleanup's kill-server) on the REAL server.
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"
mkdir -p "$HOME" "$TMUX_TMPDIR" "$SCRATCH/bin"
unset GROVE_STATE_DIR || true   # per-workspace state is the subject

SUPERVISE_PID=""
WATCH_PID=""
cleanup() {
  kill "$SUPERVISE_PID" 2>/dev/null || true
  kill "$WATCH_PID" 2>/dev/null || true
  tmux kill-server 2>/dev/null || true   # isolated server only (TMUX_TMPDIR)
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH"
}
trap cleanup EXIT

say "fake gh: the pr-list answer is a file this script rewrites between steps"
echo '[]' > "$SCRATCH/gh-answer.json"
cat > "$SCRATCH/bin/gh" <<EOF
#!/usr/bin/env bash
case "\$*" in
  *"pr list"*)
    if [ -f "$SCRATCH/gh-fail" ]; then
      echo "fake gh: lookup failed" >&2
      exit 1
    fi
    cat "$SCRATCH/gh-answer.json"
    ;;
  *) echo '[]' ;;
esac
EOF
chmod +x "$SCRATCH/bin/gh"

say "claude-shaped stub pane, redrawn from a control file: idle / waiting / errored"
echo idle > "$SCRATCH/pane-mode"
cat > "$SCRATCH/bin/claude-stub" <<EOF
#!/usr/bin/env bash
CTRL="$SCRATCH/pane-mode"
while true; do
  mode=\$(cat "\$CTRL" 2>/dev/null || echo idle)
  printf '\033[H\033[2J'
  case "\$mode" in
    waiting)
      printf '%s\n' "Claude Code" "" "Which approach should I take?" "  1. Option A" "  2. Option B" "" "(Enter to select)" ;;
    errored)
      printf '%s\n' "Claude Code" "" "API Error: Request rejected (429) · Usage limit reached for 5 hour" ;;
    *)
      printf '%s\n' "Claude Code" "" "❯ " "  /help for commands" ;;
  esac
  sleep 0.3
done
EOF
chmod +x "$SCRATCH/bin/claude-stub"
export PATH="$SCRATCH/bin:$PATH"

say "scratch workspace, dummy repo, worker on the claude-shaped stub"
DUMMY="$SCRATCH/repos/dummy"
mkdir -p "$DUMMY" && cd "$DUMMY"
git init -q -b main
git config user.email e2e@grove.test && git config user.name "grove e2e"
echo "# dummy" > README.md
git add -A && git commit -qm "init"
"$GV" init --yes > "$SCRATCH/init.out"
WCFG="$DUMMY/.grove/config.yaml"
perl -pi -e "s#^(\\s*)base: main\$#\$1base: main\\n\$1claude: $SCRATCH/bin/claude-stub#" "$WCFG"
grep -q 'claude-stub' "$WCFG" || fail "claude stub not written to config"
"$GV" grab task-001 > "$SCRATCH/grab.out"
WTDIR="$(ls -d "$SCRATCH"/repos/.worktrees/dummy/task-001-*)"
STATE="$DUMMY/.grove/state"
EVENTS="$STATE/events.jsonl"
[ -f "$EVENTS" ] || fail "no events.jsonl after grab"
"$GV" ls --json --no-pr --no-cost > /dev/null   # seed derived tasks.json before the first hook

say "worker goes idle (Stop hook) — a known liveness baseline"
printf '{"session_id":"s-sup","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"STATUS: DONE — scaffold"}' "$WTDIR" | "$GV" hook stop

# --- start the loop, and a watcher armed ahead of the merge in step 6 ---
say "gv supervise --interval 1s in the background"
"$GV" supervise --interval 1s > "$SCRATCH/supervise.out" 2>"$SCRATCH/supervise.err" &
SUPERVISE_PID=$!
sleep 1
kill -0 "$SUPERVISE_PID" 2>/dev/null || fail "gv supervise exited immediately: $(cat "$SCRATCH/supervise.err")"

say "gv watch --until pr_merged armed now, ahead of the merge landing in step 6"
( "$GV" watch --until pr_merged > "$SCRATCH/until.out" 2>&1; echo "exit=$?" > "$SCRATCH/until.rc" ) &
WATCH_PID=$!

# --- 2. no PR: zero delivery events -------------------------------------
say "gh: no PR -> after 3s, gv watch --replay --json shows zero delivery events"
sleep 3
timeout 2 "$GV" watch --replay --json --type all > "$SCRATCH/replay-nopr.out" 2>&1 || true
for t in pr_opened pr_updated pr_ci_failed pr_conflicting pr_ready pr_merged pr_closed; do
  grep -q "\"type\":\"$t\"" "$SCRATCH/replay-nopr.out" && fail "delivery event $t fired with no PR: $(cat "$SCRATCH/replay-nopr.out")"
done

# --- 3. open PR, checks pending: exactly one pr_opened, idempotent ------
say "gh: open PR, checks pending -> exactly one pr_opened"
cat > "$SCRATCH/gh-answer.json" <<'JSON'
[{"number":42,"url":"https://example.test/pr/42","state":"OPEN","mergedAt":"","isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"UNSTABLE","statusCheckRollup":[{"name":"build","status":"IN_PROGRESS"}],"comments":[]}]
JSON
sleep 2
timeout 2 "$GV" watch --replay --type all > "$SCRATCH/replay-opened.out" 2>&1 || true
[ "$(grep -c 'pr_opened' "$SCRATCH/replay-opened.out")" -eq 1 ] || fail "expected exactly one pr_opened: $(cat "$SCRATCH/replay-opened.out")"

say "…repeat the same answer: still exactly one"
sleep 2
timeout 2 "$GV" watch --replay --type all > "$SCRATCH/replay-repeat.out" 2>&1 || true
[ "$(grep -c 'pr_opened' "$SCRATCH/replay-repeat.out")" -eq 1 ] || fail "pr_opened re-fired on an unchanged observation: $(cat "$SCRATCH/replay-repeat.out")"

# --- 4. a CANCELLED check: pr_ci_failed naming it ------------------------
say "gh: a CANCELLED check -> pr_ci_failed naming it"
cat > "$SCRATCH/gh-answer.json" <<'JSON'
[{"number":42,"url":"https://example.test/pr/42","state":"OPEN","mergedAt":"","isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"UNSTABLE","statusCheckRollup":[{"name":"lint","conclusion":"CANCELLED"}],"comments":[]}]
JSON
sleep 2
timeout 2 "$GV" watch --replay --type all --json > "$SCRATCH/replay-cifailed.out" 2>&1 || true
grep -q '"type":"pr_ci_failed"' "$SCRATCH/replay-cifailed.out" || fail "pr_ci_failed did not fire: $(cat "$SCRATCH/replay-cifailed.out")"
grep '"type":"pr_ci_failed"' "$SCRATCH/replay-cifailed.out" | grep -q '"failing":"lint"' || fail "pr_ci_failed missing failing=lint: $(cat "$SCRATCH/replay-cifailed.out")"

# --- 5. gh lookup failure: nothing appended, folded state survives ------
say "gh: exit 1 (lookup failure) -> nothing appended, delivery.state still ci_failed"
EV_LINES=$(wc -l < "$EVENTS")
touch "$SCRATCH/gh-fail"
sleep 2
[ "$(wc -l < "$EVENTS")" -eq "$EV_LINES" ] || fail "a failed gh lookup appended an event"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-fail.json"
python3 - "$SCRATCH/ls-fail.json" <<'PY' || fail "delivery.state did not survive a failed lookup"
import json, sys
row = [t for t in json.load(open(sys.argv[1]))["tasks"] if t["ticket"] == "task-001"][0]
sys.exit(0 if row.get("delivery", {}).get("state") == "ci_failed" else 1)
PY
rm -f "$SCRATCH/gh-fail"

# --- 6. green + CLEAN -> pr_ready; then MERGED -> pr_merged --------------
say "gh: green + CLEAN -> pr_ready"
cat > "$SCRATCH/gh-answer.json" <<'JSON'
[{"number":42,"url":"https://example.test/pr/42","state":"OPEN","mergedAt":"","isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","statusCheckRollup":[{"name":"build","conclusion":"SUCCESS"}],"comments":[]}]
JSON
sleep 2
timeout 2 "$GV" watch --replay --type all > "$SCRATCH/replay-ready.out" 2>&1 || true
grep -q 'pr_ready' "$SCRATCH/replay-ready.out" || fail "pr_ready did not fire: $(cat "$SCRATCH/replay-ready.out")"

say "gh: MERGED -> pr_merged"
cat > "$SCRATCH/gh-answer.json" <<'JSON'
[{"number":42,"url":"https://example.test/pr/42","state":"MERGED","mergedAt":"2026-09-05T00:00:00Z","isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","statusCheckRollup":[{"name":"build","conclusion":"SUCCESS"}],"comments":[]}]
JSON
for _ in $(seq 1 20); do
  [ -f "$SCRATCH/until.rc" ] && break
  sleep 0.5
done
grep -q 'pr_merged' "$EVENTS" || fail "pr_merged never landed in events.jsonl: $(tail -5 "$EVENTS")"

# --- 10. the watcher armed before step 6 fired exactly then -------------
say "gv watch --until pr_merged (armed before step 6) exits 0"
wait "$WATCH_PID" 2>/dev/null || true
WATCH_PID=""
grep -q 'exit=0' "$SCRATCH/until.rc" 2>/dev/null || fail "watch --until pr_merged did not fire: $(cat "$SCRATCH/until.rc" 2>/dev/null) / $(cat "$SCRATCH/until.out")"

# --- 7. worker_waiting after >=10s, not before; recovered after --------
say "stub pane -> AskUserQuestion menu: worker_waiting after >=10s, not before"
echo waiting > "$SCRATCH/pane-mode"
sleep 8
timeout 2 "$GV" watch --replay --type all > "$SCRATCH/waiting-early.out" 2>&1 || true
grep -q 'worker_waiting' "$SCRATCH/waiting-early.out" && fail "worker_waiting fired before the 10s hysteresis window: $(cat "$SCRATCH/waiting-early.out")"
sleep 5
timeout 2 "$GV" watch --replay --type all > "$SCRATCH/waiting-late.out" 2>&1 || true
grep -q 'worker_waiting' "$SCRATCH/waiting-late.out" || fail "worker_waiting never fired: $(cat "$SCRATCH/waiting-late.out")"

say "stub pane back to idle -> worker_recovered"
echo idle > "$SCRATCH/pane-mode"
sleep 2
timeout 2 "$GV" watch --replay --type all > "$SCRATCH/recovered.out" 2>&1 || true
grep -q 'worker_recovered' "$SCRATCH/recovered.out" || fail "worker_recovered did not fire: $(cat "$SCRATCH/recovered.out")"

# --- 8. usage-limit marker: worker_errored within one interval ----------
say "stub pane -> usage-limit marker: worker_errored within one interval"
echo errored > "$SCRATCH/pane-mode"
sleep 2
timeout 2 "$GV" watch --replay --type all --json > "$SCRATCH/errored.out" 2>&1 || true
grep -q '"type":"worker_errored"' "$SCRATCH/errored.out" || fail "worker_errored did not fire: $(cat "$SCRATCH/errored.out")"
grep '"type":"worker_errored"' "$SCRATCH/errored.out" | tail -1 | grep -q '"reason":"usage_limit"' || fail "worker_errored missing reason=usage_limit: $(cat "$SCRATCH/errored.out")"
echo idle > "$SCRATCH/pane-mode"

# --- 9. a second gv supervise refuses, naming the pid -------------------
say "a second gv supervise exits non-zero, naming the pid already emitting"
set +e
"$GV" supervise --interval 1s > "$SCRATCH/supervise2.out" 2>&1
RC=$?
set -e
[ "$RC" -ne 0 ] || fail "a second gv supervise must refuse while the first still holds the lock"
grep -q "already supervised (pid $SUPERVISE_PID)" "$SCRATCH/supervise2.out" || fail "refusal did not name the holder's pid: $(cat "$SCRATCH/supervise2.out")"

kill "$SUPERVISE_PID" 2>/dev/null || true
wait "$SUPERVISE_PID" 2>/dev/null || true
SUPERVISE_PID=""

say "the lock releases on exit: a fresh gv supervise --once succeeds"
"$GV" supervise --interval 1s --once > "$SCRATCH/supervise-once.out" 2>&1 || fail "gv supervise --once failed after the holder exited: $(cat "$SCRATCH/supervise-once.out")"

# --- 11. e2e/plugin.sh green; real-server canary untouched --------------
say "e2e/plugin.sh still green"
"$REPO_ROOT/e2e/plugin.sh" > "$SCRATCH/plugin.out" 2>&1 || { cat "$SCRATCH/plugin.out"; fail "e2e/plugin.sh failed"; }

say "real tmux server untouched"
REAL_TMUX_AFTER="$(real_tmux)"
[ "$REAL_TMUX_BEFORE" = "$REAL_TMUX_AFTER" ] || fail "real tmux session list changed: before=[$REAL_TMUX_BEFORE] after=[$REAL_TMUX_AFTER]"

say "PASS — gv supervise: headless loop, single-emitter lock, ntfy pushes, 11-type stream"
