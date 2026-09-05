#!/usr/bin/env bash
# Brain sweep E2E (grove-236): the dummy-data pattern — scratch HOME
# (registry + config) and scratch GROVE_STATE_DIR, no tmux, no network.
#
# Three registered workspaces cover the states that matter: one seeded
# from this binary's own seed (current), one with a hand-written brain
# that has no seed stamp (unstamped — the shape found on the operator's
# Mac), and one whose root is deleted after registration (missing-root,
# which must be a ROW and never an error). Asserts `gv brains --json`
# classifies all three, that the text report collapses the current one
# into its count, and — the invariant — that the sweep mutates NOTHING:
# both brains are byte-compared before and after, and no CLAUDE.md.new
# may appear anywhere.
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# macOS's /tmp is a symlink to /private/tmp — gv brains prints the registry
# root realpath'd, so an un-realpath'd scratch root never matches (grove-228
# had the same bug in chat.sh). Resolve with pwd -P up front.
SCRATCH="$(cd "$(mktemp -d /tmp/grove-brains.XXXXXX)" && pwd -P)"

# Build with the real environment BEFORE HOME is redirected — otherwise Go
# re-downloads its module cache into the scratch HOME (dummy.sh, same note).
say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

export HOME="$SCRATCH/home"
export GROVE_STATE_DIR="$SCRATCH/state"
export HISTFILE=/dev/null
mkdir -p "$HOME" "$GROVE_STATE_DIR"
cleanup() { chmod -R u+w "$SCRATCH" 2>/dev/null || true; rm -rf "$SCRATCH" 2>/dev/null || true; }
trap cleanup EXIT

mkrepo() { # dir
  mkdir -p "$1"
  git -C "$1" init -qb main
  git -C "$1" config user.email e2e@x && git -C "$1" config user.name e2e
  ( cd "$1" && echo x > README.md && git add -A && git commit -qm init )
}

say "workspace 'seeded': brain installed from this binary's own seed"
SEEDED="$SCRATCH/seeded"
mkrepo "$SEEDED"
( cd "$SEEDED" && "$GV" init --yes --label seeded > /dev/null )
( cd "$SEEDED" && "$GV" init --only orchestrator-md > /dev/null )
SEEDED_BRAIN="$SEEDED/.grove/orchestrator/CLAUDE.md"
[ -f "$SEEDED_BRAIN" ] || fail "orchestrator-md step did not install a brain"
grep -q '<!-- grove-seed: ' "$SEEDED_BRAIN" || fail "installed brain carries no seed stamp"

say "workspace 'handmade': hand-written brain, no seed stamp (pre-grove-190 shape)"
HAND="$SCRATCH/handmade"
mkrepo "$HAND"
( cd "$HAND" && "$GV" init --yes --label handmade > /dev/null )
mkdir -p "$HAND/.grove/orchestrator"
HAND_BRAIN="$HAND/.grove/orchestrator/CLAUDE.md"
printf '# my own orchestrator brain\n\nhand-managed since forever.\n' > "$HAND_BRAIN"

say "workspace 'ghost': registered, then its root deleted"
GHOST="$SCRATCH/ghost"
mkrepo "$GHOST"
( cd "$GHOST" && "$GV" init --yes --label ghost > /dev/null )
rm -rf "$GHOST"

say "registry holds all three"
"$GV" workspaces > "$SCRATCH/ws.out"
for l in seeded handmade ghost; do
  grep -q "$l" "$SCRATCH/ws.out" || fail "registry missing $l:
$(cat "$SCRATCH/ws.out")"
done

# Byte-exact snapshots: the sweep is a pure read and must prove it.
BEFORE_SEEDED="$(cksum < "$SEEDED_BRAIN")"
BEFORE_HAND="$(cksum < "$HAND_BRAIN")"

say "gv brains --json classifies every workspace"
"$GV" brains --json > "$SCRATCH/brains.json" || fail "gv brains --json exited non-zero"
python3 - "$SCRATCH/brains.json" <<'PY' || fail "brain sweep classification wrong (see above)"
import json, sys
doc = json.load(open(sys.argv[1]))
assert doc["schema_version"] == 1, doc
rows = {r["label"]: r for r in doc["brains"]}
want = {
    "seeded":   ("current",      ""),
    "handmade": ("unstamped",    "gv init --only orchestrator-md --force-orchestrator-md"),
    "ghost":    ("missing-root", ""),
}
ok = True
for label, (state, command) in want.items():
    r = rows.get(label)
    if r is None:
        print(f"  no row for {label}"); ok = False; continue
    if r["state"] != state:
        print(f"  {label}: state {r['state']!r}, want {state!r}"); ok = False
    if r["command"] != command:
        print(f"  {label}: command {r['command']!r}, want {command!r}"); ok = False
    for field in ("label", "root", "state", "have", "want", "command"):
        if field not in r:
            print(f"  {label}: contract field {field} missing"); ok = False
# a current brain's stamp is the seed's stamp
s = rows.get("seeded", {})
if s.get("have") != s.get("want") or not s.get("have"):
    print(f"  seeded: have/want must match and be non-empty, got {s.get('have')!r}/{s.get('want')!r}"); ok = False
if rows.get("handmade", {}).get("have") != "":
    print("  handmade: an unstamped brain has no stamp to report"); ok = False
sys.exit(0 if ok else 1)
PY

say "gv brains text report names the fix and collapses the current workspace"
"$GV" brains > "$SCRATCH/brains.txt" || fail "gv brains exited non-zero"
cat "$SCRATCH/brains.txt"
grep -q 'handmade' "$SCRATCH/brains.txt" || fail "unstamped workspace missing from the report"
grep -q -- '--force-orchestrator-md' "$SCRATCH/brains.txt" || fail "report must hand over the force command"
grep -q "cd $HAND && gv init" "$SCRATCH/brains.txt" || fail "report must say WHERE to run it"
grep -q 'ghost' "$SCRATCH/brains.txt" || fail "a vanished root must be reported, not dropped"
grep -q '✓ 1 workspace current' "$SCRATCH/brains.txt" || fail "current workspaces must collapse to a count"
grep -q '^seeded' "$SCRATCH/brains.txt" && fail "an up-to-date workspace must not get its own row" || true
grep -q 'never overwrites' "$SCRATCH/brains.txt" || fail "report must state the never-overwrite invariant"

say "the sweep mutated nothing"
[ "$(cksum < "$SEEDED_BRAIN")" = "$BEFORE_SEEDED" ] || fail "seeded brain changed under a pure-read sweep"
[ "$(cksum < "$HAND_BRAIN")" = "$BEFORE_HAND" ] || fail "hand-managed brain changed under a pure-read sweep"
NEW="$(find "$SCRATCH" -name 'CLAUDE.md.new' 2>/dev/null || true)"
[ -z "$NEW" ] || fail "sweep wrote CLAUDE.md.new — it is report-only:
$NEW"
[ -d "$GHOST" ] && fail "sweep recreated a deleted workspace root" || true

say "empty registry is a quiet, zero-exit line"
mkdir -p "$SCRATCH/empty"
HOME="$SCRATCH/empty" "$GV" brains > "$SCRATCH/empty.out" || fail "gv brains on an empty registry must exit 0"
grep -q 'no workspaces registered' "$SCRATCH/empty.out" || fail "empty registry message missing:
$(cat "$SCRATCH/empty.out")"

printf '\n\033[32mbrains E2E: PASS\033[0m — sweep classifies, reports, and mutates nothing\n'
