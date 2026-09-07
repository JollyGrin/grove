#!/usr/bin/env bash
# grove-288: `gv sub` — a read-only micro-task on a model_profiles lane.
# Scaffold: the dummy-data pattern (e2e/dummy.sh:15-25) — scratch HOME
# (config) + scratch GROVE_STATE_DIR (state). No tmux needed: gv sub is a
# one-shot subprocess call, not a worker.
#
# The fake /v1/messages endpoint is a small stdlib python3 HTTP server
# (no third-party deps) that records every request to $SCRATCH/req.jsonl
# and rejects a request that doesn't disable thinking — the one
# behavioral requirement raw.go has (GLM 5.3 Flash returns empty text
# with thinking on, LEARNINGS.md 2026-09-06).
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-e2e-sub.XXXXXX)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

export HOME="$SCRATCH/home"
export GROVE_STATE_DIR="$SCRATCH/state"
mkdir -p "$HOME" "$GROVE_STATE_DIR"

SERVER_PID=""
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  chmod -R u+w "$SCRATCH" 2>/dev/null || true
  rm -rf "$SCRATCH"
}
trap cleanup EXIT

say "fake /v1/messages endpoint"
cat > "$SCRATCH/stub_server.py" <<'PYEOF'
import http.server, json, os, socketserver, sys

SCRATCH = sys.argv[1]
REQ_FILE = os.path.join(SCRATCH, "req.jsonl")
EMPTY_ONCE = os.path.join(SCRATCH, "empty-once")

class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length)
        try:
            payload = json.loads(raw)
        except Exception:
            payload = {}
        with open(REQ_FILE, "a") as f:
            f.write(json.dumps({
                "path": self.path,
                "x_api_key": self.headers.get("x-api-key", ""),
                "body": payload,
            }) + "\n")

        if payload.get("thinking") != {"type": "disabled"}:
            self.send_response(400)
            self.end_headers()
            return

        if os.path.exists(EMPTY_ONCE):
            os.remove(EMPTY_ONCE)
            resp = {"content": [], "usage": {"input_tokens": 1, "output_tokens": 0}}
        else:
            resp = {"content": [{"type": "text", "text": "- ok: line 1"}],
                     "usage": {"input_tokens": 10, "output_tokens": 3}}

        out = json.dumps(resp).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, fmt, *args):
        pass

socketserver.TCPServer.allow_reuse_address = True
httpd = socketserver.TCPServer(("127.0.0.1", 0), Handler)
with open(os.path.join(SCRATCH, "port.txt"), "w") as f:
    f.write(str(httpd.server_address[1]))
httpd.serve_forever()
PYEOF
python3 "$SCRATCH/stub_server.py" "$SCRATCH" &
SERVER_PID=$!
for _ in $(seq 1 50); do
  [ -f "$SCRATCH/port.txt" ] && break
  sleep 0.1
done
[ -f "$SCRATCH/port.txt" ] || fail "stub server never came up"
PORT="$(cat "$SCRATCH/port.txt")"
BASE="http://127.0.0.1:$PORT"

say "config + secrets"
mkdir -p "$HOME/.config/grove"
WCFG="$HOME/.config/grove/config.yaml"
cat > "$WCFG" <<EOF
model_profiles:
  e2e-flat:
    base_url: $BASE
    auth_token_env: E2E_KEY
    haiku: fake-1
  openrouter-fake:
    base_url: $BASE
    auth_token_env: E2E_KEY
    haiku: fake-1
sub:
  lane: e2e-flat
EOF
cat > "$HOME/.config/grove/.env" <<'EOF'
export E2E_KEY=sekret
EOF
chmod 600 "$HOME/.config/grove/.env"

# A scratch "project" dir standing in for a worker's cwd — never the real
# repo — so gv sub's relative path args resolve inside SCRATCH only.
PROJ="$SCRATCH/proj"
mkdir -p "$PROJ/e2e"
cat > "$PROJ/e2e/sub.sh" <<'EOF'
#!/usr/bin/env bash
# dummy stand-in file for e2e/sub.sh's own Collect/Assemble exercise.
echo hello
EOF

say "raw call: prints only the answer, sends the numbered file + disabled thinking"
: > "$SCRATCH/req.jsonl"
OUT3="$( (cd "$PROJ" && "$GV" sub "hello" e2e/sub.sh) )"
[ "$OUT3" = "- ok: line 1" ] || fail "stdout = $OUT3, want '- ok: line 1'"
[ "$(wc -l < "$SCRATCH/req.jsonl")" -eq 1 ] || fail "expected exactly 1 request"
python3 - "$SCRATCH/req.jsonl" <<'PY'
import json, sys
r = json.loads(open(sys.argv[1]).readline())
assert r["x_api_key"] == "sekret", r
content = r["body"]["messages"][0]["content"]
assert 'path="e2e/sub.sh"' in content, content
assert "    1\t" in content, content
assert r["body"]["thinking"] == {"type": "disabled"}, r["body"]
print("ok")
PY

say "stdin is the input when no path is given"
echo "log line" | (cd "$PROJ" && "$GV" sub "what failed") > /dev/null
python3 - "$SCRATCH/req.jsonl" <<'PY'
import json, sys
lines = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
r = lines[-1]
assert 'path="stdin"' in r["body"]["messages"][0]["content"], r
print("ok")
PY

say "openrouter-fake bills per token — gated behind --allow-paid"
set +e
(cd "$PROJ" && "$GV" sub --lane openrouter-fake "x" e2e/sub.sh) >/dev/null 2>"$SCRATCH/step5-deny.err"
RC=$?
set -e
[ "$RC" -eq 4 ] || fail "expected exit 4, got $RC ($(cat "$SCRATCH/step5-deny.err"))"
grep -qi -- "--allow-paid" "$SCRATCH/step5-deny.err" || fail "missing --allow-paid hint"

OUT5B="$( (cd "$PROJ" && "$GV" sub --lane openrouter-fake --allow-paid "x" e2e/sub.sh) )"
[ "$OUT5B" = "- ok: line 1" ] || fail "--allow-paid call failed: $OUT5B"

say "unknown lane lists the configured ones"
set +e
(cd "$PROJ" && "$GV" sub --lane nope "x") >/dev/null 2>"$SCRATCH/step6.err"
RC=$?
set -e
[ "$RC" -eq 2 ] || fail "expected exit 2, got $RC"
grep -q "e2e-flat" "$SCRATCH/step6.err" || fail "unknown-lane error did not list e2e-flat: $(cat "$SCRATCH/step6.err")"

say "no lane configured — exit 2 with the hint; gv ls unaffected"
cp "$WCFG" "$WCFG.bak"
python3 - "$WCFG" <<'PY'
import sys
path = sys.argv[1]
lines = [l for l in open(path) if "lane: e2e-flat" not in l]
open(path, "w").writelines(lines)
PY
set +e
(cd "$PROJ" && "$GV" sub "x") >/dev/null 2>"$SCRATCH/step7.err"
RC=$?
set -e
[ "$RC" -eq 2 ] || fail "expected exit 2, got $RC"
grep -q "no lane configured" "$SCRATCH/step7.err" || fail "missing 'no lane configured' hint: $(cat "$SCRATCH/step7.err")"
(cd "$PROJ" && "$GV" ls --json --no-pr --no-cost) > "$SCRATCH/ls-nolane.json" || fail "gv ls broke with sub.lane unset"
cp "$WCFG.bak" "$WCFG"

say "empty response is retried once, then succeeds"
: > "$SCRATCH/req.jsonl"
touch "$SCRATCH/empty-once"
OUT8="$( (cd "$PROJ" && "$GV" sub "x" e2e/sub.sh) )"
[ "$OUT8" = "- ok: line 1" ] || fail "retry-then-ok mismatch: $OUT8"
[ "$(wc -l < "$SCRATCH/req.jsonl")" -eq 2 ] || fail "expected 2 requests (the retry), got $(wc -l < "$SCRATCH/req.jsonl")"

say "agentic mode: read-only claude -p --bare, --start hint honored"
STUBBIN="$SCRATCH/bin"
mkdir -p "$STUBBIN"
cat > "$STUBBIN/claude" <<EOF
#!/bin/sh
{
  echo "BASE_URL=\$ANTHROPIC_BASE_URL"
  for a in "\$@"; do printf 'ARG:%s\n' "\$a"; done
} > "$SCRATCH/claude-argv"
cat <<'JSON'
{"result":"- found: a.go:1","num_turns":2,"duration_api_ms":5,"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0}}
JSON
EOF
chmod +x "$STUBBIN/claude"
REAL_PATH="$PATH"
export PATH="$STUBBIN:$PATH"

OUT9="$( (cd "$PROJ" && "$GV" sub --agentic --start "grep -rn x ." "where") )"
[ "$OUT9" = "- found: a.go:1" ] || fail "agentic stdout mismatch: $OUT9"
grep -q "^ARG:--bare$" "$SCRATCH/claude-argv" || fail "argv missing --bare"
grep -q "^ARG:--max-turns$" "$SCRATCH/claude-argv" || fail "argv missing --max-turns"
grep -q "^ARG:12$" "$SCRATCH/claude-argv" || fail "argv missing the default max-turns value 12"
grep -q "^ARG:--allowedTools$" "$SCRATCH/claude-argv" || fail "argv missing --allowedTools"
grep -q "Edit" "$SCRATCH/claude-argv" && fail "Edit present without --allow-write" || true
grep -q "^BASE_URL=$BASE$" "$SCRATCH/claude-argv" || fail "child did not see the lane's ANTHROPIC_BASE_URL"
[ -z "${ANTHROPIC_BASE_URL:-}" ] || fail "this shell's own ANTHROPIC_BASE_URL leaked"

say "agentic turn cap (null result) — exit 3, hint mentions --start"
cat > "$STUBBIN/claude" <<'EOF'
#!/bin/sh
cat <<'JSON'
{"result":null,"num_turns":12,"duration_api_ms":5,"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0}}
JSON
EOF
chmod +x "$STUBBIN/claude"
set +e
(cd "$PROJ" && "$GV" sub --agentic "where") >/dev/null 2>"$SCRATCH/step10.err"
RC=$?
set -e
[ "$RC" -eq 3 ] || fail "expected exit 3, got $RC"
grep -q -- "--start" "$SCRATCH/step10.err" || fail "no-answer hint missing --start: $(cat "$SCRATCH/step10.err")"

export PATH="$REAL_PATH"

say "sub.jsonl ledger: 6 rows, no answer text, no secret"
(cd "$PROJ" && "$GV" sub --ledger --json) > "$SCRATCH/ledger.json"
python3 - "$SCRATCH/ledger.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d["schema_version"] == 1, d
rows = d["rows"]
assert len(rows) == 6, f"got {len(rows)} rows: {rows}"
for r in rows:
    assert "mode" in r and "exit" in r and "lane" in r, r
    assert "answer" not in r, r
print("ok")
PY
grep -q "sekret" "$GROVE_STATE_DIR/sub.jsonl" && fail "the key leaked into sub.jsonl" || true

say "dry-run: no network call, no ledger row, key masked"
REQ_BEFORE="$(wc -l < "$SCRATCH/req.jsonl")"
ROWS_BEFORE="$(wc -l < "$GROVE_STATE_DIR/sub.jsonl")"
OUT12="$( (cd "$PROJ" && "$GV" sub --dry-run "x" e2e/sub.sh) )"
echo "$OUT12" | grep -q "key=" || fail "dry-run missing key="
echo "$OUT12" | grep -q "sekret" && fail "dry-run leaked the raw key" || true
[ "$(wc -l < "$SCRATCH/req.jsonl")" -eq "$REQ_BEFORE" ] || fail "dry-run made a network call"
[ "$(wc -l < "$GROVE_STATE_DIR/sub.jsonl")" -eq "$ROWS_BEFORE" ] || fail "dry-run added a ledger row"

say "gv sub --lanes lists both lanes with billing + key state"
(cd "$PROJ" && "$GV" sub --lanes) > "$SCRATCH/lanes.out"
grep -q "e2e-flat" "$SCRATCH/lanes.out" || fail "lanes missing e2e-flat"
grep -q "openrouter-fake" "$SCRATCH/lanes.out" || fail "lanes missing openrouter-fake"
grep -q "flat" "$SCRATCH/lanes.out" || fail "lanes missing flat billing"
grep -q "paid" "$SCRATCH/lanes.out" || fail "lanes missing paid billing"

say "gv doctor --json: sub:lane ok with .env present, warn without"
set +e
(cd "$PROJ" && "$GV" doctor --json) > "$SCRATCH/doctor-ok.json" 2>&1
set -e
python3 - "$SCRATCH/doctor-ok.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
rows = {r["id"]: r for r in d["rows"]}
assert rows["sub:lane"]["state"] == "ok", rows.get("sub:lane")
print("ok")
PY

mv "$HOME/.config/grove/.env" "$HOME/.config/grove/.env.bak"
set +e
(cd "$PROJ" && "$GV" doctor --json) > "$SCRATCH/doctor-warn.json" 2>&1
set -e
python3 - "$SCRATCH/doctor-warn.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
rows = {r["id"]: r for r in d["rows"]}
assert rows["sub:lane"]["state"] == "warn", rows.get("sub:lane")
print("ok")
PY
mv "$HOME/.config/grove/.env.bak" "$HOME/.config/grove/.env"

say "no secret anywhere in the scratch dir except .env and req.jsonl"
LEAKED="$(grep -rl "sekret" "$SCRATCH" 2>/dev/null | grep -v -e '/\.env$' -e '/req\.jsonl$' || true)"
[ -z "$LEAKED" ] || fail "secret leaked into: $LEAKED"

say "PASS — gv sub raw + agentic, gates, ledger, lanes, doctor all green"
