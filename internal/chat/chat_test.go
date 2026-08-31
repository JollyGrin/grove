package chat

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/transcript"
)

// Writable is the field a client disables its input off — never its own
// reading of kind — so pin every kind, including one nobody minted.
func TestWritable(t *testing.T) {
	cases := map[string]bool{
		KindChat:     true,
		KindCockpit:  false,
		KindArchived: false,
		"":           false,
		"nonsense":   false,
	}
	for kind, want := range cases {
		if got := Writable(kind); got != want {
			t.Errorf("Writable(%q) = %v, want %v", kind, got, want)
		}
	}
}

// Resolve is the join the ticket exists for: two chats sharing one project
// dir must not both take the newest transcript.
func TestResolve(t *testing.T) {
	newest := transcript.Session{ID: "aaa", FirstPrompt: "triage the backlog"}
	middle := transcript.Session{ID: "bbb", FirstPrompt: "write the plan"}
	oldest := transcript.Session{ID: "ccc", FirstPrompt: "yesterday"}
	sessions := []transcript.Session{newest, middle, oldest} // ListSessions order: newest first

	cases := []struct {
		name    string
		claimed map[string]bool
		want    string
		ok      bool
	}{
		{"nothing claimed takes the newest", map[string]bool{}, "aaa", true},
		{"newest claimed falls to the next", map[string]bool{"aaa": true}, "bbb", true},
		{"two claimed falls to the third", map[string]bool{"aaa": true, "bbb": true}, "ccc", true},
		{"all claimed resolves nothing", map[string]bool{"aaa": true, "bbb": true, "ccc": true}, "", false},
		{"a nil claim set is empty, not a panic", nil, "aaa", true},
	}
	for _, c := range cases {
		got, ok := Resolve(sessions, c.claimed)
		if ok != c.ok || got.ID != c.want {
			t.Errorf("%s: Resolve = (%q, %v), want (%q, %v)", c.name, got.ID, ok, c.want, c.ok)
		}
	}
	// A booting chat has no .jsonl at all: null, not a wrong id.
	if _, ok := Resolve(nil, map[string]bool{}); ok {
		t.Error("Resolve over no transcripts must resolve nothing (session_id: null)")
	}
	// An id-less transcript is skipped rather than handed out as "".
	if got, ok := Resolve([]transcript.Session{{}, newest}, map[string]bool{}); !ok || got.ID != "aaa" {
		t.Errorf("Resolve skipped past an empty id wrong: (%q, %v)", got.ID, ok)
	}
}

// A live pane's row: `writable` and `busy` are derived, never passed in.
func TestLiveRow(t *testing.T) {
	created := time.Unix(1756600000, 0)
	row := Live{
		Session: "grove-chat-unbrewed-1", Workspace: "unbrewed", N: 1, Kind: KindChat,
		Command: "claude", Attached: false, Created: created,
		SessionID: "eeeb", Label: "triage the artgen backlog",
	}.Row()
	if !row.Writable || !row.Busy {
		t.Errorf("a live chat running claude must be writable and busy: %+v", row)
	}
	if row.SessionID == nil || *row.SessionID != "eeeb" {
		t.Errorf("session_id lost: %+v", row.SessionID)
	}
	// The cockpit's own pane is the same agent and NEVER writable.
	cockpit := Live{Session: "grove-unbrewed", Workspace: "unbrewed", N: 1, Kind: KindCockpit, Command: "claude"}.Row()
	if cockpit.Writable {
		t.Error("a cockpit orchestrator pane must never be writable")
	}
	if !cockpit.Busy {
		t.Error("busy is read off pane_current_command for every kind")
	}
	// An unresolved chat is null, not "" — a client must not mistake an
	// empty string for an id.
	starting := Live{Session: "grove-chat-unbrewed-2", Kind: KindChat, Command: "zsh"}.Row()
	if starting.SessionID != nil {
		t.Errorf("an unresolved chat must carry a nil session_id, got %q", *starting.SessionID)
	}
	if starting.Busy {
		t.Error("a shell prompt is not busy")
	}
	raw, err := json.Marshal(starting)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSub(string(raw), `"session_id":null`) {
		t.Errorf("an unresolved chat must marshal session_id as null: %s", raw)
	}
}

// An archived transcript is read-only and carries no live pane fields.
func TestArchivedRow(t *testing.T) {
	mod := time.Unix(1756500000, 0)
	row := ArchivedRow("unbrewed", transcript.Session{ID: "ccc", FirstPrompt: "yesterday", ModTime: mod})
	if row.Kind != KindArchived || row.Writable || row.Busy || row.Session != "" || row.N != 0 {
		t.Errorf("archived row wrong: %+v", row)
	}
	if row.SessionID == nil || *row.SessionID != "ccc" || row.Label != "yesterday" || !row.Created.Equal(mod) {
		t.Errorf("archived row lost its identity: %+v", row)
	}
}

func TestBusy(t *testing.T) {
	for _, cmd := range []string{"claude", "node", "bun", " claude "} {
		if !Busy(cmd) {
			t.Errorf("Busy(%q) = false, want true", cmd)
		}
	}
	for _, cmd := range []string{"", "zsh", "bash", "ssh", "vim"} {
		if Busy(cmd) {
			t.Errorf("Busy(%q) = true, want false", cmd)
		}
	}
}

// Classification of a COCKPIT session's panes: by cwd, because the
// dashboard and a grove-199 remote-chat pane both sit at the workspace root
// and neither is an orchestrator chat running here.
func TestIsOrchestratorPane(t *testing.T) {
	root := filepath.FromSlash("/home/o/ws")
	orch := filepath.Join(root, ".grove", "orchestrator")
	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"the brain dir itself", orch, true},
		{"a profile subdir (grove-36 T4)", filepath.Join(orch, "openrouter-glm"), true},
		{"a trailing slash is the same dir", orch + string(filepath.Separator), true},
		{"the dashboard pane (workspace root)", root, false},
		{"a remote chat pane (workspace root)", root, false},
		{"a sibling dir that merely shares the prefix", orch + "-old", false},
		{"a worker worktree", filepath.Join(root, ".worktrees", "repo", "t-1"), false},
		{"no cwd", "", false},
		{"no brain dir", orch, true},
	}
	for _, c := range cases {
		if got := IsOrchestratorPane(c.dir, orch); got != c.want {
			t.Errorf("%s: IsOrchestratorPane(%q) = %v, want %v", c.name, c.dir, got, c.want)
		}
	}
	if IsOrchestratorPane(orch, "") {
		t.Error("an empty brain dir must classify nothing")
	}
}

// The report order a client diffs two runs against.
func TestSort(t *testing.T) {
	older, newer := time.Unix(1000, 0), time.Unix(2000, 0)
	rows := []Row{
		{Workspace: "b", Kind: KindChat, N: 1},
		{Workspace: "a", Kind: KindArchived, Created: older, Label: "old"},
		{Workspace: "a", Kind: KindCockpit, N: 1},
		{Workspace: "a", Kind: KindChat, N: 2},
		{Workspace: "a", Kind: KindArchived, Created: newer, Label: "new"},
		{Workspace: "a", Kind: KindChat, N: 1},
	}
	Sort(rows)
	var got []string
	for _, r := range rows {
		got = append(got, r.Workspace+"/"+r.Kind+"/"+r.Label)
	}
	want := []string{"a/chat/", "a/chat/", "a/cockpit/", "a/archived/new", "a/archived/old", "b/chat/"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Sort order = %v, want %v", got, want)
		}
	}
	if rows[0].N != 1 || rows[1].N != 2 {
		t.Errorf("live chats must sort by number: %v, %v", rows[0].N, rows[1].N)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
