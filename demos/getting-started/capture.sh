#!/usr/bin/env bash
# demos/getting-started/capture.sh — record the getting-started tutorial.
#
# Storyboard: the first grove loop — track a task → grab it → a worker picks
# it up → it reports back ready for review → clean up. Driven entirely
# against the dummy-data sandbox (demos/lib/dummy-env.sh): scratch HOME +
# GROVE_STATE_DIR + an isolated tmux server + a remote-less repo whose worker
# is stubbed to `echo`. NOTHING here touches a live fleet, tmux server, or
# task backend.
#
# Output: demos/casts/getting-started.cast
#
# Run:  demos/getting-started/capture.sh
set -euo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$DEMO_DIR/lib/dummy-env.sh"
source "$DEMO_DIR/lib/tape.sh"

DUMMY_REPO="acme"
dummy_env_up

# Pre-stage everything that shouldn't appear on camera: register the repo,
# stub the worker, and seed a story-shaped ticket. `gv init` runs against the
# scratch config; the sample task it scaffolds is replaced with ours.
( cd "$DUMMY_REPO_PATH" && "$GV" init --yes >/dev/null )
dummy_config_stub_worker
rm -f "$DUMMY_REPO_PATH"/.grove/tasks/task-*.md
dummy_seed_task "acme-42" "Add a --version flag to the CLI" \
  "Print the build version and exit. Cover it with a test."

tape_init "getting-started" 100 28 "$DEMO_DIR/casts"

tape_comment "grove turns a task into an autonomous Claude Code worker — and a PR."
tape_sleep 0.6

tape_comment "1. See the backlog. (running inside the acme repo)"
tape_send "cd '$DUMMY_REPO_PATH'"
tape_run "gv grab"
tape_wait_for "acme-42" 10
tape_sleep 2.2

tape_comment "2. Grab the ticket — grove cuts a worktree, opens a tmux window, launches the worker."
tape_run "gv grab acme-42"
tape_wait_for "acme-42|worktree|window" 12
tape_sleep 2.4

tape_comment "3. Glance at the fleet."
tape_run "gv ls --no-pr --no-cost"
tape_wait_for "acme-42" 10
tape_sleep 2.6

# The worker (stubbed to echo) "finishes" — feed grove's Stop hook exactly
# what a real Claude session would emit. This is the same path e2e/dummy.sh
# exercises; it writes only to the scratch event log.
# Resolve the REAL worktree path (macOS /var → /private/var) so it matches
# the cwd grove tracked — otherwise the Stop hook sees an untracked dir and
# is a silent no-op (the hook ownership contract).
WTDIR="$(ls -d "$DUMMY_ROOT/repos/.worktrees/$DUMMY_REPO"/acme-42-* 2>/dev/null | head -1)"
WTDIR="$(cd "$WTDIR" && pwd -P)"
dummy_finish_worker "$WTDIR" "question" \
  "STATUS: QUESTION — should --version print the git SHA too, or just the tag?"

tape_comment "4. The worker hits a question — it surfaces on the board."
tape_run "gv ls --no-pr --no-cost"
tape_wait_for "waiting|question|SHA" 10
tape_sleep 2.8

tape_comment "5. Answer it without leaving your shell."
tape_run "gv answer acme-42 'Just the tag is fine.'"
tape_sleep 2.0

# Worker resumes and finishes, review-ready.
dummy_finish_worker "$WTDIR" "done" \
  "STATUS: DONE — added --version flag + test, opened PR."

tape_comment "6. It reports back DONE — branch pushed, PR opened, ready for your review."
tape_run "gv ls --no-pr --no-cost"
tape_wait_for "done|DONE|review" 10
tape_sleep 3.0

tape_comment "One task in, one PR out. That's the loop."
tape_sleep 2.0

tape_finish

echo "Done. Cast: $DEMO_DIR/casts/getting-started.cast"
