#!/usr/bin/env bash
# grove-533: measured before/after of a focus swap — `main` (BENCH_BASE) vs
# this checkout — for each entry point: `gv chat focus`, a rail click, the
# CHATS box's enter. NOT part of e2e/all.sh: it asserts nothing, it prints a
# table (median of BENCH_RUNS runs, after one warm-up per entry point).
#
#   e2e/focus-bench.sh                 # 9 runs, base origin/main
#   BENCH_RUNS=15 BENCH_BASE=<rev> e2e/focus-bench.sh
#
# Fixture, per build and per pass (a fresh one each time): scratch HOME +
# GROVE_STATE_DIR + GV_CLAUDE_CONFIG_DIR, an isolated tmux server, 10
# workspaces, 200 synthetic transcripts in the cockpit's project dir, a
# 200x50 cockpit in focus with 3 chats running REAL Claude Code (`claude
# --resume` of 3 transcripts copied from ~/.claude/projects, or from
# BENCH_TRANSCRIPTS, under a scratch CLAUDE_CONFIG_DIR: not logged in, no
# API call — the screen is a full transcript that repaints on SIGWINCH).
#
# Two passes per build: "timing" (plain tmux) reads wall time, start →
# target pane in the slot, start → target visually settled (its capture
# unchanged for 300ms), start → rail redrawn (its last capture change,
# which then holds for 300ms too);
# "count" (a Go shim on PATH logs every tmux exec with its parent pid —
# the shim's own cost would skew the timings, so they never share a pass)
# reads the acting process's tmux exec count and summed exec time over
# start → settled. The poller uses the real tmux binary, so it is never
# counted. Poll resolution is one tmux exec (~3–6ms); times are taken
# after each poll returns.
#
# tmux: isolated — unsets TMUX/TMUX_PANE, scratch TMUX_TMPDIR, scoped
# kill-server, real-server canary before/after (tmux-discipline).
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RUNS="${BENCH_RUNS:-9}"
BASE="${BENCH_BASE:-origin/main}"
ROOT=$(mktemp -d /tmp/gvb.XXXXXX) || exit 1
ROOT="$(cd "$ROOT" && pwd -P)"
REAL_TMUX="$(command -v tmux)"
REAL_HOME="$HOME"
REAL_PATH="$PATH"

real_tmux() { env -u TMUX -u TMUX_PANE -u TMUX_TMPDIR "$REAL_TMUX" list-sessions -F '#{session_name}' 2>/dev/null | sort; true; }
REAL_TMUX_BEFORE="$(real_tmux)"

FIX=""   # the live fixture's scratch dir, for cleanup
kill_fixture() {
  [ -n "$FIX" ] || return 0
  env -u TMUX TMUX_TMPDIR="$FIX/t" "$REAL_TMUX" kill-server 2>/dev/null || true   # isolated server only
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -S "$FIX/t/tmux-$(id -u)/default" ] || break
    sleep 0.2
  done
  FIX=""
}
cleanup() {
  kill_fixture
  chmod -R u+w "$ROOT" 2>/dev/null || true
  case "$ROOT" in /tmp/gvb.*|/private/tmp/gvb.*) rm -rf "$ROOT" 2>/dev/null || true ;; esac
}
trap cleanup EXIT

say "build gv: $BASE and this checkout"
mkdir -p "$ROOT/src-base"
git -C "$REPO_ROOT" archive "$BASE" | tar -x -C "$ROOT/src-base"
(cd "$ROOT/src-base" && go build -o "$ROOT/gv-base" ./cmd/gv)
(cd "$REPO_ROOT" && go build -o "$ROOT/gv-tip" ./cmd/gv)
BASE_SHA="$(git -C "$REPO_ROOT" rev-parse --short "$BASE")"
TIP_SHA="$(git -C "$REPO_ROOT" rev-parse --short HEAD)"

say "tmux exec shim"
mkdir -p "$ROOT/shimsrc" "$ROOT/shim"
cat > "$ROOT/shimsrc/main.go" <<'GO'
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	t0 := time.Now().UnixNano()
	c := exec.Command(os.Getenv("BENCH_REAL_TMUX"), os.Args[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := c.Run()
	t1 := time.Now().UnixNano()
	if f, e := os.OpenFile(os.Getenv("BENCH_TMUX_LOG"), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); e == nil {
		fmt.Fprintf(f, "%d %d %d %s\n", t0, t1, os.Getppid(), strings.ReplaceAll(strings.Join(os.Args[1:], " "), "\n", `\n`))
		f.Close()
	}
	var x *exec.ExitError
	if errors.As(err, &x) {
		os.Exit(x.ExitCode())
	} else if err != nil {
		os.Exit(1)
	}
}
GO
(cd "$ROOT/shimsrc" && GOWORK=off go build -o "$ROOT/shim/tmux" main.go)

say "3 real transcripts"
mkdir -p "$ROOT/transcripts"
SRC="${BENCH_TRANSCRIPTS:-$REAL_HOME/.claude/projects}"
find "$SRC" -name '*.jsonl' -size +300k -size -900k > "$ROOT/transcripts.list" 2>/dev/null || true
sed -n 1,3p "$ROOT/transcripts.list" | while read -r f; do cp "$f" "$ROOT/transcripts/"; done
[ "$(ls "$ROOT/transcripts" | wc -l | tr -d ' ')" = 3 ] || fail "need 3 transcripts (300k–900k) under $SRC — set BENCH_TRANSCRIPTS"

cat > "$ROOT/drive.py" <<'PY'
import hashlib, json, os, random, subprocess, sys, time
TM, GV, WS, MODE, LOG, RUNS, OUT = os.environ["BENCH_REAL_TMUX"], sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], int(sys.argv[5]), sys.argv[6]
W = "=grove-alpha:cockpit"

def tm(*a):
    return subprocess.run([TM, *a], capture_output=True, text=True).stdout

def layout():
    rows = [l.split("\t") for l in tm("list-panes", "-t", W, "-F", "#{pane_id}\t#{@grove_rail}\t#{pane_pid}").splitlines()]
    dash = rows[0]
    rail = next(r for r in rows if r[1])
    slot = [r for r in rows[1:] if not r[1]]
    return dash, rail, slot

def acting(pid):  # a pane's process and its children
    kids = subprocess.run(["pgrep", "-P", pid], capture_output=True, text=True).stdout.split()
    return {int(pid), *map(int, kids)}

def poll(tgt, rail):
    out = tm("list-panes", "-t", W, "-F", "#{pane_id}", ";", "display-message", "-p", "-t", rail, "@@SEP@@",
             ";", "capture-pane", "-p", "-t", tgt, ";", "display-message", "-p", "-t", rail, "@@SEP@@",
             ";", "capture-pane", "-p", "-e", "-t", rail)
    a, b, c = out.split("@@SEP@@\n", 2)
    ps = a.split()
    return (ps[1] if len(ps) > 1 else ""), hashlib.md5(b.encode()).hexdigest(), hashlib.md5(c.encode()).hexdigest()

def dash_has(dash, s):
    return s in tm("capture-pane", "-p", "-t", dash)

def chats_cursor(dash, name):  # tab to CHATS, walk the cursor onto the row
    tm("select-pane", "-t", dash)
    for _ in range(4):
        if "▸▣" in tm("capture-pane", "-p", "-t", dash) or "▸○" in tm("capture-pane", "-p", "-t", dash):
            break
        tm("send-keys", "-t", dash, "Tab"); time.sleep(0.4)
    for k in "jjjjjjkkkkkkkkkkkk":
        cap = tm("capture-pane", "-p", "-t", dash)
        if any(l.find("▸") >= 0 and name in l[l.find("▸"):] for l in cap.splitlines()):
            return True
        tm("send-keys", "-t", dash, k); time.sleep(0.3)
    return False

def rail_col(rail, sess):
    rows = [l.split("\t") for l in tm("show-options", "-t", "=grove-alpha:", "-v", "@grove_rail_rows").splitlines()[1:] if l]
    idx = next(i for i, r in enumerate(rows) if len(r) > 4 and r[4] == sess)
    rw = int(tm("display-message", "-p", "-t", rail, "#{pane_width}").strip())
    slots = len(rows) if rw // len(rows) >= 16 else rw // 16
    cw = rw // slots - 1
    return idx * (cw + 1) + 3

def one(entry):
    dash, rail, slot = layout()
    shown = slot[0][0]
    hidden = [p for p in CHATS if p != shown]
    tgt = hidden[one.n % len(hidden)]; one.n += 1
    sess = tm("display-message", "-p", "-t", tgt, "#{session_name}").strip()
    name = tm("display-message", "-p", "-t", tgt, "#{@grove_chat_name}").strip()
    if entry == "chats" and not chats_cursor(dash[0], name):
        sys.exit("CHATS: no row for " + name)
    col = rail_col(rail[0], sess) if entry == "rail" else 0
    time.sleep(1.5)   # quiet: the dash/rail have drawn, any tick has passed
    _, _, rail0 = poll(tgt, rail[0])
    who = {"rail": acting(rail[2]), "chats": acting(dash[2])}.get(entry)
    t0 = time.time()
    if entry == "cli":
        proc = subprocess.Popen([GV, "chat", "focus", sess], cwd=WS, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        who = {proc.pid}
    elif entry == "rail":
        tm("send-keys", "-t", rail[0], "-l", "\033[<0;%d;1m" % col); proc = None
    else:
        tm("send-keys", "-t", dash[0], "Enter"); proc = None
    wall = in_slot = last = rail_t = None; h_last = None; hr_last = rail0
    while True:
        cur, h, hr = poll(tgt, rail[0]); now = time.time()
        if proc is not None and wall is None and proc.poll() is not None:
            wall = now
        if in_slot is None and cur == tgt:
            in_slot, last, h_last = now, now, h
        elif in_slot is not None and h != h_last:
            last, h_last = now, h
        if hr != hr_last:   # the rail's LAST change: its final picture, not a click echo
            rail_t, hr_last = now, hr
        quiet = in_slot is not None and now - max(last, rail_t or 0) >= 0.3
        if quiet and (rail_t or now - t0 > 5) and (proc is None or wall):
            break
        if now - t0 > 15:
            sys.exit("%s: %s never settled in the slot" % (entry, tgt))
    ms = lambda t: None if t is None else round((t - t0) * 1000, 1)
    r = {"entry": entry, "wall": ms(wall), "in_slot": ms(in_slot), "settled": ms(last), "rail": ms(rail_t)}
    if MODE == "count":
        time.sleep(0.3)
        lo, hi = int(t0 * 1e9), int(last * 1e9)
        execs = []
        for l in open(LOG):
            s, e, ppid, *_ = l.split(" ", 3)
            if lo <= int(s) <= hi and int(ppid) in who:
                execs.append((int(e) - int(s)) / 1e6)
        r["execs"], r["exec_ms"] = len(execs), round(sum(execs), 1)
    return r
one.n = 0

dash, rail, slot = layout()
CHATS = sys.argv[7].split()
res = []
for entry in ("cli", "rail", "chats"):
    for i in range(RUNS + 1):
        r = one(entry)
        print(" ", MODE, r, flush=True)
        if i:   # the first run is a warm-up
            res.append(r)
json.dump(res, open(OUT, "w"))
PY

fixture() { # <gv binary> <mode timing|count> <out.json>
  local GV="$1" MODE="$2" OUT="$3" WS i
  FIX=$(mktemp -d /tmp/gvb.f.XXXXXX) || exit 1
  FIX="$(cd "$FIX" && pwd -P)"
  export HOME="$FIX/home" GROVE_STATE_DIR="$FIX/state" GV_CLAUDE_CONFIG_DIR="$FIX/claude"
  export HISTFILE=/dev/null LESSHISTFILE=-
  export TMUX_TMPDIR="$FIX/t"
  unset TMUX TMUX_PANE
  mkdir -p "$HOME" "$GROVE_STATE_DIR" "$TMUX_TMPDIR" "$GV_CLAUDE_CONFIG_DIR"
  export BENCH_REAL_TMUX="$REAL_TMUX" BENCH_TMUX_LOG="$FIX/tmux.log"
  if [ "$MODE" = count ]; then export PATH="$ROOT/shim:$REAL_PATH"; else export PATH="$REAL_PATH"; fi
  printf 'set -g exit-empty off\n' > "$FIX/tmux.conf"
  tmux -f "$FIX/tmux.conf" start-server

  for i in 0 1 2 3 4 5 6 7 8 9; do   # 10 workspaces; ws0 = alpha has the cockpit
    local L="ws$i"; [ "$i" = 0 ] && L=alpha
    WS="$FIX/$L"; mkdir -p "$WS"
    git -C "$WS" init -q -b main
    git -C "$WS" config user.email b@x && git -C "$WS" config user.name b
    ( cd "$WS" && echo x > README.md && git add -A && git commit -qm init )
    ( cd "$WS" && "$GV" init --yes --label "$L" > /dev/null )
    printf 'orchestrator: {claude: echo}\n' >> "$WS/.grove/config.yaml"
  done
  WS="$FIX/alpha"
  local CWD="$WS/.grove/orchestrator"; mkdir -p "$CWD"
  local ENC; ENC="$(echo "$CWD" | sed 's#[/.]#-#g')"
  local PD="$GV_CLAUDE_CONFIG_DIR/projects/$ENC"; mkdir -p "$PD"
  for i in $(seq 1 200); do   # 200 archived transcripts
    printf '{"type":"user","cwd":"%s","sessionId":"00000000-0000-4000-8000-%012d","timestamp":"2026-10-01T10:00:00.000Z","message":{"role":"user","content":"hi %d"}}\n' "$CWD" "$i" "$i" > "$PD/00000000-0000-4000-8000-$(printf '%012d' "$i").jsonl"
    touch -t 202610011000 "$PD/00000000-0000-4000-8000-$(printf '%012d' "$i").jsonl"
  done
  cp "$ROOT"/transcripts/*.jsonl "$PD/"
  printf '{"hasCompletedOnboarding":true,"theme":"dark","projects":{"%s":{"hasTrustDialogAccepted":true,"hasCompletedProjectOnboarding":true}}}\n' "$CWD" > "$GV_CLAUDE_CONFIG_DIR/.claude.json"

  W='=grove-alpha:cockpit'
  ( cd "$WS" && "$GV" >/dev/null 2>&1 || true )
  tmux has-session -t '=grove-alpha' 2>/dev/null || fail "grove-alpha missing"
  tmux resize-window -t "$W" -x 200 -y 50
  sleep 1
  for i in 1 2; do
    tmux run-shell "cd '$WS' && '$GV' orchestrator new > '$FIX/spawn.out' 2>&1" || true
    grep -q 'new orchestrator chat pane' "$FIX/spawn.out" || { cat "$FIX/spawn.out"; fail "spawn $i"; }
  done
  local PANES; PANES="$(tmux list-panes -t "$W" -F '#{pane_id}')"
  [ "$(echo "$PANES" | wc -l | tr -d ' ')" = 4 ] || fail "want dash + 3 chats"
  local DASH; DASH="$(echo "$PANES" | head -1)"
  local CHATS; CHATS="$(echo "$PANES" | sed 1d | tr '\n' ' ')"
  i=0
  for f in "$ROOT"/transcripts/*.jsonl; do
    i=$((i + 1))
    local P; P="$(echo "$CHATS" | awk -v n=$i '{print $n}')"
    tmux respawn-pane -k -t "$P" "cd '$CWD' && CLAUDE_CONFIG_DIR='$GV_CLAUDE_CONFIG_DIR' exec claude --resume $(basename "$f" .jsonl)"
  done
  for P in $CHATS; do
    for _ in $(seq 1 60); do tmux capture-pane -p -t "$P" | grep -q '❯' && break; sleep 0.5; done
    tmux capture-pane -p -t "$P" | grep -q '❯' || fail "claude did not draw in $P"
  done
  tmux select-pane -t "$(echo "$CHATS" | awk '{print $2}')"
  sleep 1
  tmux send-keys -t "$DASH" L
  for _ in $(seq 1 60); do [ "$(tmux list-panes -t "$W" | wc -l | tr -d ' ')" = 3 ] && break; sleep 0.3; done
  [ "$(tmux list-panes -t "$W" | wc -l | tr -d ' ')" = 3 ] || fail "focus: want dash + chat + rail"
  sleep 4   # minimize, size sync, first rail draw, Claude's off-screen repaints
  : > "$BENCH_TMUX_LOG"
  python3 "$ROOT/drive.py" "$GV" "$WS" "$MODE" "$BENCH_TMUX_LOG" "$RUNS" "$OUT" "$CHATS"
  cp "$(ls "$WS/.grove/state/events.jsonl" "$GROVE_STATE_DIR/events.jsonl" 2>/dev/null | head -1)" "$OUT.events"
  kill_fixture
}

for b in base tip; do
  for m in timing count; do
    say "$b ($([ $b = base ] && echo "$BASE_SHA" || echo "$TIP_SHA")) — $m pass"
    fixture "$ROOT/gv-$b" "$m" "$ROOT/$b-$m.json"
  done
done

say "medians of $RUNS runs (ms) — base $BASE ($BASE_SHA) vs tip ($TIP_SHA)"
python3 - "$ROOT" "$BASE_SHA" "$TIP_SHA" <<'PY'
import json, statistics, sys
root, bsha, tsha = sys.argv[1:4]
def med(rows, k):
    v = [r[k] for r in rows if r.get(k) is not None]
    return "—" if not v else ("%d" % statistics.median(v) if k == "execs" else "%.0f" % statistics.median(v))
names = {"cli": "`gv chat focus`", "rail": "rail click", "chats": "CHATS `enter`"}
print("| entry | build | wall | → in slot | → settled | → rail redrawn | tmux execs | tmux exec time |")
print("|---|---|---|---|---|---|---|---|")
for e in ("cli", "rail", "chats"):
    for b, sha in (("base", bsha), ("tip", tsha)):
        t = [r for r in json.load(open("%s/%s-timing.json" % (root, b))) if r["entry"] == e]
        c = [r for r in json.load(open("%s/%s-count.json" % (root, b))) if r["entry"] == e]
        print("| %s | %s `%s` | %s | %s | %s | %s | %s | %s |" % (names[e], "main" if b == "base" else "tip", sha,
              med(t, "wall"), med(t, "in_slot"), med(t, "settled"), med(t, "rail"), med(c, "execs"), med(c, "exec_ms")))
PY

say "events written by the swaps: type + keys, base vs tip (plugin contract)"
python3 - "$ROOT" <<'PY'
import json, sys
def shapes(f):
    out = set()
    for l in open(f):
        e = json.loads(l)
        if e.get("type") in ("chat_hidden", "chat_shown"):
            out.add((e["type"], tuple(sorted(e)), tuple(sorted(e.get("data") or {}))))
    return out
b, t = shapes(sys.argv[1] + "/base-timing.json.events"), shapes(sys.argv[1] + "/tip-timing.json.events")
for s in sorted(b | t):
    print("  %-4s %s %s data=%s" % ("both" if s in b and s in t else ("base" if s in b else "TIP"), s[0], ",".join(s[1]), ",".join(s[2])))
print("  same shapes" if b == t else "  SHAPES DIFFER")
PY

REAL_TMUX_AFTER="$(env -u TMUX -u TMUX_PANE -u TMUX_TMPDIR HOME="$REAL_HOME" "$REAL_TMUX" list-sessions -F '#{session_name}' 2>/dev/null | sort; true)"
[ "$REAL_TMUX_BEFORE" = "$REAL_TMUX_AFTER" ] || fail "the real tmux server's sessions changed"
