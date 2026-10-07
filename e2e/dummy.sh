#!/usr/bin/env bash
# Phase-0 E2E: the dummy-data pattern (docs/seed-manifest.md).
#
# Exercises init → grab (list + start) → hook ownership no-op → ls →
# untrack --rm → re-grab → done (degraded no-remote path) with ZERO risk to
# live state: scratch HOME (config), scratch GROVE_STATE_DIR (state), a
# scratch remote-less git repo, the worker command stubbed to `echo`, and
# its OWN tmux server (unset TMUX + scratch TMUX_TMPDIR — tmux-discipline
# rule 1). Asserts at the end that live overstory AND grove state were
# untouched, and that the REAL tmux server's session list is unchanged.
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-e2e.XXXXXX)"

# Build with the real environment BEFORE pointing HOME at the scratch dir —
# otherwise Go re-downloads its module cache into the scratch HOME (and the
# cache is read-only, breaking cleanup).
say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

export HOME="$SCRATCH/home"
export GROVE_STATE_DIR="$SCRATCH/state"
mkdir -p "$HOME" "$GROVE_STATE_DIR"

REAL_HOME="$(dscl . -read /Users/"$(whoami)" NFSHomeDirectory 2>/dev/null | awk '{print $2}' || echo "/Users/$(whoami)")"
snapshot_live() {
  # tasks.json is deliberately absent: it is a DERIVED view that live ovs
  # (hooks on running sessions) rewrites concurrently — its mtime churning
  # is not evidence grove touched anything. The append-only events.jsonl
  # files + configs are the real canaries.
  for f in "$REAL_HOME/.local/state/overstory/events.jsonl" \
           "$REAL_HOME/.config/overstory/config.yaml" \
           "$REAL_HOME/.local/state/grove/events.jsonl" \
           "$REAL_HOME/.config/grove/config.yaml" \
           "$REAL_HOME/.cc-work/settings.json"; do
    [ -e "$f" ] && stat -f '%N %m %z' "$f"
  done
  true
}
LIVE_BEFORE="$(snapshot_live)"
real_tmux() { env -u TMUX -u TMUX_PANE -u TMUX_TMPDIR tmux list-sessions -F '#{session_name}' 2>/dev/null | sort; true; }
REAL_TMUX_BEFORE="$(real_tmux)"

# $TMUX beats TMUX_TMPDIR in tmux's socket resolution — launched from inside
# a tmux pane, TMUX_TMPDIR alone is a silent no-op and every tmux call
# (grab's session, cleanup's kill) hits the REAL server. Unset first.
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"   # isolated tmux server — never the user's
mkdir -p "$TMUX_TMPDIR"

DUMMY="$SCRATCH/repos/dummy"
# grove-29 P2: a workspace's cockpit + workers collapse into one
# grove-<label> session (was the per-repo pr-<repo>). The dummy repo is its
# own workspace, label = its dir base ("dummy").
SESSION="grove-dummy"
cleanup() {
  # Scoped to the isolated server: TMUX is unset and TMUX_TMPDIR is ours.
  # A bare kill-server once took down every worker on the machine (2026-07-07).
  env -u TMUX TMUX_TMPDIR="$TMUX_TMPDIR" tmux kill-server 2>/dev/null || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -S "$TMUX_TMPDIR/tmux-$(id -u)/default" ] || break
    sleep 0.2
  done
  kill "${ORPHAN_PID:-}" 2>/dev/null || true   # grove-92 seeded lookalike, if a fail left it running
  # grove-156 seeded worktree-path sleepers, if a fail left them running
  kill "${SLEEPER_PID:-}" "${SLEEPER2_PID:-}" "${ZOMBIE_PID:-}" 2>/dev/null || true
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH"
}
trap cleanup EXIT

say "scratch repo (no remote)"
mkdir -p "$DUMMY" && cd "$DUMMY"
git init -q -b main
git config user.email e2e@grove.test && git config user.name "grove e2e"
echo "# dummy" > README.md
git add -A && git commit -qm "init"

say "gv init --yes"
"$GV" init --yes > "$SCRATCH/init.out"; cat "$SCRATCH/init.out"
grep -q 'config updated' "$SCRATCH/init.out" || fail "init did not register the repo"
[ -f .grove/tasks/task-001.md ] || fail "sample task missing"
WCFG="$DUMMY/.grove/config.yaml"
grep -q 'kind: markdown' "$WCFG" || fail "workspace config missing markdown provider"

say "gv init idempotent"
# capture-then-grep everywhere a gv/tmux command feeds grep -q: grep exits
# at first match and SIGPIPEs the producer's remaining output, which
# pipefail then reports as failure (observed flake).
"$GV" init --yes > "$SCRATCH/init2.out"
grep -q 'already up to date' "$SCRATCH/init2.out" || fail "re-init not idempotent"

say "stub worker command to echo"
perl -pi -e 's/^(\s*)base: main$/$1base: main\n$1claude: echo/' "$WCFG"
grep -q 'claude: echo' "$WCFG" || fail "claude stub not written"

say "gv grab (no args) lists the backlog"
"$GV" grab | tee "$SCRATCH/backlog.out"
grep -q 'task-001' "$SCRATCH/backlog.out" || fail "backlog missing task-001"

say "gv grab task-001"
"$GV" grab task-001 | tee "$SCRATCH/grab.out"
WT="$SCRATCH/repos/.worktrees/dummy"
ls -d "$WT"/task-001-* >/dev/null || fail "worktree not created under $WT"
WTDIR="$(ls -d "$WT"/task-001-*)"
tmux list-windows -t "$SESSION" > "$SCRATCH/windows.out"
grep -q task-001 "$SCRATCH/windows.out" || fail "tmux window missing"
PROMPT="$GROVE_STATE_DIR/prompts/task-001.txt"
[ -f "$PROMPT" ] || fail "kickoff prompt not written"
grep -q 'status: in-progress' "$PROMPT" || fail "prompt missing markdown start verb"
grep -qi 'linear' "$PROMPT" && fail "markdown prompt leaks Linear" || true
grep -q 'STATUS: QUESTION' "$PROMPT" || fail "prompt missing sentinel contract"

say "dedup: second grab of an in-flight task refuses"
("$GV" grab task-001 2>&1 || true) | grep -q 'already tracked' || fail "dedup missing"

say "gv ls"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls.json"
grep -q '"task-001"' "$SCRATCH/ls.json" || fail "ls missing task"

say "hook ownership contract: untracked cwd is a silent no-op"
EV_LINES=$(wc -l < "$GROVE_STATE_DIR/events.jsonl")
printf '{"session_id":"s-e2e","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"STATUS: DONE — not ours"}' \
  "$SCRATCH/not-a-worktree" | "$GV" hook stop
[ "$(wc -l < "$GROVE_STATE_DIR/events.jsonl")" -eq "$EV_LINES" ] || fail "hook wrote an event for an untracked cwd"

say "hook ownership contract: tracked cwd IS captured"
printf '{"session_id":"s-e2e-2","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"STATUS: QUESTION — tabs or spaces?"}' \
  "$WTDIR" | "$GV" hook stop
grep -q 's-e2e-2\|tabs or spaces' "$GROVE_STATE_DIR/events.jsonl" || fail "hook ignored a tracked cwd"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls2.json"
grep -q 'tabs or spaces' "$SCRATCH/ls2.json" || fail "question not folded into task state"

say "gv park kills the session and logs a durable parked event (grove-33)"
tmux has-session -t "$SESSION" 2>/dev/null || fail "session should be live before park"
EV_BEFORE_PARK=$(wc -l < "$GROVE_STATE_DIR/events.jsonl")
"$GV" park | tee "$SCRATCH/park.out"
tmux has-session -t "$SESSION" 2>/dev/null && fail "park did not kill the session" || true
grep -q '"type":"workspace_parked"' "$GROVE_STATE_DIR/events.jsonl" || fail "park did not append workspace_parked"
[ "$(wc -l < "$GROVE_STATE_DIR/events.jsonl")" -gt "$EV_BEFORE_PARK" ] || fail "park appended no event"

say "audit sees the parked task and never calls it abandoned"
"$GV" audit --json > "$SCRATCH/audit-parked.json"
grep -q '"parked": *true' "$SCRATCH/audit-parked.json" || fail "audit facts do not reflect the parked marker"
grep -q '"class": *"abandoned"' "$SCRATCH/audit-parked.json" && fail "a parked task must never be abandoned" || true

say "gv park refuses when nothing is running"
("$GV" park 2>&1 || true) | grep -q 'not running' || fail "park should refuse with no live session"

say "gv adopt revives the parked worker and clears the marker"
"$GV" adopt task-001 | tee "$SCRATCH/adopt.out"
tmux has-session -t "$SESSION" 2>/dev/null || fail "adopt did not rebuild the session"
tmux list-windows -t "$SESSION" > "$SCRATCH/windows-adopt.out"
grep -q task-001 "$SCRATCH/windows-adopt.out" || fail "adopt did not recreate the worker window"
"$GV" audit --json > "$SCRATCH/audit-adopted.json"
grep -q '"parked": *true' "$SCRATCH/audit-adopted.json" && fail "adopt did not clear the parked marker" || true

# --- gv pause: park ONE worker, resume losslessly with adopt (grove-90) ---

say "seed a session id via the SessionStart hook so pause→adopt can resume"
printf '{"session_id":"s-pause-1","cwd":"%s","hook_event_name":"SessionStart"}' "$WTDIR" | "$GV" hook session-start
grep -q 's-pause-1' "$GROVE_STATE_DIR/events.jsonl" || fail "session-start hook not captured"

say "hook session gate: a foreign session at the worker's cwd is a silent no-op (grove-250)"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-sid.json" # fold → tasks.json carries the recorded id
grep -q '"claude_session_id": *"s-pause-1"' "$SCRATCH/ls-sid.json" || fail "ls --json missing the recorded session id"
EV_BEFORE_INTRUDER=$(wc -l < "$GROVE_STATE_DIR/events.jsonl")
printf '{"session_id":"s-intruder","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"orchestrator chat reply, no sentinel"}' \
  "$WTDIR" | "$GV" hook stop
[ "$(wc -l < "$GROVE_STATE_DIR/events.jsonl")" -eq "$EV_BEFORE_INTRUDER" ] || fail "a foreign session's stop hijacked the worker (appended an event)"
grep -q 's-intruder' "$GROVE_STATE_DIR/events.jsonl" && fail "intruder session id leaked into events.jsonl" || true

say "hook session gate: a nested claude in the live worker's worktree cannot re-register, idle or kill it (grove-339)"
# The full hook set a nested `claude -p` fires from the worktree (Claude
# Code 2.1.283 shapes): its own session id on every payload, SessionStart
# included. That start used to be exempt and re-point the task at itself.
EV_BEFORE_NESTED=$(wc -l < "$GROVE_STATE_DIR/events.jsonl")
printf '{"session_id":"s-nested","cwd":"%s","hook_event_name":"SessionStart","source":"startup"}' "$WTDIR" | "$GV" hook session-start
"$GV" ls --json --no-pr --no-cost > /dev/null # a fold between hooks must not help it either
printf '{"session_id":"s-nested","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"OK"}' "$WTDIR" | "$GV" hook stop
printf '{"session_id":"s-nested","cwd":"%s","hook_event_name":"SessionEnd","reason":"other"}' "$WTDIR" | "$GV" hook session-end
[ "$(wc -l < "$GROVE_STATE_DIR/events.jsonl")" -eq "$EV_BEFORE_NESTED" ] || fail "a nested session's hooks appended to the worker's task"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-nested.json"
grep -q '"claude_session_id": *"s-pause-1"' "$SCRATCH/ls-nested.json" || fail "nested session re-registered the task"
grep -q '"agent": *"dead"' "$SCRATCH/ls-nested.json" && fail "nested session-end stamped the live worker dead" || true
grep -q '"agent": *"idle"' "$SCRATCH/ls-nested.json" && fail "nested stop stamped the live worker idle" || true

say "gv pause guards a mid-turn worker (agent working) behind --force"
("$GV" pause task-001 2>&1 || true) > "$SCRATCH/pause-guard.out"
grep -q 'mid-turn' "$SCRATCH/pause-guard.out" || fail "pause should warn about the in-flight turn"
tmux list-windows -t "$SESSION" > "$SCRATCH/windows-guard.out"
grep -q task-001 "$SCRATCH/windows-guard.out" || fail "guarded pause must not kill the window"

say "gv pause --force parks the worker: window dies, worktree survives"
"$GV" pause task-001 --force | tee "$SCRATCH/pause.out"
tmux list-windows -t "$SESSION" > "$SCRATCH/windows-paused.out"
grep -q task-001 "$SCRATCH/windows-paused.out" && fail "pause did not kill the worker window" || true
[ -d "$WTDIR" ] || fail "pause must leave the worktree untouched"
grep -q '"type":"task_paused"' "$GROVE_STATE_DIR/events.jsonl" || fail "pause did not append task_paused"

say "paused stays on the plate: ls --json carries paused, the table shows ⏸"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-paused.json"
grep -q '"paused": *true' "$SCRATCH/ls-paused.json" || fail "ls --json missing the paused flag"
"$GV" ls --no-pr --no-cost > "$SCRATCH/ls-paused.txt"
grep -q '⏸ paused' "$SCRATCH/ls-paused.txt" || fail "ls table missing the ⏸ paused status"

say "gv pause refuses a second time (already paused)"
("$GV" pause task-001 2>&1 || true) | grep -q 'already paused' || fail "double pause should refuse"

say "audit classifies paused — never disconnected, never abandoned (grove-90)"
"$GV" audit --json > "$SCRATCH/audit-paused.json"
grep -q '"class": *"paused"' "$SCRATCH/audit-paused.json" || fail "audit did not classify the task paused"
grep -q '"suggestion": *"gv adopt"' "$SCRATCH/audit-paused.json" || fail "paused suggestion should be gv adopt"
grep -q '"class": *"disconnected"' "$SCRATCH/audit-paused.json" && fail "paused fell through to disconnected" || true
grep -q '"class": *"abandoned"' "$SCRATCH/audit-paused.json" && fail "paused fell through to abandoned" || true

say "gv adopt resumes the paused worker via --resume <sessionID>"
"$GV" adopt task-001 | tee "$SCRATCH/adopt-paused.out"
grep -q 'resume s-pause-1' "$SCRATCH/adopt-paused.out" || fail "adopt did not resume the stored session id"
tmux list-windows -t "$SESSION" > "$SCRATCH/windows-resumed.out"
grep -q task-001 "$SCRATCH/windows-resumed.out" || fail "adopt did not recreate the worker window"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-resumed.json"
grep -q '"paused": *true' "$SCRATCH/ls-resumed.json" && fail "adopt did not clear the paused flag" || true

# --- gv sweep offers: idle → pause, orphan process → kill, paused invisible (grove-92) ---
# Every sweep below runs with `ps` stubbed via PATH so the process table gv
# sees is fully controlled — a piped `y` must never be able to reach a real
# process on this machine. The "orphan" is a sleep we own, dressed in
# claude-shaped args by the stub; the SIGTERM goes to its real pid.

# --- Stop hook done gate (grove-441): evidence or no DONE ---

say "done gate (block): a dirty worktree turns STATUS: DONE into a block decision"
perl -pi -e 's/^(\s*)claude: echo$/$1claude: echo\n$1done_gate: block/' "$WCFG"
grep -q 'done_gate: block' "$WCFG" || fail "done_gate not written"
echo wip > "$WTDIR/scratch.txt"
printf '{"session_id":"s-pause-1","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"STATUS: DONE — all wrapped up"}' \
  "$WTDIR" | "$GV" hook stop > "$SCRATCH/gate-block.out"
grep -q '"decision":"block"' "$SCRATCH/gate-block.out" || fail "dirty DONE under done_gate: block did not block: $(cat "$SCRATCH/gate-block.out")"
grep -q '1 uncommitted file' "$SCRATCH/gate-block.out" || fail "block reason does not name the dirty file count"
tail -n 1 "$GROVE_STATE_DIR/events.jsonl" | grep -q '"sentinel":"done_unverified"' || fail "blocked DONE not recorded as done_unverified"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-gate.json"
grep -q '"sentinel": *"done_unverified"' "$SCRATCH/ls-gate.json" || fail "gv ls --json missing done_unverified"

say "done gate (block): a re-entry stop (stop_hook_active) is never blocked"
printf '{"session_id":"s-pause-1","cwd":"%s","hook_event_name":"Stop","stop_hook_active":true,"last_assistant_message":"STATUS: DONE — still claiming"}' \
  "$WTDIR" | "$GV" hook stop > "$SCRATCH/gate-active.out"
[ -s "$SCRATCH/gate-active.out" ] && fail "stop_hook_active stop was blocked: $(cat "$SCRATCH/gate-active.out")" || true

say "done gate (warn, the default): no block, verdict recorded"
perl -pi -e 's/done_gate: block/done_gate: warn/' "$WCFG"
printf '{"session_id":"s-pause-1","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"STATUS: DONE — all wrapped up"}' \
  "$WTDIR" | "$GV" hook stop > "$SCRATCH/gate-warn.out"
[ -s "$SCRATCH/gate-warn.out" ] && fail "warn mode wrote a decision: $(cat "$SCRATCH/gate-warn.out")" || true
tail -n 1 "$GROVE_STATE_DIR/events.jsonl" > "$SCRATCH/gate-warn.ev"
grep -q '"sentinel":"done"' "$SCRATCH/gate-warn.ev" || fail "warn mode must keep the done sentinel"
grep -q '"gate":"warn"' "$SCRATCH/gate-warn.ev" || fail "warn mode must record the gate verdict"
grep -q '"gate_reason":"1 uncommitted file"' "$SCRATCH/gate-warn.ev" || fail "warn verdict missing the reason"

say "done gate: the tolerant sentinel parser takes the LAST STATUS line and a bare STATUS: DONE"
rm "$WTDIR/scratch.txt"
perl -pi -e 's/^\s*done_gate: warn\n//' "$WCFG"
grep -q done_gate "$WCFG" && fail "done_gate key not removed" || true
printf '{"session_id":"s-pause-1","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"I said STATUS: DONE — too early.\\n\\n**STATUS: BLOCKED**"}' \
  "$WTDIR" | "$GV" hook stop > "$SCRATCH/gate-last.out"
[ -s "$SCRATCH/gate-last.out" ] && fail "BLOCKED must never be gated" || true
tail -n 1 "$GROVE_STATE_DIR/events.jsonl" | grep -q '"sentinel":"blocked"' || fail "last STATUS line (bold, bare BLOCKED) did not win"

say "stub ps (empty table) so orphan scans are deterministic"
STUBBIN="$SCRATCH/bin"
mkdir -p "$STUBBIN"
cat > "$STUBBIN/ps" <<'PSEOF'
#!/bin/sh
echo "  PID  PPID  %CPU    RSS ELAPSED ARGS"
PSEOF
chmod +x "$STUBBIN/ps"
REAL_PATH="$PATH"
export PATH="$STUBBIN:$PATH"

say "make task-001 idle: agent done + quiet past a tiny idle_after"
printf 'audit:\n  idle_after: 1ms\n' >> "$WCFG"
EV_BEFORE_OWN_STOP=$(wc -l < "$GROVE_STATE_DIR/events.jsonl")
printf '{"session_id":"s-pause-1","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"STATUS: DONE — wrapped up"}' \
  "$WTDIR" | "$GV" hook stop
[ "$(wc -l < "$GROVE_STATE_DIR/events.jsonl")" -eq $((EV_BEFORE_OWN_STOP + 1)) ] || fail "the worker's own stop did not land after the session gate (grove-250)"
tail -n 1 "$GROVE_STATE_DIR/events.jsonl" | grep -q '"session_id":"s-pause-1"' || fail "agent_status missing additive data.session_id (grove-250)"
"$GV" audit --json > "$SCRATCH/audit-idle.json"
grep -q '"class": *"idle"' "$SCRATCH/audit-idle.json" || fail "done+quiet worker should classify idle"

say "sweep --json offers pause for the idle worker without acting"
"$GV" sweep --json > "$SCRATCH/sweep-idle.json"
grep -q '"class": *"idle"' "$SCRATCH/sweep-idle.json" || fail "sweep --json missing the idle offer"
grep -q '"action": *"pause' "$SCRATCH/sweep-idle.json" || fail "idle offer action should be pause"
tmux list-windows -t "$SESSION" > "$SCRATCH/windows-sweepjson.out"
grep -q task-001 "$SCRATCH/windows-sweepjson.out" || fail "sweep --json must not act"

say "pause offer declined → worker untouched"
PAUSED_BEFORE=$(grep -c '"type":"task_paused"' "$GROVE_STATE_DIR/events.jsonl")
printf 'n\n' | "$GV" sweep > "$SCRATCH/sweep-decline.out"
grep -q 'pause' "$SCRATCH/sweep-decline.out" || fail "sweep did not offer pause interactively"
tmux list-windows -t "$SESSION" > "$SCRATCH/windows-decline.out"
grep -q task-001 "$SCRATCH/windows-decline.out" || fail "declined sweep must not kill the window"
[ "$(grep -c '"type":"task_paused"' "$GROVE_STATE_DIR/events.jsonl")" -eq "$PAUSED_BEFORE" ] || fail "declined sweep appended task_paused"

say "pause offer confirmed → window gone, task paused"
printf 'y\n' | "$GV" sweep > "$SCRATCH/sweep-confirm.out"
tmux list-windows -t "$SESSION" > "$SCRATCH/windows-swept.out"
grep -q task-001 "$SCRATCH/windows-swept.out" && fail "confirmed sweep did not kill the idle window" || true
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-swept.json"
grep -q '"paused": *true' "$SCRATCH/ls-swept.json" || fail "swept idle task should be paused"

say "paused is invisible to sweep: ZERO offers of any kind (grove-92 hard rule)"
"$GV" sweep --json > "$SCRATCH/sweep-paused.json"
grep -q 'task-001' "$SCRATCH/sweep-paused.json" && fail "paused task appeared in sweep offers" || true

say "seed an orphan-lookalike: real sleep pid, claude-shaped args via the ps stub"
sleep 300 &
ORPHAN_PID=$!
cat > "$STUBBIN/ps" <<PSEOF
#!/bin/sh
echo "  PID  PPID  %CPU    RSS ELAPSED ARGS"
echo "  $ORPHAN_PID     1   0.0   1024   05:00 claude --resume orphan-e2e"
PSEOF
"$GV" sweep --json > "$SCRATCH/sweep-orphan.json"
grep -q '"pid": *'"$ORPHAN_PID" "$SCRATCH/sweep-orphan.json" || fail "sweep --json missing the orphan-process kill offer"
kill -0 "$ORPHAN_PID" 2>/dev/null || fail "sweep --json must not kill"

say "kill offer declined → process untouched"
printf 'n\n' | "$GV" sweep > "$SCRATCH/sweep-orphan-decline.out"
grep -q "$ORPHAN_PID" "$SCRATCH/sweep-orphan-decline.out" || fail "sweep did not offer the kill interactively"
kill -0 "$ORPHAN_PID" 2>/dev/null || fail "declined kill must leave the process alone"

say "kill offer confirmed → SIGTERM, process gone (never SIGKILL)"
printf 'y\n' | "$GV" sweep > "$SCRATCH/sweep-orphan-confirm.out"
grep -q "pid $ORPHAN_PID terminated" "$SCRATCH/sweep-orphan-confirm.out" || fail "sweep did not report the SIGTERM landing"
kill -0 "$ORPHAN_PID" 2>/dev/null && fail "confirmed kill left the process alive" || true

say "restore real ps; adopt the paused worker back for the untrack leg"
export PATH="$REAL_PATH"
"$GV" adopt task-001 > "$SCRATCH/adopt-sweep.out"
tmux list-windows -t "$SESSION" > "$SCRATCH/windows-readopt.out"
grep -q task-001 "$SCRATCH/windows-readopt.out" || fail "adopt after sweep-pause did not rebuild the window"

say "seed a daemonized build child: real sleeper with the worktree path in argv (grove-156)"
# grove stores the worktree symlink-resolved; the argv must embed that
# real path for the reap-time matcher to own it.
WTDIR_REAL="$(cd "$WTDIR" && pwd -P)"
perl -e 'sleep 300' -- "$WTDIR_REAL" &
SLEEPER_PID=$!
kill -0 "$SLEEPER_PID" 2>/dev/null || fail "sleeper did not start"

say "gv untrack --rm --force (degraded: no remote to verify against) kills worktree children"
"$GV" untrack task-001 --rm --force | tee "$SCRATCH/untrack.out"
[ ! -d "$WTDIR" ] || fail "worktree survived untrack --rm"
grep -q "pid $SLEEPER_PID terminated" "$SCRATCH/untrack.out" || fail "untrack --rm did not report killing the worktree child"
kill -0 "$SLEEPER_PID" 2>/dev/null && fail "worktree child survived untrack --rm" || true
tmux list-windows -t "$SESSION" > "$SCRATCH/windows2.out" 2>/dev/null || true
grep -q task-001 "$SCRATCH/windows2.out" && fail "window survived untrack" || true

say "re-grab after untrack (grove-146: --brief attaches an operator brief section)"
# grove-435: --effort rides along so the done-path ledger row below carries it.
"$GV" grab task-001 --brief "Only touch the staging config, do not deploy." --effort xhigh >/dev/null
ls -d "$WT"/task-001-* >/dev/null || fail "re-grab did not recreate the worktree"
grep -q '## Operator brief' "$PROMPT" || fail "prompt missing the Operator brief section"
grep -q 'Only touch the staging config, do not deploy.' "$PROMPT" || fail "prompt missing the brief text"

say "gv done refuses without --force (no remote = no merge proof)"
if "$GV" done task-001 >"$SCRATCH/done1.out" 2>&1; then fail "done should have refused"; fi
grep -q 'no remote' "$SCRATCH/done1.out" || fail "done refusal must explain the no-remote degradation"

say "spend ledger: enable recording (persists in the state dir)"
"$GV" cost --record on > "$SCRATCH/record.out"
grep -q 'recording on' "$SCRATCH/record.out" || fail "cost --record on failed"
grep -q '^on$' "$GROVE_STATE_DIR/cost-recording" || fail "recording toggle not persisted"

say "spend ledger: plant a fake transcript so done has cost to snapshot"
# grove stores the worktree symlink-resolved (/tmp → /private/tmp on
# macOS); the transcript project-dir encoding starts from that real path.
WTDIR2="$(cd "$(ls -d "$WT"/task-001-*)" && pwd -P)"
ENC="$(printf '%s' "$WTDIR2" | tr '/.' '--')"
PROJ="$HOME/.claude/projects/$ENC"
mkdir -p "$PROJ"
# Two models so the per-task model breakdown (grove-14) has a mix to show.
# At current sonnet-5 pricing ($2/$10, grove-249) this totals ≈$0.0233;
# the tiny haiku entry keeps it under $0.025 (still rounds to $0.02).
#
# grove-289: the fixture also carries a `gv cost --context` shape — (a) a
# second assistant line sharing call 1's message/request id (dedup: still
# one API call), (b) a Bash tool_use (`cat internal/x.go`, classifies
# bash_read) sharing call 2's ids (a multi-block turn) plus its 2,000-char
# /60-line tool_result, (c) a compact_boundary, (d) a genuinely new third
# call sharing NO prior ids. Total: 3 deduped API calls. The (a)/(d)
# duplicate lines' own usage numbers are irrelevant — Dedup keeps only the
# first occurrence of each id pair — and call 3's usage is kept tiny so
# the ledger snapshot below still rounds to $0.02.
# 60 lines of 32 a's (33 decoded chars each with the JSON-escaped
# newline) + a trailing 20 z's with no newline: 2,000 decoded chars, 60
# decoded newlines — no JSON escaping needed (only a/z and literal \n).
BASH_RESULT_LINE='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n'
BASH_RESULT=""
for _ in $(seq 1 60); do BASH_RESULT="${BASH_RESULT}${BASH_RESULT_LINE}"; done
BASH_RESULT="${BASH_RESULT}zzzzzzzzzzzzzzzzzzzz"
{
  printf '%s\n' '{"type":"assistant","timestamp":"2026-07-07T10:00:00.000Z","requestId":"req-e2e","message":{"id":"msg-e2e","model":"claude-sonnet-5","content":[{"type":"text","text":"looking"}],"usage":{"input_tokens":1000,"output_tokens":2000,"cache_read_input_tokens":500,"cache_creation_input_tokens":100}}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-07-07T10:01:00.000Z","requestId":"req-e2e2","message":{"id":"msg-e2e2","model":"claude-haiku-4-5","content":[{"type":"text","text":"looking too"}],"usage":{"input_tokens":500,"output_tokens":100,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-07-07T10:00:30.000Z","requestId":"req-e2e","message":{"id":"msg-e2e","model":"claude-sonnet-5","content":[{"type":"text","text":"still call 1"}],"usage":{"input_tokens":1000,"output_tokens":2000,"cache_read_input_tokens":500,"cache_creation_input_tokens":100}}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-07-07T10:01:30.000Z","requestId":"req-e2e2","message":{"id":"msg-e2e2","model":"claude-haiku-4-5","content":[{"type":"tool_use","id":"tu-e2e-1","name":"Bash","input":{"command":"cat internal/x.go"}}],"usage":{"input_tokens":500,"output_tokens":100,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}'
  printf '{"type":"user","timestamp":"2026-07-07T10:01:31.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu-e2e-1","content":"%s"}]}}\n' "$BASH_RESULT"
  printf '%s\n' '{"type":"system","subtype":"compact_boundary","timestamp":"2026-07-07T10:02:00.000Z","compactMetadata":{"trigger":"auto","preTokens":150000,"postTokens":9000}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-07-07T10:03:00.000Z","requestId":"req-e2e3","message":{"id":"msg-e2e3","model":"claude-sonnet-5","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}'
} > "$PROJ/e2e-session.jsonl"

say "gv cost --context: per-call decomposition, growth buckets, compactions"
"$GV" cost --context task-001 --json > "$SCRATCH/cost-context.json"
grep -q '"api_calls": 3' "$SCRATCH/cost-context.json" || fail "api_calls != 3"
sed -n '/"compactions": \[/,/\]/p' "$SCRATCH/cost-context.json" | grep -q '"trigger": "auto"' \
  || fail "compaction trigger != auto"
sed -n '/"growth": {/,/}/p' "$SCRATCH/cost-context.json" | grep -q '"bash_read": 0[,}]' \
  && fail "growth.bash_read is zero"
sed -n '/"growth": {/,/}/p' "$SCRATCH/cost-context.json" | grep -q '"bash_read":' \
  || fail "growth.bash_read missing"
TOP_BUCKET="$(sed -n '/"top": \[/,/\]/p' "$SCRATCH/cost-context.json" | grep -m1 '"bucket"' | sed -E 's/.*"bucket": "([^"]+)".*/\1/')"
[ "$TOP_BUCKET" = "bash_read" ] || fail "top[0].bucket = $TOP_BUCKET, want bash_read"
sed -n '/"delegation": {/,/}/p' "$SCRATCH/cost-context.json" | grep -q '"calls": 0' \
  || fail "delegation.calls != 0 before sub.jsonl exists"

say "gv cost --context: delegation joins sub.jsonl once it exists"
echo '{"time":"2026-07-07T10:04:00Z","v":1,"ticket":"task-001","lane":"x","model":"y","mode":"raw","input_chars":10,"input_tokens":3,"output_tokens":1,"cached_tokens":0,"turns":0,"ms":5,"exit":0,"prompt_head":"p"}' >> "$GROVE_STATE_DIR/sub.jsonl"
"$GV" cost --context task-001 --json > "$SCRATCH/cost-context2.json"
sed -n '/"delegation": {/,/}/p' "$SCRATCH/cost-context2.json" | grep -q '"calls": 1' \
  || fail "delegation.calls != 1 after sub.jsonl append"

say "seed another daemonized build child for the done path (grove-156)"
perl -e 'sleep 300' -- "$WTDIR2" &
SLEEPER2_PID=$!
kill -0 "$SLEEPER2_PID" 2>/dev/null || fail "second sleeper did not start"

say "gv done --force cleans up (and kills worktree children)"
"$GV" done task-001 --force | tee "$SCRATCH/done2.out"
grep -q 'cleaned up' "$SCRATCH/done2.out" || fail "done --force failed"
ls -d "$WT"/task-001-* 2>/dev/null && fail "worktree survived done" || true
grep -q "pid $SLEEPER2_PID terminated" "$SCRATCH/done2.out" || fail "done did not report killing the worktree child"
kill -0 "$SLEEPER2_PID" 2>/dev/null && fail "worktree child survived gv done" || true
grep -q 'ledger: final snapshot recorded' "$SCRATCH/done2.out" || fail "done did not record a ledger row"

say "spend ledger: final row exists with title + description + outcome"
LEDGER="$GROVE_STATE_DIR/ledger.csv"
[ -f "$LEDGER" ] || fail "ledger.csv missing from the state dir"
grep -q 'task-001' "$LEDGER" || fail "ledger row missing ticket id"
grep -q 'Replace me' "$LEDGER" || fail "ledger row missing ticket title"
grep -q 'Describe the change' "$LEDGER" || fail "ledger row missing description snippet"
grep -q ',none,' "$LEDGER" || fail "ledger row missing PR outcome (no remote → none)"
grep -q 'sonnet' "$LEDGER" || fail "ledger row missing per-model mix (grove-14 models column)"
grep -q 'haiku' "$LEDGER" || fail "ledger row missing the second model in the mix"
grep -q ',effort$' "$LEDGER" || fail "ledger header missing the effort column (grove-435)"
grep -q ',xhigh$' "$LEDGER" || fail "ledger row missing the worker's --effort pin (grove-435)"

say "spend ledger: history survives transcript + worktree deletion"
rm -rf "$HOME/.claude/projects"
"$GV" cost --ledger > "$SCRATCH/ledger.out"
grep -q 'task-001' "$SCRATCH/ledger.out" || fail "history lost after transcript deletion"
grep -q 'Replace me' "$SCRATCH/ledger.out" || fail "history lost the title after transcript deletion"
grep -q '0.02' "$SCRATCH/ledger.out" || fail "history lost the cost estimate (2k out tokens ≈ \$0.02)"

say "worktree process of a DONE task: audit reports it, sweep offers the kill (grove-156)"
# Same discipline as the orphan-lookalike above: stubbed ps so the row is
# fully controlled, real sleep pid so the SIGTERM lands on something we own.
sleep 300 &
ZOMBIE_PID=$!
cat > "$STUBBIN/ps" <<PSEOF
#!/bin/sh
echo "  PID  PPID  %CPU    RSS ELAPSED ARGS"
echo "  $ZOMBIE_PID     1  99.0 819200   4-01:00:00 node $WTDIR2/node_modules/jest-worker/processChild.js"
PSEOF
export PATH="$STUBBIN:$PATH"
"$GV" audit --json > "$SCRATCH/audit-zombie.json"
grep -q '"worktree_processes"' "$SCRATCH/audit-zombie.json" || fail "audit --json missing the worktree_processes field"
grep -q '"pid": *'"$ZOMBIE_PID" "$SCRATCH/audit-zombie.json" || fail "audit missing the done-task worktree process"
grep -q '"ticket": *"task-001"' "$SCRATCH/audit-zombie.json" || fail "worktree process not attributed to its ticket"
kill -0 "$ZOMBIE_PID" 2>/dev/null || fail "audit must not kill (pure read)"
printf 'y\n' | "$GV" sweep > "$SCRATCH/sweep-zombie.out"
grep -q "pid $ZOMBIE_PID terminated" "$SCRATCH/sweep-zombie.out" || fail "sweep did not SIGTERM the worktree process"
kill -0 "$ZOMBIE_PID" 2>/dev/null && fail "worktree process survived confirmed sweep" || true
export PATH="$REAL_PATH"

# --- hooks binary mismatch (grove-348) ---

say "hooks status flags entries pointing at another (nonexistent) gv binary"
mkdir -p "$HOME/.claude"
cat > "$HOME/.claude/settings.json" <<'EOF'
{"hooks": {
  "SessionStart": [{"hooks": [{"type": "command", "command": "/nonexistent/gv hook session-start"}]}],
  "Notification": [{"hooks": [{"type": "command", "command": "/nonexistent/gv hook notification"}]}],
  "Stop": [{"hooks": [{"type": "command", "command": "/nonexistent/gv hook stop"}]}],
  "SessionEnd": [{"hooks": [{"type": "command", "command": "/nonexistent/gv hook session-end"}]}]
}}
EOF
"$GV" hooks status > "$SCRATCH/hooks-status.out"
grep -q '✗ Stop → /nonexistent/gv (no such binary)' "$SCRATCH/hooks-status.out" \
  || fail "hooks status did not flag the stale/missing hook binary"
grep -q 'run: gv hooks install' "$SCRATCH/hooks-status.out" \
  || fail "hooks status did not print the fix line"
"$GV" hooks status --json > "$SCRATCH/hooks-status.json"
grep -q '"binary": *"/nonexistent/gv"' "$SCRATCH/hooks-status.json" \
  || fail "hooks status --json missing the mismatch entry"
grep -q '"schema_version"' "$SCRATCH/hooks-status.json" \
  || fail "hooks status --json missing the contract envelope"

say "doctor warns about the mismatched hook binary"
("$GV" doctor --json > "$SCRATCH/doctor-hooks.json") || true # scratch board has error rows (gh-auth) — exit code is not under test
grep -q '"hooks-binary:' "$SCRATCH/doctor-hooks.json" \
  || fail "doctor --json missing the hooks-binary row"
("$GV" doctor > "$SCRATCH/doctor.out") || true
grep -q 'no such binary' "$SCRATCH/doctor.out" \
  || fail "doctor did not flag the mismatched hook binary"

# --- effort dial (grove-435) ---
# The launched command is what the pane was typed: with `claude: echo` the
# worker pane shows `echo --effort <l> "$(cat …)"` verbatim, so the pane's
# scrollback is the one place the flag can be asserted exactly once.
pane_text() { # $1 = ticket → every pane of its window, scrollback joined
  local wid
  tmux list-windows -t "=$SESSION" -F '#{window_id} #{window_name}' > "$SCRATCH/effort-wins.txt"
  wid="$(grep "$1" "$SCRATCH/effort-wins.txt" | head -1 | cut -d' ' -f1)"
  [ -n "$wid" ] || fail "no worker window for $1"
  : > "$SCRATCH/effort-pane.txt"
  tmux list-panes -t "$wid" -F '#{pane_id}' > "$SCRATCH/effort-panes.txt"
  while read -r p; do tmux capture-pane -p -S - -J -t "$p" >> "$SCRATCH/effort-pane.txt"; done < "$SCRATCH/effort-panes.txt"
  tr -d '\n' < "$SCRATCH/effort-pane.txt" > "$SCRATCH/effort-pane.flat"
}
launch_line() { # $1 = ticket → the typed `echo … "$(cat …` launch line (the echo stub
  local i             # prints its args back, so the pane carries the flags twice)
  for i in 1 2 3 4 5 6 7 8 9 10; do
    pane_text "$1"
    grep -q -- 'echo .*\$(cat' "$SCRATCH/effort-pane.flat" && break
    sleep 0.5
  done
  grep -o -- 'echo [^$]*\$(cat' "$SCRATCH/effort-pane.flat" | head -1 > "$SCRATCH/effort-launch.txt"
  [ -s "$SCRATCH/effort-launch.txt" ] || { cat "$SCRATCH/effort-pane.flat"; fail "no launch line typed into $1's pane"; }
}
effort_count() { # $1 = ticket, $2 = level → how many times `--effort <level>` is on the launch line
  launch_line "$1"
  grep -o -- "--effort $2" "$SCRATCH/effort-launch.txt" | wc -l | tr -d ' '
}
mdtask() { printf -- '---\nid: %s\ntitle: %s\nstatus: todo\nlabels: []\n---\n\n%s\n' "$1" "$2" "$2" > "$DUMMY/.grove/tasks/$1.md"; }

say "effort dial: a repo effort: key is the standing default (grove-435)"
mdtask task-002 "effort from config"
perl -pi -e 's/^(\s*)claude: echo$/$1claude: echo\n$1effort: medium/' "$WCFG"
grep -q 'effort: medium' "$WCFG" || fail "repo effort key not written"
"$GV" grab task-002 > "$SCRATCH/grab-effort-cfg.out"
grep -q '→ effort medium (repo default)' "$SCRATCH/grab-effort-cfg.out" || fail "grab did not report the repo's effort default"
[ "$(effort_count task-002 medium)" = "1" ] || { cat "$SCRATCH/effort-pane.flat"; fail "repo effort: medium must reach the launched command exactly once"; }

say "effort dial: --effort pins one worker and beats the repo key"
mdtask task-003 "effort from flag"
"$GV" grab task-003 --effort low > "$SCRATCH/grab-effort-flag.out"
grep -q '→ effort low (this worker only)' "$SCRATCH/grab-effort-flag.out" || fail "grab did not report the --effort pin"
[ "$(effort_count task-003 low)" = "1" ] || { cat "$SCRATCH/effort-pane.flat"; fail "--effort low must reach the launched command exactly once"; }
grep -q -- '--effort medium' "$SCRATCH/effort-launch.txt" && fail "the repo's effort: medium survived the --effort low pin" || true
[ "$(grep -o -- '--effort' "$SCRATCH/effort-launch.txt" | wc -l | tr -d ' ')" = "1" ] || { cat "$SCRATCH/effort-launch.txt"; fail "the launch line must carry --effort exactly once"; }

say "effort dial: an unknown level fails before anything exists"
mdtask task-004 "effort typo"
("$GV" grab task-004 --effort ultra 2>&1 || true) > "$SCRATCH/grab-effort-bad.out"
grep -q 'unknown effort "ultra"' "$SCRATCH/grab-effort-bad.out" || fail "grab accepted an undocumented effort level"
ls -d "$WT"/task-004-* >/dev/null 2>&1 && fail "a rejected --effort still created a worktree" || true

say "effort dial: gv ls --json carries effort for pinned tasks and omits it otherwise"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-effort.json"
python3 - "$SCRATCH/ls-effort.json" <<'PYEOF2' || fail "ls --json effort field wrong"
import json, sys
rows = {t['ticket']: t for t in json.load(open(sys.argv[1]))['tasks']}
assert rows['task-002'].get('effort') == 'medium', rows['task-002']
assert rows['task-003'].get('effort') == 'low', rows['task-003']
assert 'task-004' not in rows, sorted(rows)
PYEOF2
"$GV" ls --json --no-pr --no-cost | grep -q '"effort": *"low"' || fail "ls --json missing the effort field"

say "effort dial: adopt keeps the pin it was grabbed with, and --effort overrides it"
"$GV" pause task-003 --force > "$SCRATCH/effort-pause.out"
"$GV" adopt task-003 > "$SCRATCH/adopt-effort-keep.out"
grep -q '→ effort low' "$SCRATCH/adopt-effort-keep.out" || fail "adopt did not keep the grabbed effort"
"$GV" pause task-003 --force > "$SCRATCH/effort-pause2.out"
"$GV" adopt task-003 --effort max > "$SCRATCH/adopt-effort-flag.out"
grep -q '→ effort max' "$SCRATCH/adopt-effort-flag.out" || fail "adopt --effort did not override the stored pin"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-effort2.json"
grep -q '"effort": *"max"' "$SCRATCH/ls-effort2.json" || fail "ls --json did not pick up the adopt-time effort"

say "effort dial: doctor warns when CLAUDE_CODE_EFFORT_LEVEL would override every pin"
("$GV" doctor --json > "$SCRATCH/doctor-effort-ok.json") || true
grep -q '"effort-override"' "$SCRATCH/doctor-effort-ok.json" || fail "doctor --json missing the effort-override row"
python3 - "$SCRATCH/doctor-effort-ok.json" <<'PYEOF2' || fail "effort-override row must be ok with a clean env"
import json, sys
row = [r for r in json.load(open(sys.argv[1]))['rows'] if r['id'] == 'effort-override'][0]
assert row['state'] == 'ok', row
PYEOF2
(CLAUDE_CODE_EFFORT_LEVEL=low "$GV" doctor --json > "$SCRATCH/doctor-effort-warn.json") || true
python3 - "$SCRATCH/doctor-effort-warn.json" <<'PYEOF2' || fail "effort-override row must warn when the env var is set"
import json, sys
row = [r for r in json.load(open(sys.argv[1]))['rows'] if r['id'] == 'effort-override'][0]
assert row['state'] == 'warn' and 'CLAUDE_CODE_EFFORT_LEVEL=low' in row['info'], row
PYEOF2

say "audit is quiet afterwards"
"$GV" audit --json | tee "$SCRATCH/audit.json" >/dev/null

say "live state untouched"
LIVE_AFTER="$(snapshot_live)"
[ "$LIVE_BEFORE" = "$LIVE_AFTER" ] || { printf '%s\n---\n%s\n' "$LIVE_BEFORE" "$LIVE_AFTER"; fail "live overstory/grove state changed"; }
[ "$(real_tmux)" = "$REAL_TMUX_BEFORE" ] || fail "the REAL tmux server's session list changed — the suite leaked out of isolation"

say "PASS — full grab/ls/hook/untrack/done loop green on a remote-less repo"
