#!/usr/bin/env bash
# gv keys E2E — two legs. (1) exec (design §9/§10): the value reaches only
# the child and appears nowhere gv writes. (2) gv-keys 03 (grab half): a profiled launch gets its secrets from the
# encrypted store at launch time, and the plaintext lands in the worker's
# environment ONLY — never a pane, state file, prompt, ls --json, shell
# history or the config tree (ciphertext only). Dummy-data pattern
# (e2e/dummy.sh): scratch HOME + GROVE_STATE_DIR + isolated tmux server.
# The fake-ssh half (push --host) is ticket 08.
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH=$(mktemp -d /tmp/grove-e2e-keys.XXXXXX) || exit 1
SCRATCH="$(cd "$SCRATCH" && pwd -P)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

REAL_HOME="$HOME"
export HOME="$SCRATCH/home"
export GROVE_STATE_DIR="$SCRATCH/state"
export HISTFILE="$SCRATCH/histfile"
mkdir -p "$HOME" "$GROVE_STATE_DIR"

snapshot_live() {
  for f in "$REAL_HOME/.local/state/grove/events.jsonl" \
           "$REAL_HOME/.config/grove/config.yaml" \
           "$REAL_HOME/.cc-work/settings.json"; do
    [ -e "$f" ] && ls -l "$f"
  done
  true
}
LIVE_BEFORE="$(snapshot_live)"
real_tmux() { env -u TMUX -u TMUX_PANE -u TMUX_TMPDIR tmux list-sessions -F '#{session_name}' 2>/dev/null | sort; true; }
REAL_TMUX_BEFORE="$(real_tmux)"

unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"
mkdir -p "$TMUX_TMPDIR"

mkdir -p "$SCRATCH/work" && cd "$SCRATCH/work"
say "exec leg: init + set (global namespace, outside any workspace)"
"$GV" keys init >/dev/null
printf hunter2 | "$GV" keys set DUMMY_KEY >/dev/null
printf other | "$GV" keys set DUMMY_OTHER >/dev/null

say "exec: value reaches the child"
"$GV" keys exec DUMMY_KEY -- sh -c 'printf %s "$DUMMY_KEY" > seen-exec'
[ "$(cat seen-exec)" = hunter2 ] || fail "seen-exec = $(cat seen-exec)"
out="$("$GV" keys exec DUMMY_KEY -- printenv DUMMY_OTHER || true)"
[ -z "$out" ] || fail "unnamed secret leaked: $out"

say "exec: child's exit code"
"$GV" keys exec DUMMY_KEY -- true || fail "true should exit 0"
rc=0; "$GV" keys exec DUMMY_KEY -- false || rc=$?
[ "$rc" = 1 ] || fail "false exit = $rc"

say "exec: usage error and unknown name never run the command"
rc=0; "$GV" keys exec DUMMY_KEY true 2>/dev/null || rc=$?
[ "$rc" != 0 ] || fail "no -- should fail"
rc=0; "$GV" keys exec NOPE_KEY -- touch fired 2>/dev/null || rc=$?
[ "$rc" != 0 ] || fail "unknown name should fail"
[ ! -e fired ] || fail "command ran despite unknown name"

say "exec: empty-arg warning"
"$GV" keys exec DUMMY_KEY -- sh -c 'echo ""' 2>err >/dev/null
[ ! -s err ] || fail "warned on a script-internal empty string"
"$GV" keys exec DUMMY_KEY -- echo "" 2>err >/dev/null
grep -q "sh -c" err || fail "no warning for an empty argument"

say "exec leg: hunter2 appears nowhere gv writes"
if grep -rl hunter2 "$GROVE_STATE_DIR" "$HOME" "$HISTFILE" 2>/dev/null; then
  fail "plaintext found on disk"
fi
"$GV" ls --json 2>/dev/null | grep -q hunter2 && fail "gv ls --json leaked the value"

SESSION="grove-dummy"
kill_tmux() {
  env -u TMUX TMUX_TMPDIR="$TMUX_TMPDIR" tmux kill-server 2>/dev/null || true
  for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
    [ -S "$TMUX_TMPDIR/tmux-$(id -u)/default" ] || break
    sleep 0.2
  done
}
cleanup() {
  kill_tmux
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH" 2>/dev/null || { sleep 1; rm -rf "$SCRATCH"; }
}
trap cleanup EXIT

say "scratch repo + workspace"
DUMMY="$SCRATCH/repos/dummy"
mkdir -p "$DUMMY" && cd "$DUMMY"
git init -q -b main
git config user.email e2e@grove.test && git config user.name "grove e2e"
echo "# dummy" > README.md
git add -A && git commit -qm "init"
"$GV" init --yes > "$SCRATCH/init.out"
WCFG="$DUMMY/.grove/config.yaml"
sed -e "s/^id: task-001/id: task-002/" -e "s/^title: .*/title: \"Second task\"/" .grove/tasks/task-001.md > .grove/tasks/task-002.md

say "gv keys init + set DUMMY_KEY (inside the workspace)"
"$GV" keys init > "$SCRATCH/keys-init.out"
printf 'hunter2' | "$GV" keys set DUMMY_KEY > "$SCRATCH/keys-set.out"

say "profiles + a fake claude that records what it sees"
FAKE_CLAUDE="$SCRATCH/fake-claude.sh"
cat > "$FAKE_CLAUDE" <<SHEOF
#!/bin/sh
printf '%s|%s|%s' "\$ANTHROPIC_AUTH_TOKEN" "\$DUMMY_EXTRA" "\$LIT" > "$SCRATCH/seen"
SHEOF
chmod +x "$FAKE_CLAUDE"
perl -pi -e "s#^(\s*)base: main\$#\$1base: main\n\$1claude: $FAKE_CLAUDE#" "$WCFG"
grep -q "claude: $FAKE_CLAUDE" "$WCFG" || fail "fake claude not written"
cat >> "$WCFG" <<'YEOF'
model_profiles:
  p:
    base_url: https://example.invalid
    auth_token_env: DUMMY_KEY
    sonnet: s
    opus: o
    haiku: h
    env:
      DUMMY_EXTRA: "secret:DUMMY_KEY"
      LIT: "plain"
  q:
    base_url: https://example.invalid
    auth_token_env: NOPE_KEY
    sonnet: s
    opus: o
    haiku: h
YEOF

wait_for() { # <file> — poll up to ~10s
  local i
  for i in $(seq 1 20); do [ -e "$1" ] && return 0; sleep 0.5; done
  return 1
}
pane_text() { # $1 = ticket → every pane of its window, scrollback joined
  local wid
  env -u TMUX tmux list-windows -t "=$SESSION" -F '#{window_id} #{window_name}' > "$SCRATCH/wins.txt"
  wid="$(grep "$1" "$SCRATCH/wins.txt" | head -1 | cut -d' ' -f1)"
  [ -n "$wid" ] || fail "no worker window for $1"
  : > "$SCRATCH/pane.txt"
  tmux list-panes -t "$wid" -F '#{pane_id}' > "$SCRATCH/panes.txt"
  while read -r p; do tmux capture-pane -p -S - -J -t "$p" >> "$SCRATCH/pane.txt"; done < "$SCRATCH/panes.txt"
}

say "gv grab task-001 --profile p"
"$GV" grab task-001 --repo dummy --profile p > "$SCRATCH/grab.out" || { cat "$SCRATCH/grab.out"; fail "grab failed"; }
wait_for "$SCRATCH/seen" || { pane_text task-001; cat "$SCRATCH/pane.txt"; fail "worker never ran (no seen file)"; }
sleep 1
[ "$(cat "$SCRATCH/seen")" = "hunter2|hunter2|plain" ] || fail "worker env wrong: $(cat "$SCRATCH/seen")"

say "plaintext absent from every surface"
pane_text task-001
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls.json"
grep -q 'hunter2' "$SCRATCH/pane.txt" && fail "hunter2 reached the worker pane" || true
grep -q 'hunter2' "$SCRATCH/ls.json" && fail "hunter2 reached gv ls --json" || true
grep -rl 'hunter2' "$GROVE_STATE_DIR" > "$SCRATCH/hits-state.txt" || true
[ -s "$SCRATCH/hits-state.txt" ] && { cat "$SCRATCH/hits-state.txt"; fail "hunter2 in state dir"; } || true
grep -rl 'hunter2' "$DUMMY/.grove" > "$SCRATCH/hits-ws.txt" || true
[ -s "$SCRATCH/hits-ws.txt" ] && { cat "$SCRATCH/hits-ws.txt"; fail "hunter2 in workspace .grove"; } || true
PROMPT="$GROVE_STATE_DIR/prompts/task-001.txt"
[ -f "$PROMPT" ] || fail "kickoff prompt missing"
grep -q 'hunter2' "$PROMPT" && fail "hunter2 in the kickoff prompt" || true
grep -rl 'hunter2' "$HOME/.config/grove" > "$SCRATCH/hits-cfg.txt" || true
[ -s "$SCRATCH/hits-cfg.txt" ] && { cat "$SCRATCH/hits-cfg.txt"; fail "hunter2 in ~/.config/grove (ciphertext only)"; } || true

say "unset secret: pane stays at a shell with the naming error, no claude"
rm -f "$SCRATCH/seen"
"$GV" grab task-002 --repo dummy --profile q > "$SCRATCH/grab2.out" || { cat "$SCRATCH/grab2.out"; fail "grab q failed"; }
sleep 3
pane_text task-002
grep -q 'NOPE_KEY' "$SCRATCH/pane.txt" || { cat "$SCRATCH/pane.txt"; fail "naming error not visible in the pane"; }
grep -q 'gv keys set' "$SCRATCH/pane.txt" || fail "fix hint missing from the pane"
[ -e "$SCRATCH/seen" ] && fail "claude ran despite the missing secret" || true
wid="$(grep task-002 "$SCRATCH/wins.txt" | head -1 | cut -d' ' -f1)"
cmd="$(tmux list-panes -t "$wid" -F '#{pane_current_command}' | head -1)"
case "$cmd" in bash|zsh|sh|fish|dash) ;; *) fail "pane is running $cmd, not a shell" ;; esac

say "shell history (flushed when the isolated server's shells exit)"
kill_tmux
sleep 1
if [ -e "$HISTFILE" ]; then
  grep -q 'hunter2' "$HISTFILE" && fail "hunter2 in HISTFILE" || true
fi

say "live state untouched"
[ "$LIVE_BEFORE" = "$(snapshot_live)" ] || fail "live grove state changed"
[ "$(real_tmux)" = "$REAL_TMUX_BEFORE" ] || fail "the REAL tmux server's session list changed"

say "PASS — profiled grab resolves secrets at launch; plaintext only in the worker env"
