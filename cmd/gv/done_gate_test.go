package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
)

// gateFixture builds a repo with a (dead) origin remote, puts a stub gh on
// PATH, and returns the cfg/task finishTask needs. Every path under test
// returns before teardown, so no worktree or tmux state is touched.
func gateFixture(t *testing.T, ghScript string) (*config.Config, *state.Task) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "remote", "add", "origin", filepath.Join(dir, "nowhere.git")},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(ghScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := &config.Config{Repos: map[string]*config.Repo{"r": {Path: repo}}}
	return cfg, &state.Task{Ticket: "r-1", Repo: "r", Branch: "r-1-x"}
}

// TestFinishTaskDistinguishesGHFailureFromNoPR (grove-451): a gh command
// failure must read "merge check failed … retry", never "no PR found" —
// the latter funnels operators to --force when a retry would do.
func TestFinishTaskDistinguishesGHFailureFromNoPR(t *testing.T) {
	cfg, task := gateFixture(t, "#!/bin/sh\necho 'HTTP 403: Resource not accessible by integration' >&2\nexit 1\n")
	err := finishTask(cfg, task, false)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "merge check failed") || !strings.Contains(msg, "retry, or use --force to override") {
		t.Errorf("error = %q, want the merge-check-failed/retry message", msg)
	}
	if strings.Contains(msg, "no PR found") {
		t.Errorf("error = %q, must not claim no PR when gh itself failed", msg)
	}
	if !strings.Contains(msg, "403") {
		t.Errorf("error = %q, want the underlying gh error included", msg)
	}
}

// TestFinishTaskNoPRMessage: a gh that succeeds with an empty list is the
// genuine "no PR found" case and keeps its message.
func TestFinishTaskNoPRMessage(t *testing.T) {
	cfg, task := gateFixture(t, "#!/bin/sh\necho '[]'\n")
	err := finishTask(cfg, task, false)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "no PR found") || strings.Contains(err.Error(), "merge check failed") {
		t.Errorf("error = %q, want the plain no-PR message", err)
	}
}
