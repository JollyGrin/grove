package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepoWithOrigin creates a repo with a bare origin, main pushed
// with upstream tracking. Returns the working repo path.
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

// addTestWorktree creates a worktree on a new branch, optionally pushed
// with upstream tracking.
func addTestWorktree(t *testing.T, repo, branch string, push bool) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), branch)
	cmd := exec.Command("git", "worktree", "add", "-b", branch, wt)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %s\n%s", err, out)
	}
	if push {
		cmd := exec.Command("git", "push", "-u", "origin", branch)
		cmd.Dir = wt
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("push: %s\n%s", err, out)
		}
	}
	return wt
}

func commitEmpty(t *testing.T, dir, msg string) {
	t.Helper()
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", msg)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %s\n%s", err, out)
	}
}

func TestSafeToRemoveDirty(t *testing.T) {
	repo := initTestRepoWithOrigin(t)
	wt := addTestWorktree(t, repo, "feat-dirty", true)
	os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("x"), 0o644)

	ok, reason, err := SafeToRemove(wt, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("dirty worktree should not be safe to remove")
	}
	if reason != "uncommitted changes" {
		t.Errorf("reason = %q", reason)
	}
}

func TestSafeToRemoveAhead(t *testing.T) {
	repo := initTestRepoWithOrigin(t)
	wt := addTestWorktree(t, repo, "feat-ahead", true)
	commitEmpty(t, wt, "unpushed work")

	ok, reason, err := SafeToRemove(wt, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("ahead-of-upstream worktree should not be safe to remove")
	}
	if reason != "1 unpushed commit(s)" {
		t.Errorf("reason = %q", reason)
	}
}

func TestSafeToRemoveClean(t *testing.T) {
	repo := initTestRepoWithOrigin(t)
	wt := addTestWorktree(t, repo, "feat-clean", true)

	ok, reason, err := SafeToRemove(wt, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("clean+pushed worktree should be safe to remove (reason %q)", reason)
	}
}

// A branch whose initial push failed (offline grab) has NO upstream —
// BranchStatus reports ahead=0 and there is no remote copy. The guard
// must fall back to counting commits against the base.
func TestSafeToRemoveNoUpstream(t *testing.T) {
	repo := initTestRepoWithOrigin(t)
	wt := addTestWorktree(t, repo, "feat-no-upstream", false)
	commitEmpty(t, wt, "only copy of this work")

	ok, reason, err := SafeToRemove(wt, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("no-upstream branch with commits should not be safe to remove")
	}
	if reason == "" {
		t.Error("expected a reason naming the unpushed commits")
	}

	// No commits beyond base → safe even without an upstream.
	wt2 := addTestWorktree(t, repo, "feat-no-upstream-empty", false)
	ok, reason, err = SafeToRemove(wt2, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("no-upstream branch with no commits should be safe (reason %q)", reason)
	}
}

func TestCommitsNotOn(t *testing.T) {
	repo := initTestRepoWithOrigin(t)
	wt := addTestWorktree(t, repo, "feat-count", false)
	commitEmpty(t, wt, "one")
	commitEmpty(t, wt, "two")

	n, err := CommitsNotOn(repo, "origin/main", "feat-count")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("CommitsNotOn = %d, want 2", n)
	}
}

func TestLocalBranchExists(t *testing.T) {
	repo := initTestRepo(t)
	if !LocalBranchExists(repo, "main") {
		t.Error("main should exist")
	}
	if LocalBranchExists(repo, "nope") {
		t.Error("nope should not exist")
	}
}

func TestDiffBase(t *testing.T) {
	// With origin/main present → prefer the remote ref.
	repo := initTestRepoWithOrigin(t)
	if got := DiffBase(repo, "main"); got != "origin/main" {
		t.Errorf("DiffBase with origin = %q, want origin/main", got)
	}
	// No remote at all → fall back to the local base branch.
	local := initTestRepo(t)
	if got := DiffBase(local, "main"); got != "main" {
		t.Errorf("DiffBase without origin = %q, want main", got)
	}
}
