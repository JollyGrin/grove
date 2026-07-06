package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEncodePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			"/Users/dev/git/acme/.trees/dev-1301",
			"-Users-dev-git-acme--trees-dev-1301",
		},
		{
			"/Users/dev/git/myrepo",
			"-Users-dev-git-myrepo",
		},
		{
			"/home/user/.config/test",
			"-home-user--config-test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := EncodePath(tt.input)
			if got != tt.want {
				t.Errorf("EncodePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestProjectDir(t *testing.T) {
	dir := ProjectDir("/Users/dev/git/acme/.trees/dev-1301")
	home, _ := os.UserHomeDir()
	// ovs divergence: sessions run under CLAUDE_CONFIG_DIR=~/.cc-work
	want := filepath.Join(home, ".cc-work", "projects", "-Users-dev-git-acme--trees-dev-1301")
	if dir != want {
		t.Errorf("ProjectDir = %q, want %q", dir, want)
	}
}

func TestListSessions(t *testing.T) {
	// Set up a fake project dir
	dir := t.TempDir()
	worktreePath := dir // use tmpdir as the "worktree" so cwd matches

	projDir := ProjectDir(worktreePath)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write two matching sessions and one non-matching
	writeSession := func(name, cwd, prompt string, age time.Duration) {
		content := `{"type":"system","cwd":"` + cwd + `","content":"init"}
{"type":"user","cwd":"` + cwd + `","message":{"role":"user","content":"` + prompt + `"}}
`
		path := filepath.Join(projDir, name+".jsonl")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		// Set mtime
		mtime := time.Now().Add(-age)
		os.Chtimes(path, mtime, mtime)
	}

	writeSession("newer", worktreePath, "newer prompt", 1*time.Hour)
	writeSession("older", worktreePath, "older prompt", 24*time.Hour)
	writeSession("wrong", "/some/other/path", "wrong cwd", 0)

	sessions, err := ListSessions(worktreePath)
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	// Should be sorted by ModTime descending
	if sessions[0].ID != "newer" {
		t.Errorf("first session ID = %q, want newer", sessions[0].ID)
	}
	if sessions[1].ID != "older" {
		t.Errorf("second session ID = %q, want older", sessions[1].ID)
	}
	if sessions[0].FirstPrompt != "newer prompt" {
		t.Errorf("first prompt = %q", sessions[0].FirstPrompt)
	}
}

func TestListSessions_NoDir(t *testing.T) {
	sessions, err := ListSessions("/nonexistent/path/that/wont/match")
	if err != nil {
		t.Fatal(err)
	}
	if sessions != nil {
		t.Errorf("expected nil, got %v", sessions)
	}
}
