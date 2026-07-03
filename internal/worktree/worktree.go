// Package worktree provides git worktree CRUD operations.
package worktree

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JollyGrin/grove/internal/git"
)

// Worktree represents a single git worktree with status info.
type Worktree struct {
	Name   string // filepath.Base(Path)
	Path   string // absolute path
	Branch string
	IsMain bool // first entry from git worktree list
	Ahead  int
	Behind int
	Dirty  bool
}

// List returns all worktrees for the given repo root, enriched with status.
func List(repoRoot string) ([]Worktree, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git worktree list: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}

	wts := parseWorktreeList(stdout.String())

	// Enrich with status
	for i := range wts {
		if i == 0 {
			wts[i].IsMain = true
		}

		ahead, behind, err := git.BranchStatus(wts[i].Path)
		if err == nil {
			wts[i].Ahead = ahead
			wts[i].Behind = behind
		}

		dirty, err := git.IsDirty(wts[i].Path)
		if err == nil {
			wts[i].Dirty = dirty
		}
	}

	return wts, nil
}

// parseWorktreeList parses `git worktree list --porcelain` output.
func parseWorktreeList(output string) []Worktree {
	var wts []Worktree
	var current Worktree

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(line, "worktree "):
			current = Worktree{
				Path: strings.TrimPrefix(line, "worktree "),
			}
			current.Name = filepath.Base(current.Path)

		case strings.HasPrefix(line, "branch "):
			// branch refs/heads/main → main
			ref := strings.TrimPrefix(line, "branch ")
			current.Branch = filepath.Base(ref)

		case line == "":
			if current.Path != "" {
				wts = append(wts, current)
				current = Worktree{}
			}
		}
	}

	// Handle last entry if no trailing newline
	if current.Path != "" {
		wts = append(wts, current)
	}

	return wts
}

// Add creates a new worktree with a new branch based on baseBranch.
func Add(repoRoot, name, baseBranch string) (Worktree, error) {
	wtPath := DefaultPath(repoRoot, name)

	cmd := exec.Command("git", "worktree", "add", "-b", name, wtPath, baseBranch)
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Worktree{}, fmt.Errorf("git worktree add: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}

	// Push the new branch and set up tracking so `git push` works immediately.
	if err := git.PushNewBranch(wtPath, name); err != nil {
		// Non-fatal: worktree is usable, just needs manual push -u later.
		// This can fail if offline or if origin doesn't accept the push.
		fmt.Fprintf(os.Stderr, "warning: could not push branch %s: %v\n", name, err)
	}

	return Worktree{
		Name:   name,
		Path:   wtPath,
		Branch: name,
	}, nil
}

// Remove deletes a worktree. Does NOT use --force — fails on dirty worktrees.
func Remove(repoRoot, path string) error {
	cmd := exec.Command("git", "worktree", "remove", path)
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git worktree remove: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// DefaultPath returns the standard worktree location:
// <parent>/.worktrees/<reponame>/<name> (outside the repo).
func DefaultPath(repoRoot, name string) string {
	parent := filepath.Dir(repoRoot)
	repoName := filepath.Base(repoRoot)
	return filepath.Join(parent, ".worktrees", repoName, name)
}

// FindByName returns the first worktree matching name, or nil.
func FindByName(wts []Worktree, name string) *Worktree {
	for i := range wts {
		if wts[i].Name == name {
			return &wts[i]
		}
	}
	return nil
}
