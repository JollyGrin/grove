package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
)

// compactRig is a tracked markdown-backend worker in a real git worktree:
// the ticket file carries an acceptance-criteria section, the worktree
// one commit, and HOME holds a config whose repos.r points at it.
type compactRig struct {
	stateDir, wt, ticket string
}

func newCompactRig(t *testing.T) *compactRig {
	t.Helper()
	withNtfy(t, config.Notify{})
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "nogitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	stateDir := t.TempDir()
	ticket := "grove-1"
	wt := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(filepath.Join(wt, ".grove", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	ticketMD := "---\nid: grove-1\ntitle: Persist the filter state\nstatus: in-progress\nlabels: []\n---\n\n## Goal\nKeep filters.\n\n## Acceptance criteria\n- [ ] Filters survive a page reload\n- [ ] Clearing resets the URL\n\n## Out of scope\nServer-side state.\n"
	if err := os.WriteFile(filepath.Join(wt, ".grove", "tasks", ticket+".md"), []byte(ticketMD), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, wt, "init", "-q")
	gitRun(t, wt, "config", "user.email", "t@grove.test")
	gitRun(t, wt, "config", "user.name", "grove test")
	gitRun(t, wt, "checkout", "-q", "-b", "b-"+ticket)
	gitRun(t, wt, "add", "-A")
	gitRun(t, wt, "commit", "-q", "-m", "seed: ticket file and readme")

	cfg := "repos:\n  r:\n    path: " + wt + "\n"
	if err := os.MkdirAll(filepath.Join(home, ".config", "grove"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "grove", "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	real := seedFleet(t, stateDir, ticket, wt)
	if err := state.Append(stateDir, state.Event{Type: state.EvSessionStarted, Ticket: ticket, Data: map[string]string{"session_id": "s1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Load(stateDir); err != nil {
		t.Fatal(err)
	}
	return &compactRig{stateDir: stateDir, wt: real, ticket: ticket}
}

// stubGhScript puts a fake gh first on PATH running script verbatim.
func stubGhScript(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// sessionStart sends one SessionStart for the rig's worker and returns
// the hook's stdout.
func (r *compactRig) sessionStart(t *testing.T, source string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{
		"session_id": "s1", "cwd": r.wt, "hook_event_name": "SessionStart", "source": source,
	})
	var out bytes.Buffer
	if err := ReceiveTo(single(r.stateDir), "session-start", bytes.NewReader(payload), &out); err != nil {
		t.Fatalf("session-start(%s): %v", source, err)
	}
	return out.String()
}

func countType(t *testing.T, stateDir, typ string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(stateDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(raw), `"type":"`+typ+`"`)
}

// A compact SessionStart with gh unavailable re-orients from the ticket
// and git alone: id, title, acceptance criteria, git log, git status,
// and the git-only PR fallback — plus exactly one compaction event.
func TestCompactReorientsFromTicketAndGit(t *testing.T) {
	r := newCompactRig(t)
	stubGhScript(t, "echo 'no pull requests found for branch' >&2; exit 1")
	compactions, started := countType(t, r.stateDir, state.EvCompaction), countType(t, r.stateDir, state.EvSessionStarted)

	out := r.sessionStart(t, "compact")

	for _, want := range []string{
		"Task: grove-1 — Persist the filter state",
		"Acceptance criteria:\n- [ ] Filters survive a page reload\n- [ ] Clearing resets the URL",
		"git log --oneline -8 (" + r.wt + ", branch b-grove-1):",
		"seed: ticket file and readme",
		"git status --short:\n(clean)",
		"PR: not readable via gh (gh pr view: no pull requests found for branch) — no origin remote",
		"STATUS contract:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("re-orientation missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Out of scope") || strings.Contains(out, "Server-side state") {
		t.Errorf("re-orientation leaked past the acceptance-criteria section:\n%s", out)
	}
	if got := countType(t, r.stateDir, state.EvCompaction); got != compactions+1 {
		t.Errorf("compaction events %d → %d, want +1", compactions, got)
	}
	if got := countType(t, r.stateDir, state.EvSessionStarted); got != started {
		t.Errorf("session_started events %d → %d, want unchanged", started, got)
	}
}

// With gh answering, the PR number, state, URL and body (the handoff)
// are printed; a dirty worktree shows in git status.
func TestCompactReorientsWithPRBody(t *testing.T) {
	r := newCompactRig(t)
	stubGhScript(t, `printf '%s' '{"number":12,"url":"https://example.test/pr/12","state":"OPEN","isDraft":true,"body":"## Goal\nShip the filter.\n\n## Next step\nWire the reset button."}'`)
	if err := os.WriteFile(filepath.Join(r.wt, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := r.sessionStart(t, "compact")

	for _, want := range []string{
		"PR #12 (draft) https://example.test/pr/12",
		"## Next step\nWire the reset button.",
		"git status --short:\n?? scratch.txt",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("re-orientation missing %q:\n%s", want, out)
		}
	}
}

// A startup SessionStart prints nothing — only a compact restart gets the
// re-orientation.
func TestStartupSessionStartPrintsNothing(t *testing.T) {
	r := newCompactRig(t)
	stubGhScript(t, "exit 1")
	if out := r.sessionStart(t, "startup"); out != "" {
		t.Errorf("startup session-start wrote to stdout: %q", out)
	}
}

// An unreadable worktree never fails the receiver: the ticket facts still
// print, the git blocks degrade to a note, and the event still lands.
func TestCompactUnreadableWorktreeStillExitsClean(t *testing.T) {
	r := newCompactRig(t)
	stubGhScript(t, "exit 1")
	if err := os.RemoveAll(r.wt); err != nil {
		t.Fatal(err)
	}
	before := countType(t, r.stateDir, state.EvCompaction)

	out := r.sessionStart(t, "compact") // t.Fatal on a non-nil error

	if !strings.Contains(out, "Task: grove-1") || !strings.Contains(out, "unreadable") {
		t.Errorf("degraded re-orientation wrong:\n%s", out)
	}
	if got := countType(t, r.stateDir, state.EvCompaction); got != before+1 {
		t.Errorf("compaction events %d → %d, want +1", before, got)
	}
}

func TestAcceptanceCriteriaSection(t *testing.T) {
	cases := map[string]struct{ desc, want string }{
		"h2 section ends at next heading": {"## Goal\nx\n\n## Acceptance criteria\n- [ ] a\n- [ ] b\n\n## Out of scope\nz", "- [ ] a\n- [ ] b"},
		"h3 and mixed case":               {"### Acceptance Criteria\n- one\n#### Notes\nn", "- one"},
		"runs to end of body":             {"## Acceptance criteria\n- only", "- only"},
		"absent":                          {"## Goal\nno criteria here", ""},
		"heading text only, not a prefix": {"## Acceptance criteria review\n- not it", "- not it"},
	}
	for name, tc := range cases {
		if got := acceptanceCriteria(tc.desc); got != tc.want {
			t.Errorf("%s: acceptanceCriteria = %q, want %q", name, got, tc.want)
		}
	}
	if got := capLines("a\nb\nc\nd", 2); got != "a\nb\n… (2 more lines)" {
		t.Errorf("capLines = %q", got)
	}
}
