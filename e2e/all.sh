#!/usr/bin/env bash
# Run every e2e suite in sequence and report one PASS/FAIL line each.
# Born from grove-79: three TUI PRs merged while cockpit.sh and workspace.sh
# were red, because nothing ran them — no CI covers these suites, so this is
# the one command to run before merging anything that touches the TUI or the
# task lifecycle. Each suite is self-isolating (scratch HOME/state/tmux); this
# runner adds nothing but sequencing.
set -u
cd "$(dirname "$0")"

LOGS="$(mktemp -d /tmp/grove-e2e-all.XXXXXX)"
fail=0

run_suite() { # <script> <mode: default|hostile>
  local s="$1" mode="$2" tag='' log="$1.log"
  if [ "$mode" = hostile ]; then
    tag=' (hostile tmux conf)'
    log="hostile-$1.log"
  fi
  printf '\033[1m== e2e/%s%s ==\033[0m ' "$s" "$tag"
  # Redirect to a file and test the suite's own exit status — never pipe it
  # (the piped-gate trap: a pipe reports the pipe's status, not the suite's).
  local rc=0
  if [ "$mode" = hostile ]; then
    GROVE_E2E_TMUX_CONF=hostile ./"$s" > "$LOGS/$log" 2>&1 || rc=$?
  else
    ./"$s" > "$LOGS/$log" 2>&1 || rc=$?
  fi
  if [ "$rc" -eq 0 ]; then
    printf '\033[32mPASS\033[0m\n'
  else
    printf '\033[31mFAIL\033[0m — log: %s/%s\n' "$LOGS" "$log"
    fail=1
  fi
}

for s in dummy.sh wizard.sh workspace.sh github.sh cockpit.sh plugin.sh relay.sh handoff.sh chat.sh watch.sh; do
  run_suite "$s" default
done
# Second pass under a hostile tmux conf (base-index 1 / pane-base-index 1,
# grove-168): the isolated servers never load a user's tmux.conf, so the
# default pass structurally cannot catch literal pane-index targets.
for s in workspace.sh cockpit.sh; do
  run_suite "$s" hostile
done

if [ "$fail" -eq 0 ]; then
  rm -rf "$LOGS"
  printf '\n\033[32mall e2e suites green\033[0m\n'
else
  printf '\n\033[31mred suites above — logs kept in %s\033[0m\n' "$LOGS"
fi
exit "$fail"
