#!/usr/bin/env bash
# github-issues provider E2E (plan 2026-07-05-github-issues): a stub `gh`
# first on PATH serves canned JSON — no network, no real issues. Two child
# repos in one parent workspace, both provider github, worker echo.
# Asserts canonical <repo>-<n> ids, the label/Closes verbs in the kickoff,
# short-ref resolution (unique + ambiguous), dedup, done, and the cap note.
set -euo pipefail

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="$(mktemp -d /tmp/grove-gh.XXXXXX)"

say "build gv"
GV="$SCRATCH/gv"
(cd "$REPO_ROOT" && go build -o "$GV" ./cmd/gv)

export HOME="$SCRATCH/home"
# $TMUX beats TMUX_TMPDIR — without the unset, a run from inside a tmux
# pane puts every tmux call (incl. cleanup's kill-server) on the REAL
# server. See LEARNINGS.md (2026-07-07 grove-7 crash).
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$SCRATCH/tmux"
mkdir -p "$HOME" "$TMUX_TMPDIR" "$SCRATCH/bin"
unset GROVE_STATE_DIR || true
cleanup() { tmux kill-server 2>/dev/null || true; chmod -R u+w "$SCRATCH" 2>/dev/null || true; rm -rf "$SCRATCH"; }
trap cleanup EXIT

say "stub gh (records argv, serves canned JSON)"
cat > "$SCRATCH/bin/gh" <<'EOF'
#!/bin/sh
echo "$PWD :: $*" >> "$GH_LOG"
case "$1 $2" in
  "issue list")
    if [ "$GH_FULL_PAGE" = "1" ]; then
      printf '['
      i=1
      while [ $i -le 200 ]; do
        [ $i -gt 1 ] && printf ','
        printf '{"number":%d,"title":"issue %d","labels":[]}' "$i" "$i"
        i=$((i+1))
      done
      printf ']'
    else
      printf '[{"number":7,"title":"Bot: humanlike delay","labels":[{"name":"bot"}]},{"number":9,"title":"Deck import","labels":[]}]'
    fi
    ;;
  "issue view")
    printf '{"number":%s,"title":"Bot: humanlike delay","body":"Make the bot pause before declining defense.","url":"https://github.com/x/y/issues/%s","state":"OPEN","labels":[{"name":"bot"}],"comments":[{"author":{"login":"jollygrin"},"body":"context"}]}' "$3" "$3"
    ;;
  "pr view")
    printf '{"state":"OPEN","mergedAt":null}'
    ;;
  "auth status") exit 0 ;;
  *) exit 0 ;;
esac
EOF
chmod +x "$SCRATCH/bin/gh"
export PATH="$SCRATCH/bin:$PATH"
export GH_LOG="$SCRATCH/gh.log"

say "parent workspace with two github-driven repos"
mkrepo() {
  mkdir -p "$1" && git -C "$1" init -qb main
  git -C "$1" config user.email e2e@x && git -C "$1" config user.name e2e
  ( cd "$1" && echo x > README.md && git add -A && git commit -qm init )
}
DUO="$SCRATCH/duo"
mkrepo "$DUO/alpha"
mkrepo "$DUO/beta"
( cd "$DUO" && "$GV" init --yes --label duo --provider github > /dev/null )
perl -pi -e 's/^(\s*)base: main$/$1base: main\n$1claude: echo/' "$DUO/.grove/config.yaml"

say "no-arg grab lists issues with canonical ids"
( cd "$DUO" && "$GV" grab --repo alpha > "$SCRATCH/list.out" )
grep -q 'alpha-7' "$SCRATCH/list.out" || fail "canonical id missing:
$(cat "$SCRATCH/list.out")"
grep -q 'Bot: humanlike delay' "$SCRATCH/list.out" || fail "title missing"

say "cap note appears on a full page"
( cd "$DUO" && GH_FULL_PAGE=1 "$GV" grab --repo alpha > "$SCRATCH/cap.out" )
grep -q 'capped at 200' "$SCRATCH/cap.out" || fail "cap note missing"

say "grab #7 in alpha"
( cd "$DUO" && "$GV" grab 7 --repo alpha > "$SCRATCH/grab.out" )
grep -q '→ alpha-7 on alpha' "$SCRATCH/grab.out" || fail "canonical grab line missing:
$(cat "$SCRATCH/grab.out")"
ls -d "$SCRATCH"/*/.worktrees/alpha/alpha-7-* >/dev/null 2>&1 || ls -d "$DUO/.worktrees/alpha/alpha-7-"* >/dev/null || fail "worktree alpha-7-<slug> missing"
PROMPT="$DUO/.grove/state/prompts/alpha-7.txt"
[ -f "$PROMPT" ] || fail "kickoff prompt missing at $PROMPT"
grep -q 'add-label in-progress' "$PROMPT" || fail "start verb missing"
grep -q 'Closes #' "$PROMPT" || fail "Closes #N verb missing"
grep -q 'NEVER close' "$PROMPT" || fail "never-close rule missing"
grep -q 'STATUS: QUESTION' "$PROMPT" || fail "sentinel missing"
grep -qi 'linear' "$PROMPT" && fail "github prompt leaks Linear" || true
grep -q 'status: in-progress' "$PROMPT" && fail "github prompt leaks markdown verbs" || true

say "dedup on re-grab"
( cd "$DUO" && "$GV" grab '#7' --repo alpha 2>&1 || true ) | grep -q 'already tracked' || fail "dedup missing"

say "short-ref resolution: unique"
( cd "$DUO" && "$GV" done 7 2> "$SCRATCH/done1.err" || true )
grep -q 'no remote\|not cleaning up\|PR' "$SCRATCH/done1.err" || fail "done 7 did not resolve the tracked task:
$(cat "$SCRATCH/done1.err")"

say "short-ref resolution: ambiguous when #7 tracked in both repos"
( cd "$DUO" && "$GV" grab 7 --repo beta > /dev/null )
( cd "$DUO" && "$GV" done 7 2> "$SCRATCH/done2.err" || true )
grep -q 'several repos' "$SCRATCH/done2.err" || fail "ambiguous short ref must error with the list:
$(cat "$SCRATCH/done2.err")"

say "cleanup via full ids"
( cd "$DUO" && "$GV" untrack alpha-7 --rm --force > /dev/null )
( cd "$DUO" && "$GV" untrack beta-7 --rm --force > /dev/null )

say "gh ran inside the right repo dirs"
grep -q "$DUO/alpha :: issue view 7" "$GH_LOG" || fail "gh issue view did not run in alpha:
$(cat "$GH_LOG")"

say "PASS — github provider: canonical ids, verbs, short refs, dedup, cap note"
