"""Gate 0: did the probe's model emit real Anthropic tool_use blocks?

usage: python3 gate0.py [WORKTREE_PATH]   (default: cwd)

Reads the newest transcript under <config>/projects/<encoded path>/ and
counts tool_use and text blocks across assistant turns. <config> is
$CLAUDE_CONFIG_DIR, else ~/.claude — set it to the probe profile's config
dir if that profile runs under its own.

Both lookups fail SILENTLY if done wrong, and both read as tool_use: 0 —
the kill-the-lane verdict:
  - the project dir encodes the path with BOTH rules, / -> - AND . -> -
    (/home/x/.grove/orch -> -home-x--grove-orch), as transcript.EncodePath;
  - transcript filenames are session UUIDs, so pick the newest by mtime,
    never by sorted name.
"""
import glob
import json
import os
import sys

path = os.path.abspath(sys.argv[1] if len(sys.argv) > 1 else os.getcwd())
config = os.environ.get("CLAUDE_CONFIG_DIR") or os.path.expanduser("~/.claude")
tdir = os.path.join(config, "projects", path.replace("/", "-").replace(".", "-"))

files = glob.glob(os.path.join(tdir, "*.jsonl"))
if not files:
    sys.exit("no transcript under " + tdir)
f = max(files, key=os.path.getmtime)

tool_use = text = 0
model = None
for line in open(f):
    try:
        r = json.loads(line)
    except ValueError:
        continue
    if r.get("type") != "assistant":
        continue
    model = r["message"].get("model")
    for b in r["message"].get("content", []):
        tool_use += b.get("type") == "tool_use"
        text += b.get("type") == "text"
print(os.path.basename(f), model, "tool_use:", tool_use, "text:", text)
