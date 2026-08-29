#!/usr/bin/env bash
# grove-198 E2E: `gv orchestrator new --host` spawns a chat in the HOST's
# twin of the calling workspace.
#
# Dummy-data pattern with a fake second host (the e2e/handoff.sh shape):
# the "remote" is this machine behind a fake `ssh` on PATH that re-runs the
# relayed command from a NON-workspace cwd (the host's global layer, as a
# real ssh login lands) against a second ISOLATED tmux server. Proves:
#   1. the relayed argv: op id + --as + --workspace, only names travel
#   2. the spawn lands in the twin's own tmux server, in its orchestrator
#      dir, and appends one orchestrator_spawned event to the TWIN's state
#   3. session numbering (grove-chat-<label>-<n>)
#   4. an ssh-255 retry does NOT double-spawn (op-id receipt)
#   5. no twin / dead twin / unknown profile / no label = hard, non-zero
#      errors — never a fall-back to the host's global layer
#   6. (grove-199) the spawned chat can dismiss itself with `gv orchestrator
#      close` — its single pane is not a dashboard to protect — while a
#      workspace whose LABEL collides with the chat naming (`chat-app` ⇒
#      session `grove-chat-app`) keeps its dashboard guard
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-chat.XXXXXX)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

# Real-server canary (tmux-discipline): snapshot the machine's REAL tmux
# session list now, compare at the end — if this suite ever leaks onto the
# real server, fail loudly instead of silently.
real_tmux() { env -u TMUX -u TMUX_PANE -u TMUX_TMPDIR tmux list-sessions -F '#{session_name}' 2>/dev/null | sort; true; }
REAL_TMUX_BEFORE="$(real_tmux)"

export HOME="$SCRATCH/home"
# Hermetic scratch HOME: a dying pane shell flushes .bash_history into it
# during cleanup's rm -rf (see e2e/workspace.sh).
export HISTFILE=/dev/null
export LESSHISTFILE=-
# $TMUX beats TMUX_TMPDIR — unset or every tmux call hits the REAL server.
unset TMUX TMUX_PANE
unset GROVE_STATE_DIR || true   # the twin's own .grove/state is the subject
export TMUX_TMPDIR="$SCRATCH/tmux-local"
REMOTE_TMUX="$SCRATCH/tmux-remote"
mkdir -p "$HOME" "$TMUX_TMPDIR" "$REMOTE_TMUX" "$SCRATCH/bin"

remote_tmux() { env -u TMUX TMUX_TMPDIR="$REMOTE_TMUX" tmux "$@"; }
chat_sessions() { remote_tmux list-sessions -F '#{session_name}' 2>/dev/null | grep -c "^grove-chat-$1-" || true; }

cleanup() {
  env -u TMUX TMUX_TMPDIR="$TMUX_TMPDIR" tmux kill-server 2>/dev/null || true   # isolated servers only
  env -u TMUX TMUX_TMPDIR="$REMOTE_TMUX" tmux kill-server 2>/dev/null || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -S "$REMOTE_TMUX/tmux-$(id -u)/default" ] || break
    sleep 0.2
  done
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH" 2>/dev/null || { sleep 0.5; rm -rf "$SCRATCH" 2>/dev/null || true; }
}
trap cleanup EXIT

say "fake ssh on PATH (target-checked, global-layer cwd, second tmux server)"
# Parses like the real thing and REQUIRES the configured hosts.pc.ssh value
# as target (handoff.sh findings 8/9: a fake that ignores the target lets a
# wrong dial-name pass review). The relayed command runs from $SCRATCH — a
# directory with no .grove marker — so the receiving half must find the twin
# through the registry, exactly as a real ssh login does.
cat > "$SCRATCH/bin/ssh" <<EOF
#!/usr/bin/env bash
target=""
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) shift 2 ;;
    --) shift; break ;;
    -*) shift ;;
    *) if [ -z "\$target" ]; then target="\$1"; shift; else break; fi ;;
  esac
done
[ "\$target" = "localhost" ] || { echo "fake ssh: target '\$target' is not the configured hosts.pc.ssh value (localhost)" >&2; exit 42; }
cmd="\$*"
echo "[fake ssh] \$cmd" >&2
# 255 knob: with the marker present the FIRST hop RUNS the command and then
# reports the connection dead — delivery happened, the sender cannot know.
if [ -f "$SCRATCH/ssh-fail-first" ]; then
  n=\$(cat "$SCRATCH/ssh-op-hops" 2>/dev/null || echo 0)
  echo \$((n+1)) > "$SCRATCH/ssh-op-hops"
  if [ "\$n" -eq 0 ]; then
    (cd "$SCRATCH" && env TMUX_TMPDIR="$REMOTE_TMUX" sh -c "\$cmd")
    exit 255
  fi
  cd "$SCRATCH" && exec env TMUX_TMPDIR="$REMOTE_TMUX" sh -c "\$cmd"
fi
cd "$SCRATCH" && exec env TMUX_TMPDIR="$REMOTE_TMUX" sh -c "\$cmd"
EOF
chmod +x "$SCRATCH/bin/ssh"
export PATH="$SCRATCH/bin:$PATH"

mkrepo() { # dir
  mkdir -p "$1"
  git -C "$1" init -qb main
  git -C "$1" config user.email e2e@grove.test && git -C "$1" config user.name "grove e2e"
  ( cd "$1" && echo x > README.md && git add -A && git commit -qm init )
}

say "workspace twin: chatws (registered, orchestrator stubbed to echo)"
WS="$SCRATCH/chatws"
mkrepo "$WS"
( cd "$WS" && "$GV" init --yes --label chatws > /dev/null )
WCFG="$WS/.grove/config.yaml"
cat >> "$WCFG" <<EOF
orchestrator:
  claude: echo grove-198-chat
model_profiles:
  e2e-glm:
    base_url: https://openrouter.ai/api
    auth_token_env: OPENROUTER_API_KEY
    opus: z-ai/glm-5.2
    sonnet: z-ai/glm-5.2
    haiku: z-ai/glm-4.5-air
hosts:
  pc:
    ssh: localhost
    gv: $GV
EOF
"$GV" workspaces | grep -q chatws || fail "chatws not registered"

say "gv orchestrator new --host pc (from inside the workspace)"
( cd "$WS" && "$GV" orchestrator new --host pc ) > "$SCRATCH/spawn1.out" 2> "$SCRATCH/spawn1.err"
cat "$SCRATCH/spawn1.out" "$SCRATCH/spawn1.err"
# Only NAMES travel: op id, the caller's alias for this host, the label.
grep -Eq "\[fake ssh\] $GV orchestrator new --op-id [0-9a-f]{32} --as pc --workspace chatws\$" "$SCRATCH/spawn1.err" \
  || fail "relayed argv wrong — want 'orchestrator new --op-id <32-hex> --as pc --workspace chatws'"
grep -q '✓ orchestrator chat grove-chat-chatws-1 — workspace chatws' "$SCRATCH/spawn1.out" || fail "missing the remote's success line"
grep -q 'attach: tmux attach -t =grove-chat-chatws-1' "$SCRATCH/spawn1.out" || fail "missing the host-side attach line"
grep -q 'from here: ssh -t localhost tmux attach -t =grove-chat-chatws-1' "$SCRATCH/spawn1.out" \
  || fail "missing the paste-able ssh attach line (must dial hosts.pc.ssh)"

say "the session lives on the HOST's tmux server, not this one"
remote_tmux has-session -t '=grove-chat-chatws-1' 2>/dev/null || fail "chat session missing on the remote tmux server"
if tmux has-session -t '=grove-chat-chatws-1' 2>/dev/null; then fail "chat session leaked onto the local tmux server"; fi
[ "$(chat_sessions chatws)" -eq 1 ] || fail "want exactly 1 chat session, got $(chat_sessions chatws)"

say "it runs in the TWIN's orchestrator dir, with the brain seeded"
PANE_CWD="$(remote_tmux display-message -p -t '=grove-chat-chatws-1:' '#{pane_current_path}')"
[ "$PANE_CWD" = "$WS/.grove/orchestrator" ] || fail "chat cwd = $PANE_CWD, want $WS/.grove/orchestrator"
[ -f "$WS/.grove/orchestrator/CLAUDE.md" ] || fail "orchestrator CLAUDE.md not seeded in the twin"

say "one orchestrator_spawned event, in the TWIN's state (not the global layer)"
EVENTS="$WS/.grove/state/events.jsonl"
grep -q '"type":"orchestrator_spawned"' "$EVENTS" || fail "no orchestrator_spawned event in the twin's log"
grep -q '"session":"grove-chat-chatws-1"' "$EVENTS" || fail "spawn event missing the session name"
grep -q '"workspace":"chatws"' "$EVENTS" || fail "spawn event missing the workspace label"
[ ! -e "$HOME/.local/state/grove/events.jsonl" ] || fail "the spawn must not write to the host's global state"

say "a second spawn takes the next number, and --profile gets its own cwd"
( cd "$WS" && "$GV" orchestrator new --host pc --profile e2e-glm ) > "$SCRATCH/spawn2.out" 2>&1
cat "$SCRATCH/spawn2.out"
grep -q 'grove-chat-chatws-2' "$SCRATCH/spawn2.out" || fail "second chat must be grove-chat-chatws-2"
grep -q 'profile e2e-glm' "$SCRATCH/spawn2.out" || fail "profiled spawn must name the profile"
remote_tmux has-session -t '=grove-chat-chatws-2' 2>/dev/null || fail "second chat session missing"
PANE_CWD2="$(remote_tmux display-message -p -t '=grove-chat-chatws-2:' '#{pane_current_path}')"
[ "$PANE_CWD2" = "$WS/.grove/orchestrator/e2e-glm" ] || fail "profiled chat cwd = $PANE_CWD2, want the per-profile dir"

say "grove-198: an ssh-255 retry spawns exactly once (op-id receipt)"
# The fake ssh's first hop RUNS the spawn and then dies with 255 — outcome
# unknown to the sender. The sender must re-run the SAME argv once; the
# twin's receipt makes that a no-op. Want: 2 hops, ONE new session, the
# retry printing "already applied" with the SAME session name.
touch "$SCRATCH/ssh-fail-first"
( cd "$WS" && "$GV" orchestrator new --host pc ) > "$SCRATCH/retry.out" 2> "$SCRATCH/retry.err"
cat "$SCRATCH/retry.out" "$SCRATCH/retry.err"
rm -f "$SCRATCH/ssh-fail-first"
grep -q 'retrying once with the same op id' "$SCRATCH/retry.err" || fail "ssh 255 did not trigger the same-op-id retry"
grep -q 'already applied' "$SCRATCH/retry.out" || fail "the retry did not hit the op-id receipt"
grep -q 'already applied (op .*) — orchestrator chat grove-chat-chatws-3' "$SCRATCH/retry.out" \
  || fail "the retry must reprint the FIRST hop's session name"
grep -q 'from here: ssh -t localhost tmux attach -t =grove-chat-chatws-3' "$SCRATCH/retry.out" \
  || fail "a deduped retry must still print the attach line"
[ "$(cat "$SCRATCH/ssh-op-hops")" -eq 2 ] || fail "expected exactly 2 ssh hops, got $(cat "$SCRATCH/ssh-op-hops")"
[ "$(chat_sessions chatws)" -eq 3 ] || fail "want 3 chat sessions after the retry (no double-spawn), got $(chat_sessions chatws)"
RETRY_OP=$(grep -o '"op_id":"[0-9a-f]*"' "$EVENTS" | sort | uniq -c | awk '$1 > 1 {print $2}')
[ -z "$RETRY_OP" ] || fail "an op id appears twice in the twin's log: $RETRY_OP"
[ "$(grep -c '"type":"orchestrator_spawned"' "$EVENTS")" -eq 3 ] || fail "want 3 spawn events, got $(grep -c '"type":"orchestrator_spawned"' "$EVENTS")"

say "no twin on the host = hard error, no fall-back to its global layer"
rc=0
( cd "$WS" && "$GV" orchestrator new --host pc --workspace nope ) > "$SCRATCH/notwin.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "a missing twin must exit non-zero"
grep -q "no workspace 'nope' on @pc — register a twin there or spawn locally" "$SCRATCH/notwin.out" \
  || { cat "$SCRATCH/notwin.out"; fail "wrong missing-twin error"; }
[ "$(chat_sessions nope)" -eq 0 ] || fail "a refused spawn must create nothing"

say "a registered-but-dead twin is the same hard error"
GHOST="$SCRATCH/ghostws"
mkrepo "$GHOST"
( cd "$GHOST" && "$GV" init --yes --label ghostws > /dev/null )
rm -rf "$GHOST/.grove"   # marker gone: the root moved away under the registry
rc=0
( cd "$WS" && "$GV" orchestrator new --host pc --workspace ghostws ) > "$SCRATCH/dead.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "a dead twin must exit non-zero"
grep -q "no workspace 'ghostws' on @pc" "$SCRATCH/dead.out" || { cat "$SCRATCH/dead.out"; fail "wrong dead-twin error"; }
grep -q "marker gone" "$SCRATCH/dead.out" || fail "the dead-twin error must name the stale root"

say "a profile the HOST doesn't have is a hard error too"
rc=0
( cd "$WS" && "$GV" orchestrator new --host pc --profile nope ) > "$SCRATCH/profile.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "an unknown profile must exit non-zero"
grep -q 'unknown model profile' "$SCRATCH/profile.out" || { cat "$SCRATCH/profile.out"; fail "wrong unknown-profile error"; }
[ "$(chat_sessions chatws)" -eq 3 ] || fail "a refused profile must create no session"

say "outside a workspace, --host needs an explicit label"
rc=0
( cd "$SCRATCH" && "$GV" orchestrator new --host pc ) > "$SCRATCH/nolabel.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "a labelless spawn must exit non-zero"
grep -q 'needs a workspace label' "$SCRATCH/nolabel.out" || { cat "$SCRATCH/nolabel.out"; fail "wrong no-label error"; }

say "--host on another orchestrator subcommand gets the friendly error"
rc=0
( cd "$WS" && "$GV" orchestrator close --host pc ) > "$SCRATCH/close.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "orchestrator close --host must exit non-zero"
grep -q 'only supported for `gv orchestrator new`' "$SCRATCH/close.out" || { cat "$SCRATCH/close.out"; fail "wrong subcommand error"; }

say "grove-199: a chat session self-closes (gv orchestrator close from its pane)"
# The seeded brain instructs exactly this for dispatch-and-dismiss, and the
# chat's single pane is its window's FIRST — the dashboard guard used to
# refuse it and strand the claude process alive on the host forever.
CHAT_PANE="$(remote_tmux list-panes -t '=grove-chat-chatws-1:chat' -F '#{pane_id}' | head -1)"
[ -n "$CHAT_PANE" ] || fail "no pane in grove-chat-chatws-1"
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" TMUX_PANE="$CHAT_PANE" "$GV" orchestrator close --reason e2e ) \
  > "$SCRATCH/selfclose.out" 2>&1 || { cat "$SCRATCH/selfclose.out"; fail "a chat session must be able to close itself"; }
for _ in 1 2 3 4 5 6 7 8 9 10; do
  remote_tmux has-session -t '=grove-chat-chatws-1' 2>/dev/null || break
  sleep 0.2
done
if remote_tmux has-session -t '=grove-chat-chatws-1' 2>/dev/null; then fail "the chat session survived its self-close"; fi
[ "$(chat_sessions chatws)" -eq 2 ] || fail "want 2 chat sessions after the self-close, got $(chat_sessions chatws)"
grep -q '"type":"orchestrator_closed"' "$EVENTS" || fail "the self-close left no orchestrator_closed event in the twin's log"

say "grove-199: a workspace labelled chat-app still gets its dashboard guard"
# The name-shape collision: labels are [a-z0-9][a-z0-9_-]*, so the workspace
# `chat-app` owns cockpit session `grove-chat-app` — exactly the shape of a
# chat session. The registry decides, and a registered label means COCKPIT:
# its first pane is a dashboard, and closing it is refused.
COLLIDE="$SCRATCH/chat-app"
mkrepo "$COLLIDE"
( cd "$COLLIDE" && "$GV" init --yes --label chat-app > /dev/null )
remote_tmux new-session -d -s grove-chat-app -n cockpit -c "$COLLIDE"
COLLIDE_PANE="$(remote_tmux list-panes -t '=grove-chat-app:cockpit' -F '#{pane_id}' | head -1)"
[ -n "$COLLIDE_PANE" ] || fail "no pane in the colliding cockpit session"
rc=0
( cd "$COLLIDE" && env TMUX_TMPDIR="$REMOTE_TMUX" TMUX_PANE="$COLLIDE_PANE" "$GV" orchestrator close ) \
  > "$SCRATCH/collide.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || { cat "$SCRATCH/collide.out"; fail "closing the chat-app cockpit's DASHBOARD must be refused"; }
grep -q "first pane" "$SCRATCH/collide.out" || { cat "$SCRATCH/collide.out"; fail "wrong refusal — want the dashboard guard"; }
remote_tmux has-session -t '=grove-chat-app' 2>/dev/null || fail "the chat-app cockpit was killed by the chat exemption"
grep -q '"type":"orchestrator_closed"' "$COLLIDE/.grove/state/events.jsonl" 2>/dev/null \
  && fail "a refused close must log nothing" || true

say "the local half never spawns here"
tmux list-sessions -F '#{session_name}' 2>/dev/null | grep '^grove-chat-' && fail "chat sessions leaked onto the local server" || true

say "real tmux server untouched (canary)"
[ "$(real_tmux)" = "$REAL_TMUX_BEFORE" ] || fail "the REAL tmux server's session list changed — the suite leaked out of isolation"

printf '\n\033[32mchat e2e green\033[0m\n'
