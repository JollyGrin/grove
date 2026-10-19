#!/usr/bin/env bash
# Run every e2e suite in sequence and report one PASS/FAIL line each.
# Born from grove-79: three TUI PRs merged while cockpit.sh and workspace.sh
# were red, because nothing ran them — no CI covers these suites, so this is
# the one command to run before merging anything that touches the TUI or the
# task lifecycle. Each suite is self-isolating (scratch HOME/state/tmux); this
# runner adds nothing but sequencing.
# tmux: never talks to a tmux server itself — each suite isolates its own, so no tmux isolation here.
set -u
cd "$(dirname "$0")"

LOGS="$(mktemp -d /tmp/grove-e2e-all.XXXXXX)"
fail=0

# grove-507: no suite may spend the operator's real GitHub quota (a cockpit
# polls `gh` every beat, and 5 cockpits once exhausted the 5000/hr GraphQL
# limit). A guard `gh` goes first on PATH for every suite: it records the
# call and fails it, so a suite that reaches for the real gh can't
# spend quota (GROVE_E2E_STRICT_GH=1 turns any hit red). Suites that stub their own gh prepend
# it ahead of the guard, so the guard only ever sees an unstubbed call.
GUARD_BIN="$LOGS/guard-bin"
GUARD_HITS="$LOGS/gh-guard-hits.log"
mkdir -p "$GUARD_BIN"
: > "$GUARD_HITS"
cat > "$GUARD_BIN/gh" <<GUARD
#!/usr/bin/env bash
echo "[\${GROVE_E2E_SUITE:-?}] gh \$*" >> "$GUARD_HITS"
echo "gh blocked by e2e/all.sh (grove-507): stub gh in the suite" >&2
exit 1
GUARD
chmod +x "$GUARD_BIN/gh"
export PATH="$GUARD_BIN:$PATH"

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
  export GROVE_E2E_SUITE="$1"
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

for s in dummy.sh wizard.sh workspace.sh github.sh cockpit.sh plugin.sh relay.sh handoff.sh chat.sh chat_hide.sh watch.sh brains.sh supervise.sh sub.sh serve.sh feature.sh rail.sh focus.sh; do
  run_suite "$s" default
done
# Second pass under a hostile tmux conf (base-index 1 / pane-base-index 1,
# grove-168): the isolated servers never load a user's tmux.conf, so the
# default pass structurally cannot catch literal pane-index targets.
for s in workspace.sh cockpit.sh chat.sh chat_hide.sh focus.sh; do
  run_suite "$s" hostile
done

# Unstubbed gh calls were blocked above, so no suite spent live quota. Many
# older suites still reach for gh and tolerate it failing — list them so the
# leak is visible; GROVE_E2E_STRICT_GH=1 makes any hit a failure.
if [ -s "$GUARD_HITS" ]; then
  printf '\nnote: unstubbed gh calls were blocked (suite: count):\n'
  sed 's/^\[\([^]]*\)\].*/\1/' "$GUARD_HITS" | sort | uniq -c
  [ "${GROVE_E2E_STRICT_GH:-}" = 1 ] && fail=1
fi

if [ "$fail" -eq 0 ]; then
  rm -rf "$LOGS"
  printf '\n\033[32mall e2e suites green\033[0m\n'
else
  printf '\n\033[31mred suites above — logs kept in %s\033[0m\n' "$LOGS"
fi
exit "$fail"
