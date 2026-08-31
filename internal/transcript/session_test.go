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
	// Default: grove workers run under the default Claude Code config dir
	// (~/.claude), not a separate work-subscription profile.
	t.Setenv("GV_CLAUDE_CONFIG_DIR", "")
	dir := ProjectDir("/Users/dev/git/acme/.trees/dev-1301")
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".claude", "projects", "-Users-dev-git-acme--trees-dev-1301")
	if dir != want {
		t.Errorf("ProjectDir = %q, want %q", dir, want)
	}
}

func TestProjectDir_ConfigDirOverride(t *testing.T) {
	// GV_CLAUDE_CONFIG_DIR takes precedence over the ~/.claude default — how
	// ovs-style repos and the e2e harness point discovery elsewhere.
	t.Setenv("GV_CLAUDE_CONFIG_DIR", "/custom/cc")
	dir := ProjectDir("/Users/dev/git/acme/.trees/dev-1301")
	want := filepath.Join("/custom/cc", "projects", "-Users-dev-git-acme--trees-dev-1301")
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

// grove-227: the regression guard. The chat reader now resolves a Claude
// config dir PER WORKSPACE and passes it down; a workspace that sets no
// claude_config_dir passes "" and must land on exactly the path today's
// code computes. Asserted against the literal expected path — comparing
// against ProjectDir would pass even if both drifted together.
func TestProjectDirIn_EmptyIsTodaysDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GV_CLAUDE_CONFIG_DIR", "")

	const wt = "/Users/dev/git/acme/.trees/dev-1301"
	want := filepath.Join(home, ".claude", "projects", "-Users-dev-git-acme--trees-dev-1301")
	if got := ProjectDirIn("", wt); got != want {
		t.Errorf("ProjectDirIn(\"\", %q) = %q, want %q", wt, got, want)
	}
}

// "" also means "keep honouring the env" — the e2e harness and ovs-style
// repos point discovery with GV_CLAUDE_CONFIG_DIR and must be unaffected.
func TestProjectDirIn_EmptyKeepsEnv(t *testing.T) {
	t.Setenv("GV_CLAUDE_CONFIG_DIR", "/custom/cc")
	want := filepath.Join("/custom/cc", "projects", "-Users-dev-git-acme")
	if got := ProjectDirIn("", "/Users/dev/git/acme"); got != want {
		t.Errorf("ProjectDirIn(\"\", …) = %q, want %q", got, want)
	}
}

// An explicit dir wins over the env, deliberately: thegrid's transcripts
// live under ~/.cc-work whatever environment `gv chat serve` was started
// in, and one process serves every workspace.
func TestProjectDirIn_ExplicitBeatsEnv(t *testing.T) {
	t.Setenv("GV_CLAUDE_CONFIG_DIR", "/custom/cc")
	want := filepath.Join("/work/cc-work", "projects", "-Users-dev-git-acme")
	if got := ProjectDirIn("/work/cc-work", "/Users/dev/git/acme"); got != want {
		t.Errorf("ProjectDirIn(\"/work/cc-work\", …) = %q, want %q", got, want)
	}
}

// ListSessionsIn reads the project dir under the config dir it is handed,
// and sees nothing under any other.
func TestListSessionsIn_ExplicitConfigDir(t *testing.T) {
	t.Setenv("GV_CLAUDE_CONFIG_DIR", "")
	cc := t.TempDir()
	wt := t.TempDir()

	projDir := ProjectDirIn(cc, wt)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + wt + `","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projDir, "abc.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ListSessionsIn(cc, wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "abc" {
		t.Fatalf("ListSessionsIn(%q, …) = %+v, want the one session under that config dir", cc, got)
	}
	// The same worktree under a different config dir holds nothing — the
	// blindness this ticket fixes, in reverse.
	other, err := ListSessionsIn(t.TempDir(), wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Errorf("ListSessionsIn(other config dir) = %+v, want none", other)
	}
}
