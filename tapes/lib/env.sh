# tapes/lib/env.sh — isolation wrapper for vhs-recorded grove demos.
#
# Sourced by the shell a vhs tape records (NOT executed): it rebases the
# whole world onto a scratch dir so the recording can run `gv` bare —
# cockpit, tmux server and all — with ZERO risk to live state. Same recipe
# as e2e/cockpit.sh (scratch HOME + GROVE_STATE_DIR + isolated tmux server
# via TMUX_TMPDIR + `claude: echo`-style stubs), factored so tapes can
# reuse it. Do not source this in a shell you care about: it rewrites HOME.
#
# Contract with tapes/run.sh (the recommended entrypoint):
#   - run.sh exports GROVE_TAPE_SCRATCH and pre-builds gv into it, so
#     sourcing here is fast (no `go build` on camera).
#   - run.sh snapshots live grove/overstory state before the recording and
#     diffs after (the e2e/dummy.sh canary), then cleans the scratch up.
# Sourcing this standalone also works — it builds gv and mktemps its own
# scratch — but nothing cleans up after you.
#
# Prints "grove tape env ready" last so tapes can `Wait+Screen` on it.

# $TMUX beats TMUX_TMPDIR in tmux's socket resolution (-S/-L > $TMUX >
# TMUX_TMPDIR). The recorded shell inherits $TMUX whenever the recording is
# launched from inside a tmux pane (every grove worker), and with it set,
# every tmux call below — AND gv's cockpit attach — lands on the REAL
# server instead of the scratch one. Unset it before anything else touches
# tmux. (Root cause of the 2026-07-07 grove-7 kill-server crash.)
unset TMUX TMUX_PANE

_TAPE_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TAPE_REPO_ROOT="$(cd "$_TAPE_LIB_DIR/../.." && pwd)"

SCRATCH="${GROVE_TAPE_SCRATCH:-$(mktemp -d /tmp/grove-tape.XXXXXX)}"
GV="$SCRATCH/gv"
if [ ! -x "$GV" ]; then
  # Build with the REAL environment (before HOME moves) so Go reuses the
  # module cache instead of re-downloading it read-only into the scratch
  # HOME — same footgun e2e/dummy.sh documents.
  (cd "$TAPE_REPO_ROOT" && go build -o "$GV" ./cmd/gv) || return 1
fi

export HOME="$SCRATCH/home"
export GROVE_STATE_DIR="$SCRATCH/state"
export TMUX_TMPDIR="$SCRATCH/tmux"   # isolated tmux server — never the user's
export PATH="$SCRATCH:$PATH"         # `gv` in the recording = the scratch build
mkdir -p "$HOME/.config/grove" "$GROVE_STATE_DIR" "$TMUX_TMPDIR" "$SCRATCH/repo"

# Recordings are for public PRs: strip the operator's hostname from the
# tmux status bar and the user@host default zsh prompt (the scratch HOME
# means these dotfiles are ours to write; the isolated server reads them
# because it starts after HOME moves).
cat > "$HOME/.tmux.conf" <<'EOF'
set -g status-left "[grove] "
set -g status-right ""
EOF
cat > "$HOME/.zshrc" <<'EOF'
PROMPT='❯ '
EOF

# Orchestrator stub: a canned "chat" pane that costs zero API credits but
# keeps the right-hand cockpit pane alive and plausible on camera. It
# swallows the --add-dir flags orchestratorLaunch appends, and clears the
# pane first so the typed launch command (scratch paths) leaves the frame.
cat > "$SCRATCH/orch-stub.sh" <<'STUB'
#!/usr/bin/env bash
printf '\033[2J\033[H'
printf '\n  \033[1m❉ orchestrator\033[0m \033[2m(stub — no model attached)\033[0m\n\n'
printf '  \033[2m>\033[0m fleet looks healthy. task-102 has a question for you —\n'
printf '    answer it from the dashboard (enter on the row).\n\n'
exec cat
STUB
chmod +x "$SCRATCH/orch-stub.sh"

cat > "$HOME/.config/grove/config.yaml" <<EOF
provider: {kind: markdown}
repos:
  demo: {path: $SCRATCH/repo, claude: echo}
orchestrator: {claude: $SCRATCH/orch-stub.sh}
EOF

# Seeded fleet: one question (floats to the top, selected by default), one
# reports-done, one working. Timestamps are fixed offsets from now with a
# 30s cushion so the AGE column stays stable while the tape records
# (age() rounds to the nearest minute).
_ts() { # _ts <minutes-ago>
  if date -v -1M +%s >/dev/null 2>&1; then
    date -u -v "-${1}M" -v -30S +%Y-%m-%dT%H:%M:%SZ
  else
    date -u -d "-${1} minutes -30 seconds" +%Y-%m-%dT%H:%M:%SZ
  fi
}
cat > "$GROVE_STATE_DIR/events.jsonl" <<EOF
{"time":"$(_ts 58)","type":"task_created","ticket":"task-101","data":{"title":"Add dark-mode toggle to settings","repo":"demo","branch":"task-101-dark-mode","worktree":"$SCRATCH/repo","tmux_session":"pr-demo","tmux_window":"task-101-dark-mode"}}
{"time":"$(_ts 57)","type":"session_started","ticket":"task-101","data":{"session_id":"s-101"}}
{"time":"$(_ts 41)","type":"task_created","ticket":"task-103","data":{"title":"Bump linter to v2","repo":"demo","branch":"task-103-linter","worktree":"$SCRATCH/repo","tmux_session":"pr-demo","tmux_window":"task-103-linter"}}
{"time":"$(_ts 40)","type":"session_started","ticket":"task-103","data":{"session_id":"s-103"}}
{"time":"$(_ts 9)","type":"agent_status","ticket":"task-103","data":{"status":"idle","sentinel":"done","message":"STATUS: DONE — linter bumped, all green"}}
{"time":"$(_ts 18)","type":"task_created","ticket":"task-102","data":{"title":"Fix flaky auth test","repo":"demo","branch":"task-102-flaky-auth","worktree":"$SCRATCH/repo","tmux_session":"pr-demo","tmux_window":"task-102-flaky-auth"}}
{"time":"$(_ts 17)","type":"session_started","ticket":"task-102","data":{"session_id":"s-102"}}
{"time":"$(_ts 3)","type":"agent_status","ticket":"task-102","data":{"status":"waiting","sentinel":"question","question":"retry the flaky auth test automatically, or fail fast and page?","message":"STATUS: QUESTION — retry the flaky auth test automatically, or fail fast and page?"}}
EOF

# Stub worker window for task-102 on the ISOLATED server: answering from
# the dashboard pastes into <session>:<window>.1, so give it a pane 1.
tmux new-session -d -s pr-demo -n task-102-flaky-auth -x 80 -y 24
tmux split-window -d -t pr-demo:task-102-flaky-auth

# cd somewhere with no .grove marker above it so ambient workspace
# detection stays on the legacy global cockpit (session name `grove`).
cd "$SCRATCH"

echo "grove tape env ready"
