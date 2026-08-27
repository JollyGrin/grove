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
# grove-186 fail-first knob: with the marker present, the FIRST --op-id
# hop runs the relayed command against the remote state and THEN reports
# the connection dead (exit 255) — delivery happened, but the sender
# cannot know. The hop counter lets the suite prove the sender retried
# exactly once.
if [ -f "$SCRATCH/ssh-fail-first" ] && [[ "\$cmd" == *--op-id* ]]; then
  n=\$(cat "$SCRATCH/ssh-op-hops" 2>/dev/null || echo 0)
  echo \$((n+1)) > "$SCRATCH/ssh-op-hops"
  if [ "\$n" -eq 0 ]; then
    env GROVE_STATE_DIR="$REMOTE_STATE" TMUX_TMPDIR="$REMOTE_TMUX" sh -c "\$cmd"
    exit 255
  fi
fi
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

say "adopt --sync refuses to discard committed-but-unpushed local work"
# The kept hand-edit checkout gains a local commit; --sync's dirty guard
# can't see it, so the ancestor check must refuse before the hard reset.
echo "hand edit" >> "$WTDIR/README.md"
git -C "$WTDIR" commit -qam "local-only hand edit"
("$GV" adopt task-001 --repo dummy --branch "$BRANCH" --sync 2>&1 || true) > "$SCRATCH/sync-ahead.out"
grep -q 'refuses to discard' "$SCRATCH/sync-ahead.out" || { cat "$SCRATCH/sync-ahead.out"; fail "adopt --sync must refuse when local commits are ahead of origin"; }
grep -q 'local-only hand edit' "$SCRATCH/sync-ahead.out" || fail "the refusal must name the ahead commit(s)"
git -C "$WTDIR" log --oneline -1 | grep -q 'local-only hand edit' || fail "the ahead commit must survive the refusal"
git -C "$WTDIR" reset -q --hard "origin/$BRANCH"   # undo the hand edit so the pull-back below is unchanged

say "grove-186: an ssh-255 retry delivers exactly once (op-id receipt)"
# The fake ssh's first --op-id hop runs the remote nudge, then dies with
# 255 — outcome unknown to the sender. The sender must re-run the SAME
# argv (same op id) once; the receiver's SeenOpID receipt makes that a
# no-op. Want: exactly 2 ssh hops, ONE paste into the remote pane, ONE
# answered event carrying the op id, "✓ already applied" on the retry.
touch "$SCRATCH/ssh-fail-first"
RMSG="grove-186 retry probe"
"$GV" nudge --host pc task-001 "$RMSG" > "$SCRATCH/relay-retry.out" 2>&1 || { cat "$SCRATCH/relay-retry.out"; fail "retried relayed nudge failed"; }
cat "$SCRATCH/relay-retry.out"
grep -q 'retrying once with the same op id' "$SCRATCH/relay-retry.out" || { cat "$SCRATCH/relay-retry.out"; fail "ssh 255 did not trigger the automatic same-op-id retry"; }
grep -q 'already applied' "$SCRATCH/relay-retry.out" || fail "the retry did not hit the op-id receipt"
rm -f "$SCRATCH/ssh-fail-first"
[ "$(cat "$SCRATCH/ssh-op-hops")" -eq 2 ] || fail "expected exactly 2 ssh hops (failed first + retry), got $(cat "$SCRATCH/ssh-op-hops")"
OP_EVENTS=$(grep -c '"type":"answered".*"op_id"' "$REMOTE_STATE/events.jsonl" || true)
[ "$OP_EVENTS" -eq 1 ] || fail "want exactly 1 answered event with an op id in the remote log, got $OP_EVENTS"
UNIQ_OPS=$(grep -o '"op_id":"[0-9a-f]*"' "$REMOTE_STATE/events.jsonl" | sort -u | wc -l || true)
[ "$UNIQ_OPS" -eq 1 ] || fail "expected one unique op id in the remote log, got $UNIQ_OPS"
# Exactly one paste: the probe text appears once across every pane of the
# remote worker window, scrollback included (grove-75: capture with -S -).
# Window names are "repo · ticket" — target the immutable @id (window_id
# already renders as "@N"), never the name (grove-116).
RWIN=$(env -u TMUX TMUX_TMPDIR="$REMOTE_TMUX" tmux list-windows -t "=$SESSION" -F '#{window_id} #{window_name}' | grep task-001 | awk '{print $1}')
[ -n "$RWIN" ] || fail "remote worker window vanished before the paste count"
# Squeeze ALL whitespace, not just newlines: capture-pane strips each
# line's trailing spaces, so a wrap landing on a space silently welds the
# words either side of it ("retry probe" → "retryprobe") and a plain
# `tr -d '\n'` grep finds nothing. Same defence as squeeze() in
# internal/tmux/ovs.go. grep -o | wc -l counts OCCURRENCES (grep -c would
# count matching lines — always 1 once the capture is a single line), so a
# double delivery still fails this assertion.
env -u TMUX TMUX_TMPDIR="$REMOTE_TMUX" tmux list-panes -t "$RWIN" -F '#{pane_id}' | while read -r p; do
  env -u TMUX TMUX_TMPDIR="$REMOTE_TMUX" tmux capture-pane -p -S - -t "$p"
done | tr -d '[:space:]' > "$SCRATCH/retry-pane.flat"
RSQUEEZED=$(printf '%s' "$RMSG" | tr -d '[:space:]')
PASTES=$(grep -o "$RSQUEEZED" "$SCRATCH/retry-pane.flat" | wc -l || true)
[ "$PASTES" -eq 1 ] || fail "want exactly 1 paste of the probe text into the remote pane, got $PASTES"
# The answered event flips the remote agent optimistically to working;
# park it idle again so the --from release below clears its mid-turn
# guard (the same Stop-hook flip the local side used before --to).
printf '{"session_id":"s-h1","cwd":"%s","hook_event_name":"Stop","last_assistant_message":"STATUS: DONE — retry probe consumed"}' "$WTDIR" | GROVE_STATE_DIR="$REMOTE_STATE" "$GV" hook stop

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
# The relay verbs (answer/nudge) match --host only in leading-flag
# position, before the ticket (ExtractHostPrefix): a nudge whose text
# mentions it must reach the pane as text, never reroute (or error) the
# whole command.
"$GV" nudge task-001 "when idle, compare with gv ls --host pc" > "$SCRATCH/nudge-host.out" 2>&1 || { cat "$SCRATCH/nudge-host.out"; fail "nudge with '--host' in its free text was intercepted"; }
# ...while a REAL --host flag on an unsupported verb gets the friendly
# supported-list error, not a flag-parse death.
("$GV" done task-001 --host pc 2>&1 || true) > "$SCRATCH/done-host.out"
grep -q 'supported: grab, ls, adopt, handoff, answer, nudge, diff, pause, untrack' "$SCRATCH/done-host.out" || { cat "$SCRATCH/done-host.out"; fail "gv done --host must return the friendly supported-list error"; }

say "tombstone terminal path: gv untrack drops the remote's pointer"
GROVE_STATE_DIR="$REMOTE_STATE" "$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-remote-tomb.json"
grep -q '"handed_off_to": *"mac"' "$SCRATCH/ls-remote-tomb.json" || fail "remote pointer missing before untrack"
GROVE_STATE_DIR="$REMOTE_STATE" "$GV" untrack task-001 > "$SCRATCH/untrack-tomb.out"
grep -q 'pointer dropped' "$SCRATCH/untrack-tomb.out" || { cat "$SCRATCH/untrack-tomb.out"; fail "untrack on a tombstone row must drop the pointer"; }
GROVE_STATE_DIR="$REMOTE_STATE" "$GV" ls --json --no-pr --no-cost > "$SCRATCH/ls-remote-after.json"
grep -q 'handed_off_to' "$SCRATCH/ls-remote-after.json" && fail "pointer must clear after untrack" || true

say "extended passthrough: nudge/diff/pause/untrack relay with --host stripped"
# grove-184: the new verbs hop with --host removed and every other flag
# intact (quoting per remote.Quote: bare tokens stay bare). The remote no
# longer tracks task-001 here (released + pointer dropped above), so the
# relayed gv errors AFTER the hop — the assertion is on what fake ssh
# received, not the remote's exit. nudge uses the leading-only rule:
# --host before the ticket relays; in the free-text test above it must
# not.
("$GV" nudge --host pc task-001 'ping' > "$SCRATCH/relay-nudge.out" 2>&1 || true)
# grove-186: relayed hops now carry a client --op-id (minted by the
# sender, leading position) — the receipt a retried hop dedups against.
grep -Eq "\[fake ssh\] $GV nudge --op-id [0-9a-f]{32} task-001 ping" "$SCRATCH/relay-nudge.out" || { cat "$SCRATCH/relay-nudge.out"; fail "nudge --host must reach ssh as 'nudge --op-id <32-hex-id> task-001 ping'"; }
("$GV" diff task-001 --stat --host pc > "$SCRATCH/relay-diff.out" 2>&1 || true)
grep -Fq "[fake ssh] $GV diff task-001 --stat" "$SCRATCH/relay-diff.out" || { cat "$SCRATCH/relay-diff.out"; fail "diff --stat --host must reach ssh with --host stripped"; }
("$GV" pause task-001 --force --host pc > "$SCRATCH/relay-pause.out" 2>&1 || true)
grep -Fq "[fake ssh] $GV pause task-001 --force" "$SCRATCH/relay-pause.out" || { cat "$SCRATCH/relay-pause.out"; fail "pause --force --host must reach ssh with --host stripped"; }
("$GV" untrack task-001 --rm --host pc > "$SCRATCH/relay-untrack.out" 2>&1 || true)
grep -Fq "[fake ssh] $GV untrack task-001 --rm" "$SCRATCH/relay-untrack.out" || { cat "$SCRATCH/relay-untrack.out"; fail "untrack --rm --host must reach ssh with --host stripped"; }

say "real tmux server untouched (canary)"
[ "$(real_tmux)" = "$REAL_TMUX_BEFORE" ] || fail "the REAL tmux server's session list changed — the suite leaked out of isolation"

printf '\n\033[32mhandoff e2e green\033[0m\n'
