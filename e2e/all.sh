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
for s in dummy.sh wizard.sh workspace.sh github.sh cockpit.sh plugin.sh relay.sh; do
  printf '\033[1m== e2e/%s ==\033[0m ' "$s"
  # Redirect to a file and test the suite's own exit status — never pipe it
  # (the piped-gate trap: a pipe reports the pipe's status, not the suite's).
  if ./"$s" > "$LOGS/$s.log" 2>&1; then
    printf '\033[32mPASS\033[0m\n'
  else
    printf '\033[31mFAIL\033[0m — log: %s/%s.log\n' "$LOGS" "$s"
    fail=1
  fi
done

if [ "$fail" -eq 0 ]; then
  rm -rf "$LOGS"
  printf '\n\033[32mall e2e suites green\033[0m\n'
else
  printf '\n\033[31mred suites above — logs kept in %s\033[0m\n' "$LOGS"
fi
exit "$fail"
