// Overstory additions to the copied parkranger worktree wrapper. New
// functionality lives here so worktree.go stays byte-comparable with
// upstream.
package worktree

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/JollyGrin/grove/internal/git"
)

// RemoveSafe removes a worktree like Remove, but tolerates a worktree
// whose directory was already deleted by hand: `git worktree remove`
// (with or without --force) fails on a missing path, so in that case the
// stale registration is dropped via `git worktree prune` instead. Prune
// only drops registrations whose directories are gone — intact sibling
// worktrees are unaffected.
func RemoveSafe(repoRoot, path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return git.PruneWorktrees(repoRoot)
	}
	return Remove(repoRoot, path)
}

// AddExisting creates a worktree at the conventional path from a branch
// that already exists — locally, or on origin only (adopting work grabbed
// before a disconnect). Unlike Add it creates no new branch and never
// pushes. A branch checked out in another worktree fails loudly. The
// fetch is best-effort (same stance as grab's): offline adoption of a
// local branch still works.
func AddExisting(repoRoot, branch string) (Worktree, error) {
	if err := git.Fetch(repoRoot, "origin", branch); err != nil {
		fmt.Fprintf(os.Stderr, "warning: git fetch failed (%v) — resolving %s from local refs\n", err, branch)
	}
	wtPath := DefaultPath(repoRoot, branch)

	var args []string
	switch {
	case git.LocalBranchExists(repoRoot, branch):
		args = []string{"worktree", "add", wtPath, branch}
	case remoteBranchExists(repoRoot, branch):
		// Resolve explicitly against origin/ rather than trusting DWIM
		// (which mis-picks when a same-named branch exists on two remotes).
		args = []string{"worktree", "add", "--track", "-b", branch, wtPath, "origin/" + branch}
	default:
		return Worktree{}, fmt.Errorf("branch %q exists neither locally nor on origin", branch)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Worktree{}, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return Worktree{Name: branch, Path: wtPath, Branch: branch}, nil
}

func remoteBranchExists(repoRoot, branch string) bool {
	remotes, err := git.RemoteBranches(repoRoot, branch)
	return err == nil && len(remotes) > 0
}
