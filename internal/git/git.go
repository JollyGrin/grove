// Package git provides thin wrappers around the git CLI.
// Every function is stateless — pass in a working directory.
package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// run executes git with the given args in dir, returning trimmed stdout.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// RepoRoot returns the repository root for the given path.
func RepoRoot(path string) (string, error) {
	return run(path, "rev-parse", "--show-toplevel")
}

// MainRepoRoot resolves the main repository root. If path is inside a
// worktree, it reads the .git pointer file to find the main repo.
func MainRepoRoot(path string) (string, error) {
	root, err := RepoRoot(path)
	if err != nil {
		return "", err
	}

	gitPath := filepath.Join(root, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", gitPath, err)
	}

	// Regular directory → this is the main repo
	if info.IsDir() {
		return root, nil
	}

	// File → worktree pointer: "gitdir: /path/to/repo/.git/worktrees/<name>"
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", gitPath, err)
	}

	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir: ") {
		return "", fmt.Errorf("unexpected .git file content: %s", line)
	}

	gitdir := strings.TrimPrefix(line, "gitdir: ")
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}

	// Walk up from .git/worktrees/<name> to .git, then parent is main repo
	// Expected: /repo/.git/worktrees/<name> → /repo/.git → /repo
	dotGit := filepath.Dir(filepath.Dir(gitdir))
	mainRoot := filepath.Dir(dotGit)

	// Verify it's actually a git repo
	if _, err := os.Stat(filepath.Join(mainRoot, ".git")); err != nil {
		return "", fmt.Errorf("resolved main root %s is not a git repo", mainRoot)
	}

	return mainRoot, nil
}

// RepoName returns the base directory name of the repo root.
func RepoName(root string) string {
	return filepath.Base(root)
}

// CurrentBranch returns the current branch name, or empty string if detached.
func CurrentBranch(path string) (string, error) {
	out, err := run(path, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return out, nil
}

// BranchStatus returns how many commits ahead/behind the current branch is
// relative to its upstream. Returns (0, 0) if there is no upstream.
func BranchStatus(path string) (ahead, behind int, err error) {
	out, err := run(path, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		// No upstream is not an error
		if strings.Contains(err.Error(), "no upstream") || strings.Contains(err.Error(), "unknown revision") {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	parts := strings.Fields(out)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output: %q", out)
	}

	ahead, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("parse ahead count: %w", err)
	}
	behind, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("parse behind count: %w", err)
	}
	return ahead, behind, nil
}

// IsDirty returns true if the working tree has uncommitted changes.
func IsDirty(path string) (bool, error) {
	out, err := run(path, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// DefaultBranch returns the default branch name (main, master, etc).
func DefaultBranch(root string) (string, error) {
	out, err := run(root, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err == nil {
		// refs/remotes/origin/main → main
		return filepath.Base(out), nil
	}

	// Fallback: check if main or master exists
	if _, err := run(root, "rev-parse", "--verify", "refs/heads/main"); err == nil {
		return "main", nil
	}
	if _, err := run(root, "rev-parse", "--verify", "refs/heads/master"); err == nil {
		return "master", nil
	}

	return "", fmt.Errorf("cannot determine default branch")
}

// MergeBranch merges source into target. On conflict it aborts and returns an error.
func MergeBranch(root, source, target string) error {
	if _, err := run(root, "checkout", target); err != nil {
		return fmt.Errorf("checkout %s: %w", target, err)
	}

	if _, err := run(root, "merge", source); err != nil {
		// Abort the failed merge
		_, _ = run(root, "merge", "--abort")
		return fmt.Errorf("merge %s into %s: %w", source, target, err)
	}

	return nil
}

// DeleteBranch deletes a fully-merged branch. Refuses unmerged branches.
func DeleteBranch(root, branch string) error {
	_, err := run(root, "branch", "-d", branch)
	return err
}

// PushNewBranch pushes a local branch to origin and sets up upstream tracking.
// Equivalent to: git push -u origin <branch>
func PushNewBranch(root, branch string) error {
	_, err := run(root, "push", "-u", "origin", branch)
	return err
}

// ListRemoteBranches returns branch names from origin, sorted by most recent
// commit first. Each entry is the short name (e.g. "main", "develop").
func ListRemoteBranches(root string) ([]string, error) {
	out, err := run(root, "branch", "-r", "--sort=-committerdate", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip HEAD pointer (origin/HEAD -> origin/main)
		if strings.Contains(line, "->") {
			continue
		}
		// Strip "origin/" prefix
		if strings.HasPrefix(line, "origin/") {
			line = strings.TrimPrefix(line, "origin/")
		}
		branches = append(branches, line)
	}
	return branches, nil
}
