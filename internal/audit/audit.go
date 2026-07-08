// Package audit reconciles gv's tracked state against reality (worktree
// on disk, branches, tmux window, PR state) and classifies each task.
// Pure read — nothing here mutates git, tmux, or the event log. gv only
// audits what it created; removing untracked worktrees stays with the
// human (dev-core:cleanup-local-state is the cross-repo remover).
package audit

import (
	"time"

	"github.com/JollyGrin/grove/internal/state"
)

// Class is the audit verdict for one tracked task.
type Class string

const (
	Healthy      Class = "healthy"
	Merged       Class = "merged"
	Disconnected Class = "disconnected"
	Abandoned    Class = "abandoned"
	Drifted      Class = "drifted"
)

func (c Class) String() string { return string(c) }

// Facts is everything Classify needs, gathered by the caller so the
// classification itself stays pure and table-testable.
type Facts struct {
	WorktreeExists bool          `json:"worktree_exists"`
	LocalBranch    bool          `json:"local_branch"`
	RemoteBranch   bool          `json:"remote_branch"`
	WindowAlive    bool          `json:"window_alive"`
	HasSessionID   bool          `json:"has_session_id"`
	PRKnown        bool          `json:"pr_known"` // false = lookup failed; never enough to call abandoned
	PRState        string        `json:"pr_state"` // OPEN | MERGED | CLOSED | "" (none)
	Agent          string        `json:"agent"`
	Parked         bool          `json:"parked"` // workspace parked (grove-33) — resumable, never abandoned
	Age            time.Duration `json:"-"`      // since the task's last update
}

// Classify maps facts to a class. Precedence: merged > drifted >
// abandoned > disconnected > healthy. Abandoned requires a definitive
// answer — a CLOSED PR, or provably no PR plus a dead/idle agent past
// staleAfter; a failed PR lookup degrades to disconnected/healthy. A
// parked task (grove-33) is deliberately resumable: its dead window and
// staleness never make it Abandoned, so it drops to Disconnected ("gv
// adopt") and is never offered for untrack --rm.
func Classify(f Facts, staleAfter time.Duration) Class {
	if f.PRKnown && f.PRState == "MERGED" {
		return Merged
	}
	if !f.WorktreeExists {
		return Drifted
	}
	if f.PRKnown && f.PRState == "CLOSED" {
		return Abandoned
	}
	agentGone := f.Agent == state.AgentDead || f.Agent == state.AgentIdle
	if f.PRKnown && f.PRState == "" && agentGone && f.Age > staleAfter && !f.Parked {
		return Abandoned
	}
	if !f.WindowAlive {
		return Disconnected
	}
	return Healthy
}

// Suggestion is the one command the audit table prints per class.
func Suggestion(c Class) string {
	switch c {
	case Merged:
		return "gv done"
	case Disconnected:
		return "gv adopt"
	case Abandoned:
		return "gv untrack --rm"
	case Drifted:
		return "gv adopt (or gv untrack)"
	default:
		return ""
	}
}
