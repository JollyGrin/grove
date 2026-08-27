// Routing (grove-191): the registry is the table that makes the global
// layer workspace-transparent. Ticket-addressed verbs that miss the
// global state scan it for the owning workspace and re-exec themselves
// there; `gv ls` aggregates every alive entry. Kept beside the registry
// it reads so the ownership rule (alive registered workspaces, registry
// order) has one home.
package workspace

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JollyGrin/grove/internal/provider"
	"github.com/JollyGrin/grove/internal/state"
)

// shortRefRe matches bare github issue refs (7, #7) — the numeric-suffix
// fallback `gv done 7` uses (same shape as cmd/gv's shortRefRe).
var shortRefRe = regexp.MustCompile(`^#?(\d+)$`)

// stateDir is the state path for a workspace root, creation-free: unlike
// config.StateDirAt it never MkdirAlls, so read-only scans leave no empty
// .grove/state/ behind in a workspace that has never run a verb. The
// GROVE_STATE_DIR override wins for both shapes (test isolation).
func stateDir(root string) string {
	if override := os.Getenv("GROVE_STATE_DIR"); override != "" {
		return override
	}
	return filepath.Join(root, ".grove", "state")
}

// FindTicket returns the alive registered workspaces whose task state
// tracks the referenced ticket, in the given registry order. ref resolves
// the way findTask does: provider.IDCandidates normalizations first, then
// a bare numeric suffix (#7 matches a ticket ending -7). includeDone
// widens the match to done/untracked tasks — adopt revives those, the
// active-task verbs must not route to them. Read-only throughout: the
// scan folds each root's events.jsonl via state.Peek (fresher than the
// derived tasks.json, which only refreshes when a verb runs inside that
// workspace) and never writes; dead roots and unreadable state disclaim
// ownership instead of erroring — routing is a fallback, never a gate.
func FindTicket(list []Workspace, ref string, includeDone bool) []Workspace {
	cands := provider.IDCandidates(ref)
	numeric := ""
	if m := shortRefRe.FindStringSubmatch(strings.TrimSpace(ref)); m != nil {
		numeric = m[1]
	}
	owns := func(tasks map[string]*state.Task) bool {
		for id, t := range tasks {
			if t == nil || (!includeDone && t.Done) {
				continue
			}
			if numeric != "" && strings.HasSuffix(id, "-"+numeric) {
				return true
			}
			for _, c := range cands {
				if id == c {
					return true
				}
			}
		}
		return false
	}
	var owners []Workspace
	for _, ws := range list {
		if !Alive(ws) {
			continue
		}
		if tasks, err := state.Peek(stateDir(ws.Root)); err == nil && owns(tasks) {
			owners = append(owners, ws)
		}
	}
	return owners
}
