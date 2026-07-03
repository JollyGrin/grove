// Overstory additions to the copied parkranger git wrapper. New
// functionality lives here so git.go stays byte-comparable with upstream.
package git

import (
	"fmt"
	"strconv"
	"strings"
)

// Fetch updates a remote ref so worktrees branch from a fresh base.
func Fetch(root, remote, ref string) error {
	_, err := run(root, "fetch", remote, ref)
	return err
}

// DiffBase picks the ref to diff a task branch against: the remote base
// (origin/<base>) when it exists, else the local base branch — so `ovs
// diff` shows work-since-fork even when origin is unreachable or absent.
func DiffBase(worktree, base string) string {
	if _, err := run(worktree, "rev-parse", "--verify", "refs/remotes/origin/"+base); err == nil {
		return "origin/" + base
	}
	return base
}

// PruneWorktrees drops registrations for worktrees whose directories no
// longer exist. `git worktree remove` fails on a missing path, so this is
// the only way to clean up a hand-deleted worktree.
func PruneWorktrees(repoRoot string) error {
	_, err := run(repoRoot, "worktree", "prune")
	return err
}

// LocalBranchExists reports whether a local branch ref exists.
func LocalBranchExists(root, branch string) bool {
	_, err := run(root, "rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil
}

// RemoteBranches lists origin branches matching a glob pattern (e.g.
// "DEV-1234-*"), short names without the origin/ prefix.
func RemoteBranches(root, pattern string) ([]string, error) {
	out, err := run(root, "branch", "-r", "--list", "origin/"+pattern, "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "->") {
			continue
		}
		branches = append(branches, strings.TrimPrefix(line, "origin/"))
	}
	return branches, nil
}

// HasUpstream reports whether the checked-out branch has an upstream.
func HasUpstream(path string) bool {
	_, err := run(path, "rev-parse", "--abbrev-ref", "@{upstream}")
	return err == nil
}

// CommitsNotOn counts commits reachable from ref but not from baseRef
// (rev-list baseRef..ref).
func CommitsNotOn(dir, baseRef, ref string) (int, error) {
	out, err := run(dir, "rev-list", "--count", baseRef+".."+ref)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// SafeToRemove reports whether a worktree can be deleted without losing
// work: the tree must be clean and every commit must exist somewhere
// else. BranchStatus reports ahead=0 for a branch with NO upstream
// (grab's initial push is non-fatal, so an offline grab leaves exactly
// that state, with no remote copy at all) — so without an upstream the
// commits are counted against baseRef instead, and an unverifiable state
// refuses rather than guesses.
func SafeToRemove(worktreePath, baseRef string) (ok bool, reason string, err error) {
	dirty, err := IsDirty(worktreePath)
	if err != nil {
		return false, "", err
	}
	if dirty {
		return false, "uncommitted changes", nil
	}
	if HasUpstream(worktreePath) {
		ahead, _, err := BranchStatus(worktreePath)
		if err != nil {
			return false, "", err
		}
		if ahead > 0 {
			return false, fmt.Sprintf("%d unpushed commit(s)", ahead), nil
		}
		return true, "", nil
	}
	n, err := CommitsNotOn(worktreePath, baseRef, "HEAD")
	if err != nil {
		return false, fmt.Sprintf("no upstream and cannot compare against %s", baseRef), nil
	}
	if n > 0 {
		return false, fmt.Sprintf("%d commit(s) not on %s (branch has no upstream — no remote copy exists)", n, baseRef), nil
	}
	return true, "", nil
}

// ForceDeleteBranch removes a local branch with -D. Required because the
// monorepo squash-merges: squash-merged branches are never ancestry-merged,
// so `git branch -d` refuses them every time. Merge status must be verified
// via the GitHub API (gh pr view) before calling this.
func ForceDeleteBranch(root, branch string) error {
	_, err := run(root, "branch", "-D", branch)
	return err
}

// DeleteRemoteBranch removes the remote branch. Grab pushes branches at
// creation time (parkranger parity), so cleanup must delete remotely too or
// abandoned branches leak.
func DeleteRemoteBranch(root, branch string) error {
	_, err := run(root, "push", "origin", "--delete", branch)
	return err
}

// RemoveWorktreeForce removes a worktree even if dirty. Used by `ovs done
// --force` after the user confirms.
func RemoveWorktreeForce(repoRoot, path string) error {
	_, err := run(repoRoot, "worktree", "remove", "--force", path)
	return err
}
