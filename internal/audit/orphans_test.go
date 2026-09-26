package audit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/workspace"
	"github.com/JollyGrin/grove/internal/worktree"
)

// initRepo makes a minimal git repo scanOrphans can list worktrees for.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	run("commit", "--allow-empty", "-m", "init")
	return dir
}

// mkSibling registers an alive workspace at root tracking one task whose
// worktree is worktreePath — the shape scanOrphans must consult so a
// sibling's live worker isn't reported as an orphan (grove-350).
func mkSibling(t *testing.T, root, label, ticket, worktreePath string, done bool) workspace.Workspace {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".grove"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgYaml := "workspace:\n  label: " + label + "\n"
	if err := os.WriteFile(filepath.Join(root, ".grove", "config.yaml"), []byte(cfgYaml), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".grove", "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ev := state.Event{Type: state.EvTaskCreated, Ticket: ticket, Data: map[string]string{
		"title": ticket, "repo": "r", "branch": ticket, "worktree": worktreePath,
	}}
	if err := state.Append(dir, ev); err != nil {
		t.Fatal(err)
	}
	if done {
		if err := state.Append(dir, state.Event{Type: state.EvTaskUntracked, Ticket: ticket}); err != nil {
			t.Fatal(err)
		}
	}
	return workspace.Workspace{Root: root, Label: label, Scope: workspace.ScopeRepo}
}

// orphanPaths realpath-normalizes both the report and the path under test —
// git's own `worktree list --porcelain` output is already symlink-resolved
// (e.g. macOS /var → /private/var), while worktree.Add returns the
// unresolved path it was given, so a raw string comparison would spuriously
// fail on every host where TMPDIR is a symlink.
func orphanPaths(orphans []Orphan) map[string]bool {
	out := map[string]bool{}
	for _, o := range orphans {
		out[realpath(o.Path)] = true
	}
	return out
}

func TestScanOrphansSiblingWorkspaceNotOrphan(t *testing.T) {
	t.Setenv("GROVE_STATE_DIR", "") // per-root state dirs, not the scratch override
	t.Setenv("HOME", t.TempDir())   // keep well away from any real ~/.local/state/grove

	repoDir := initRepo(t)
	wtB, err := worktree.Add(repoDir, "sib-task", "main")
	if err != nil {
		t.Fatal(err)
	}
	wtOrphan, err := worktree.Add(repoDir, "nobody-tracks-me", "main")
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Repos: map[string]*config.Repo{"r": {Path: repoDir}}}

	tmp := t.TempDir()
	sibling := mkSibling(t, filepath.Join(tmp, "workspace-b"), "b", "sib-1", wtB.Path, false)

	orphans := scanOrphans(cfg, map[string]*state.Task{}, []workspace.Workspace{sibling})
	byPath := orphanPaths(orphans)

	if byPath[realpath(wtB.Path)] {
		t.Errorf("sibling-tracked worktree %s reported as orphan, want excluded", wtB.Path)
	}
	if !byPath[realpath(wtOrphan.Path)] {
		t.Errorf("untracked worktree %s NOT reported as orphan, want included", wtOrphan.Path)
	}
}

func TestScanOrphansMissingSiblingStateDirNonFatal(t *testing.T) {
	t.Setenv("GROVE_STATE_DIR", "")
	t.Setenv("HOME", t.TempDir())

	repoDir := initRepo(t)
	wt, err := worktree.Add(repoDir, "solo-task", "main")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Repos: map[string]*config.Repo{"r": {Path: repoDir}}}

	// A registered workspace whose root/.grove marker is gone (moved or
	// deleted) — Alive() is false, so it must be skipped rather than
	// erroring the whole scan.
	dead := workspace.Workspace{Root: filepath.Join(t.TempDir(), "gone"), Label: "dead", Scope: workspace.ScopeRepo}

	orphans := scanOrphans(cfg, map[string]*state.Task{}, []workspace.Workspace{dead})
	byPath := orphanPaths(orphans)

	if !byPath[realpath(wt.Path)] {
		t.Errorf("untracked worktree %s should still be reported as orphan despite the dead sibling", wt.Path)
	}
}
