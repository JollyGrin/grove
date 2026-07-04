package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a git repo with one commit on branch main.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestBaseRefNoRemote(t *testing.T) {
	dir := initRepo(t)
	ref, err := BaseRef(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "main" {
		t.Errorf("no-remote BaseRef = %q, want local main", ref)
	}
	if HasRemote(dir, "origin") {
		t.Error("HasRemote must be false with no remote")
	}
}

func TestBaseRefMissingBranch(t *testing.T) {
	dir := initRepo(t)
	_, err := BaseRef(dir, "develop")
	if err == nil || !strings.Contains(err.Error(), "develop") {
		t.Errorf("want missing-base error naming the branch, got %v", err)
	}
}

func TestBaseRefPrefersOrigin(t *testing.T) {
	origin := initRepo(t)
	dir := t.TempDir()
	clone := exec.Command("git", "clone", origin, dir)
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	// Guard against a stray global git config: the clone must see origin.
	if !HasRemote(dir, "origin") {
		t.Fatal("clone should have origin")
	}
	ref, err := BaseRef(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "origin/main" {
		t.Errorf("BaseRef = %q, want origin/main", ref)
	}
	_ = os.RemoveAll(filepath.Join(dir, ".git", "refs", "remotes"))
}
