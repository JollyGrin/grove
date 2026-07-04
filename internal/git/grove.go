// Grove additions to the git wrapper (the ovs.go convention, continued):
// helpers the generalized grab/done paths need. New code goes here, not in
// the byte-comparable upstream files.
package git

import "fmt"

// HasRemote reports whether the repo has the named remote configured.
func HasRemote(root, name string) bool {
	_, err := run(root, "remote", "get-url", name)
	return err == nil
}

// BaseRef resolves the ref a new task worktree branches from: origin/<base>
// when the remote ref exists, else the local branch — the degraded
// no-remote path (DESIGN.md §5.2). The caller should git.Fetch first when a
// remote is present.
func BaseRef(root, base string) (string, error) {
	if _, err := run(root, "rev-parse", "--verify", "--quiet", "origin/"+base); err == nil {
		return "origin/" + base, nil
	}
	if LocalBranchExists(root, base) {
		return base, nil
	}
	return "", fmt.Errorf("base branch %q not found (neither origin/%s nor a local branch) — check the repo's `base:` in config", base, base)
}
