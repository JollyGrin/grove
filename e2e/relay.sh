#!/usr/bin/env bash
# grove-144 E2E: the relay must deliver text AND submit it.
#
# The bug: PasteText pressed Enter with zero settle after paste-buffer, so a
# TUI still ingesting the paste swallowed the Enter — the text sat unsent in
# the input box while `gv nudge` printed ✓ and appended EvAnswered, leaving
# `gv ls` claiming a dead worker was working.
#
# Leg 1 (happy path): the worker command is a stub that only ever sees a line
# once Enter submits it, so the inbox file proves BOTH halves landed.
# Leg 2 (failure path): a stub that swallows the Enter and redraws a fake
# Claude input box still holding the text — gv must retry, then fail loudly,
# exit non-zero, and record NO answered event.
#
# Dummy-data pattern (docs/seed-manifest.md): scratch HOME, scratch
# GROVE_STATE_DIR, scratch remote-less repo, uniquely-named tmux session on
# the real server (never kill-server — tmux-discipline rule 1).
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-e2e-relay.XXXXXX)"

# Build before HOME moves, or Go re-downloads its module cache into the
# scratch HOME (and that cache is read-only, breaking cleanup).
say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

export HOME="$SCRATCH/home"
export GROVE_STATE_DIR="$SCRATCH/state"
mkdir -p "$HOME" "$GROVE_STATE_DIR"

REAL_HOME="$(dscl . -read /Users/"$(whoami)" NFSHomeDirectory 2>/dev/null | awk '{print $2}' || echo "/Users/$(whoami)")"
snapshot_live() {
  for f in "$REAL_HOME/.local/state/grove/events.jsonl" \
           "$REAL_HOME/.config/grove/config.yaml" \
           "$REAL_HOME/.local/state/overstory/events.jsonl"; do
    [ -e "$f" ] && stat -f '%N %m %z' "$f"
  done
  true
}
LIVE_BEFORE="$(snapshot_live)"

DUMMY="$SCRATCH/repos/relaytest"
SESSION="grove-relaytest"   # workspace label = repo dir base (grove-29 P2)
cleanup() {
  # Scoped kill-session on OUR uniquely named session only — a bare
  # kill-server once took down every worker on the machine (2026-07-07).
  tmux kill-session -t "=$SESSION" 2>/dev/null || true
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH"
}
trap cleanup EXIT

say "scratch repo (no remote)"
mkdir -p "$DUMMY" && cd "$DUMMY"
git init -q -b main
git config user.email e2e@grove.test && git config user.name "grove e2e"
echo "# relaytest" > README.md
git add -A && git commit -qm "init"

say "gv init --yes"
"$GV" init --yes > "$SCRATCH/init.out"
WCFG="$DUMMY/.grove/config.yaml"
grep -q 'kind: markdown' "$WCFG" || fail "workspace config missing markdown provider"

say "seed a second task for the failure leg"
sed 's/task-001/task-002/' .grove/tasks/task-001.md > .grove/tasks/task-002.md

# --- the stubs: fake agents whose ONLY input is a submitted line ---
STUBS="$SCRATCH/stubs"; mkdir -p "$STUBS"
INBOX="$SCRATCH/inbox.txt"; : > "$INBOX"

# A line reaches `read` only after Enter commits it, so the inbox is proof of
# submit — not just delivery. argv (the kickoff prompt) is ignored.
cat > "$STUBS/reader" <<EOF
#!/usr/bin/env bash
touch "$SCRATCH/reader.ready"
while IFS= read -r line; do printf '%s\n' "\$line" >> "$INBOX"; done
EOF

# The grove-144 failure mode, reproduced deterministically: consume the paste
# AND its Enter, then redraw the text inside a Claude-shaped input box and
# never submit anything.
cat > "$STUBS/swallow" <<EOF
#!/usr/bin/env bash
touch "$SCRATCH/swallow.ready"
IFS= read -r line
clear
printf '╭──────────────────────────────────────╮\n'
printf '│ > %s\n' "\$line"
printf '╰──────────────────────────────────────╯\n'
printf '  ? for shortcuts\n'
sleep 300
EOF
chmod +x "$STUBS/reader" "$STUBS/swallow"

wait_for() { # wait_for <file> <what>
  for _ in $(seq 1 100); do [ -e "$1" ] && return 0; sleep 0.1; done
  fail "timed out waiting for $2"
}

say "point the worker command at the reader stub"
perl -pi -e "s|^(\s*)base: main\$|\$1base: main\n\$1claude: $STUBS/reader|" "$WCFG"
grep -q "claude: $STUBS/reader" "$WCFG" || fail "reader stub not wired into the workspace config"

say "gv grab task-001"
"$GV" grab task-001 > "$SCRATCH/grab1.out"
wait_for "$SCRATCH/reader.ready" "the reader stub to start in the worker pane"

# --- leg 1: text AND submit both land ---

say "gv nudge task-001 — text must arrive AND be submitted"
MSG="hello from the relay gate"
"$GV" nudge task-001 "$MSG" > "$SCRATCH/nudge1.out" || fail "nudge exited non-zero on the happy path"
grep -q '✓ sent' "$SCRATCH/nudge1.out" || fail "nudge did not report success"
for _ in $(seq 1 50); do grep -qx "$MSG" "$INBOX" && break; sleep 0.1; done
grep -qx "$MSG" "$INBOX" || {
  printf 'inbox:\n'; cat "$INBOX"
  fail "the agent never received a SUBMITTED line (paste→Enter race)"
}
# grep -x also proves the paste arrived clean: no bracketed-paste escape
# bytes, no truncation, no tmux key-name mangling.
[ "$(wc -l < "$INBOX")" -eq 1 ] || fail "expected exactly one submitted line, got $(wc -l < "$INBOX")"

say "a verified submit records EvAnswered"
grep -q '"type":"answered"' "$GROVE_STATE_DIR/events.jsonl" || fail "verified submit did not append answered"

say "prose survives the relay (send-keys would eat the 'Enter' lookalike)"
"$GV" nudge task-001 "one two Enter three" > "$SCRATCH/nudge-lookalike.out" || fail "nudge with a key-name lookalike failed"
for _ in $(seq 1 50); do grep -qx "one two Enter three" "$INBOX" && break; sleep 0.1; done
grep -qx "one two Enter three" "$INBOX" || fail "tmux interpreted the 'Enter' lookalike inside the prose"
# Baseline AFTER every relay that is supposed to succeed, so the failure leg
# below is compared against a settled count.
ANSWERED_AFTER_OK=$(grep -c '"type":"answered"' "$GROVE_STATE_DIR/events.jsonl")

# --- leg 2: the swallowed Enter must be caught, not reported as success ---

say "point the worker command at the swallow stub"
perl -pi -e "s|claude: .*/reader\$|claude: $STUBS/swallow|" "$WCFG"
grep -q "claude: $STUBS/swallow" "$WCFG" || fail "swallow stub not wired into the workspace config"

say "gv grab task-002"
"$GV" grab task-002 > "$SCRATCH/grab2.out"
wait_for "$SCRATCH/swallow.ready" "the swallow stub to start in the worker pane"

say "gv nudge task-002 — unsent text must fail loudly"
if "$GV" nudge task-002 "this one gets swallowed" > "$SCRATCH/nudge2.out" 2>&1; then
  cat "$SCRATCH/nudge2.out"
  fail "nudge reported success while the text sat unsent in the input box"
fi
grep -qi 'never submitted' "$SCRATCH/nudge2.out" || {
  cat "$SCRATCH/nudge2.out"
  fail "failure message does not say the relay never submitted"
}
grep -q 'send-keys' "$SCRATCH/nudge2.out" || fail "failure message gives the operator no recovery command"
grep -q '✓ sent' "$SCRATCH/nudge2.out" && fail "a failed relay must not print ✓" || true

say "an unverified submit records NOTHING (the silent-failure fix)"
[ "$(grep -c '"type":"answered"' "$GROVE_STATE_DIR/events.jsonl")" -eq "$ANSWERED_AFTER_OK" ] \
  || fail "EvAnswered was appended for a relay that never submitted"

say "live state untouched"
LIVE_AFTER="$(snapshot_live)"
[ "$LIVE_BEFORE" = "$LIVE_AFTER" ] || { printf '%s\n---\n%s\n' "$LIVE_BEFORE" "$LIVE_AFTER"; fail "live grove/overstory state changed"; }

say "PASS — relay delivers and submits; a swallowed Enter fails loudly and records nothing"
