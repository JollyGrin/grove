package state

import (
	"time"

	"github.com/JollyGrin/grove/internal/tmux"
)

const (
	// setupTimeout is how long a task may sit in setup with no claude in
	// its pane before Resolve calls the setup failed.
	setupTimeout = 10 * time.Minute
	// answerDeliveryGrace is how long after a `gv answer` the
	// prompt_submitted hook gets to confirm the paste landed.
	answerDeliveryGrace = 15 * time.Second
)

// Resolved states.
const (
	StateSetup       = "setup"
	StateSetupFailed = "setup_failed"
	StateWorking     = "working"
	StateWaiting     = "waiting"
	StateBlocked     = "blocked"
	StateIdle        = "idle"
	StateErrored     = "errored"
	StateDead        = "dead"
	StatePaused      = "paused"
	StateDone        = "done"
)

// Resolved sources: why Resolve believes its answer.
const (
	SourceHook    = "hook"
	SourceClaude  = "claude"
	SourcePane    = "pane"
	SourceTimeout = "timeout"
)

// Signals is everything observed about one worker this beat, outside the
// event log. Named Signals because supervise.Observation already exists
// and will carry one.
type Signals struct {
	WindowExists bool
	PaneCommand  string        // from the existing tmux snapshot; "" = unknown
	Claude       *ClaudeStatus // nil = no session file for this pane
	PaneError    string        // ErrorMarker kind, only when a fallback capture ran
}

// ClaudeStatus is Claude Code's own <config dir>/sessions/<pid>.json.
type ClaudeStatus struct {
	Status     string // busy | idle | waiting
	WaitingFor string
	UpdatedAt  time.Time
	SessionID  string
}

// Resolved is the one state shown everywhere.
type Resolved struct {
	State  string // see State* constants
	Detail string // "permission prompt", "rate_limit", "done ✓", "stalled?", …
	Source string // hook | claude | pane | timeout
}

// Detail values the label renderer keys on.
const (
	detailQuestion    = "question"
	detailDone        = "done ✓"
	detailDoneUnverif = "done?"
	detailStalled     = "stalled?"
	detailInterrupted = "interrupted"
	detailNotDeliver  = "answer not delivered?"
	detailStuck       = "stuck?"
)

// Resolve folds everything known about one worker into the one state
// shown everywhere. Pure: no I/O, no clock reads, no allocation beyond
// the returned value. A zero now disables the time-based rules.
func Resolve(t *Task, s Signals, now time.Time) Resolved {
	// 1. Done / paused / handed off.
	switch {
	case t.Done:
		return Resolved{State: StateDone, Source: SourceHook}
	case t.Paused:
		return Resolved{State: StatePaused, Source: SourceHook}
	}
	// 2. Window gone, or the pane no longer runs claude after it started.
	if !s.WindowExists {
		return Resolved{State: StateDead, Detail: "window gone", Source: SourcePane}
	}
	paneKnown := s.PaneCommand != ""
	paneClaude := paneKnown && tmux.Claudeish(s.PaneCommand)
	if t.SessionStarted && paneKnown && !paneClaude {
		return Resolved{State: StateDead, Detail: "claude exited", Source: SourcePane}
	}
	// 3. Setup that failed or never finished.
	if t.Agent == AgentSetup {
		if t.SetupFailed {
			return Resolved{State: StateSetupFailed, Detail: t.SetupError, Source: SourceHook}
		}
		if !now.IsZero() && !t.LiveSince.IsZero() && !paneClaude && now.Sub(t.LiveSince) > setupTimeout {
			return Resolved{State: StateSetupFailed, Detail: detailStuck, Source: SourceTimeout}
		}
	}
	// 4. A turn that ended in an API error.
	if !t.StopFailedAt.IsZero() && t.StopFailedAt.After(t.PromptAt) {
		return Resolved{State: StateErrored, Detail: t.StopError, Source: SourceHook}
	}
	// 5. Claude's own session file wins for busy/idle/waiting.
	if c := s.Claude; c != nil && t.SessionID != "" && c.SessionID == t.SessionID {
		switch c.Status {
		case "busy":
			return Resolved{State: StateWorking, Source: SourceClaude}
		case "waiting":
			return Resolved{State: StateWaiting, Detail: c.WaitingFor, Source: SourceClaude}
		case "idle":
			if t.Agent == AgentWorking {
				if !t.AnsweredAt.IsZero() && !t.PromptAt.After(t.AnsweredAt) &&
					!now.IsZero() && now.Sub(t.AnsweredAt) > answerDeliveryGrace {
					return Resolved{State: StateIdle, Detail: detailNotDeliver, Source: SourceClaude}
				}
				return Resolved{State: StateIdle, Detail: detailInterrupted, Source: SourceClaude}
			}
		}
	}
	// 6. Today's hook fold (the pane fallback feeds PaneError).
	if t.Agent == AgentWorking {
		if s.PaneError != "" {
			return Resolved{State: StateErrored, Detail: s.PaneError, Source: SourcePane}
		}
		if !t.WaitingSince.IsZero() {
			return Resolved{State: StateWaiting, Detail: "permission prompt", Source: SourceHook}
		}
	}
	switch t.Agent {
	case AgentWaiting:
		return Resolved{State: StateWaiting, Detail: detailQuestion, Source: SourceHook}
	case AgentBlocked:
		return Resolved{State: StateBlocked, Source: SourceHook}
	case AgentDead:
		return Resolved{State: StateDead, Source: SourceHook}
	case AgentIdle:
		d := detailStalled
		switch t.Sentinel {
		case "done":
			d = detailDone
		case "done_unverified":
			d = detailDoneUnverif
		}
		return Resolved{State: StateIdle, Detail: d, Source: SourceHook}
	}
	return Resolved{State: t.Agent, Source: SourceHook}
}

// Label renders r as the table-row string. Hook-derived states keep
// today's vocabulary byte for byte; newer states add a parenthesised
// detail.
func (r Resolved) Label() string {
	switch r.State {
	case StateWaiting:
		switch r.Detail {
		case detailQuestion:
			return "QUESTION"
		case "":
			return "waiting"
		}
		return "waiting (" + r.Detail + ")"
	case StateBlocked:
		return "BLOCKED"
	case StateSetupFailed:
		if r.Detail == "" {
			return "setup failed"
		}
		return "setup failed (" + r.Detail + ")"
	case StateErrored:
		if r.Detail == "" {
			return "errored"
		}
		return "errored (" + r.Detail + ")"
	case StateIdle:
		switch r.Detail {
		case detailDone:
			return "idle ✓"
		case detailDoneUnverif, detailStalled:
			return r.Detail
		}
		return "idle (" + r.Detail + ")"
	}
	return r.State
}
