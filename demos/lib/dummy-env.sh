#!/usr/bin/env bash
# demos/lib/dummy-env.sh — the dummy-data sandbox for grove demo recordings.
#
# Source this, then call `dummy_env_up`. It builds a fully isolated grove
# world so a recording can drive real `gv` commands with ZERO risk to any
# live fleet state (docs/seed-manifest.md §Dummy-data E2E):
#
#   * HOME              → scratch dir  (isolates ~/.config/grove config)
#   * GROVE_STATE_DIR   → scratch dir  (isolates the event log / tasks.json)
#   * TMUX_TMPDIR       → scratch dir  (an isolated tmux server — gv's worker
#                                       sessions never touch the user's tmux)
#   * a scratch git repo with NO remote and `claude: echo` as the worker,
#     so the "agent" just echoes and exits — every state transition, hook
#     path and cleanup verb still runs for real, against throwaway state.
#
# The three hard rules this honors: append-only events.jsonl is only ever
# written under the scratch GROVE_STATE_DIR; no live worktree/branch is
# created or deleted; no task backend terminal state is mutated (there is
# no backend but the local markdown files in the scratch repo).
#
# Env knobs (optional, exported before sourcing / calling dummy_env_up):
#   DUMMY_ROOT   base scratch dir (default: mktemp under $TMPDIR)
#   GV_BIN       path to the gv binary to drive (default: build from repo)
#   DUMMY_REPO   name of the scratch repo (default: acme)
set -euo pipefail

# Resolve the grove repo root from this file's location (demos/lib/..).
DUMMY_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GROVE_REPO_ROOT="$(cd "$DUMMY_LIB_DIR/../.." && pwd)"

DUMMY_REPO="${DUMMY_REPO:-acme}"

# ── dummy_env_up ─────────────────────────────────────────────
# Stand up the sandbox. Exports HOME, GROVE_STATE_DIR, TMUX_TMPDIR, GV,
# DUMMY_REPO_PATH, DUMMY_SESSION. Registers an EXIT trap that tears it all
# down (kills the isolated tmux server, removes the scratch tree).
dummy_env_up() {
  # Build gv with the REAL environment first — before HOME is repointed —
  # so the Go module cache isn't re-downloaded into (read-only) scratch.
  # dummy.sh learned this the hard way; keep the ordering.
  if [ -n "${GV_BIN:-}" ]; then
    GV="$GV_BIN"
  else
    printf '\033[2m… building gv\033[0m\n' >&2
    GV="${DUMMY_ROOT_PREBUILD:-$GROVE_REPO_ROOT}/.gv-demo-build"
    ( cd "$GROVE_REPO_ROOT" && go build -o "$GV" ./cmd/gv )
  fi
  export GV

  DUMMY_ROOT="${DUMMY_ROOT:-$(mktemp -d "${TMPDIR:-/tmp}/grove-demo.XXXXXX")}"
  export HOME="$DUMMY_ROOT/home"
  export GROVE_STATE_DIR="$DUMMY_ROOT/state"
  export TMUX_TMPDIR="$DUMMY_ROOT/tmux"
  mkdir -p "$HOME" "$GROVE_STATE_DIR" "$TMUX_TMPDIR"

  # A friendlier shell prompt for the recording (no user@host noise).
  export PS1='$ '

  DUMMY_REPO_PATH="$DUMMY_ROOT/repos/$DUMMY_REPO"
  export DUMMY_REPO_PATH
  DUMMY_SESSION="pr-$DUMMY_REPO"
  export DUMMY_SESSION

  # A local bare "remote" — a throwaway repo on disk in the sandbox, NOT
  # GitHub. It lets `gv grab`'s branch push succeed (so the demo shows a
  # clean "branch pushed" instead of a no-remote fatal) while staying 100%
  # offline and isolated. No `gh`, no network, no live repo is touched.
  local bare="$DUMMY_ROOT/remotes/$DUMMY_REPO.git"
  mkdir -p "$DUMMY_REPO_PATH" "$(dirname "$bare")"
  git init -q --bare "$bare"
  (
    cd "$DUMMY_REPO_PATH"
    git init -q -b main
    git config user.email demo@grove.test
    git config user.name "grove demo"
    printf '# %s\n\nA sample project managed by grove.\n' "$DUMMY_REPO" > README.md
    git add -A && git commit -qm "init" >/dev/null
    git remote add origin "$bare"
    git push -q -u origin main
  )

  trap dummy_env_down EXIT INT TERM
}

# ── dummy_env_down ───────────────────────────────────────────
# Tear the sandbox down. Safe to call twice.
dummy_env_down() {
  [ -n "${TMUX_TMPDIR:-}" ] && tmux kill-server 2>/dev/null || true
  if [ -n "${DUMMY_ROOT:-}" ] && [ -d "$DUMMY_ROOT" ]; then
    chmod -R u+w "$DUMMY_ROOT" 2>/dev/null || true
    rm -rf "$DUMMY_ROOT"
  fi
  trap - EXIT INT TERM
}

# ── dummy_seed_task ──────────────────────────────────────────
# Overwrite the auto-scaffolded sample task with a nicer demo ticket so the
# recording tells a clear story. Call AFTER `gv init`.
#   dummy_seed_task <id> <title> <body...>
dummy_seed_task() {
  local id="${1:?id}" title="${2:?title}"; shift 2
  local body="${*:-Implement the change and open a PR.}"
  cat > "$DUMMY_REPO_PATH/.grove/tasks/${id}.md" <<EOF
---
id: $id
title: $title
status: todo
---

$body
EOF
}

# ── dummy_config_stub_worker ─────────────────────────────────
# Point the scratch repo's worker command at \`echo\` so grabbing a task
# spawns a harmless stub instead of a real Claude session. Call AFTER
# \`gv init\` (which writes .grove/config.yaml).
dummy_config_stub_worker() {
  local wcfg="$DUMMY_REPO_PATH/.grove/config.yaml"
  grep -q 'claude:' "$wcfg" 2>/dev/null && return 0
  perl -pi -e 's/^(\s*)base: main$/$1base: main\n$1claude: echo/' "$wcfg"
}

# ── dummy_finish_worker ──────────────────────────────────────
# Simulate the stubbed worker reaching a STATUS line by feeding grove's
# Stop hook exactly what a real Claude session would (docs/seed-manifest.md
# — the same mechanism e2e/dummy.sh exercises). Writes only to the scratch
# events.jsonl.
#   dummy_finish_worker <worktree-dir> <sentinel> <message>
dummy_finish_worker() {
  local wt="${1:?worktree}" sentinel="${2:?sentinel}" msg="${3:?message}"
  printf '{"session_id":"demo-%s","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"%s"}' \
    "$sentinel" "$wt" "$msg" | "$GV" hook stop
}
