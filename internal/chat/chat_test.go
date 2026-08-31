package chat

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/tmux"
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

// Resolve is the fallback, and its whole job is knowing when NOT to answer:
// one candidate pane per project dir it can pair, rivals it must refuse
// (grove-222 — ordering rivals by transcript mtime inverted a live pair).
func TestResolve(t *testing.T) {
	newest := transcript.Session{ID: "aaa", FirstPrompt: "triage the backlog"}
	middle := transcript.Session{ID: "bbb", FirstPrompt: "write the plan"}
	oldest := transcript.Session{ID: "ccc", FirstPrompt: "yesterday"}
	sessions := []transcript.Session{newest, middle, oldest} // ListSessions order: newest first

	cases := []struct {
		name    string
		claimed map[string]bool
		rivals  int
		want    string
		ok      bool
	}{
		{"the sole candidate takes the newest", map[string]bool{}, 1, "aaa", true},
		{"newest claimed falls to the next", map[string]bool{"aaa": true}, 1, "bbb", true},
		{"two claimed falls to the third", map[string]bool{"aaa": true, "bbb": true}, 1, "ccc", true},
		{"all claimed resolves nothing", map[string]bool{"aaa": true, "bbb": true, "ccc": true}, 1, "", false},
		{"a nil claim set is empty, not a panic", nil, 1, "aaa", true},
		{"two rival panes: refuse, never guess", map[string]bool{}, 2, "", false},
		{"three rivals: still a refusal", map[string]bool{}, 3, "", false},
		{"no candidate at all is not a claim", map[string]bool{}, 0, "", false},
	}
	for _, c := range cases {
		got, ok := Resolve(sessions, c.claimed, c.rivals)
		if ok != c.ok || got.ID != c.want {
			t.Errorf("%s: Resolve = (%q, %v), want (%q, %v)", c.name, got.ID, ok, c.want, c.ok)
		}
	}
	// A booting chat has no .jsonl at all: null, not a wrong id.
	if _, ok := Resolve(nil, map[string]bool{}, 1); ok {
		t.Error("Resolve over no transcripts must resolve nothing (session_id: null)")
	}
	// An id-less transcript is skipped rather than handed out as "".
	if got, ok := Resolve([]transcript.Session{{}, newest}, map[string]bool{}, 1); !ok || got.ID != "aaa" {
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

// TestValidSessionID is a safety test as much as a shape test: the id ends
// up inside the shell command tmux runs in the new chat pane (grove-217).
func TestValidSessionID(t *testing.T) {
	for _, id := range []string{
		"eeeb1a2b-3c4d-5e6f-8a9b-0c1d2e3f4a5b",
		"aaaa1111",
		"A_b-9",
	} {
		if !ValidSessionID(id) {
			t.Errorf("ValidSessionID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{
		"", " ", "a b", "id;rm -rf /", "$(whoami)", "`id`", "a'b", `a"b`,
		"--dangerously-skip-permissions", "../../etc/passwd", "a/b",
		strings.Repeat("a", 65),
	} {
		if ValidSessionID(id) {
			t.Errorf("ValidSessionID(%q) = true, want false", id)
		}
	}
}

// TestFindSession: the dir is the load-bearing half — a profiled chat can
// only be resumed from the cwd it ran in.
func TestFindSession(t *testing.T) {
	dirs := []ProjectDir{
		{Dir: "/w/.grove/orchestrator", Sessions: []transcript.Session{
			{ID: "bbbb2222", FirstPrompt: "release notes"},
			{ID: "aaaa1111", FirstPrompt: "triage the backlog"},
		}},
		{Dir: "/w/.grove/orchestrator/glm", Sessions: []transcript.Session{
			{ID: "cccc3333", FirstPrompt: "the cheap lane"},
		}},
	}
	d, s, ok := FindSession(dirs, "aaaa1111")
	if !ok || d.Dir != "/w/.grove/orchestrator" || s.FirstPrompt != "triage the backlog" {
		t.Fatalf("FindSession(aaaa1111) = %q/%+v/%v", d.Dir, s, ok)
	}
	d, _, ok = FindSession(dirs, "cccc3333")
	if !ok || d.Dir != "/w/.grove/orchestrator/glm" {
		t.Fatalf("a profiled chat must resolve to ITS dir, got %q (ok=%v)", d.Dir, ok)
	}
	if _, _, ok := FindSession(dirs, "nope"); ok {
		t.Error("an unknown id must not resolve")
	}
	if _, _, ok := FindSession(nil, "aaaa1111"); ok {
		t.Error("no dirs, no answer")
	}
}

func TestProfileForDir(t *testing.T) {
	orch := "/w/.grove/orchestrator"
	if name, ok := ProfileForDir(orch, orch); !ok || name != "" {
		t.Errorf("brain dir = %q/%v, want the operator's own Claude", name, ok)
	}
	if name, ok := ProfileForDir(orch, orch+"/openrouter-glm"); !ok || name != "openrouter-glm" {
		t.Errorf("profile dir = %q/%v", name, ok)
	}
	if name, ok := ProfileForDir(orch, orch+"/a/b"); ok {
		t.Errorf("a nested dir is not a profile dir, got %q/%v", name, ok)
	}
	if _, ok := ProfileForDir(orch, "/elsewhere"); ok {
		t.Error("a dir outside the brain dir is not a profile dir")
	}
}

func TestLiveHolder(t *testing.T) {
	panes := []tmux.LivePane{
		{Session: "grove-chat-w-1", ChatSession: "aaaa1111"},
		{Session: "grove-chat-w-2"},
	}
	if got := LiveHolder(panes, "aaaa1111"); got != "grove-chat-w-1" {
		t.Errorf("LiveHolder = %q, want the holding session", got)
	}
	if got := LiveHolder(panes, "bbbb2222"); got != "" {
		t.Errorf("LiveHolder(unheld) = %q, want empty", got)
	}
	// An unstamped pane must never match the empty id.
	if got := LiveHolder(panes, ""); got != "" {
		t.Errorf("LiveHolder(\"\") = %q, want empty", got)
	}
}
