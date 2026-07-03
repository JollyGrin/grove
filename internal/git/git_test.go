package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepo creates a temporary git repo with one commit and returns its path.
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
			t.Fatalf("%v failed: %s\n%s", args, err, out)
		}
	}
	return dir
}

func TestRepoRoot(t *testing.T) {
	dir := initTestRepo(t)

	got, err := RepoRoot(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Resolve symlinks for macOS /private/tmp
	want, _ := filepath.EvalSymlinks(dir)
	got, _ = filepath.EvalSymlinks(got)
	if got != want {
		t.Errorf("RepoRoot = %q, want %q", got, want)
	}
}

func TestRepoName(t *testing.T) {
	got := RepoName("/home/user/projects/myrepo")
	if got != "myrepo" {
		t.Errorf("RepoName = %q, want %q", got, "myrepo")
	}
}

func TestCurrentBranch(t *testing.T) {
	dir := initTestRepo(t)

	got, err := CurrentBranch(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "main" {
		t.Errorf("CurrentBranch = %q, want %q", got, "main")
	}
}

func TestBranchStatusNoUpstream(t *testing.T) {
	dir := initTestRepo(t)

	ahead, behind, err := BranchStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ahead != 0 || behind != 0 {
		t.Errorf("BranchStatus = (%d, %d), want (0, 0)", ahead, behind)
	}
}

func TestIsDirty(t *testing.T) {
	dir := initTestRepo(t)

	dirty, err := IsDirty(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Error("expected clean repo")
	}

	// Create an untracked file
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello"), 0644)

	dirty, err = IsDirty(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Error("expected dirty repo")
	}
}

func TestDefaultBranch(t *testing.T) {
	dir := initTestRepo(t)

	got, err := DefaultBranch(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "main" {
		t.Errorf("DefaultBranch = %q, want %q", got, "main")
	}
}

func TestMainRepoRoot(t *testing.T) {
	dir := initTestRepo(t)

	// For a non-worktree, MainRepoRoot should equal RepoRoot
	got, err := MainRepoRoot(dir)
	if err != nil {
		t.Fatal(err)
	}

	want, _ := filepath.EvalSymlinks(dir)
	got, _ = filepath.EvalSymlinks(got)
	if got != want {
		t.Errorf("MainRepoRoot = %q, want %q", got, want)
	}
}

func TestMainRepoRootFromWorktree(t *testing.T) {
	dir := initTestRepo(t)
	wtPath := filepath.Join(t.TempDir(), "wt-test")

	// Create a worktree
	cmd := exec.Command("git", "worktree", "add", "-b", "test-branch", wtPath)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %s\n%s", err, out)
	}

	got, err := MainRepoRoot(wtPath)
	if err != nil {
		t.Fatal(err)
	}

	want, _ := filepath.EvalSymlinks(dir)
	got, _ = filepath.EvalSymlinks(got)
	if got != want {
		t.Errorf("MainRepoRoot (from worktree) = %q, want %q", got, want)
	}
}

func TestDeleteBranch(t *testing.T) {
	dir := initTestRepo(t)

	// Create and merge a branch so it's safe to delete
	cmds := [][]string{
		{"git", "checkout", "-b", "feature"},
		{"git", "commit", "--allow-empty", "-m", "feature commit"},
		{"git", "checkout", "main"},
		{"git", "merge", "feature"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}

	if err := DeleteBranch(dir, "feature"); err != nil {
		t.Errorf("DeleteBranch failed: %v", err)
	}
}

func TestMergeBranch(t *testing.T) {
	dir := initTestRepo(t)

	// Create a feature branch with a commit
	cmds := [][]string{
		{"git", "checkout", "-b", "feature"},
		{"git", "commit", "--allow-empty", "-m", "feature work"},
		{"git", "checkout", "main"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}

	if err := MergeBranch(dir, "feature", "main"); err != nil {
		t.Errorf("MergeBranch failed: %v", err)
	}
}
