#!/usr/bin/env bash
# grove-177 E2E: `gv handoff` moves a task between hosts.
#
# Dummy-data pattern with a fake second host: the "remote" is the same
# machine behind a fake `ssh` on PATH that re-runs the command with a
# second GROVE_STATE_DIR and a second ISOLATED tmux server. `gh` is faked
# too (a canned draft PR carrying the five handoff headings). Proves:
#   1. the mid-turn guard aborts before any mutation
#   2. --to: checkpoint nudge → idle wait → verify → untrack → remote adopt
#      → tombstone: task ends tracked on the remote state dir, untracked
#      locally, `gv ls --json` carries handed_off_to (additive field)
#   3. --from: the mirror pulls it back (remote release + local adopt)
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-handoff.XXXXXX)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

# Real-server canary (tmux-discipline, tapes/run.sh pattern): snapshot the
# machine's REAL tmux session list now, compare at the end — if this suite
# ever leaks onto the real server, fail loudly instead of silently.
real_tmux() { env -u TMUX -u TMUX_PANE -u TMUX_TMPDIR tmux list-sessions -F '#{session_name}' 2>/dev/null | sort; true; }
REAL_TMUX_BEFORE="$(real_tmux)"

export HOME="$SCRATCH/home"
export GROVE_STATE_DIR="$SCRATCH/state-local"
REMOTE_STATE="$SCRATCH/state-remote"
# $TMUX beats TMUX_TMPDIR — unset or every tmux call hits the REAL server.
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux-local"
REMOTE_TMUX="$SCRATCH/tmux-remote"
mkdir -p "$HOME" "$GROVE_STATE_DIR" "$REMOTE_STATE" "$TMUX_TMPDIR" "$REMOTE_TMUX" "$SCRATCH/bin"
cleanup() {
  env -u TMUX TMUX_TMPDIR="$TMUX_TMPDIR" tmux kill-server 2>/dev/null || true   # isolated servers only
  env -u TMUX TMUX_TMPDIR="$REMOTE_TMUX" tmux kill-server 2>/dev/null || true
  kill "${STOP_PID:-}" 2>/dev/null || true
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH"
}
trap cleanup EXIT

say "fake ssh + gh on PATH"
# ssh: parse like the real thing — skip options, take the first bare word
# as the TARGET and everything after -- as the command. The target must be
# the host's configured `ssh:` value (findings 8/9 escaped review because
# the old fake ignored it: gv could print/dial the config KEY, or a wrong
# name entirely, and this suite stayed green).
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
exec env GROVE_STATE_DIR="$REMOTE_STATE" TMUX_TMPDIR="$REMOTE_TMUX" sh -c "\$cmd"
EOF
# gh: one open draft PR for any branch, body = the five handoff headings.
cat > "$SCRATCH/bin/gh" <<'EOF'
#!/usr/bin/env bash
case "$*" in
  *"pr list"*) printf '[{"number":7,"url":"https://example.test/pr/7","body":"## Goal\\nmove it\\n## Done + verified\\nnothing yet\\n## Verified surprises\\nnone\\n## Remaining\\nall\\n## Next step\\nstart"}]' ;;
  *) echo '[]' ;;
esac
EOF
chmod +x "$SCRATCH/bin/ssh" "$SCRATCH/bin/gh"
export PATH="$SCRATCH/bin:$PATH"

say "scratch repo WITH a bare origin (verify needs ls-remote + push)"
ORIGIN="$SCRATCH/origin.git"
git init -q --bare -b main "$ORIGIN"
DUMMY="$SCRATCH/repos/dummy"
SESSION="grove-dummy"
mkdir -p "$DUMMY" && cd "$DUMMY"
git init -q -b main
git config user.email e2e@grove.test && git config user.name "grove e2e"
echo "# dummy" > README.md
git add -A && git commit -qm "init"
git remote add origin "$ORIGIN" && git push -q -u origin main

say "gv init + echo worker + host pc (ssh: localhost — faked)"
"$GV" init --yes > "$SCRATCH/init.out"
WCFG="$DUMMY/.grove/config.yaml"
perl -pi -e 's/^(\s*)base: main$/$1base: main\n$1claude: echo/' "$WCFG"
grep -q 'claude: echo' "$WCFG" || fail "claude stub not written"
printf 'hosts:\n  pc:\n    ssh: localhost\n    gv: %s\n' "$GV" >> "$WCFG"

say "gv grab task-001, push its branch"
"$GV" grab task-001 > "$SCRATCH/grab.out"
WTDIR="$(ls -d "$SCRATCH/repos/.worktrees/dummy"/task-001-*)"
BRANCH="$(git -C "$WTDIR" rev-parse --abbrev-ref HEAD)"
git -C "$WTDIR" push -q -u origin "$BRANCH"
# Hooks match cwd against the DERIVED tasks.json, which a Load (any ls)
# rebuilds — seed it before the first hook, as dummy.sh does.
"$GV" ls --json --no-pr --no-cost > /dev/null
printf '{"session_id":"s-h1","cwd":"%s","hook_event_name":"SessionStart"}' "$WTDIR" | "$GV" hook session-start

say "guard: a mid-turn worker (agent working) refuses before any mutation"
EV_LINES=$(wc -l < "$GROVE_STATE_DIR/events.jsonl")
("$GV" handoff task-001 --to pc --yes 2>&1 || true) > "$SCRATCH/guard.out"
grep -q 'mid-turn' "$SCRATCH/guard.out" || { cat "$SCRATCH/guard.out"; fail "missing mid-turn guard"; }
[ "$(wc -l < "$GROVE_STATE_DIR/events.jsonl")" -eq "$EV_LINES" ] || fail "guard abort wrote an event"

say "worker goes idle (Stop hook)"
printf '{"session_id":"s-h1","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"STATUS: DONE — scaffold"}' "$WTDIR" | "$GV" hook stop
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-idle.json"
grep -q '"agent": *"idle"' "$SCRATCH/ls-idle.json" || fail "worker not idle after Stop"

say "gv handoff task-001 --to pc --yes (checkpoint nudge, then a Stop hook flips it idle)"
# The nudge flips agent→working (EvAnswered); a delayed Stop hook is the
# echo worker's stand-in for "finished the checkpoint turn".
( sleep 4; printf '{"session_id":"s-h1","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"STATUS: DONE — handoff written"}' "$WTDIR" | "$GV" hook stop ) &
STOP_PID=$!
"$GV" handoff task-001 --to pc --yes --timeout 40s > "$SCRATCH/handoff.out" 2>&1 || { cat "$SCRATCH/handoff.out"; fail "handoff --to failed"; }
cat "$SCRATCH/handoff.out"
grep -q 'nudging task-001' "$SCRATCH/handoff.out" || fail "checkpoint nudge missing"
grep -q 'task-001 → pc' "$SCRATCH/handoff.out" || fail "follow line missing"
# The follow command must dial hosts.pc.ssh (localhost), never the config
# key, and resolve the window via the remote's own gv attach.
grep -q "ssh localhost -t $GV attach task-001" "$SCRATCH/handoff.out" || fail "follow line must be 'ssh <hosts.pc.ssh> -t <hosts.pc.gv> attach task-001'"

say "local: untracked + tombstone; window closed; worktree kept"
grep -q '"type":"task_untracked"' "$GROVE_STATE_DIR/events.jsonl" || fail "no task_untracked locally"
grep -q '"type":"task_handed_off"' "$GROVE_STATE_DIR/events.jsonl" || fail "no tombstone locally"
grep -q '"host":"pc"' "$GROVE_STATE_DIR/events.jsonl" || fail "tombstone missing host"
[ -d "$WTDIR" ] || fail "worktree must survive a --to without --rm"
tmux list-windows -t "=$SESSION" > "$SCRATCH/windows-local.out" 2>/dev/null || true
grep -q task-001 "$SCRATCH/windows-local.out" && fail "local worker window should be closed" || true
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-after.json"
grep -q '"handed_off_to": *"pc"' "$SCRATCH/ls-after.json" || { cat "$SCRATCH/ls-after.json"; fail "ls --json missing handed_off_to"; }
grep -q '"live": *"handed-off"' "$SCRATCH/ls-after.json" || fail "tombstone row not marked handed-off"
"$GV" ls --no-pr --no-cost > "$SCRATCH/ls-after.txt"
grep -q '→ pc' "$SCRATCH/ls-after.txt" || fail "ls table missing the → pc pointer"

say "remote: task adopted in the second state dir + second tmux server"
grep -q '"type":"task_adopted"' "$REMOTE_STATE/events.jsonl" || fail "remote did not adopt"
env -u TMUX TMUX_TMPDIR="$REMOTE_TMUX" tmux list-windows -t "=$SESSION" > "$SCRATCH/windows-remote.out"
grep -q task-001 "$SCRATCH/windows-remote.out" || fail "remote worker window missing"
GROVE_STATE_DIR="$REMOTE_STATE" "$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-remote.json"
grep -q '"task-001"' "$SCRATCH/ls-remote.json" || fail "remote ls missing task"
grep -q 'handed_off_to' "$SCRATCH/ls-remote.json" && fail "remote must carry no tombstone" || true

say "gv handoff task-001 --from pc --as mac --yes --no-checkpoint (the mirror)"
"$GV" handoff task-001 --from pc --as mac --yes --no-checkpoint > "$SCRATCH/from.out" 2>&1 || { cat "$SCRATCH/from.out"; fail "handoff --from failed"; }
cat "$SCRATCH/from.out"
grep -q 'released' "$SCRATCH/from.out" || fail "remote release missing"
grep -q 'adopted (pickup prompt)' "$SCRATCH/from.out" || fail "pull-back must be a cold adopt (pickup prompt, no stale --resume)"
grep -q '"type":"task_untracked"' "$REMOTE_STATE/events.jsonl" || fail "remote not untracked"
grep -q '"host":"mac"' "$REMOTE_STATE/events.jsonl" || fail "remote tombstone must carry the --as name"
# Tombstone ordering on a pull too: it lands on the remote via the
# post-adopt call-back, so it must be the LAST event written there.
tail -1 "$REMOTE_STATE/events.jsonl" | grep -q '"type":"task_handed_off"' || fail "remote tombstone must be the last write (post-adopt call-back)"
"$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-back.json"
grep -q '"task-001"' "$SCRATCH/ls-back.json" || fail "task not back locally"
grep -q '"handed_off_to"' "$SCRATCH/ls-back.json" && fail "local tombstone must clear on re-adopt" || true
tmux list-windows -t "=$SESSION" > "$SCRATCH/windows-back.out"
grep -q task-001 "$SCRATCH/windows-back.out" || fail "local worker window not rebuilt"

say "relay free text mentioning --host is relayed, not intercepted"
# --host is only parsed for verbs that support it (grab/ls/adopt/handoff):
# a nudge whose text mentions it must reach the pane as text, never
# reroute (or error) the whole command.
"$GV" nudge task-001 "when idle, compare with gv ls --host pc" > "$SCRATCH/nudge-host.out" 2>&1 || { cat "$SCRATCH/nudge-host.out"; fail "nudge with '--host' in its free text was intercepted"; }

say "tombstone terminal path: gv untrack drops the remote's pointer"
GROVE_STATE_DIR="$REMOTE_STATE" "$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-remote-tomb.json"
grep -q '"handed_off_to": *"mac"' "$SCRATCH/ls-remote-tomb.json" || fail "remote pointer missing before untrack"
GROVE_STATE_DIR="$REMOTE_STATE" "$GV" untrack task-001 > "$SCRATCH/untrack-tomb.out"
grep -q 'pointer dropped' "$SCRATCH/untrack-tomb.out" || { cat "$SCRATCH/untrack-tomb.out"; fail "untrack on a tombstone row must drop the pointer"; }
GROVE_STATE_DIR="$REMOTE_STATE" "$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-remote-after.json"
grep -q 'handed_off_to' "$SCRATCH/ls-remote-after.json" && fail "pointer must clear after untrack" || true

say "real tmux server untouched (canary)"
[ "$(real_tmux)" = "$REAL_TMUX_BEFORE" ] || fail "the REAL tmux server's session list changed — the suite leaked out of isolation"

printf '\n\033[32mhandoff e2e green\033[0m\n'
