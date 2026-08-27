package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/JollyGrin/grove/internal/state"
)

// mkFleet creates an alive workspace at root (label from config) with one
// tracked ticket: a task_created event, plus a terminal event when done.
func mkFleet(t *testing.T, root, label, ticket string, done bool) Workspace {
	t.Helper()
	mkWorkspace(t, root, fmt.Sprintf("workspace:\n  label: %s\n", label))
	ws := Workspace{Root: root, Label: label, Scope: ScopeRepo}
	dir := stateDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ev := state.Event{Type: state.EvTaskCreated, Ticket: ticket, Data: map[string]string{
		"title": ticket, "repo": "r", "branch": ticket, "worktree": "/tmp/x",
	}}
	if err := state.Append(dir, ev); err != nil {
		t.Fatal(err)
	}
	if done {
		if err := state.Append(dir, state.Event{Type: state.EvTaskUntracked, Ticket: ticket}); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

func TestFindTicket(t *testing.T) {
	t.Setenv("GROVE_STATE_DIR", "") // per-root state is the subject
	tmp := realTemp(t)

	alpha := mkFleet(t, filepath.Join(tmp, "alpha"), "alpha", "task-001", false)
	duo := mkFleet(t, filepath.Join(tmp, "duo"), "duo", "grove-7", false)
	dead := mkFleet(t, filepath.Join(tmp, "dead"), "dead", "task-001", false)
	if err := os.RemoveAll(filepath.Join(tmp, "dead")); err != nil {
		t.Fatal(err)
	}
	list := []Workspace{alpha, duo, dead}

	for _, tc := range []struct {
		ref         string
		includeDone bool
		want        []string
	}{
		{"task-001", false, []string{"alpha"}}, // exact markdown id
		{"grove-7", false, []string{"duo"}},    // exact github id
		{"#7", false, []string{"duo"}},         // numeric-suffix fallback
		{"7", false, []string{"duo"}},          // bare numeric
		{"task-002", false, nil},               // tracked nowhere
		{"nothing-99", false, nil},             // never tracked
		{"task-001", true, []string{"alpha"}},  // includeDone is a no-op here
	} {
		var got []string
		for _, ws := range FindTicket(list, tc.ref, tc.includeDone) {
			got = append(got, ws.Label)
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("FindTicket(%q, %v) = %v, want %v", tc.ref, tc.includeDone, got, tc.want)
		}
	}
}

func TestFindTicketAmbiguousAndDone(t *testing.T) {
	t.Setenv("GROVE_STATE_DIR", "")
	tmp := realTemp(t)

	// Same ticket in two workspaces: both own it, registry order kept.
	a := mkFleet(t, filepath.Join(tmp, "a"), "aaa", "task-005", false)
	b := mkFleet(t, filepath.Join(tmp, "b"), "bbb", "task-005", false)
	owners := FindTicket([]Workspace{a, b}, "task-005", false)
	if len(owners) != 2 || owners[0].Label != "aaa" || owners[1].Label != "bbb" {
		t.Fatalf("ambiguous ticket must name both owners in order, got %v", owners)
	}

	// A done/untracked task is not an owner for the active verbs, but is
	// for adopt (it revives done tasks).
	c := mkFleet(t, filepath.Join(tmp, "c"), "ccc", "task-006", true)
	if got := FindTicket([]Workspace{c}, "task-006", false); len(got) != 0 {
		t.Errorf("done task must not route active verbs, got %v", got)
	}
	if got := FindTicket([]Workspace{c}, "task-006", true); len(got) != 1 || got[0].Label != "ccc" {
		t.Errorf("adopt scan must match the done task's workspace, got %v", got)
	}
}

func TestFindTicketUnreadableState(t *testing.T) {
	t.Setenv("GROVE_STATE_DIR", "")
	tmp := realTemp(t)
	// Alive marker, but events.jsonl is a directory: Peek fails — the
	// workspace disclaims ownership instead of erroring the scan.
	root := filepath.Join(tmp, "broken")
	mkWorkspace(t, root, "workspace:\n  label: broken\n")
	ws := Workspace{Root: root, Label: "broken", Scope: ScopeRepo}
	if err := os.MkdirAll(filepath.Join(stateDir(root), "events.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindTicket([]Workspace{ws}, "task-001", false); len(got) != 0 {
		t.Errorf("unreadable state must disclaim ownership, got %v", got)
	}
}
