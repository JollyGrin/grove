package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepoWithOrigin gives the test repo a bare origin with main pushed.
func initTestRepoWithOrigin(t *testing.T) string {
	t.Helper()
	dir := initTestRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	cmds := [][]string{
		{"git", "init", "--bare", origin},
		{"git", "remote", "add", "origin", origin},
		{"git", "push", "-u", "origin", "main"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %s\n%s", args, err, out)
		}
	}
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s\n%s", args, err, out)
	}
}

func TestAddExistingLocal(t *testing.T) {
	dir := initTestRepoWithOrigin(t)
	gitIn(t, dir, "branch", "feat-local", "main")

	wt, err := AddExisting(dir, "feat-local")
	if err != nil {
		t.Fatal(err)
	}
	if wt.Branch != "feat-local" {
		t.Errorf("Branch = %q", wt.Branch)
	}
	wts, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := FindByName(wts, "feat-local")
	if found == nil {
		t.Fatal("worktree not registered")
	}
	if found.Branch != "feat-local" {
		t.Errorf("checked out branch = %q, want feat-local", found.Branch)
	}
}

func TestAddExistingRemoteOnly(t *testing.T) {
	dir := initTestRepoWithOrigin(t)
	// Branch exists on origin but not locally (grabbed on another machine
	// / state lost after a local branch delete).
	gitIn(t, dir, "branch", "feat-remote", "main")
	gitIn(t, dir, "push", "origin", "feat-remote")
	gitIn(t, dir, "branch", "-D", "feat-remote")

	wt, err := AddExisting(dir, "feat-remote")
	if err != nil {
		t.Fatal(err)
	}
	wts, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := FindByName(wts, "feat-remote")
	if found == nil || found.Branch != "feat-remote" {
		t.Fatalf("remote-only adopt: worktree = %+v", found)
	}
	_ = wt
}

func TestAddExistingConflict(t *testing.T) {
	dir := initTestRepoWithOrigin(t)
	if _, err := Add(dir, "feat-conflict", "main"); err != nil {
		t.Fatal(err)
	}
	// Branch is already checked out in the worktree Add created — a second
	// worktree on the same branch must fail loudly, not force.
	if _, err := AddExisting(dir, "feat-conflict"); err == nil {
		t.Fatal("expected conflict error for a branch checked out elsewhere")
	}
}

func TestAddExistingUnknownBranch(t *testing.T) {
	dir := initTestRepoWithOrigin(t)
	if _, err := AddExisting(dir, "no-such-branch"); err == nil {
		t.Fatal("expected error for unknown branch")
	}
}

func TestRemoveSafeMissingDir(t *testing.T) {
	dir := initTestRepo(t)

	gone, err := Add(dir, "gone-feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	kept, err := Add(dir, "kept-feature", "main")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(gone.Path); err != nil {
		t.Fatal(err)
	}

	if err := RemoveSafe(dir, gone.Path); err != nil {
		t.Fatalf("RemoveSafe on missing dir: %v", err)
	}

	wts, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if FindByName(wts, "gone-feature") != nil {
		t.Error("stale registration for gone-feature survived")
	}
	if FindByName(wts, "kept-feature") == nil {
		t.Error("sibling worktree kept-feature was lost")
	}
	if len(wts) != 2 { // main + kept
		t.Errorf("got %d worktrees, want 2", len(wts))
	}
	_ = kept
}

func TestRemoveSafeClean(t *testing.T) {
	dir := initTestRepo(t)

	wt, err := Add(dir, "clean-feature", "main")
	if err != nil {
		t.Fatal(err)
	}

	if err := RemoveSafe(dir, wt.Path); err != nil {
		t.Fatalf("RemoveSafe on clean worktree: %v", err)
	}

	wts, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 1 {
		t.Errorf("got %d worktrees after remove, want 1", len(wts))
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Error("worktree dir still exists")
	}
}

func TestRemoveSafeDirtyRefuses(t *testing.T) {
	dir := initTestRepo(t)

	wt, err := Add(dir, "dirty-feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "junk.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Untracked files alone don't block `git worktree remove`; a staged
	// change does.
	cmd := exec.Command("git", "add", "junk.txt")
	cmd.Dir = wt.Path
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	if err := RemoveSafe(dir, wt.Path); err == nil {
		t.Fatal("RemoveSafe on dirty worktree should refuse")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Error("dirty worktree dir should survive the refusal")
	}
}
