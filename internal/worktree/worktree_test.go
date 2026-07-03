package worktree

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	return dir
}

func TestParseWorktreeList(t *testing.T) {
	input := `worktree /home/user/repo
branch refs/heads/main

worktree /home/user/.worktrees/repo/feat-x
branch refs/heads/feat-x

`
	wts := parseWorktreeList(input)

	if len(wts) != 2 {
		t.Fatalf("got %d worktrees, want 2", len(wts))
	}

	if wts[0].Path != "/home/user/repo" || wts[0].Branch != "main" {
		t.Errorf("wt[0] = %+v", wts[0])
	}
	if wts[1].Path != "/home/user/.worktrees/repo/feat-x" || wts[1].Branch != "feat-x" {
		t.Errorf("wt[1] = %+v", wts[1])
	}
	if wts[0].Name != "repo" {
		t.Errorf("wt[0].Name = %q, want %q", wts[0].Name, "repo")
	}
	if wts[1].Name != "feat-x" {
		t.Errorf("wt[1].Name = %q, want %q", wts[1].Name, "feat-x")
	}
}

func TestList(t *testing.T) {
	dir := initTestRepo(t)

	wts, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(wts) != 1 {
		t.Fatalf("got %d worktrees, want 1", len(wts))
	}

	// Resolve symlinks for macOS /private/tmp
	gotPath, _ := filepath.EvalSymlinks(wts[0].Path)
	wantPath, _ := filepath.EvalSymlinks(dir)
	if gotPath != wantPath {
		t.Errorf("Path = %q, want %q", gotPath, wantPath)
	}

	if !wts[0].IsMain {
		t.Error("expected first worktree to be main")
	}
}

func TestAddAndRemove(t *testing.T) {
	dir := initTestRepo(t)

	wt, err := Add(dir, "test-feature", "main")
	if err != nil {
		t.Fatal(err)
	}

	if wt.Name != "test-feature" {
		t.Errorf("Name = %q, want %q", wt.Name, "test-feature")
	}
	if wt.Branch != "test-feature" {
		t.Errorf("Branch = %q, want %q", wt.Branch, "test-feature")
	}

	// Verify it shows up in list
	wts, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 {
		t.Fatalf("got %d worktrees after add, want 2", len(wts))
	}

	// Remove it
	if err := Remove(dir, wt.Path); err != nil {
		t.Fatal(err)
	}

	wts, err = List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 1 {
		t.Fatalf("got %d worktrees after remove, want 1", len(wts))
	}
}

func TestDefaultPath(t *testing.T) {
	got := DefaultPath("/home/user/myrepo", "feat-x")
	want := "/home/user/.worktrees/myrepo/feat-x"
	if got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
}

func TestFindByName(t *testing.T) {
	wts := []Worktree{
		{Name: "main", Path: "/a"},
		{Name: "feat-x", Path: "/b"},
	}

	found := FindByName(wts, "feat-x")
	if found == nil || found.Path != "/b" {
		t.Errorf("FindByName = %v, want feat-x", found)
	}

	if FindByName(wts, "nope") != nil {
		t.Error("expected nil for missing worktree")
	}
}
