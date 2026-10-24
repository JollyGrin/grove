#!/usr/bin/env bash
# Brain sweep E2E (grove-236): the dummy-data pattern — scratch HOME
# (registry + config) and scratch GROVE_STATE_DIR, no tmux, no network.
#
# Five registered workspaces cover the states that matter: one seeded
# from this binary's own seed (current), one with a hand-written brain
# that has no seed stamp (unstamped — the shape found on the operator's
# Mac), one whose root is deleted after registration (missing-root,
# which must be a ROW and never an error), one PRISTINE stale brain (an
# older seed, byte-for-byte as grove wrote it) and one EDITED stale brain;
# plus the `(global)` row for the brain used outside any workspace
# (grove-511). Asserts `gv brains --json` classifies all of them, that the
# text report collapses the current one into its count, and — the
# invariant — that the plain sweep mutates NOTHING: every brain is
# byte-compared and mtime-compared before and after, and no CLAUDE.md.new
# may appear anywhere. Then `gv brains --refresh` (grove-511) replaces the
# pristine brain in place, seeds the global one, writes .new beside the
# edited one and leaves the hand-managed one alone — and deletes nothing.
# grove-512: the first seed writes the CLAUDE.local.md stub (comments only,
# `overlay: false`), a real overlay reports `overlay: true` + its line
# count, and no sweep or refresh ever writes over an existing overlay.
# grove-513: `gv brains --migrate` (dry run) drafts CLAUDE.local.md.draft
# beside each edited brain whose seed the archive knows — an appended
# section lands under Workspace duties, a DEV-X→grove-N brain becomes a
# Glossary line, a tracked brain gets its two git commands PRINTED — and
# every other file's bytes are unchanged; unknown stamps are reported.
# grove-514: `gv brains --migrate --apply --label L` swaps one reviewed
# draft in — CLAUDE.md → CLAUDE.md.bak-pre-overlay (byte-identical), the
# binary's seed → CLAUDE.md, draft → CLAUDE.local.md (over the stub) — and
# refuses, non-zero and touching nothing, on a pristine brain, a missing
# draft, a draft still carrying a Review block, a real CLAUDE.local.md or
# a declined prompt; a tracked brain's git commands are printed, not run;
# three workspaces (pristine / additive / conflicting) then read
# `pristine: true, overlay: true` from `gv brains --json`.
# tmux: never talks to a tmux server, so no tmux isolation needed.
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
# grove-512: the first seed drops the overlay stub beside the brain — HTML
# comments only, so it is `overlay: false` and costs the model nothing.
SEEDED_OVERLAY="$SEEDED/.grove/orchestrator/CLAUDE.local.md"
[ -f "$SEEDED_OVERLAY" ] || fail "first seed must write the CLAUDE.local.md stub"
grep -v '^<!--' "$SEEDED_OVERLAY" > "$SCRATCH/stub.rest" || true
[ ! -s "$SCRATCH/stub.rest" ] || fail "stub must be HTML comments only:
$(cat "$SEEDED_OVERLAY")"
grep -q 'docs/orchestrator-overlay.md' "$SEEDED_OVERLAY" || fail "stub must point at the convention doc"

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

# stamp_seed <body-file> <out>: write <body> + grove's stamp line, exactly as
# StampSeed does (sha256[:12] of the body with trailing newlines trimmed),
# so the result is PRISTINE — grove's own bytes for an older seed.
stamp_seed() {
  python3 - "$1" "$2" <<'PY'
import hashlib, sys
body = open(sys.argv[1]).read().rstrip("\n")
stamp = hashlib.sha256(body.encode()).hexdigest()[:12]
open(sys.argv[2], "w").write(body + "\n\n<!-- grove-seed: " + stamp + " -->\n")
PY
}

say "workspace 'pristine': an OLDER seed, byte-for-byte as grove wrote it (grove-511)"
PRISTINE="$SCRATCH/pristine"
mkrepo "$PRISTINE"
( cd "$PRISTINE" && "$GV" init --yes --label pristine > /dev/null )
mkdir -p "$PRISTINE/.grove/orchestrator"
PRISTINE_BRAIN="$PRISTINE/.grove/orchestrator/CLAUDE.md"
printf '# Orchestrator\n\nan older seed body\n' > "$SCRATCH/oldseed.md"
stamp_seed "$SCRATCH/oldseed.md" "$PRISTINE_BRAIN"
# A leftover from an earlier hand merge: listed by the report, never removed.
PRISTINE_BAK="$PRISTINE/.grove/orchestrator/CLAUDE.md.bak-seed-000000000000"
printf 'old backup\n' > "$PRISTINE_BAK"

say "workspace 'edited': the same older seed, plus operator prose"
EDITED="$SCRATCH/edited"
mkrepo "$EDITED"
( cd "$EDITED" && "$GV" init --yes --label edited > /dev/null )
mkdir -p "$EDITED/.grove/orchestrator"
EDITED_BRAIN="$EDITED/.grove/orchestrator/CLAUDE.md"
cp "$PRISTINE_BRAIN" "$EDITED_BRAIN"
# grove-512: a real overlay beside it — the stub's comments plus ONE line.
EDITED_OVERLAY="$EDITED/.grove/orchestrator/CLAUDE.local.md"
cp "$SEEDED_OVERLAY" "$EDITED_OVERLAY"
printf -- '- The operator is Dean.\n' >> "$EDITED_OVERLAY"
printf '\n## my own duty\n' >> "$EDITED_BRAIN"

# Byte-exact + mtime snapshots: the sweep is a pure read and must prove it.
mtime() { python3 -c 'import os,sys; print(os.stat(sys.argv[1]).st_mtime_ns)' "$1"; }
BEFORE_SEEDED="$(cksum < "$SEEDED_BRAIN")"
BEFORE_HAND="$(cksum < "$HAND_BRAIN")"
BEFORE_PRISTINE="$(cksum < "$PRISTINE_BRAIN")"
BEFORE_EDITED="$(cksum < "$EDITED_BRAIN")"
MT_BEFORE="$(for f in "$SEEDED_BRAIN" "$HAND_BRAIN" "$PRISTINE_BRAIN" "$EDITED_BRAIN" "$(dirname "$SEEDED_BRAIN")" "$(dirname "$PRISTINE_BRAIN")" "$(dirname "$EDITED_BRAIN")"; do mtime "$f"; done)"
GLOBAL_DIR="$HOME/.config/grove/orchestrator"
[ ! -e "$GLOBAL_DIR" ] || fail "scratch HOME must start with no global orchestrator dir"
# grove-512: every `gv init --yes` above seeded a stub beside its brain
# (handmade/pristine/edited overwrote only the brain). Snapshot the set and
# the bytes: no sweep or refresh may add to it or change one.
OVERLAYS_BEFORE="$(find "$SCRATCH" -name 'CLAUDE.local.md' -exec cksum {} + | sort)"

say "gv brains --json classifies every workspace"
"$GV" brains --json > "$SCRATCH/brains.json" || fail "gv brains --json exited non-zero"
python3 - "$SCRATCH/brains.json" <<'PY' || fail "brain sweep classification wrong (see above)"
import json, sys
doc = json.load(open(sys.argv[1]))
assert doc["schema_version"] == 1, doc
rows = {r["label"]: r for r in doc["brains"]}
want = {
    "seeded":   ("current",      "",                                                       True),
    "handmade": ("unstamped",    "gv init --only orchestrator-md --force-orchestrator-md", False),
    "ghost":    ("missing-root", "",                                                       False),
    # grove-511: pristine → the fleet-wide refresh; edited → today's .new path.
    "pristine": ("stale",        "gv brains --refresh",                                    True),
    "edited":   ("stale",        "gv init --only orchestrator-md",                         False),
    "(global)": ("absent",       "gv brains --refresh",                                    False),
}
# grove-512: only the edited workspace carries a real overlay; the seeded
# one has the comments-only stub, which counts as none.
overlay = {"edited": (True, 1)}
ok = True
if len(rows) != len(want):
    print(f"  {len(rows)} rows, want {len(want)}: {sorted(rows)}"); ok = False
for label, (state, command, pristine) in want.items():
    r = rows.get(label)
    if r is None:
        print(f"  no row for {label}"); ok = False; continue
    if (r.get("overlay"), r.get("overlay_lines")) != overlay.get(label, (False, 0)):
        print(f"  {label}: overlay {r.get('overlay')!r}/{r.get('overlay_lines')!r}, want {overlay.get(label, (False, 0))!r}"); ok = False
    if r["state"] != state:
        print(f"  {label}: state {r['state']!r}, want {state!r}"); ok = False
    if r["command"] != command:
        print(f"  {label}: command {r['command']!r}, want {command!r}"); ok = False
    if r["pristine"] is not pristine:
        print(f"  {label}: pristine {r['pristine']!r}, want {pristine!r}"); ok = False
    if r["action"] != "":
        print(f"  {label}: a plain sweep must leave action empty, got {r['action']!r}"); ok = False
    if not isinstance(r["leftovers"], list):
        print(f"  {label}: leftovers must be a list, got {r['leftovers']!r}"); ok = False
    for field in ("label", "root", "state", "pristine", "have", "want", "command", "note", "action", "leftovers", "overlay", "overlay_lines"):
        if field not in r:
            print(f"  {label}: contract field {field} missing"); ok = False
if "gv brains --migrate" not in rows.get("edited", {}).get("note", ""):
    print(f"  edited: note must point at the migrate verb (grove-512), got {rows.get('edited', {}).get('note')!r}"); ok = False
if "--migrate" in rows.get("pristine", {}).get("note", ""):
    print(f"  pristine: nothing to migrate, got {rows.get('pristine', {}).get('note')!r}"); ok = False
if rows.get("pristine", {}).get("leftovers") != ["CLAUDE.md.bak-seed-000000000000"]:
    print(f"  pristine: leftovers must list the .bak, got {rows.get('pristine', {}).get('leftovers')!r}"); ok = False
if rows.get("pristine", {}).get("have") != rows.get("edited", {}).get("have") or not rows.get("pristine", {}).get("have"):
    print("  pristine/edited: both carry the SAME old stamp"); ok = False
if rows.get("pristine", {}).get("have") == rows.get("pristine", {}).get("want"):
    print("  pristine: must be behind the embedded seed"); ok = False
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
grep -q '✓ 1 brain current' "$SCRATCH/brains.txt" || fail "current brains must collapse to a count"
grep -q '^. seeded' "$SCRATCH/brains.txt" && fail "an up-to-date workspace must not get its own row" || true
grep -q 'never overwrites' "$SCRATCH/brains.txt" || fail "report must state the never-overwrite invariant"
grep -q '(global)' "$SCRATCH/brains.txt" || fail "the global brain must get a row (grove-511)"
grep -q 'pristine' "$SCRATCH/brains.txt" || fail "a pristine stale brain must be named"
grep -q '1 pristine brain .*gv brains --refresh' "$SCRATCH/brains.txt" || fail "report must offer gv brains --refresh for the pristine brain"
grep -q "safe to delete" "$SCRATCH/brains.txt" || fail "report must list leftovers as safe to delete"
grep -q "$PRISTINE_BAK" "$SCRATCH/brains.txt" || fail "report must name the .bak leftover by path"
grep -q 'overlay: 1 line' "$SCRATCH/brains.txt" || fail "report must show the edited brain's overlay (grove-512)"
grep -q '1 brain carries a CLAUDE.local.md overlay' "$SCRATCH/brains.txt" || fail "report must count overlays (grove-512)"

say "the sweep mutated nothing — bytes AND mtimes"
[ "$(cksum < "$SEEDED_BRAIN")" = "$BEFORE_SEEDED" ] || fail "seeded brain changed under a pure-read sweep"
[ "$(cksum < "$HAND_BRAIN")" = "$BEFORE_HAND" ] || fail "hand-managed brain changed under a pure-read sweep"
[ "$(cksum < "$PRISTINE_BRAIN")" = "$BEFORE_PRISTINE" ] || fail "pristine brain changed under a pure-read sweep"
[ "$(cksum < "$EDITED_BRAIN")" = "$BEFORE_EDITED" ] || fail "edited brain changed under a pure-read sweep"
MT_AFTER="$(for f in "$SEEDED_BRAIN" "$HAND_BRAIN" "$PRISTINE_BRAIN" "$EDITED_BRAIN" "$(dirname "$SEEDED_BRAIN")" "$(dirname "$PRISTINE_BRAIN")" "$(dirname "$EDITED_BRAIN")"; do mtime "$f"; done)"
[ "$MT_AFTER" = "$MT_BEFORE" ] || fail "plain gv brains touched an mtime — it must be a pure read"
NEW="$(find "$SCRATCH" -name 'CLAUDE.md.new' 2>/dev/null || true)"
[ -z "$NEW" ] || fail "sweep wrote CLAUDE.md.new — it is report-only:
$NEW"
[ -d "$GHOST" ] && fail "sweep recreated a deleted workspace root" || true
[ ! -e "$GLOBAL_DIR" ] || fail "plain sweep created the global orchestrator dir"
[ -f "$PRISTINE_BAK" ] || fail "sweep removed a leftover"
[ "$(find "$SCRATCH" -name 'CLAUDE.local.md' -exec cksum {} + | sort)" = "$OVERLAYS_BEFORE" ] || fail "plain sweep wrote or changed a CLAUDE.local.md — the stub is a first-seed write only"

# grove-512: an overlay that exists BEFORE the first seed is the operator's
# — the refresh seeds the brain beside it and must not write the stub over it.
mkdir -p "$GLOBAL_DIR"
GLOBAL_OVERLAY="$GLOBAL_DIR/CLAUDE.local.md"
printf '# Workspace overlay: global\n\n## Glossary\n- The operator is Dean.\n' > "$GLOBAL_OVERLAY"
BEFORE_GLOBAL_OVERLAY="$(cksum < "$GLOBAL_OVERLAY")"

say "gv brains --refresh: pristine replaced in place, global seeded, edited gets .new, hand-managed untouched (grove-511)"
"$GV" brains --refresh --json > "$SCRATCH/refresh.json" || fail "gv brains --refresh --json exited non-zero"
python3 - "$SCRATCH/refresh.json" <<'PY' || fail "refresh actions wrong (see above)"
import json, sys
rows = {r["label"]: r for r in json.load(open(sys.argv[1]))["brains"]}
want = {"seeded": "none", "handmade": "report", "ghost": "report",
        "pristine": "refreshed", "edited": "new", "(global)": "seed"}
ok = True
for label, action in want.items():
    r = rows.get(label)
    if r is None:
        print(f"  no row for {label}"); ok = False; continue
    if r["action"] != action:
        print(f"  {label}: action {r['action']!r}, want {action!r}"); ok = False
    if action in ("refreshed", "seed") and r["command"]:
        print(f"  {label}: nothing left to run after {action}, got {r['command']!r}"); ok = False
if rows.get("edited", {}).get("leftovers") != ["CLAUDE.md.new"]:
    print(f"  edited: leftovers must now list the .new, got {rows.get('edited', {}).get('leftovers')!r}"); ok = False
sys.exit(0 if ok else 1)
PY
# The replaced brain is exactly what `gv init` installs: StampSeed(seed).
cmp -s "$PRISTINE_BRAIN" "$SEEDED_BRAIN" || fail "pristine brain must now be byte-identical to a fresh seed"
[ ! -f "$PRISTINE_BRAIN.new" ] || fail "a pristine refresh must not write .new"
[ -f "$PRISTINE_BAK" ] || fail "refresh removed a leftover — grove never deletes them"
[ "$(cksum < "$EDITED_BRAIN")" = "$BEFORE_EDITED" ] || fail "refresh overwrote an EDITED brain"
[ -f "$EDITED_BRAIN.new" ] || fail "an edited stale brain must get CLAUDE.md.new"
cmp -s "$EDITED_BRAIN.new" "$SEEDED_BRAIN" || fail ".new must carry the stamped current seed"
[ "$(cksum < "$HAND_BRAIN")" = "$BEFORE_HAND" ] || fail "refresh touched a hand-managed brain"
[ ! -f "$HAND_BRAIN.new" ] || fail "refresh must not nag an unstamped brain with .new"
[ "$(cksum < "$SEEDED_BRAIN")" = "$BEFORE_SEEDED" ] || fail "refresh rewrote a current brain"
[ -f "$GLOBAL_DIR/CLAUDE.md" ] || fail "refresh must seed the global brain"
cmp -s "$GLOBAL_DIR/CLAUDE.md" "$SEEDED_BRAIN" || fail "global brain must be the stamped seed"
[ "$(cksum < "$GLOBAL_OVERLAY")" = "$BEFORE_GLOBAL_OVERLAY" ] || fail "refresh wrote the stub over an existing CLAUDE.local.md"
[ "$(find "$SCRATCH" -path "$GLOBAL_DIR" -prune -o -name 'CLAUDE.local.md' -exec cksum {} + | sort)" = "$OVERLAYS_BEFORE" ] || fail "refresh wrote or changed a workspace CLAUDE.local.md — only the global dir was seeded"
[ -d "$GHOST" ] && fail "refresh recreated a deleted workspace root" || true

say "refresh text report says what it did; a second plain sweep finds them current"
"$GV" brains --refresh > "$SCRATCH/refresh.txt" || fail "second gv brains --refresh exited non-zero"
cat "$SCRATCH/refresh.txt"
grep -q 'edited' "$SCRATCH/refresh.txt" || fail "edited brain must still be reported after refresh"
grep -q "diff $EDITED_BRAIN" "$SCRATCH/refresh.txt" || fail "refresh must point at the .new to diff"
grep -q '✓ 3 brains current' "$SCRATCH/refresh.txt" || fail "pristine + global + seeded must now be current:
$(cat "$SCRATCH/refresh.txt")"
[ "$(cksum < "$EDITED_BRAIN")" = "$BEFORE_EDITED" ] || fail "second refresh overwrote the edited brain"
"$GV" brains --json > "$SCRATCH/brains2.json" || fail "gv brains --json after refresh exited non-zero"
python3 - "$SCRATCH/brains2.json" <<'PY' || fail "post-refresh classification wrong"
import json, sys
rows = {r["label"]: r for r in json.load(open(sys.argv[1]))["brains"]}
ok = True
for label in ("pristine", "(global)", "seeded"):
    r = rows[label]
    if r["state"] != "current" or not r["pristine"]:
        print(f"  {label}: {r['state']}/{r['pristine']}, want current/pristine"); ok = False
# grove-512: the pre-existing global overlay (3 real lines) survived the
# seed and is reported; the edited one still reads 1 line.
if (rows["(global)"]["overlay"], rows["(global)"]["overlay_lines"]) != (True, 3):
    print(f"  (global): overlay {rows['(global)']['overlay']!r}/{rows['(global)']['overlay_lines']!r}, want True/3"); ok = False
if (rows["edited"]["overlay"], rows["edited"]["overlay_lines"]) != (True, 1):
    print(f"  edited: overlay {rows['edited']['overlay']!r}/{rows['edited']['overlay_lines']!r}, want True/1"); ok = False
if rows["pristine"]["overlay"] or rows["seeded"]["overlay"]:
    print("  pristine/seeded: no real overlay, must read False"); ok = False
if rows["edited"]["state"] != "stale" or rows["edited"]["pristine"]:
    print(f"  edited: {rows['edited']}"); ok = False
sys.exit(0 if ok else 1)
PY

say "gv brains --migrate: drafts beside edited brains, nothing else written (grove-513)"
# mig-add: THIS binary's seed (its stamp is in the archive by construction)
# plus an own section appended after the stamp line → Workspace duties.
MIGADD="$SCRATCH/mig-add"
mkrepo "$MIGADD"
( cd "$MIGADD" && "$GV" init --yes --label mig-add > /dev/null )
MIGADD_BRAIN="$MIGADD/.grove/orchestrator/CLAUDE.md"
[ -f "$MIGADD_BRAIN" ] || fail "init did not seed mig-add"
printf '\n## Local pair testing\n\nRun both servers before any PR that touches the wire protocol.\n' >> "$MIGADD_BRAIN"
# mig-subst: the seed with every DEV-X swapped for grove-N — the fleet's
# commonest edit — and the brain force-added to git, as thegrid's is.
MIGSUB="$SCRATCH/mig-subst"
mkrepo "$MIGSUB"
( cd "$MIGSUB" && "$GV" init --yes --label mig-subst > /dev/null )
MIGSUB_BRAIN="$MIGSUB/.grove/orchestrator/CLAUDE.md"
sed 's/DEV-X/grove-N/g' "$MIGSUB_BRAIN" > "$SCRATCH/subst.md" && mv "$SCRATCH/subst.md" "$MIGSUB_BRAIN"
grep -q 'grove-N' "$MIGSUB_BRAIN" || fail "substitution fixture has no grove-N"
git -C "$MIGSUB" add -f .grove/orchestrator/CLAUDE.md && git -C "$MIGSUB" commit -qm brain
# Snapshot every file but the drafts: the dry run may add *.draft and
# nothing else, and may change no existing byte.
# -mindepth 2: the workspaces, HOME and state — not this script's own
# capture files (and the gv binary) at the scratch root.
snapshot() { find "$SCRATCH" -mindepth 2 -type f ! -name '*.draft' ! -path '*/.git/*' -exec cksum {} + | sort; }
SNAP_BEFORE="$(snapshot)"
"$GV" brains --migrate > "$SCRATCH/migrate.txt" || fail "gv brains --migrate exited non-zero:
$(cat "$SCRATCH/migrate.txt")"
cat "$SCRATCH/migrate.txt"
[ "$(snapshot)" = "$SNAP_BEFORE" ] || fail "--migrate changed or added a file other than *.draft:
$(diff <(printf '%s\n' "$SNAP_BEFORE") <(snapshot) || true)"
DRAFTS="$(find "$SCRATCH" -name 'CLAUDE.local.md.draft' | sort)"
[ "$DRAFTS" = "$(printf '%s\n%s' "$MIGADD/.grove/orchestrator/CLAUDE.local.md.draft" "$MIGSUB/.grove/orchestrator/CLAUDE.local.md.draft")" ] || fail "drafts must appear beside the two edited brains only, got:
$DRAFTS"
grep -q 'mig-add *draft *1 added / 0 substituted / 0 to review' "$SCRATCH/migrate.txt" || fail "mig-add must read 1 added / 0 / 0"
grep -q 'mig-subst *draft *0 added / [1-9][0-9]* substituted / 0 to review' "$SCRATCH/migrate.txt" || fail "mig-subst must read 0 / n / 0"
grep -q "cd $MIGSUB && git add -f .grove/orchestrator/CLAUDE.local.md" "$SCRATCH/migrate.txt" || fail "tracked brain: git add -f must be printed"
grep -q "cd $MIGSUB && git rm --cached .grove/orchestrator/CLAUDE.md" "$SCRATCH/migrate.txt" || fail "tracked brain: git rm --cached must be printed"
grep -q "cd $MIGADD && git" "$SCRATCH/migrate.txt" && fail "untracked brain must get no git commands" || true
git -C "$MIGSUB" ls-files --error-unmatch .grove/orchestrator/CLAUDE.md > /dev/null || fail "--migrate ran git rm --cached — it must only print it"
git -C "$MIGSUB" ls-files --error-unmatch .grove/orchestrator/CLAUDE.local.md > /dev/null 2>&1 && fail "--migrate ran git add — it must only print it" || true
grep -q 'edited *unknown-base .*not in the archive' "$SCRATCH/migrate.txt" || fail "a stamp the archive lacks must be reported as unknown base"
grep -q 'handmade *unknown-base .*no seed stamp' "$SCRATCH/migrate.txt" || fail "an unstamped brain must be reported as unknown base"
grep -q 'gv init --only orchestrator-md. and merge by hand' "$SCRATCH/migrate.txt" || fail "unknown base must hand over today's merge path"
grep -q 'ghost *missing-root' "$SCRATCH/migrate.txt" || fail "a vanished root is a row, not an error"
grep -q '✓ 3 brains have nothing to migrate' "$SCRATCH/migrate.txt" || fail "seeded + pristine + (global) must collapse into the nothing-to-migrate count"
grep -q '2 drafts drafted — dry run, nothing else written' "$SCRATCH/migrate.txt" || fail "summary line missing"
grep -q 'Review before applying' "$SCRATCH/migrate.txt" && fail "no draft here needs review" || true
grep -q "safe to delete" "$SCRATCH/migrate.txt" || fail "migrate must list leftovers too"
grep -q "$EDITED_BRAIN.new" "$SCRATCH/migrate.txt" || fail "migrate must name the .new leftover"
ADD_DRAFT="$MIGADD/.grove/orchestrator/CLAUDE.local.md.draft"
grep -q '^# Workspace overlay: mig-add' "$ADD_DRAFT" || fail "draft header missing"
grep -q '^## Workspace duties' "$ADD_DRAFT" || fail "appended section must land under Workspace duties"
grep -q '^## Local pair testing' "$ADD_DRAFT" || fail "appended section must be copied verbatim"
grep -q 'Review before applying' "$ADD_DRAFT" && fail "appended section must not need review" || true
grep -q '## Glossary' "$ADD_DRAFT" && fail "mig-add has no substitutions" || true
SUB_DRAFT="$MIGSUB/.grove/orchestrator/CLAUDE.local.md.draft"
grep -q '^## Glossary' "$SUB_DRAFT" || fail "substitution draft must carry a Glossary"
grep -q 'Wherever the seed writes `DEV-X`, read `grove-N`.' "$SUB_DRAFT" || fail "glossary line missing"
grep -c 'Wherever the seed writes' "$SUB_DRAFT" | grep -qx 1 || fail "exactly one glossary line for one token map"
grep -q 'Review before applying\|## Addenda\|## Workspace duties' "$SUB_DRAFT" && fail "substitution draft must be glossary only" || true

say "--migrate --label limits the run; a second run rewrites the same draft"
BEFORE_ADD_DRAFT="$(cksum < "$ADD_DRAFT")"
"$GV" brains --migrate --label mig-add > "$SCRATCH/migrate-label.txt" || fail "--migrate --label exited non-zero"
grep -q 'mig-add' "$SCRATCH/migrate-label.txt" || fail "--label mig-add must report mig-add"
grep -q 'mig-subst\|handmade\|edited' "$SCRATCH/migrate-label.txt" && fail "--label must leave the other rows out" || true
[ "$(cksum < "$ADD_DRAFT")" = "$BEFORE_ADD_DRAFT" ] || fail "re-running the dry run must render the same draft"
"$GV" brains --migrate --label nope > "$SCRATCH/migrate-nope.txt" 2>&1 && fail "an unknown label must fail" || true
[ "$(snapshot)" = "$SNAP_BEFORE" ] || fail "second --migrate changed a non-draft file"

say "gv brains --migrate --apply: every refusal exits non-zero and touches nothing (grove-514)"
# Everything under the workspaces, HOME and state — drafts included now.
snapshot_all() { find "$SCRATCH" -mindepth 2 -type f ! -path '*/.git/*' -exec cksum {} + | sort; }
# refuses: a pristine brain has nothing to migrate.
SNAP_ALL="$(snapshot_all)"
"$GV" brains --migrate --apply --label seeded --yes > "$SCRATCH/apply-pristine.txt" 2>&1 && fail "--apply on a pristine brain must exit non-zero" || true
grep -q 'pristine' "$SCRATCH/apply-pristine.txt" || fail "pristine refusal must say so:
$(cat "$SCRATCH/apply-pristine.txt")"
# refuses: no draft (edited is unknown-base, so the dry run drafted nothing).
"$GV" brains --migrate --apply --label edited --yes > "$SCRATCH/apply-nodraft.txt" 2>&1 && fail "--apply without a draft must exit non-zero" || true
grep -q "no $EDITED/.grove/orchestrator/CLAUDE.local.md.draft" "$SCRATCH/apply-nodraft.txt" || fail "missing-draft refusal must name the draft and the dry run:
$(cat "$SCRATCH/apply-nodraft.txt")"
grep -q 'gv brains --migrate --label edited' "$SCRATCH/apply-nodraft.txt" || fail "missing-draft refusal must hand over the dry-run command"
# refuses: --apply needs --label; --apply/--yes need --migrate.
"$GV" brains --migrate --apply --yes > "$SCRATCH/apply-nolabel.txt" 2>&1 && fail "--apply without --label must exit non-zero" || true
"$GV" brains --apply --label mig-add > "$SCRATCH/apply-nomigrate.txt" 2>&1 && fail "--apply without --migrate must exit non-zero" || true
# refuses: a real CLAUDE.local.md (one line past the stub) beside the brain.
SUB_OVERLAY="$MIGSUB/.grove/orchestrator/CLAUDE.local.md"
cp "$SUB_OVERLAY" "$SCRATCH/sub-stub.md"
printf -- '- The operator is Dean.\n' >> "$SUB_OVERLAY"
"$GV" brains --migrate --apply --label mig-subst --yes > "$SCRATCH/apply-overlay.txt" 2>&1 && fail "--apply over a real overlay must exit non-zero" || true
grep -q 'CLAUDE.local.md with real lines already exists' "$SCRATCH/apply-overlay.txt" || fail "real-overlay refusal missing:
$(cat "$SCRATCH/apply-overlay.txt")"
grep -q -- '- The operator is Dean.' "$SUB_OVERLAY" || fail "the real overlay must be untouched"
cp "$SCRATCH/sub-stub.md" "$SUB_OVERLAY"
# declined prompt: exit 0, nothing changed.
printf 'n\n' | "$GV" brains --migrate --apply --label mig-add > "$SCRATCH/apply-decline.txt" 2>&1 || fail "a declined prompt must exit 0:
$(cat "$SCRATCH/apply-decline.txt")"
grep -q 'apply? \[y/N\]' "$SCRATCH/apply-decline.txt" || fail "without --yes the prompt must show"
grep -q 'not applied — nothing changed' "$SCRATCH/apply-decline.txt" || fail "declining must say nothing changed"
[ "$(snapshot_all)" = "$SNAP_ALL" ] || fail "a refusal or a declined prompt changed a file:
$(diff <(printf '%s\n' "$SNAP_ALL") <(snapshot_all) || true)"

say "--apply on the additive draft: seed in, original parked, draft is the overlay"
BEFORE_ADD_BRAIN="$(cksum < "$MIGADD_BRAIN")"
ADD_DRAFT_BYTES="$(cksum < "$ADD_DRAFT")"
ADD_OVERLAY="$MIGADD/.grove/orchestrator/CLAUDE.local.md"
"$GV" brains --migrate --apply --label mig-add --yes > "$SCRATCH/apply-add.txt" 2>&1 || fail "--apply --yes on mig-add exited non-zero:
$(cat "$SCRATCH/apply-add.txt")"
cat "$SCRATCH/apply-add.txt"
grep -q '✓ mig-add migrated' "$SCRATCH/apply-add.txt" || fail "apply must report the migration"
cmp -s "$MIGADD_BRAIN" "$SEEDED_BRAIN" || fail "after --apply CLAUDE.md must be byte-identical to StampSeed(seed) (the seeded workspace's brain)"
ADD_BAK="$MIGADD/.grove/orchestrator/CLAUDE.md.bak-pre-overlay"
[ -f "$ADD_BAK" ] || fail "the original brain must be parked at .bak-pre-overlay"
[ "$(cksum < "$ADD_BAK")" = "$BEFORE_ADD_BRAIN" ] || fail "the backup must be byte-identical to the original brain"
[ ! -e "$ADD_DRAFT" ] || fail "the draft must have moved"
[ "$(cksum < "$ADD_OVERLAY")" = "$ADD_DRAFT_BYTES" ] || fail "CLAUDE.local.md must be the draft's bytes (the stub is replaced)"
grep -q '^## Local pair testing' "$ADD_OVERLAY" || fail "the overlay must carry the migrated section"
grep -q 'git ' "$SCRATCH/apply-add.txt" && fail "an untracked brain gets no git commands" || true
# a second --apply has no draft: refuses, changes nothing.
SNAP_ADD="$(snapshot_all)"
"$GV" brains --migrate --apply --label mig-add --yes > "$SCRATCH/apply-again.txt" 2>&1 && fail "a second --apply must refuse" || true
[ "$(snapshot_all)" = "$SNAP_ADD" ] || fail "a refused second --apply changed a file"

say "--apply on the tracked brain: git commands printed, none run"
"$GV" brains --migrate --apply --label mig-subst --yes > "$SCRATCH/apply-sub.txt" 2>&1 || fail "--apply --yes on mig-subst exited non-zero:
$(cat "$SCRATCH/apply-sub.txt")"
cat "$SCRATCH/apply-sub.txt"
cmp -s "$MIGSUB_BRAIN" "$SEEDED_BRAIN" || fail "mig-subst CLAUDE.md must be the pristine seed after --apply"
grep -q 'Wherever the seed writes `DEV-X`, read `grove-N`.' "$SUB_OVERLAY" || fail "the glossary draft must be the overlay now"
grep -q "cd $MIGSUB && git add -f .grove/orchestrator/CLAUDE.local.md" "$SCRATCH/apply-sub.txt" || fail "tracked brain: git add -f must be printed after --apply"
grep -q "cd $MIGSUB && git rm --cached .grove/orchestrator/CLAUDE.md" "$SCRATCH/apply-sub.txt" || fail "tracked brain: git rm --cached must be printed after --apply"
git -C "$MIGSUB" ls-files --error-unmatch .grove/orchestrator/CLAUDE.md > /dev/null || fail "--apply ran git rm --cached — it must only print it"
git -C "$MIGSUB" ls-files --error-unmatch .grove/orchestrator/CLAUDE.local.md > /dev/null 2>&1 && fail "--apply ran git add — it must only print it" || true
[ -f "$MIGSUB/.grove/orchestrator/CLAUDE.md.bak-pre-overlay" ] || fail "mig-subst backup missing"

say "conflicting brain: --apply refuses on the Review block, applies once it is removed"
# mig-conf: this binary's seed with one guardrail line deleted (a rewrite
# the classifier cannot explain → Review) plus an appended section (→
# Workspace duties).
MIGCONF="$SCRATCH/mig-conf"
mkrepo "$MIGCONF"
( cd "$MIGCONF" && "$GV" init --yes --label mig-conf > /dev/null )
MIGCONF_BRAIN="$MIGCONF/.grove/orchestrator/CLAUDE.md"
grep -q '^- \*\*Never edit repository code\.\*\*' "$MIGCONF_BRAIN" || fail "seed lost the guardrail this leg deletes — pick another line"
sed '/^- \*\*Never edit repository code\.\*\*/d' "$MIGCONF_BRAIN" > "$SCRATCH/conf.md" && mv "$SCRATCH/conf.md" "$MIGCONF_BRAIN"
printf '\n## Release ritual\n\nTag before the Friday demo.\n' >> "$MIGCONF_BRAIN"
BEFORE_CONF_BRAIN="$(cksum < "$MIGCONF_BRAIN")"
"$GV" brains --migrate --label mig-conf > "$SCRATCH/migrate-conf.txt" || fail "dry run on mig-conf exited non-zero"
grep -q 'mig-conf *draft *1 added / 0 substituted / 1 to review' "$SCRATCH/migrate-conf.txt" || fail "mig-conf must read 1 / 0 / 1:
$(cat "$SCRATCH/migrate-conf.txt")"
grep -q 'resolve it first; --apply refuses while it is there' "$SCRATCH/migrate-conf.txt" || fail "dry run must warn about the Review block"
CONF_DRAFT="$MIGCONF/.grove/orchestrator/CLAUDE.local.md.draft"
grep -q '^## Review before applying' "$CONF_DRAFT" || fail "conflicting draft must carry the Review block"
grep -q 'Never edit repository code' "$CONF_DRAFT" || fail "the deleted guardrail must be shown for review"
SNAP_CONF="$(snapshot_all)"
"$GV" brains --migrate --apply --label mig-conf --yes > "$SCRATCH/apply-conf.txt" 2>&1 && fail "--apply must refuse while the Review block is there" || true
grep -q 'still carries a `Review before applying` block' "$SCRATCH/apply-conf.txt" || fail "Review refusal missing:
$(cat "$SCRATCH/apply-conf.txt")"
[ "$(snapshot_all)" = "$SNAP_CONF" ] || fail "the Review refusal changed a file"
# The operator resolves it: here, drops the block (the deletion was a mistake).
python3 - "$CONF_DRAFT" <<'PY'
import sys
p = sys.argv[1]
lines = open(p).read().split("\n")
cut = lines.index("## Review before applying")
open(p, "w").write("\n".join(lines[:cut]).rstrip("\n") + "\n")
PY
grep -q 'Review before applying' "$CONF_DRAFT" && fail "fixture: block not removed" || true
"$GV" brains --migrate --apply --label mig-conf --yes > "$SCRATCH/apply-conf2.txt" 2>&1 || fail "--apply after resolving must succeed:
$(cat "$SCRATCH/apply-conf2.txt")"
cmp -s "$MIGCONF_BRAIN" "$SEEDED_BRAIN" || fail "mig-conf CLAUDE.md must be the pristine seed after --apply"
[ "$(cksum < "$MIGCONF/.grove/orchestrator/CLAUDE.md.bak-pre-overlay")" = "$BEFORE_CONF_BRAIN" ] || fail "mig-conf backup must be the edited brain, byte for byte"
grep -q '^## Release ritual' "$MIGCONF/.grove/orchestrator/CLAUDE.local.md" || fail "the resolved draft must be the overlay"
grep -q '^- \*\*Never edit repository code\.\*\*' "$MIGCONF_BRAIN" || fail "the seed guardrail is back in the pristine brain"

say "gv brains --json after --apply: migrated brains are pristine and carry an overlay"
"$GV" brains --json > "$SCRATCH/brains-after.json" || fail "gv brains --json after --apply exited non-zero"
python3 - "$SCRATCH/brains-after.json" <<'PY' || fail "post-apply rows wrong"
import json, sys
rows = {r["label"]: r for r in json.load(open(sys.argv[1]))["brains"]}
ok = True
for l in ("mig-add", "mig-subst", "mig-conf"):
    r = rows.get(l)
    if r is None:
        print(f"  {l}: missing"); ok = False; continue
    if r["state"] != "current" or not r["pristine"] or not r["overlay"] or r["overlay_lines"] < 1:
        print(f"  {l}: want current/pristine/overlay, got {r}"); ok = False
    if "CLAUDE.md.bak-pre-overlay" not in r.get("leftovers", []):
        print(f"  {l}: .bak-pre-overlay must be listed as a leftover, got {r.get('leftovers')}"); ok = False
if not ok:
    sys.exit(1)
PY

say "empty registry is a quiet, zero-exit line"
mkdir -p "$SCRATCH/empty"
HOME="$SCRATCH/empty" "$GV" brains > "$SCRATCH/empty.out" || fail "gv brains on an empty registry must exit 0"
grep -q 'no workspaces registered' "$SCRATCH/empty.out" || fail "empty registry message missing:
$(cat "$SCRATCH/empty.out")"

printf '\n\033[32mbrains E2E: PASS\033[0m — sweep classifies, reports, mutates nothing; --refresh replaces only pristine brains; --migrate writes drafts only; --apply swaps one reviewed draft in\n'
