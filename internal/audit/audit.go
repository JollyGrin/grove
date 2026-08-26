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
	Paused       Class = "paused"
	Idle         Class = "idle"
	Disconnected Class = "disconnected"
	Abandoned    Class = "abandoned"
	Drifted      Class = "drifted"
	// HandedOff (grove-178) is a forwarding tombstone: the task was moved to
	// another grove host by gv handoff. Report-only — never abandoned, no
	// probes; the live row is on the other host (`gv ls --remote`).
	HandedOff Class = "handed_off"
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
	Sentinel       string        `json:"sentinel,omitempty"` // last STATUS sentinel (question | blocked | done | "")
	Parked         bool          `json:"parked"`             // workspace parked (grove-33) — resumable, never abandoned
	Paused         bool          `json:"paused"`             // task paused via gv pause (grove-90) — a bookmark, never abandoned
	Age            time.Duration `json:"-"`                  // since the task's last update
}

// Classify maps facts to a class. Precedence: merged > drifted > paused >
// abandoned > disconnected > idle > healthy. Abandoned requires a
// definitive answer — a CLOSED PR, or provably no PR plus a dead/idle
// agent past staleAfter; a failed PR lookup degrades to
// disconnected/healthy. A parked task (grove-33) is deliberately
// resumable: its dead window and staleness never make it Abandoned, so it
// drops to Disconnected ("gv adopt") and is never offered for untrack
// --rm. A paused task (grove-90, `gv pause`) is a per-ticket bookmark: it
// classifies Paused ahead of abandoned and disconnected — no matter how
// stale or dead it looks, a paused task must never fall through to those
// (a worktree deleted out from under it still reads Drifted, the honest
// verdict; never abandoned). Idle (grove-91) is the finished-but-burning
// shape: window alive, agent done (STATUS done sentinel) or waiting on a
// human, and quiet past idleAfter — the worker is holding a CPU for
// nothing, so audit suggests `gv pause`. It requires a live window by
// construction (a dead window already read Disconnected above), and a
// working agent is never idle however quiet it looks — a silent working
// agent is the stuck shape, which the cost flag owns.
func Classify(f Facts, staleAfter, idleAfter time.Duration) Class {
	if f.PRKnown && f.PRState == "MERGED" {
		return Merged
	}
	if !f.WorktreeExists {
		return Drifted
	}
	if f.Paused {
		return Paused
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
	finished := (f.Agent == state.AgentIdle && f.Sentinel == "done") || f.Agent == state.AgentWaiting
	if finished && f.Age > idleAfter {
		return Idle
	}
	return Healthy
}

// Suggestion is the one command the audit table prints per class.
func Suggestion(c Class) string {
	switch c {
	case Merged:
		return "gv done"
	case Paused:
		return "gv adopt"
	case Idle:
		return "gv pause"
	case Disconnected:
		return "gv adopt"
	case Abandoned:
		return "gv untrack --rm"
	case Drifted:
		return "gv adopt (or gv untrack)"
	case HandedOff:
		return "gv ls --remote"
	default:
		return ""
	}
}
