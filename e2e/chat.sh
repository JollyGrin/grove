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
#   7. (grove-203) chat sessions are reapable: `gv audit` reports them,
#      `gv park` names the ones it leaves running, `gv park --chats` kills
#      them — and neither path touches the colliding `grove-chat-app`
#      cockpit
#   8. (grove-215) `gv chat ls [--json]` joins live panes to Claude session
#      ids: two chats sharing ONE orchestrator project dir get distinct,
#      STABLE ids (stamped on the pane as @grove_chat_session), a chat with
#      no transcript yet reports null, the cockpit's own pane is kind
#      cockpit / writable false, and an unclaimed transcript is archived
#   9. (grove-217) `gv orchestrator new --resume <id>` revives an ARCHIVED
#      chat: the launch carries --resume, the pane is stamped with that id
#      at spawn (so `gv chat ls` reports the SAME id, now kind chat /
#      writable true), a profiled conversation revives in ITS own cwd, and
#      an unknown / malformed / already-live id spawns nothing — including
#      over the relay, where an ssh-255 retry still spawns exactly once
#  10. (grove-216) `gv chat tail|send|keys`: tail reproduces a transcript in
#       order with stable seq (bookkeeping and isMeta lines project to
#       nothing), `--since N` resumes at N+1, `--follow` emits an append
#       within ~1s; send lands AND submits on a live chat — proven by the
#       TRANSCRIPT, via a fake claude that appends what it is given — and
#       refuses every `writable: false` row; keys delivers a raw char with no
#       Enter and refuses a newline
#  11. (grove-222) identity is known by CONSTRUCTION, never by mtime: every
#       spawned chat carries a minted `--session-id` into claude and is
#       stamped at creation, the decoy transcripts recency would have paired
#       with the wrong panes stay archived, an unstamped pane re-derives from
#       its RUNNING process, two unidentifiable rivals in one project dir
#       both report null rather than guess, and `gv chat restamp` clears or
#       re-points a stamp by hand
#  12. (grove-218) `gv chat serve`: the default bind is LOOPBACK and says how
#       to expose it (a non-loopback bind names what it exposes); the shell,
#       app.js and the PINNED marked.min.js are served with the CSP that
#       sanitizes rendered agent markdown; `/api/chats` is grove-215's
#       payload; a browser POST spawns a chat, reaches a live agent and gets
#       its reply back over SSE in the SAME bytes `gv chat tail` prints;
#       an archived chat revives over HTTP; and the closed route table 404s
#       everything fleet-shaped — there is no path to done/untrack from a
#       phone
#  13. (grove-225) the phone picks a backend: GET /api/profiles lists the
#       host's model profiles sorted and names-only, POST .../new takes an
#       OPTIONAL {"profile"} that runs the chat in that backend's wrapper
#       and its own cwd, a bodyless spawn is byte-compatible with grove-218,
#       and an unknown profile (or a typo'd body key) refuses in the CLI's
#       own words having spawned nothing
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
  # grove-218: the HTTP listener is the one thing here that is not a tmux
  # session, so nothing else would reap it if an assertion fails mid-suite.
  [ -n "${SERVE_PID:-}" ] && kill "$SERVE_PID" 2>/dev/null || true
  [ -n "${SERVE2_PID:-}" ] && kill "$SERVE2_PID" 2>/dev/null || true
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

# Hostile tmux config mode (grove-168, extended to this suite by grove-215):
# GROVE_E2E_TMUX_CONF=hostile boots BOTH isolated servers with the common
# dotfiles pair base-index 1 + pane-base-index 1, which turns every literal
# ".0"/".1" pane target into a failure. `gv chat ls` resolves panes by %id
# and stamps them by %id, so this is the mode that proves it. -f only applies
# at server boot, so start them now (exit-empty off keeps them alive with no
# sessions) and every later tmux call joins them with the hostile options
# already global.
if [ "${GROVE_E2E_TMUX_CONF:-}" = "hostile" ]; then
  say "hostile tmux conf (base-index 1, pane-base-index 1) on both servers"
  cat > "$SCRATCH/hostile.conf" <<'EOF'
set -g base-index 1
set -g pane-base-index 1
set -g renumber-windows on
set -g allow-rename on
set -g exit-empty off
EOF
  env -u TMUX TMUX_TMPDIR="$TMUX_TMPDIR" tmux -f "$SCRATCH/hostile.conf" start-server
  env -u TMUX TMUX_TMPDIR="$REMOTE_TMUX" tmux -f "$SCRATCH/hostile.conf" start-server
fi

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
grep -q "attach: tmux attach -t '=grove-chat-chatws-1'" "$SCRATCH/spawn1.out" || fail "missing the host-side attach line"
# grove-207: both hints quote the exact-match target — a bare `=name` is
# equals-expanded by zsh (macOS) and the pasted line dies before ssh runs.
grep -q "from here: ssh -t localhost tmux attach -t '=grove-chat-chatws-1'" "$SCRATCH/spawn1.out" \
  || fail "missing the paste-able ssh attach line (must dial hosts.pc.ssh, target quoted)"

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
grep -q "from here: ssh -t localhost tmux attach -t '=grove-chat-chatws-3'" "$SCRATCH/retry.out" \
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

say "grove-215/222: gv chat ls joins live panes to Claude session ids"
# Live right now: chatws-1 and chatws-3 in the brain dir, chatws-2 in the
# per-profile dir. The two sharing ONE project dir are the case the ticket
# exists for — "the newest .jsonl" cannot separate them, and grove-222 is
# the proof that it must not try: these transcripts are decoys, written so
# that mtime order would pair them with the wrong panes.
ORCH="$WS/.grove/orchestrator"
export GV_CLAUDE_CONFIG_DIR="$SCRATCH/claude"
proj_dir() { printf '%s/projects/%s\n' "$GV_CLAUDE_CONFIG_DIR" "$(printf '%s' "$1" | sed 's#[/.]#-#g')"; }
write_transcript() { # <cwd> <session-id> <first prompt> <epoch mtime>
  local d; d="$(proj_dir "$1")"
  mkdir -p "$d"
  printf '{"type":"user","cwd":"%s","gitBranch":"main","message":{"role":"user","content":"%s"}}\n' "$1" "$3" > "$d/$2.jsonl"
  touch -d "@$4" "$d/$2.jsonl"
}
# Field of a row, read out of the pretty envelope: row_field <file> <session> <field>
row_field() { grep -A 12 "\"session\": \"$2\"" "$1" | grep -m1 "\"$3\":" | sed 's/.*: //; s/[",]//g'; }
# The identity stamp on a chat session's single pane, and the command line
# that was typed into it (scrollback, newlines stripped — the typed line
# hard-wraps at pane width, so a bare grep would miss it).
pane_stamp() { remote_tmux list-panes -t "=$1:chat" -F '#{@grove_chat_session}'; }
pane_cmd()   { remote_tmux capture-pane -p -S - -t "=$1:chat" | tr -d '\n'; }
NOW="$(date +%s)"
write_transcript "$ORCH" aaaa1111 "triage the artgen backlog" $((NOW - 3000))
write_transcript "$ORCH" bbbb2222 "write the release notes"   $((NOW - 1000))
write_transcript "$ORCH" cccc3333 "last tuesday"              $((NOW - 90000))

say "grove-222: each chat wears the id it was LAUNCHED on, from second zero"
# Known by construction: grove mints the UUID, hands it to `claude
# --session-id`, and stamps the pane at creation — no `ls` has run yet.
ID1="$(pane_stamp grove-chat-chatws-1)"
ID2="$(pane_stamp grove-chat-chatws-2)"
ID3="$(pane_stamp grove-chat-chatws-3)"
for pair in "1:$ID1" "2:$ID2" "3:$ID3"; do
  n="${pair%%:*}"; id="${pair#*:}"
  [ -n "$id" ] || fail "chat $n was never stamped with a session id at spawn"
  case "$id" in
    [0-9a-f]*-[0-9a-f]*-[0-9a-f]*-[0-9a-f]*-[0-9a-f]*) ;;
    *) fail "chat $n's minted id is not a UUID: '$id'" ;;
  esac
done
[ "$ID1" != "$ID3" ] && [ "$ID1" != "$ID2" ] && [ "$ID2" != "$ID3" ] \
  || fail "three chats must carry three DIFFERENT ids ($ID1 / $ID2 / $ID3)"
# The stamp is only worth anything if claude was actually told that id.
pane_cmd grove-chat-chatws-1 | grep -q -- "--session-id $ID1" \
  || fail "chat 1's launch does not carry --session-id $ID1"
pane_cmd grove-chat-chatws-3 | grep -q -- "--session-id $ID3" \
  || fail "chat 3's launch does not carry --session-id $ID3"
# And on the profiled chat the flag must land INSIDE the backend wrapper's
# exec — WrapProfile ends in `exec <cmd> )`, so anything appended after the
# wrap is handed to the shell instead of to claude.
pane_cmd grove-chat-chatws-2 | grep -q -- "--session-id $ID2 )" \
  || { pane_cmd grove-chat-chatws-2; fail "the profiled chat's --session-id landed outside the wrapper"; }

( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --json ) > "$SCRATCH/ls1.json" 2> "$SCRATCH/ls1.err"
cat "$SCRATCH/ls1.err"
grep -q '"schema_version"' "$SCRATCH/ls1.json" || { cat "$SCRATCH/ls1.json"; fail "chat ls --json must carry the contract envelope"; }
grep -q '"chats"' "$SCRATCH/ls1.json" || fail "chat ls --json must key its payload on chats"

[ "$(row_field "$SCRATCH/ls1.json" grove-chat-chatws-1 session_id)" = "$ID1" ] \
  || { cat "$SCRATCH/ls1.json"; fail "chat 1's row must report the id it runs on ($ID1)"; }
[ "$(row_field "$SCRATCH/ls1.json" grove-chat-chatws-2 session_id)" = "$ID2" ] \
  || { cat "$SCRATCH/ls1.json"; fail "chat 2's row must report the id it runs on ($ID2)"; }
[ "$(row_field "$SCRATCH/ls1.json" grove-chat-chatws-3 session_id)" = "$ID3" ] \
  || { cat "$SCRATCH/ls1.json"; fail "chat 3's row must report the id it runs on ($ID3)"; }
[ "$(row_field "$SCRATCH/ls1.json" grove-chat-chatws-1 kind)" = "chat" ] || fail "a live detached chat is kind chat"
[ "$(row_field "$SCRATCH/ls1.json" grove-chat-chatws-1 writable)" = "true" ] || fail "a live chat must be writable"
[ "$(row_field "$SCRATCH/ls1.json" grove-chat-chatws-1 workspace)" = "chatws" ] || fail "every row carries its workspace"

say "grove-222: the decoy transcripts are NOT handed to a live pane"
# aaaa1111 (older) and bbbb2222 (newer) are exactly the pair mtime order
# would have handed to chatws-3 and chatws-1. Nothing running claims them.
for decoy in aaaa1111 bbbb2222 cccc3333; do
  grep -B 2 "\"session_id\": \"$decoy\"" "$SCRATCH/ls1.json" | grep -q '"kind": "archived"' \
    || { cat "$SCRATCH/ls1.json"; fail "$decoy was claimed by a live pane — mtime pairing is back"; }
done
grep -A 4 '"session_id": "aaaa1111"' "$SCRATCH/ls1.json" | grep -q 'triage the artgen backlog' \
  || fail "an archived row keeps its transcript's first prompt as its label"

say "a second ls returns the SAME ids (never re-derived)"
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --json ) > "$SCRATCH/ls2.json" 2>&1
[ "$(row_field "$SCRATCH/ls2.json" grove-chat-chatws-1 session_id)" = "$ID1" ] || fail "chat 1's id moved between calls"
[ "$(row_field "$SCRATCH/ls2.json" grove-chat-chatws-3 session_id)" = "$ID3" ] || fail "chat 3's id moved between calls"

say "grove-222: two panes grove did not spawn refuse to guess between rivals"
# Hand-made sessions with the brain dir as cwd and no stamp: two unstamped
# rivals in ONE project dir with no ground truth to separate them. The old
# resolver ordered them by transcript mtime and inverted a live pair; the
# only honest answer is null.
remote_tmux new-session -d -s grove-chat-chatws-8 -n chat -c "$ORCH"
remote_tmux new-session -d -s grove-chat-chatws-9 -n chat -c "$ORCH"
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --json ) > "$SCRATCH/rivals.json" 2>&1
for n in 8 9; do
  [ "$(row_field "$SCRATCH/rivals.json" "grove-chat-chatws-$n" session_id)" = "null" ] \
    || { cat "$SCRATCH/rivals.json"; fail "chat $n guessed an id between two unidentifiable rivals"; }
  [ -z "$(pane_stamp "grove-chat-chatws-$n")" ] || fail "a guess must never be stamped onto chat $n"
done
say "…but ONE such pane is answerable: it is the only candidate in its dir"
remote_tmux kill-session -t '=grove-chat-chatws-9'
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --json ) > "$SCRATCH/sole.json" 2>&1
[ "$(row_field "$SCRATCH/sole.json" grove-chat-chatws-8 session_id)" = "bbbb2222" ] \
  || { cat "$SCRATCH/sole.json"; fail "the sole unidentified pane must take the newest unclaimed transcript"; }
remote_tmux kill-session -t '=grove-chat-chatws-8'
# Put bbbb2222 back on the shelf for the assertions below: the stamp died
# with the pane, so the next ls sees it unclaimed again.

say "a transcript with no live pane is kind archived, read-only"
grep -B 2 '"session_id": "cccc3333"' "$SCRATCH/ls2.json" | grep -q '"kind": "archived"' \
  || { cat "$SCRATCH/ls2.json"; fail "an unclaimed transcript must be kind archived"; }
grep -A 6 '"session_id": "cccc3333"' "$SCRATCH/ls2.json" | grep -q '"writable": false' \
  || fail "an archived transcript must never be writable"
grep -q '"session_id": "aaaa1111"' "$SCRATCH/ls2.json" || fail "aaaa1111 vanished from the report"
[ "$(grep -c '"session_id": "cccc3333"' "$SCRATCH/ls2.json")" -eq 1 ] \
  || fail "a transcript must be listed exactly once"

say "the cockpit's own orchestrator pane is kind cockpit, never kind chat"
# The dashboard pane sits at the workspace root; the orchestrator pane's cwd
# IS the brain dir — that cwd, not the name, is what tells them apart.
remote_tmux new-session -d -s grove-chatws -n cockpit -c "$WS"
remote_tmux split-window -d -t '=grove-chatws:cockpit' -c "$ORCH"
write_transcript "$ORCH" dddd4444 "the cockpit conversation" $((NOW - 100))
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --json ) > "$SCRATCH/ls3.json" 2>&1
[ "$(row_field "$SCRATCH/ls3.json" grove-chatws kind)" = "cockpit" ] \
  || { cat "$SCRATCH/ls3.json"; fail "the cockpit's orchestrator pane must be kind cockpit"; }
[ "$(row_field "$SCRATCH/ls3.json" grove-chatws writable)" = "false" ] || fail "a cockpit pane must never be writable"
[ "$(row_field "$SCRATCH/ls3.json" grove-chatws session_id)" = "dddd4444" ] \
  || fail "the cockpit pane resolves its own transcript"
[ "$(row_field "$SCRATCH/ls3.json" grove-chat-chatws-1 session_id)" = "$ID1" ] || fail "the cockpit pane stole a chat's id"
grep -q '"session": "grove-chatws"' "$SCRATCH/ls3.json" || fail "the cockpit row vanished"
# The human table says read-only too.
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls ) > "$SCRATCH/ls3.txt" 2>&1
cat "$SCRATCH/ls3.txt"
grep -q 'cockpit.*read-only' "$SCRATCH/ls3.txt" || fail "the table must mark a cockpit pane read-only"

say "no --workspace = every registered workspace; --workspace narrows"
OTHER="$SCRATCH/otherws"
mkrepo "$OTHER"
( cd "$OTHER" && "$GV" init --yes --label otherws > /dev/null )
mkdir -p "$OTHER/.grove/orchestrator"
write_transcript "$OTHER/.grove/orchestrator" eeee5555 "the other workspace's chat" $((NOW - 5000))
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --json ) > "$SCRATCH/ls4.json" 2>&1
grep -q '"workspace": "otherws"' "$SCRATCH/ls4.json" || { cat "$SCRATCH/ls4.json"; fail "ls with no --workspace must span every registered workspace"; }
grep -q '"workspace": "chatws"' "$SCRATCH/ls4.json" || fail "ls dropped the ambient workspace's rows"
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --workspace chatws --json ) > "$SCRATCH/ls5.json" 2>&1
grep -q '"workspace": "otherws"' "$SCRATCH/ls5.json" && fail "--workspace must narrow to one workspace" || true
grep -q '"workspace": "chatws"' "$SCRATCH/ls5.json" || fail "--workspace chatws returned nothing"
rc=0
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --workspace nope ) > "$SCRATCH/ls6.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "an unregistered --workspace label must exit non-zero"
grep -q 'no registered workspace' "$SCRATCH/ls6.out" || { cat "$SCRATCH/ls6.out"; fail "wrong unknown-workspace error"; }


say "grove-216: gv chat tail reads the TRANSCRIPT, in order, with stable seq"
# Never the pane: a capture is ANSI soup wrapped at pane width. Give an
# archived conversation a real shape — text, thinking, a tool call and its
# result — and one line that must project to NOTHING. It is named by its id,
# because a LIVE chat now wears the id grove minted for it (grove-222), not
# whichever transcript happens to sit newest in its dir.
AAAA="$(proj_dir "$ORCH")/aaaa1111.jsonl"
cat >> "$AAAA" <<EOF
{"type":"assistant","timestamp":"2026-08-31T09:00:03.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"three tickets, one stale","signature":"s"},{"type":"text","text":"Looking at the backlog now."}]}}
{"type":"assistant","timestamp":"2026-08-31T09:00:05.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"gv ls --json"}}]}}
{"type":"user","timestamp":"2026-08-31T09:00:06.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"grove-90 working"}]}}
{"type":"user","timestamp":"2026-08-31T09:00:07.000Z","isMeta":true,"message":{"role":"user","content":"<system-reminder>not conversation</system-reminder>"}}
{"type":"file-history-snapshot","messageId":"m1","snapshot":{}}
EOF
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat tail aaaa1111 ) > "$SCRATCH/tail1.jsonl" 2> "$SCRATCH/tail1.err"
cat "$SCRATCH/tail1.err"
[ "$(wc -l < "$SCRATCH/tail1.jsonl")" -eq 5 ] \
  || { cat "$SCRATCH/tail1.jsonl"; fail "tail must emit 5 entries (isMeta + bookkeeping lines project to nothing)"; }
sed -n 1p "$SCRATCH/tail1.jsonl" | grep -q '^{"seq":1,"role":"user","kind":"text","text":"triage the artgen backlog"' \
  || { cat "$SCRATCH/tail1.jsonl"; fail "entry 1 must be the first prompt"; }
sed -n 2p "$SCRATCH/tail1.jsonl" | grep -q '"seq":2,"role":"assistant","kind":"thinking"' || fail "entry 2 must be the thinking block"
sed -n 3p "$SCRATCH/tail1.jsonl" | grep -q '"seq":3,"role":"assistant","kind":"text","text":"Looking at the backlog now."' || fail "entry 3 wrong"
sed -n 4p "$SCRATCH/tail1.jsonl" | grep -q '"seq":4,"role":"assistant","kind":"tool_use","text":"{\\"command\\":\\"gv ls --json\\"}","tool":"Bash"' \
  || { sed -n 4p "$SCRATCH/tail1.jsonl"; fail "entry 4 must be the tool_use, carrying its input and tool name"; }
sed -n 5p "$SCRATCH/tail1.jsonl" | grep -q '"seq":5,"role":"user","kind":"tool_result","text":"grove-90 working","tool":"Bash"' \
  || { sed -n 5p "$SCRATCH/tail1.jsonl"; fail "entry 5 must be the tool_result, paired back to the tool NAME"; }
grep -q 'system-reminder' "$SCRATCH/tail1.jsonl" && fail "an isMeta line is not conversation and must not be emitted" || true

say "--since N resumes at N+1, and a session-id prefix names the same chat"
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat tail aaaa1111 --since 3 ) > "$SCRATCH/tail2.jsonl" 2>&1
[ "$(wc -l < "$SCRATCH/tail2.jsonl")" -eq 2 ] || { cat "$SCRATCH/tail2.jsonl"; fail "--since 3 must leave 2 entries"; }
head -1 "$SCRATCH/tail2.jsonl" | grep -q '^{"seq":4,' || { cat "$SCRATCH/tail2.jsonl"; fail "--since 3 must resume at seq 4"; }
# An archived transcript is read-only, not unreadable.
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat tail cccc3333 ) > "$SCRATCH/tail3.jsonl" 2>&1
grep -q '"text":"last tuesday"' "$SCRATCH/tail3.jsonl" || { cat "$SCRATCH/tail3.jsonl"; fail "an archived chat must still be tailable"; }
# An ambiguous target is refused, never picked: this ends in somebody's agent.
rc=0
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat tail nosuchchat ) > "$SCRATCH/tail4.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "an unknown chat target must exit non-zero"
grep -q 'no chat matching' "$SCRATCH/tail4.out" || { cat "$SCRATCH/tail4.out"; fail "wrong unknown-chat error"; }

say "--follow emits an appended entry within ~1s"
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" timeout 5 "$GV" chat tail aaaa1111 --follow --since 5 ) > "$SCRATCH/follow.jsonl" 2>&1 &
FOLLOW_PID=$!
sleep 0.6
printf '%s\n' '{"type":"assistant","timestamp":"2026-08-31T09:01:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"appended while following"}]}}' >> "$AAAA"
FOUND=0
for _ in $(seq 1 10); do
  if grep -q 'appended while following' "$SCRATCH/follow.jsonl" 2>/dev/null; then FOUND=1; break; fi
  sleep 0.1
done
kill "$FOLLOW_PID" 2>/dev/null || true
wait "$FOLLOW_PID" 2>/dev/null || true   # timeout 5 is the belt to this braces
[ "$FOUND" -eq 1 ] || { cat "$SCRATCH/follow.jsonl" 2>/dev/null; fail "--follow did not emit the appended entry within 1s"; }
grep -q '"seq":6,' "$SCRATCH/follow.jsonl" || { cat "$SCRATCH/follow.jsonl"; fail "a followed append must keep counting seq (6)"; }
[ "$(wc -l < "$SCRATCH/follow.jsonl")" -eq 1 ] || { cat "$SCRATCH/follow.jsonl"; fail "--since 5 must not replay the first five entries"; }
# Put chat 1's transcript back where its mtime was: the appends above made
# it the NEWEST in this dir, and grove-217's spawn-time-stamp test needs
# dddd4444 to hold that spot or it stops being a tripwire.
touch -d "@$((NOW - 3000))" "$AAAA"

say "grove-216: send/keys refuse every chat that is not writable"
# The gate is the `writable` FIELD (grove-215), so the CLI and a phone can
# never disagree about which chats take input.
rc=0
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat send cccc3333 "wake up" ) > "$SCRATCH/send-arch.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "sending to an ARCHIVED transcript must exit non-zero"
grep -q 'writable: false' "$SCRATCH/send-arch.out" || { cat "$SCRATCH/send-arch.out"; fail "the refusal must name the contract field"; }
grep -q 'gv orchestrator new --resume cccc3333' "$SCRATCH/send-arch.out" || fail "an archived refusal must point at the revive verb"
rc=0
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat send grove-chatws "wake up" ) > "$SCRATCH/send-cock.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || { cat "$SCRATCH/send-cock.out"; fail "sending to the COCKPIT's own orchestrator pane must exit non-zero"; }
grep -q 'kind cockpit' "$SCRATCH/send-cock.out" || { cat "$SCRATCH/send-cock.out"; fail "wrong cockpit refusal"; }
rc=0
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat keys grove-chatws 2 ) > "$SCRATCH/keys-cock.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "keys must honour the same writable gate as send"
grep -q 'writable: false' "$SCRATCH/keys-cock.out" || { cat "$SCRATCH/keys-cock.out"; fail "wrong keys refusal"; }

say "grove-216: spawn → send → tail sees the message (the whole loop)"
# A stand-in for claude that writes its transcript where Claude Code would
# and appends every line the relay SUBMITS — so "the message landed" is
# proven by the transcript, the same source `gv chat tail` reads, rather
# than by a scrape of the pane.
SENDWS="$SCRATCH/sendws"
mkrepo "$SENDWS"
( cd "$SENDWS" && "$GV" init --yes --label sendws > /dev/null )
cat > "$SCRATCH/bin/fakeclaude" <<EOF
#!/usr/bin/env bash
# Project dir = the ENCODED cwd, two substitutions (/ and .), the rule
# people drop — a one-rule sed points at a directory that does not exist.
cwd="\$(pwd)"
enc="\$(printf '%s' "\$cwd" | sed -e 's#/#-#g' -e 's#\.#-#g')"
dir="$GV_CLAUDE_CONFIG_DIR/projects/\$enc"
mkdir -p "\$dir"
# grove-222: honour --session-id exactly as claude does — the transcript is
# named by the id its LAUNCH was given, which is the whole mechanism under
# test (grove mints it, hands it over, and stamps the pane with it).
id=f0f0aaaa
while [ \$# -gt 0 ]; do
  case "\$1" in
    --session-id) id="\$2"; shift 2 ;;
    --session-id=*) id="\${1#--session-id=}"; shift ;;
    *) shift ;;
  esac
done
f="\$dir/\$id.jsonl"
printf '{"type":"user","cwd":"%s","gitBranch":"main","message":{"role":"user","content":"fake chat boot"}}\n' "\$cwd" >> "\$f"
while IFS= read -r line; do
  printf '{"type":"user","cwd":"%s","message":{"role":"user","content":"%s"}}\n' "\$cwd" "\$line" >> "\$f"
  printf '{"type":"assistant","cwd":"%s","message":{"role":"assistant","content":[{"type":"text","text":"ack: %s"}]}}\n' "\$cwd" "\$line" >> "\$f"
done
EOF
chmod +x "$SCRATCH/bin/fakeclaude"
cat >> "$SENDWS/.grove/config.yaml" <<EOF
orchestrator:
  claude: $SCRATCH/bin/fakeclaude
EOF
( cd "$WS" && "$GV" orchestrator new --host pc --workspace sendws ) > "$SCRATCH/sendspawn.out" 2>&1
grep -q 'grove-chat-sendws-1' "$SCRATCH/sendspawn.out" || { cat "$SCRATCH/sendspawn.out"; fail "the sendws chat did not spawn"; }
# The id grove minted at spawn is the one the agent runs on: it reached the
# program (which names its transcript after it) and the pane wears it.
SENDID="$(pane_stamp grove-chat-sendws-1)"
[ -n "$SENDID" ] || fail "the sendws chat was never stamped at spawn"
SENDF="$(proj_dir "$SENDWS/.grove/orchestrator")/$SENDID.jsonl"
for _ in $(seq 1 30); do [ -s "$SENDF" ] && break; sleep 0.1; done
[ -s "$SENDF" ] || { ls -la "$(proj_dir "$SENDWS/.grove/orchestrator")"; fail "the chat never wrote the transcript grove named for it ($SENDID)"; }

( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --json ) > "$SCRATCH/ls7.json" 2>&1
[ "$(row_field "$SCRATCH/ls7.json" grove-chat-sendws-1 session_id)" = "$SENDID" ] \
  || { cat "$SCRATCH/ls7.json"; fail "the sendws chat must report the id it was launched on ($SENDID)"; }
[ "$(row_field "$SCRATCH/ls7.json" grove-chat-sendws-1 writable)" = "true" ] || fail "a live chat must be writable"

( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat send grove-chat-sendws-1 "ship the release notes" ) \
  > "$SCRATCH/send.out" 2>&1 || { cat "$SCRATCH/send.out"; fail "gv chat send to a live chat must succeed"; }
cat "$SCRATCH/send.out"
grep -q '✓ sent to grove-chat-sendws-1' "$SCRATCH/send.out" || fail "send must confirm the chat it reached"
for _ in $(seq 1 30); do grep -q 'ship the release notes' "$SENDF" && break; sleep 0.1; done
grep -q 'ship the release notes' "$SENDF" || { cat "$SENDF"; fail "the message never reached the agent — delivered is not submitted"; }

( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat tail grove-chat-sendws-1 ) > "$SCRATCH/tail5.jsonl" 2>&1
cat "$SCRATCH/tail5.jsonl"
grep -q '"seq":1,"role":"user","kind":"text","text":"fake chat boot"' "$SCRATCH/tail5.jsonl" || fail "tail lost the chat's first prompt"
grep -q '"seq":2,"role":"user","kind":"text","text":"ship the release notes"' "$SCRATCH/tail5.jsonl" \
  || fail "tail must show the message gv chat send submitted"
grep -q '"seq":3,"role":"assistant","kind":"text","text":"ack: ship the release notes"' "$SCRATCH/tail5.jsonl" \
  || fail "tail must show the agent's answer"

say "gv chat keys delivers a raw char with NO Enter"
# The relay rule's own exception: a picker acts on the keypress itself, so
# there is nothing to submit — and nothing may reach the transcript.
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat keys grove-chat-sendws-1 7 ) > "$SCRATCH/keys.out" 2>&1 \
  || { cat "$SCRATCH/keys.out"; fail "gv chat keys to a live chat must succeed"; }
grep -q 'raw, no Enter' "$SCRATCH/keys.out" || { cat "$SCRATCH/keys.out"; fail "keys must say it sent no Enter"; }
sleep 0.5
remote_tmux capture-pane -p -t '=grove-chat-sendws-1:chat' > "$SCRATCH/keypane.txt"
LASTLINE="$(grep -v '^[[:space:]]*$' "$SCRATCH/keypane.txt" | tail -1)"
[ "$LASTLINE" = "7" ] || { cat "$SCRATCH/keypane.txt"; fail "the raw char must be sitting UNSUBMITTED on the input line, got '$LASTLINE'"; }
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat tail grove-chat-sendws-1 ) > "$SCRATCH/tail6.jsonl" 2>&1
[ "$(wc -l < "$SCRATCH/tail6.jsonl")" -eq "$(wc -l < "$SCRATCH/tail5.jsonl")" ] \
  || { cat "$SCRATCH/tail6.jsonl"; fail "a raw key must not submit anything"; }
rc=0
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat keys grove-chat-sendws-1 "$(printf 'hi\nthere')" ) > "$SCRATCH/keys2.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "keys must refuse a newline rather than translate it into a submit"
grep -q 'gv chat send' "$SCRATCH/keys2.out" || { cat "$SCRATCH/keys2.out"; fail "the newline refusal must point at send"; }

say "grove-222: a cleared stamp re-derives from the RUNNING process, not mtime"
# Wipe the identity and drop a decoy transcript NEWER than the chat's own.
# Recency says the decoy; the live agent's argv says otherwise, and it wins.
# This is the inversion from the ticket, reproduced with a real process.
SENDPANE="$(remote_tmux list-panes -t '=grove-chat-sendws-1:chat' -F '#{pane_id}')"
remote_tmux set-option -p -t "$SENDPANE" -u @grove_chat_session
[ -z "$(pane_stamp grove-chat-sendws-1)" ] || fail "precondition: the stamp must be cleared"
write_transcript "$SENDWS/.grove/orchestrator" f0f0dec0 "the decoy" $(( $(date +%s) + 60 ))
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --json ) > "$SCRATCH/rederive.json" 2>&1
[ "$(row_field "$SCRATCH/rederive.json" grove-chat-sendws-1 session_id)" = "$SENDID" ] \
  || { cat "$SCRATCH/rederive.json"; fail "an unstamped pane must re-derive $SENDID from its process, not the newest .jsonl"; }
[ "$(pane_stamp grove-chat-sendws-1)" = "$SENDID" ] || fail "the re-derived id must be written back to the pane"

say "grove-222: gv chat restamp corrects an identity by hand"
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat restamp grove-chat-sendws-1 ) > "$SCRATCH/restamp1.out" 2>&1 \
  || { cat "$SCRATCH/restamp1.out"; fail "gv chat restamp with no id must clear the stamp"; }
grep -q 'cleared' "$SCRATCH/restamp1.out" || { cat "$SCRATCH/restamp1.out"; fail "the clear must say so"; }
[ -z "$(pane_stamp grove-chat-sendws-1)" ] || fail "restamp with no id must leave the pane unstamped"
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat restamp grove-chat-sendws-1 f0f0dec0 ) > "$SCRATCH/restamp2.out" 2>&1 \
  || { cat "$SCRATCH/restamp2.out"; fail "restamping to a transcript in the chat's OWN dir must succeed"; }
[ "$(pane_stamp grove-chat-sendws-1)" = "f0f0dec0" ] || fail "the hand-written stamp did not land"
grep -q "was launched on $SENDID" "$SCRATCH/restamp2.out" \
  || { cat "$SCRATCH/restamp2.out"; fail "restamping against a live process must warn that the process wins"; }
rc=0
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat restamp grove-chat-sendws-1 nosuchid ) > "$SCRATCH/restamp3.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "restamping to an id with no transcript in the chat's dir must exit non-zero"
grep -q 'no transcript nosuchid' "$SCRATCH/restamp3.out" || { cat "$SCRATCH/restamp3.out"; fail "wrong unknown-id refusal"; }
# The refusal itself wrote nothing — but the lookup behind it is the same
# report `ls` runs, so the hand stamp that contradicts the live agent has
# already been corrected back. The process outranks the hand, everywhere.
[ "$(pane_stamp grove-chat-sendws-1)" = "$SENDID" ] \
  || fail "the stamp after a refused restamp = '$(pane_stamp grove-chat-sendws-1)', want the running agent's $SENDID"
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --json ) > "$SCRATCH/restamp-ls.json" 2>&1
[ "$(row_field "$SCRATCH/restamp-ls.json" grove-chat-sendws-1 session_id)" = "$SENDID" ] \
  || { cat "$SCRATCH/restamp-ls.json"; fail "ground truth must correct a hand stamp that contradicts the running agent"; }

# Leave the suite the tmux server the later sections expect.
remote_tmux kill-session -t '=grove-chat-sendws-1' 2>/dev/null || true

# Hand the rest of the suite the tmux server it expects: no cockpit session.
remote_tmux kill-session -t '=grove-chatws'

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

say "grove-203: gv audit sees the chats; gv park names them; --chats reaps them"
# park kills grove-<label> — which since grove-198 does NOT contain the chat
# sessions, so two claude processes used to survive it invisibly. Live now:
# grove-chat-chatws-2 and -3 (chatws-1 self-closed above).
[ "$(chat_sessions chatws)" -eq 2 ] || fail "precondition: want 2 live chats, got $(chat_sessions chatws)"
remote_tmux new-session -d -s grove-chatws -n cockpit -c "$WS"

( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" audit ) > "$SCRATCH/audit.out" 2>&1
cat "$SCRATCH/audit.out"
grep -q 'CHAT SESSIONS' "$SCRATCH/audit.out" || fail "gv audit must report the workspace's live chat sessions"
grep -q 'grove-chat-chatws-2' "$SCRATCH/audit.out" || fail "audit missed chat 2"
grep -q 'grove-chat-chatws-3' "$SCRATCH/audit.out" || fail "audit missed chat 3"
grep -q 'grove-chat-app' "$SCRATCH/audit.out" && fail "audit reported another workspace's COCKPIT as a chat" || true
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" audit --json ) > "$SCRATCH/audit.json" 2>&1
grep -q '"chat_sessions"' "$SCRATCH/audit.json" || fail "audit --json must carry chat_sessions"
grep -q '"session": "grove-chat-chatws-2"' "$SCRATCH/audit.json" || fail "audit --json chat_sessions must name the session"

say "park leaves the chats running — and says so"
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" park ) > "$SCRATCH/park.out" 2>&1
cat "$SCRATCH/park.out"
grep -q 'chat grove-chat-chatws-2 still running' "$SCRATCH/park.out" || fail "park must name each chat it leaves behind"
grep -q "attach: tmux attach -t '=grove-chat-chatws-3'" "$SCRATCH/park.out" || fail "the survivor line needs a paste-able attach hint"
grep -q '2 chat session(s) survive this park' "$SCRATCH/park.out" || fail "park must count the survivors"
grep -q 'gv park --chats' "$SCRATCH/park.out" || fail "park must name the flag that reaps them"
if remote_tmux has-session -t '=grove-chatws' 2>/dev/null; then fail "park did not kill the cockpit session"; fi
[ "$(chat_sessions chatws)" -eq 2 ] || fail "a default park must NOT kill the chats, got $(chat_sessions chatws)"
grep -q '"chats":"grove-chat-chatws-2,grove-chat-chatws-3"' "$EVENTS" \
  || fail "the parked event must durably record what park left running"
grep -q '"chats_killed"' "$EVENTS" && fail "a default park must not claim it killed anything" || true

say "gv park --chats reaps them, and the colliding cockpit survives"
remote_tmux new-session -d -s grove-chatws -n cockpit -c "$WS"
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" park --chats ) > "$SCRATCH/park2.out" 2>&1
cat "$SCRATCH/park2.out"
grep -q 'chat grove-chat-chatws-2 (pid [0-9]*) — killed' "$SCRATCH/park2.out" || fail "--chats must name what it killed"
grep -q 'survive this park' "$SCRATCH/park2.out" && fail "--chats leaves nothing behind" || true
[ "$(chat_sessions chatws)" -eq 0 ] || fail "--chats must kill every chat, $(chat_sessions chatws) left"
if remote_tmux has-session -t '=grove-chatws' 2>/dev/null; then fail "park --chats did not kill the cockpit"; fi
grep -q '"chats_killed":"true"' "$EVENTS" || fail "the parked event must record the reap"
# The name-shape collision, on the KILLING path this time: grove-chat-app is
# the REGISTERED cockpit of `chat-app`, not chat <n> of anything.
remote_tmux has-session -t '=grove-chat-app' 2>/dev/null || fail "park --chats killed a registered COCKPIT that merely looks like a chat"

say "grove-217: --resume revives an archived chat (identity survives)"
# Everything spawned above is dead now, so every transcript is archived —
# the state the ticket exists for ("pick up yesterday's grove chat").
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --json ) > "$SCRATCH/pre-resume.json" 2>&1
grep -B 2 '"session_id": "aaaa1111"' "$SCRATCH/pre-resume.json" | grep -q '"kind": "archived"' \
  || { cat "$SCRATCH/pre-resume.json"; fail "precondition: aaaa1111 must be archived before the revival"; }
[ "$(chat_sessions chatws)" -eq 0 ] || fail "precondition: no live chats, got $(chat_sessions chatws)"

( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" orchestrator new --workspace chatws --resume aaaa1111 ) \
  > "$SCRATCH/resume1.out" 2>&1 || { cat "$SCRATCH/resume1.out"; fail "the revival failed"; }
cat "$SCRATCH/resume1.out"
grep -q '✓ orchestrator chat grove-chat-chatws-1 — workspace chatws, resumed aaaa1111 (triage the artgen backlog)' "$SCRATCH/resume1.out" \
  || fail "the success line must name the revived conversation by id AND first prompt"
grep -q 'resumed idle' "$SCRATCH/resume1.out" || fail "a revival must say it opens idle — it does not auto-continue"
remote_tmux has-session -t '=grove-chat-chatws-1' 2>/dev/null || fail "the revived chat session is missing"

say "the launched command carries --resume <id>"
# Scrollback + newlines stripped (grove-75): the typed line hard-wraps at
# pane width, so a bare capture-pane grep would miss it.
remote_tmux capture-pane -p -S - -t '=grove-chat-chatws-1:chat' | tr -d '\n' > "$SCRATCH/resume1.pane"
grep -q -- '--resume aaaa1111' "$SCRATCH/resume1.pane" \
  || { cat "$SCRATCH/resume1.pane"; fail "the chat pane's command must carry --resume aaaa1111"; }

say "the pane wears the id from second ZERO (stamped at spawn, before any ls)"
# dddd4444 is the NEWEST transcript in this dir: without the spawn-time
# stamp, grove-215's lazy resolver would hand this pane that id instead.
STAMP_R="$(remote_tmux list-panes -t '=grove-chat-chatws-1:chat' -F '#{@grove_chat_session}')"
[ "$STAMP_R" = "aaaa1111" ] || fail "@grove_chat_session = '$STAMP_R', want aaaa1111 stamped at spawn"

say "gv chat ls now reports it as a live, writable chat — with the SAME id"
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat ls --json ) > "$SCRATCH/resume-ls.json" 2>&1
[ "$(row_field "$SCRATCH/resume-ls.json" grove-chat-chatws-1 session_id)" = "aaaa1111" ] \
  || { cat "$SCRATCH/resume-ls.json"; fail "the revived chat must keep the id it had while archived"; }
[ "$(row_field "$SCRATCH/resume-ls.json" grove-chat-chatws-1 kind)" = "chat" ] || fail "a revived chat is kind chat"
[ "$(row_field "$SCRATCH/resume-ls.json" grove-chat-chatws-1 writable)" = "true" ] || fail "a revived chat must be writable"
[ "$(row_field "$SCRATCH/resume-ls.json" grove-chat-chatws-1 label)" = "triage the artgen backlog" ] \
  || fail "the revived chat must keep its transcript's label"
[ "$(grep -c '"session_id": "aaaa1111"' "$SCRATCH/resume-ls.json")" -eq 1 ] \
  || fail "aaaa1111 must be listed ONCE — live now, no longer archived"
grep -q '"resume":"aaaa1111"' "$EVENTS" || fail "the spawn event must record which conversation was revived"

say "a PROFILED conversation revives in its own cwd, on its own backend"
write_transcript "$ORCH/e2e-glm" ffff6666 "the cheap lane" $((NOW - 2000))
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" orchestrator new --workspace chatws --resume ffff6666 ) \
  > "$SCRATCH/resume2.out" 2>&1 || { cat "$SCRATCH/resume2.out"; fail "the profiled revival failed"; }
cat "$SCRATCH/resume2.out"
grep -q 'profile e2e-glm' "$SCRATCH/resume2.out" \
  || fail "the backend must be inferred from the conversation's own cwd, not asked for"
PANE_CWD_R="$(remote_tmux display-message -p -t '=grove-chat-chatws-2:' '#{pane_current_path}')"
[ "$PANE_CWD_R" = "$ORCH/e2e-glm" ] || fail "profiled revival cwd = $PANE_CWD_R, want the profile dir"
remote_tmux capture-pane -p -S - -t '=grove-chat-chatws-2:chat' | tr -d '\n' > "$SCRATCH/resume2.pane"
grep -q -- '--resume ffff6666 )' "$SCRATCH/resume2.pane" \
  || { cat "$SCRATCH/resume2.pane"; fail "the flag must land INSIDE the backend wrapper's exec, not after it"; }
grep -q 'ANTHROPIC_BASE_URL' "$SCRATCH/resume2.pane" || fail "a profiled revival must run wrapped in its backend"

say "unknown / malformed / already-live ids spawn nothing"
for bad in "nosuchid:nothing to resume" "aaaa1111:already live"; do
  id="${bad%%:*}"; want="${bad#*:}"
  rc=0
  ( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" orchestrator new --workspace chatws --resume "$id" ) \
    > "$SCRATCH/badresume.out" 2>&1 || rc=$?
  [ "$rc" -ne 0 ] || { cat "$SCRATCH/badresume.out"; fail "--resume $id must exit non-zero"; }
  grep -q "$want" "$SCRATCH/badresume.out" || { cat "$SCRATCH/badresume.out"; fail "--resume $id: wrong error, want '$want'"; }
done
rc=0
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" orchestrator new --workspace chatws --resume 'x; touch /tmp/gv-217-pwned' ) \
  > "$SCRATCH/hostile.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || { cat "$SCRATCH/hostile.out"; fail "a shell-hostile session id must be refused"; }
grep -q 'not a Claude session id' "$SCRATCH/hostile.out" || { cat "$SCRATCH/hostile.out"; fail "wrong shape refusal"; }
[ ! -e /tmp/gv-217-pwned ] || { rm -f /tmp/gv-217-pwned; fail "a --resume id reached the pane's shell as syntax"; }
rc=0
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" orchestrator new --workspace chatws --resume bbbb2222 --profile e2e-glm ) \
  > "$SCRATCH/bothflags.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || { cat "$SCRATCH/bothflags.out"; fail "--resume with --profile must be refused"; }
grep -q 'mutually exclusive' "$SCRATCH/bothflags.out" || { cat "$SCRATCH/bothflags.out"; fail "wrong flag-conflict error"; }
[ "$(chat_sessions chatws)" -eq 2 ] || fail "every refused revival must create nothing, got $(chat_sessions chatws)"

say "--resume composes with --host (only the id travels)"
( cd "$WS" && "$GV" orchestrator new --host pc --resume bbbb2222 ) > "$SCRATCH/resume3.out" 2> "$SCRATCH/resume3.err"
cat "$SCRATCH/resume3.out" "$SCRATCH/resume3.err"
grep -Eq "\[fake ssh\] $GV orchestrator new --op-id [0-9a-f]{32} --as pc --workspace chatws --resume bbbb2222\$" "$SCRATCH/resume3.err" \
  || fail "relayed argv wrong — want the resumed id appended after --workspace"
grep -q 'resumed bbbb2222 (write the release notes)' "$SCRATCH/resume3.out" || fail "the relayed revival must name the conversation"
remote_tmux has-session -t '=grove-chat-chatws-3' 2>/dev/null || fail "the relayed revival spawned no session"

say "a relayed revival is idempotent too (ssh 255 must not double-spawn)"
echo 0 > "$SCRATCH/ssh-op-hops"
touch "$SCRATCH/ssh-fail-first"
( cd "$WS" && "$GV" orchestrator new --host pc --resume cccc3333 ) > "$SCRATCH/resume4.out" 2> "$SCRATCH/resume4.err"
cat "$SCRATCH/resume4.out" "$SCRATCH/resume4.err"
rm -f "$SCRATCH/ssh-fail-first"
grep -q 'already applied' "$SCRATCH/resume4.out" || fail "the retried revival did not hit the op-id receipt"
[ "$(cat "$SCRATCH/ssh-op-hops")" -eq 2 ] || fail "expected exactly 2 ssh hops, got $(cat "$SCRATCH/ssh-op-hops")"
[ "$(chat_sessions chatws)" -eq 4 ] || fail "the retry double-spawned: $(chat_sessions chatws) chat sessions, want 4"

say "outside a workspace, --resume needs an explicit label"
rc=0
( cd "$SCRATCH" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" orchestrator new --resume aaaa1111 ) > "$SCRATCH/nolabel2.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "a labelless --resume must exit non-zero"
grep -q 'needs a workspace' "$SCRATCH/nolabel2.out" || { cat "$SCRATCH/nolabel2.out"; fail "wrong no-workspace error"; }

say "the local half never spawns here"
tmux list-sessions -F '#{session_name}' 2>/dev/null | grep '^grove-chat-' && fail "chat sessions leaked onto the local server" || true

# ---------------------------------------------------------------------------
# grove-218: `gv chat serve` — the phone UI's listener.
#
# The point of testing it HERE rather than only in httptest: the httptest
# suite proves the server's own behavior against a fake backend, and this
# proves the backend is really wired to tmux, the registry and the
# transcript — an end-to-end message from an HTTP POST into a live agent,
# and its reply back out over SSE.
# ---------------------------------------------------------------------------

say "grove-218: gv chat serve binds LOOPBACK by default and serves the shell"
pick_port() {
  local p
  for _ in $(seq 1 40); do
    p=$((20000 + RANDOM % 20000))
    (exec 3<>"/dev/tcp/127.0.0.1/$p") 2>/dev/null && { exec 3>&- 3<&-; continue; }
    echo "$p"; return 0
  done
  fail "could not find a free port"
}
# The API is compact JSON (the CLI's is pretty-printed, so row_field cannot
# read it). Empty for a `session_id: null` row — grove-222 leaves that null
# rather than guessing, and a client must read it as absent, not as "".
api_field() { # <file> <session> <field>
  sed 's/},{/}\n{/g' "$1" | grep -F "\"session\":\"$2\"" | grep -o "\"$3\":\"[^\"]*\"" | head -1 | sed 's/.*:"//; s/"$//' || true
}

# A conversation nothing live is holding: by now every transcript the
# earlier sections wrote has been REVIVED, so a read-only assertion needs
# its own archived row rather than one of theirs.
write_transcript "$ORCH" dddd4444 "an archived conversation" $((NOW - 100000))

PORT="$(pick_port)"
SERVE_LOG="$SCRATCH/serve.log"
( cd "$SCRATCH" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat serve --port "$PORT" ) > "$SERVE_LOG" 2>&1 &
SERVE_PID=$!
serve_stop() { kill "$SERVE_PID" 2>/dev/null || true; wait "$SERVE_PID" 2>/dev/null || true; }
for _ in $(seq 1 60); do
  curl -fsS -o /dev/null "http://127.0.0.1:$PORT/api/chats" 2>/dev/null && break
  kill -0 "$SERVE_PID" 2>/dev/null || { cat "$SERVE_LOG"; fail "gv chat serve died on startup"; }
  sleep 0.2
done
curl -fsS -o /dev/null "http://127.0.0.1:$PORT/api/chats" || { cat "$SERVE_LOG"; fail "gv chat serve never came up"; }
cat "$SERVE_LOG"
grep -q 'tailscale serve --bg' "$SERVE_LOG" || { cat "$SERVE_LOG"; fail "a loopback bind must print the sanctioned exposure"; }
grep -q 'funnel' "$SERVE_LOG" || fail "the loopback banner must say funnel is never correct"

# The three screens are one page: shell, app, and the vendored markdown lib.
curl -fsS -D "$SCRATCH/idx.h" "http://127.0.0.1:$PORT/" > "$SCRATCH/idx.html" || fail "GET / failed"
grep -q '<title>gv chat</title>' "$SCRATCH/idx.html" || { head -5 "$SCRATCH/idx.html"; fail "GET / did not serve the embedded shell"; }
grep -qi "content-security-policy: default-src 'none'" "$SCRATCH/idx.h" || { cat "$SCRATCH/idx.h"; fail "the shell must carry the CSP that sanitizes rendered agent markdown"; }
curl -fsS "http://127.0.0.1:$PORT/app.js" > "$SCRATCH/app.js" || fail "app.js is not served"
curl -fsS "http://127.0.0.1:$PORT/marked.min.js" > "$SCRATCH/marked.js" || fail "marked.min.js is not served"
grep -q 'marked v12.0.2' "$SCRATCH/marked.js" || fail "the vendored marked must stay pinned at v12.0.2"
curl -fsS "http://127.0.0.1:$PORT/sw.js" > /dev/null || fail "the service worker is not served"

say "GET /api/chats is the grove-215 payload, byte-compatible with chat ls --json"
curl -fsS "http://127.0.0.1:$PORT/api/chats" > "$SCRATCH/api-chats.json" || fail "GET /api/chats failed"
grep -q '"schema_version"' "$SCRATCH/api-chats.json" || { cat "$SCRATCH/api-chats.json"; fail "the API must carry the contract envelope"; }
grep -q '"chats"' "$SCRATCH/api-chats.json" || fail "the API must key its payload on chats"
grep -q '"session":"grove-chat-chatws-1"' "$SCRATCH/api-chats.json" \
  || { cat "$SCRATCH/api-chats.json"; fail "the API must report the live chats the CLI reports"; }
grep -q '"kind":"archived"' "$SCRATCH/api-chats.json" || fail "the API must report archived transcripts too"

say "the scope boundary is a 404, not a comment: no route reaches done/untrack"
for path in /api/done /api/tasks /api/chats/grove-chat-chatws-1/done /api/chats/grove-chat-chatws-1/kill; do
  code="$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{}' "http://127.0.0.1:$PORT$path")"
  [ "$code" = "404" ] || fail "POST $path answered $code — gv chat serve serves orchestrator chats ONLY"
done

say "a write with a form content-type is refused (a cross-origin form must not steer an agent)"
code="$(curl -s -o "$SCRATCH/ct.out" -w '%{http_code}' -X POST \
  -H 'Content-Type: text/plain' -d '{"text":"hi"}' \
  "http://127.0.0.1:$PORT/api/chats/grove-chat-chatws-1/send")"
[ "$code" = "415" ] || { cat "$SCRATCH/ct.out"; fail "a text/plain POST answered $code, want 415"; }

say "read-only chats refuse writes over HTTP with the CLI's own words"
ARCHIVED_ID=dddd4444
code="$(curl -s -o "$SCRATCH/ro.out" -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' -d '{"text":"hi"}' \
  "http://127.0.0.1:$PORT/api/chats/$ARCHIVED_ID/send")"
[ "$code" = "409" ] || { cat "$SCRATCH/ro.out"; fail "a write to an archived chat answered $code, want 409"; }
grep -q 'archived transcript' "$SCRATCH/ro.out" || { cat "$SCRATCH/ro.out"; fail "the refusal must be chat.WriteRefusal's own text"; }
grep -q 'gv orchestrator new --resume' "$SCRATCH/ro.out" || fail "the refusal must name the way out (revive it)"

say "POST /api/workspaces/<l>/new spawns a chat and names it back"
SERVEWS="$SCRATCH/servews"
mkrepo "$SERVEWS"
( cd "$SERVEWS" && "$GV" init --yes --label servews > /dev/null )
cat >> "$SERVEWS/.grove/config.yaml" <<EOF
orchestrator:
  claude: $SCRATCH/bin/fakeclaude
EOF
curl -fsS -X POST -H 'Content-Type: application/json' -d '{}' \
  "http://127.0.0.1:$PORT/api/workspaces/servews/new" > "$SCRATCH/new.json" || { cat "$SERVE_LOG"; fail "POST new failed"; }
cat "$SCRATCH/new.json"
grep -q '"session":"grove-chat-servews-1"' "$SCRATCH/new.json" \
  || { cat "$SCRATCH/new.json"; fail "the spawn must answer with the session it created, so the phone can open it"; }
remote_tmux has-session -t '=grove-chat-servews-1' 2>/dev/null || fail "no chat session was actually created"
curl -fsS "http://127.0.0.1:$PORT/api/chats" > "$SCRATCH/api2.json"
grep -q '"session":"grove-chat-servews-1"' "$SCRATCH/api2.json" || fail "the new chat is missing from /api/chats"
# grove-222: a chat grove spawned carries the id grove MINTED, stamped on
# the pane before the agent booted — so the phone reads the transcript's
# name out of the report instead of guessing a filename, and a chat that
# has said nothing yet is still fully addressable.
SERVE_SID="$(api_field "$SCRATCH/api2.json" grove-chat-servews-1 session_id)"
case "$SERVE_SID" in
  ????????-????-????-????-????????????) : ;;
  *) cat "$SCRATCH/api2.json"; fail "the served row must carry the minted session id, got '$SERVE_SID'" ;;
esac
SERVEF="$(proj_dir "$SERVEWS/.grove/orchestrator")/$SERVE_SID.jsonl"
for _ in $(seq 1 30); do [ -s "$SERVEF" ] && break; sleep 0.1; done
[ -s "$SERVEF" ] || { ls -la "$(proj_dir "$SERVEWS/.grove/orchestrator")" 2>&1; fail "the spawned chat never wrote a transcript at its minted id"; }

say "a message POSTed from the browser reaches the live agent"
curl -fsS -X POST -H 'Content-Type: application/json' \
  -d '{"text":"ship the release notes"}' \
  "http://127.0.0.1:$PORT/api/chats/grove-chat-servews-1/send" > "$SCRATCH/send.json" \
  || { cat "$SCRATCH/send.json" "$SERVE_LOG"; fail "POST send failed"; }
for _ in $(seq 1 50); do grep -q 'ship the release notes' "$SERVEF" && break; sleep 0.1; done
grep -q 'ship the release notes' "$SERVEF" \
  || { cat "$SERVEF"; fail "the browser's message never reached the agent — delivered is not submitted"; }

say "…and its reply streams back over SSE"
# --max-time, not a pipe: --follow keeps the stream open forever by design,
# and a piped curl would report the PIPE's status rather than curl's.
curl -sN --max-time 4 "http://127.0.0.1:$PORT/api/chats/grove-chat-servews-1/events" > "$SCRATCH/sse.txt" 2>/dev/null || true
cat "$SCRATCH/sse.txt"
grep -q '^event: entry$' "$SCRATCH/sse.txt" || { fail "the stream emitted no entry events"; }
grep -q '"text":"ship the release notes"' "$SCRATCH/sse.txt" || fail "SSE lost the message the browser sent"
grep -q '"text":"ack: ship the release notes"' "$SCRATCH/sse.txt" || fail "SSE lost the agent's reply"
# The SSE payload IS the `gv chat tail` line: same bytes, so a browser and a
# pipe parse the same thing.
( cd "$WS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat tail grove-chat-servews-1 ) > "$SCRATCH/sse-cli.jsonl" 2>&1
while IFS= read -r line; do
  grep -qF "data: $line" "$SCRATCH/sse.txt" || { echo "$line"; fail "an SSE frame is not the CLI's own tail line"; }
done < "$SCRATCH/sse-cli.jsonl"

say "?since=N resumes where a client left off"
curl -sN --max-time 3 "http://127.0.0.1:$PORT/api/chats/grove-chat-servews-1/events?since=2&follow=0" > "$SCRATCH/sse2.txt" 2>/dev/null || true
grep -q '"seq":1' "$SCRATCH/sse2.txt" && fail "--since must suppress entries the client already has" || true
grep -q '"seq":3' "$SCRATCH/sse2.txt" || { cat "$SCRATCH/sse2.txt"; fail "--since 2 must resume at seq 3"; }

say "grove-225: zero model_profiles is an empty list, not a 404 or a 500"
# Nothing has written a global config yet, and the serve process runs from
# $SCRATCH (no .grove marker) — so this is the no-config host, which must
# answer [] and let the page render no picker at all.
curl -fsS "http://127.0.0.1:$PORT/api/profiles" > "$SCRATCH/profiles0.json" || fail "GET /api/profiles failed with no config"
cat "$SCRATCH/profiles0.json"
grep -q '"profiles":\[\]' "$SCRATCH/profiles0.json" \
  || { cat "$SCRATCH/profiles0.json"; fail "no profiles configured must be [], so the phone shows no picker"; }

say "grove-225: GET /api/profiles lists the host's model profiles, sorted"
# The picker lists what the SERVE PROCESS can see, and this one is in the
# global layer. Written here rather than in a workspace on purpose: the
# same layer merges into every workspace (config.LoadAt), which is what
# makes a profile picked on the phone resolvable where the spawn lands.
# The handler re-reads config per request, so no restart is needed.
mkdir -p "$HOME/.config/grove"
cat > "$HOME/.config/grove/config.yaml" <<EOF
model_profiles:
  e2e-zeta:
    base_url: https://zeta.example/api
    auth_token_env: ZETA_API_KEY
    opus: zeta/one
    sonnet: zeta/one
    haiku: zeta/one
  e2e-glm:
    base_url: https://openrouter.ai/api
    auth_token_env: OPENROUTER_API_KEY
    opus: z-ai/glm-5.2
    sonnet: z-ai/glm-5.2
    haiku: z-ai/glm-4.5-air
EOF
curl -fsS "http://127.0.0.1:$PORT/api/profiles" > "$SCRATCH/profiles.json" || fail "GET /api/profiles failed"
cat "$SCRATCH/profiles.json"
grep -q '"schema_version"' "$SCRATCH/profiles.json" || fail "the profiles route must carry the contract envelope"
grep -q '"profiles":\["e2e-glm","e2e-zeta"\]' "$SCRATCH/profiles.json" \
  || { cat "$SCRATCH/profiles.json"; fail "want the two configured profiles, SORTED, and names only"; }
# Names only: a phone never needs a profile's backend, and this page leaves
# the house.
grep -q 'ZETA_API_KEY\|openrouter.ai' "$SCRATCH/profiles.json" \
  && { cat "$SCRATCH/profiles.json"; fail "the profiles route must not serve base_url / auth env — names only"; } || true
# The route is a READ. Nothing on the closed table grew a write here.
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{}' "http://127.0.0.1:$PORT/api/profiles")"
[ "$code" = "405" ] || fail "POST /api/profiles answered $code, want 405"
# The page that ships must actually use it: the embedded app is the only
# client, and a UI that drifted off this route would look identical from
# here (the sheet just never opens).
grep -q "/api/profiles" "$SCRATCH/app.js" || fail "the embedded page never asks for the profile list"
grep -q 'Claude (host default)' "$SCRATCH/app.js" || fail "the sheet must offer the host default as its first row"

say "grove-225: a spawn WITH a profile runs the chat inside that backend's wrapper"
curl -fsS -X POST -H 'Content-Type: application/json' -d '{"profile":"e2e-glm"}' \
  "http://127.0.0.1:$PORT/api/workspaces/servews/new" > "$SCRATCH/new-profiled.json" \
  || { cat "$SERVE_LOG"; fail "a profiled POST new failed"; }
cat "$SCRATCH/new-profiled.json"
grep -q '"session":"grove-chat-servews-2"' "$SCRATCH/new-profiled.json" \
  || { cat "$SCRATCH/new-profiled.json"; fail "the profiled spawn must answer with the session it created"; }
PROFILED_CWD="$(remote_tmux display-message -p -t '=grove-chat-servews-2:' '#{pane_current_path}')"
[ "$PROFILED_CWD" = "$SERVEWS/.grove/orchestrator/e2e-glm" ] \
  || fail "phone-spawned profiled chat cwd = $PROFILED_CWD, want the per-profile dir"
pane_cmd grove-chat-servews-2 > "$SCRATCH/profiled.pane"
grep -q 'ANTHROPIC_BASE_URL' "$SCRATCH/profiled.pane" \
  || { cat "$SCRATCH/profiled.pane"; fail "the phone-spawned chat must run wrapped in the picked backend"; }

say "grove-225: a spawn with NO body at all is byte-compatible with grove-218"
curl -fsS -X POST -H 'Content-Type: application/json' \
  "http://127.0.0.1:$PORT/api/workspaces/servews/new" > "$SCRATCH/new-nobody.json" \
  || { cat "$SERVE_LOG"; fail "a bodyless POST new failed — the profile field is OPTIONAL"; }
grep -q '"session":"grove-chat-servews-3"' "$SCRATCH/new-nobody.json" \
  || { cat "$SCRATCH/new-nobody.json"; fail "a bodyless spawn must behave exactly as it did before"; }
NOBODY_CWD="$(remote_tmux display-message -p -t '=grove-chat-servews-3:' '#{pane_current_path}')"
[ "$NOBODY_CWD" = "$SERVEWS/.grove/orchestrator" ] \
  || fail "a bodyless spawn must land on the host default, got cwd $NOBODY_CWD"

say "grove-225: an unknown profile is the CLI's own refusal, and spawns nothing"
BEFORE_UNKNOWN="$(chat_sessions servews)"
code="$(curl -s -o "$SCRATCH/badprofile.out" -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' -d '{"profile":"nope"}' \
  "http://127.0.0.1:$PORT/api/workspaces/servews/new")"
cat "$SCRATCH/badprofile.out"
[ "$code" = "409" ] || fail "an unknown profile answered $code, want 409"
grep -q 'unknown model profile' "$SCRATCH/badprofile.out" \
  || fail "the refusal must be the CLI's own words (cfg.ResolveProfile)"
grep -q 'configured: e2e-glm, e2e-zeta' "$SCRATCH/badprofile.out" \
  || { cat "$SCRATCH/badprofile.out"; fail "the refusal must name what IS configured, as the CLI does"; }
[ "$(chat_sessions servews)" = "$BEFORE_UNKNOWN" ] || fail "a refused profile must create no session"
# `gv orchestrator new --profile nope` says the same thing — same code path,
# proven rather than asserted by eye.
rc=0
( cd "$SERVEWS" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" orchestrator new --workspace servews --profile nope ) \
  > "$SCRATCH/badprofile-cli.out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "the CLI must refuse an unknown profile too"
CLI_REFUSAL="$(grep -o 'unknown model profile.*' "$SCRATCH/badprofile-cli.out" | head -1)"
# The API carries it as a JSON string, so the quotes around the bad name
# arrive escaped — unescape before comparing, or this passes on nothing.
sed 's/\\"/"/g' "$SCRATCH/badprofile.out" > "$SCRATCH/badprofile.txt"
grep -qF "$CLI_REFUSAL" "$SCRATCH/badprofile.txt" \
  || { cat "$SCRATCH/badprofile.txt" "$SCRATCH/badprofile-cli.out"; fail "the HTTP refusal must be the CLI's text verbatim"; }

say "grove-225: a typo'd body key is refused, never a silent spawn on the default"
BEFORE_TYPO="$(chat_sessions servews)"
code="$(curl -s -o "$SCRATCH/typo.out" -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' -d '{"prof":"e2e-glm"}' \
  "http://127.0.0.1:$PORT/api/workspaces/servews/new")"
[ "$code" = "400" ] || { cat "$SCRATCH/typo.out"; fail "an unknown body field answered $code, want 400"; }
[ "$(chat_sessions servews)" = "$BEFORE_TYPO" ] || fail "a refused body must create no session"

remote_tmux kill-session -t '=grove-chat-servews-2' 2>/dev/null || true
remote_tmux kill-session -t '=grove-chat-servews-3' 2>/dev/null || true

say "raw keys: only a picker key, never free text"
curl -fsS -X POST -H 'Content-Type: application/json' -d '{"key":"7"}' \
  "http://127.0.0.1:$PORT/api/chats/grove-chat-servews-1/keys" > /dev/null || fail "a picker key must be accepted"
sleep 0.5
remote_tmux capture-pane -p -t '=grove-chat-servews-1:chat' > "$SCRATCH/servekeys.txt"
LASTLINE="$(grep -v '^[[:space:]]*$' "$SCRATCH/servekeys.txt" | tail -1)"
[ "$LASTLINE" = "7" ] || { cat "$SCRATCH/servekeys.txt"; fail "the raw key must sit UNSUBMITTED on the input line, got '$LASTLINE'"; }
for bad in '{"key":"gv done"}' '{"key":"\n"}' '{"key":"Enter"}'; do
  code="$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d "$bad" \
    "http://127.0.0.1:$PORT/api/chats/grove-chat-servews-1/keys")"
  [ "$code" = "400" ] || fail "keys $bad answered $code — a raw-key endpoint must not take free text"
done

say "POST /api/chats/<s>/resume revives an archived chat (grove-217 through HTTP)"
curl -fsS -X POST -H 'Content-Type: application/json' -d '{}' \
  "http://127.0.0.1:$PORT/api/chats/$ARCHIVED_ID/resume" > "$SCRATCH/resume-http.json" \
  || { cat "$SCRATCH/resume-http.json" "$SERVE_LOG"; fail "POST resume failed"; }
cat "$SCRATCH/resume-http.json"
grep -q '"session":"grove-chat-chatws-' "$SCRATCH/resume-http.json" \
  || { cat "$SCRATCH/resume-http.json"; fail "a revival must answer with the session it landed in"; }
REVIVED="$(sed 's/.*"session":"\([^"]*\)".*/\1/' "$SCRATCH/resume-http.json")"
remote_tmux capture-pane -p -S - -t "=$REVIVED:chat" | tr -d '\n' > "$SCRATCH/revived.pane"
grep -q -- "--resume $ARCHIVED_ID" "$SCRATCH/revived.pane" || { cat "$SCRATCH/revived.pane"; fail "the revived pane's launch must carry --resume"; }

serve_stop

say "a non-loopback bind requires the flag AND warns about what it exposes"
PORT2="$(pick_port)"
( cd "$SCRATCH" && env TMUX_TMPDIR="$REMOTE_TMUX" "$GV" chat serve --port "$PORT2" --bind 0.0.0.0 ) > "$SCRATCH/serve2.log" 2>&1 &
SERVE2_PID=$!
for _ in $(seq 1 40); do grep -q 'NOT loopback' "$SCRATCH/serve2.log" && break; sleep 0.1; done
kill "$SERVE2_PID" 2>/dev/null || true
wait "$SERVE2_PID" 2>/dev/null || true
cat "$SCRATCH/serve2.log"
grep -q 'NOT loopback' "$SCRATCH/serve2.log" || fail "a non-loopback bind must warn"
grep -q 'spawn new Claude sessions' "$SCRATCH/serve2.log" || fail "the warning must NAME what someone on that network could do"
grep -q 'tailscale funnel' "$SCRATCH/serve2.log" || fail "the warning must say funnel is never correct"
# The default is the loopback one: no flag, no warning.
grep -q 'NOT loopback' "$SERVE_LOG" && fail "the DEFAULT bind must be loopback and must not warn" || true

remote_tmux kill-session -t '=grove-chat-servews-1' 2>/dev/null || true

say "real tmux server untouched (canary)"
[ "$(real_tmux)" = "$REAL_TMUX_BEFORE" ] || fail "the REAL tmux server's session list changed — the suite leaked out of isolation"

printf '\n\033[32mchat e2e green\033[0m\n'
