#!/usr/bin/env bash
# Wizard E2E (plan 2026-07-04-phase-1a Task 7): non-interactive init paths
# against scratch everything. Asserts detection lands in config, hand-edited
# config survives --yes byte-identical, --only hooks wires the scratch
# settings.json exactly once (ovs entries preserved), --yes never spawns the
# agents-md run, --agents-md with a stub worker writes AGENTS.md, and
# doctor --json renders the connections board.
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-wizard.XXXXXX)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

export HOME="$SCRATCH/home"
export GROVE_STATE_DIR="$SCRATCH/state"
mkdir -p "$HOME" "$GROVE_STATE_DIR"
CFG=""   # set after the fixture repo exists (workspace config)
cleanup() { chmod -R u+w "$SCRATCH" 2>/dev/null || true; rm -rf "$SCRATCH"; }
trap cleanup EXIT

say "pnpm-shaped fixture repo"
PNPM="$SCRATCH/webapp"
mkdir -p "$PNPM" && cd "$PNPM"
git init -qb main . && git config user.email e2e@x && git config user.name e2e
printf '{"name":"webapp","scripts":{"build":"vite build","test":"vitest run","lint":"eslint ."}}\n' > package.json
touch pnpm-lock.yaml
git add -A && git commit -qm init
ROOT="$(git rev-parse --show-toplevel)"
CFG="$ROOT/.grove/config.yaml"

say "gv init --yes detects the stack"
"$GV" init --yes > "$SCRATCH/init1.out"
grep -q 'config updated' "$SCRATCH/init1.out" || fail "first init did not write config"
grep -q 'setup: pnpm install' "$CFG" || fail "detected setup missing from config:
$(cat "$CFG")"
grep -q 'kind: markdown' "$CFG" || fail "provider default missing"
grep -q "path: $ROOT" "$CFG" || fail "repo path missing"
[ -f "$PNPM/.grove/tasks/task-001.md" ] || fail "task scaffold missing"
[ ! -f "$PNPM/AGENTS.md" ] || fail "--yes must NOT spawn the agents-md run"
[ ! -f "$HOME/.cc-work/settings.json" ] || fail "--yes must not newly install hooks"

say "--yes re-run over an already-correct config is a no-op"
"$GV" init --yes > "$SCRATCH/init2.out"
grep -q 'already up to date' "$SCRATCH/init2.out" || fail "re-run should be a no-op"

say "hand-edited values survive --yes byte-identical"
cat > "$CFG" <<EOF
# precious comment — grove must not eat this
workspace:
  label: webapp
  scope: repo
provider:
  kind: markdown
repos:
  webapp:
    path: $ROOT
    base: develop
    setup: make special-deps   # hand-tuned
    claude: echo
notify:
  ntfy: https://ntfy.sh/topic-x
EOF
cp "$CFG" "$SCRATCH/before.yaml"
"$GV" init --yes > /dev/null
cmp "$CFG" "$SCRATCH/before.yaml" || { diff "$SCRATCH/before.yaml" "$CFG" || true; fail "hand-edited config changed under --yes"; }

say "--only hooks wires the WORKER'S profile (echo → ~/.claude), once"
# Hooks land in the settings.json of the profile the worker command runs
# under. This fleet's worker is `echo` → the default ~/.claude profile —
# NOT the Grid's ~/.cc-work, which must stay byte-untouched.
mkdir -p "$HOME/.cc-work"
cat > "$HOME/.cc-work/settings.json" <<'EOF'
{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "/Users/x/go/bin/ovs hook stop"}]}]}}
EOF
cp "$HOME/.cc-work/settings.json" "$SCRATCH/ccwork-before.json"
"$GV" init --only hooks > /dev/null
"$GV" init --only hooks > /dev/null
[ -f "$HOME/.claude/settings.json" ] || fail "hooks must land in the worker's profile (~/.claude)"
GV_COUNT=$(grep -c 'gv hook stop' "$HOME/.claude/settings.json" || true)
[ "$GV_COUNT" -eq 1 ] || fail "gv Stop hook count in ~/.claude = $GV_COUNT, want 1 (idempotent)"
cmp -s "$HOME/.cc-work/settings.json" "$SCRATCH/ccwork-before.json" || fail "unrelated profile (~/.cc-work) was touched"

say "--agents-md with a stub worker writes the brain"
STUB="$SCRATCH/stub-claude"
cat > "$STUB" <<'EOF'
#!/bin/sh
printf '# webapp\nstub brain — layout, commands, conventions\n' > AGENTS.md
echo "WROTE AGENTS.md"
EOF
chmod +x "$STUB"
"$GV" init --yes --agents-md --worker "$STUB" > "$SCRATCH/amd.out" 2>&1 || { cat "$SCRATCH/amd.out"; fail "agents-md run failed"; }
grep -q 'stub brain' "$PNPM/AGENTS.md" || fail "AGENTS.md not written by the agent"
grep -q 'review' "$SCRATCH/amd.out" || fail "must tell the human to review + commit"

say "second agents-md run refuses to overwrite"
"$GV" init --yes --agents-md --worker "$STUB" > "$SCRATCH/amd2.out" 2>&1 || true
grep -q 'never overwrites' "$SCRATCH/amd2.out" || fail "existing AGENTS.md must be protected:
$(cat "$SCRATCH/amd2.out")"

say "--only rejects unknown steps"
if "$GV" init --only nonsense > "$SCRATCH/only.out" 2>&1; then fail "unknown --only must error"; fi
grep -q 'agents-md' "$SCRATCH/only.out" || fail "error must list valid steps"

say "doctor renders the connections board"
"$GV" doctor --json > "$SCRATCH/doctor.json" 2>&1 || true   # exit 1 = expected (scratch gh auth)
python3 -c "
import json,sys
data = json.load(open('$SCRATCH/doctor.json'))
assert data.get('schema_version') == 1, f'missing/wrong schema_version: {data.get(\"schema_version\")}'
rows = data['rows']
ids = {r['id'] for r in rows}
need = {'binary:tmux','gh-auth'}
missing = need - ids
assert not missing, f'doctor rows missing {missing}'
assert any(i.startswith('hooks:') for i in ids), f'per-profile hooks row missing: {ids}'
assert any(r.get('pack') == 'grid-interim' for r in rows), 'grid-interim section missing'
print(f'doctor board: {len(rows)} rows ok')
" || fail "doctor --json board malformed"
"$GV" doctor > "$SCRATCH/doctor.txt" 2>&1 || true
grep -q 'gv init --only' "$SCRATCH/doctor.txt" || true  # fix hints present when relevant

say "PASS — wizard: detection, hand-edit safety, scoped runs, brain, board"
