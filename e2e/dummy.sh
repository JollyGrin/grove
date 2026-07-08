#!/usr/bin/env bash
# Phase-0 E2E: the dummy-data pattern (docs/seed-manifest.md).
#
# Exercises init → grab (list + start) → hook ownership no-op → ls →
# untrack --rm → re-grab → done (degraded no-remote path) with ZERO risk to
# live state: scratch HOME (config), scratch GROVE_STATE_DIR (state), a
# scratch remote-less git repo, and the worker command stubbed to `echo`.
# Asserts at the end that live overstory AND grove state were untouched.
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

DUMMY="$SCRATCH/repos/dummy"
SESSION="pr-dummy"
cleanup() {
  tmux kill-session -t "$SESSION" 2>/dev/null || true
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

say "gv untrack --rm --force (degraded: no remote to verify against)"
"$GV" untrack task-001 --rm --force | tee "$SCRATCH/untrack.out"
[ ! -d "$WTDIR" ] || fail "worktree survived untrack --rm"
tmux list-windows -t "$SESSION" > "$SCRATCH/windows2.out" 2>/dev/null || true
grep -q task-001 "$SCRATCH/windows2.out" && fail "window survived untrack" || true

say "re-grab after untrack"
"$GV" grab task-001 >/dev/null
ls -d "$WT"/task-001-* >/dev/null || fail "re-grab did not recreate the worktree"

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
# The tiny haiku entry keeps the total under $0.035 (still rounds to $0.03).
{
  printf '%s\n' '{"timestamp":"2026-07-07T10:00:00.000Z","requestId":"req-e2e","message":{"id":"msg-e2e","model":"claude-sonnet-5","usage":{"input_tokens":1000,"output_tokens":2000,"cache_read_input_tokens":500,"cache_creation_input_tokens":100}}}'
  printf '%s\n' '{"timestamp":"2026-07-07T10:01:00.000Z","requestId":"req-e2e2","message":{"id":"msg-e2e2","model":"claude-haiku-4-5","usage":{"input_tokens":500,"output_tokens":100,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}'
} > "$PROJ/e2e-session.jsonl"

say "gv done --force cleans up"
"$GV" done task-001 --force | tee "$SCRATCH/done2.out"
grep -q 'cleaned up' "$SCRATCH/done2.out" || fail "done --force failed"
ls -d "$WT"/task-001-* 2>/dev/null && fail "worktree survived done" || true
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

say "spend ledger: history survives transcript + worktree deletion"
rm -rf "$HOME/.claude/projects"
"$GV" cost --ledger > "$SCRATCH/ledger.out"
grep -q 'task-001' "$SCRATCH/ledger.out" || fail "history lost after transcript deletion"
grep -q 'Replace me' "$SCRATCH/ledger.out" || fail "history lost the title after transcript deletion"
grep -q '0.03' "$SCRATCH/ledger.out" || fail "history lost the cost estimate (2k out tokens ≈ \$0.03)"

say "audit is quiet afterwards"
"$GV" audit --json | tee "$SCRATCH/audit.json" >/dev/null

say "live state untouched"
LIVE_AFTER="$(snapshot_live)"
[ "$LIVE_BEFORE" = "$LIVE_AFTER" ] || { printf '%s\n---\n%s\n' "$LIVE_BEFORE" "$LIVE_AFTER"; fail "live overstory/grove state changed"; }

say "PASS — full grab/ls/hook/untrack/done loop green on a remote-less repo"
