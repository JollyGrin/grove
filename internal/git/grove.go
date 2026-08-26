// Grove additions to the git wrapper (the ovs.go convention, continued):
// helpers the generalized grab/done paths need. New code goes here, not in
// the byte-comparable upstream files.
package git

import (
	"fmt"
	"strings"
)

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

// AheadCommits lists (oneline) the commits on path's HEAD that are not
// on ref — empty means HEAD is an ancestor of (or equal to) ref, so a
// hard reset to ref discards nothing. `gv adopt --sync` refuses when
// this is non-empty: the dirty guard can't see committed-but-unpushed
// work, and reset --hard would silently drop it.
func AheadCommits(path, ref string) ([]string, error) {
	out, err := run(path, "log", "--oneline", ref+"..HEAD")
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// ResetHard force-resets a worktree (checkout + its branch ref) to ref.
// The handoff pickup path (`gv adopt --sync`): another host worked the
// branch, so a surviving local checkout or branch ref is stale until
// reset to origin. Callers guard against uncommitted changes first.
func ResetHard(path, ref string) error {
	_, err := run(path, "reset", "--hard", ref)
	return err
}

// Head returns the full sha of HEAD in path (a worktree or repo root).
func Head(path string) (string, error) {
	return run(path, "rev-parse", "HEAD")
}

// RemoteHead asks origin directly (ls-remote, no local ref cache) for the
// sha of refs/heads/<branch>; "" when the branch is not on origin. Used by
// `gv handoff` to prove local == remote before another host adopts it.
func RemoteHead(root, branch string) (string, error) {
	out, err := run(root, "ls-remote", "origin", "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	if out == "" {
		return "", nil
	}
	return strings.Fields(out)[0], nil
}
