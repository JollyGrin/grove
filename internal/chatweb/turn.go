package chatweb

// grove-300: an honest(er) turn state for the phone's "working…" strip.
//
// The strip used to be read off the shape of the transcript alone —
// anything after the operator's message that is not the agent's prose
// means "still going" — and that lies forever in the two cases that matter
// most: a message nobody ever answered (the pane died, the send landed in
// a modal) and a turn that died mid-tool (usage limit, API error, a
// sleep-cut). Nothing in the transcript can end either lie, because Claude
// Code writes nothing at end-of-turn beyond the final prose.
//
// Why the pane and not a hook: the Stop hook does not fire in either
// failure case — a dead pane fires nothing, and an API error fires
// StopFailure (2.1.283), which grove does not install — and orchestrator
// chats are not tracked tasks, so a hook stamp would need new state and a
// `gv hooks install` on every host. The pane is already read once a second
// for the picker, on every host that serves the phone.
//
// What it is NOT: a completion signal (grove-205). Turn only ever takes the
// "working…" claim AWAY or confirms it; it never marks anything done, and
// the composer never looks at it.

import (
	"regexp"
	"strings"

	"github.com/JollyGrin/grove/internal/detect"
)

// Turn states. Additive contract (the `turn` SSE event): a client that does
// not know a state treats it as "unknown" and keeps its own heuristic.
const (
	TurnRunning = "running" // the live spinner is on screen
	TurnIdle    = "idle"    // claude is at its prompt, no spinner
	TurnWaiting = "waiting" // a modal is up — the picker event carries it
	TurnErrored = "errored" // a death marker sits at the bottom of the pane
	TurnStopped = "stopped" // no pane, or no claude process in it
	TurnUnknown = "unknown" // the capture says nothing either way
)

// Turn is what a pane read says about the chat's current turn.
type Turn struct {
	State string `json:"state"`
	// Reason is errored's cause: usage_limit, sleep or api_error — the
	// same names the supervisor's worker_errored carries.
	Reason string `json:"reason,omitempty"`
	// Line is the matched error line, so the phone can say what happened.
	Line string `json:"line,omitempty"`
}

// errorTail is how far up from the bottom a death marker still counts.
// Claude Code prints the error just above its input box; one further up is
// scrollback from a turn the operator has since moved past.
const errorTail = 15

// ClassifyTurn reads one pane capture. alive is whether the pane exists
// AND runs a claude process (chat.Busy) — that is the only thing busy is
// good for: it says "claude is here", never "claude is mid-turn".
//
// Order matters: a live spinner outranks an error line still on screen
// (the operator retried and the new turn is running), and a modal outranks
// both (the turn is paused on the operator, and the picker row says so).
func ClassifyTurn(capture string, alive bool) Turn {
	if !alive {
		return Turn{State: TurnStopped}
	}
	status, hasClaude := detect.Classify(typedRe.ReplaceAllString(capture, "❯ "))
	switch {
	case detect.Spinning(capture) || status == detect.StatusBusy:
		return Turn{State: TurnRunning}
	case status == detect.StatusWaiting:
		return Turn{State: TurnWaiting}
	}
	if reason, line, ok := detect.ErrorMarker(bottomLines(capture, errorTail)); ok {
		return Turn{State: TurnErrored, Reason: reason, Line: line}
	}
	if status == detect.StatusIdle && hasClaude {
		return Turn{State: TurnIdle}
	}
	return Turn{State: TurnUnknown}
}

// typedRe is the input box with something typed in it — the operator's
// own words, which the classifier would otherwise read as chrome: a
// half-typed "…esc to interrupt…" in the box reads as a busy pane. A
// modal's selected option ("❯ 1. Yes") starts with a digit and is kept.
// Claude Code 2.1.283 puts a NO-BREAK SPACE after the caret, not a space.
var typedRe = regexp.MustCompile(`(?m)^❯[ \t\x{a0}]+[^1-9 \t\x{a0}].*$`)

// bottomLines is the last n non-trailing-blank lines of a capture.
func bottomLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n \t"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// settled reports whether the stream may tell the phone about t yet.
// Running and errored go out at once — one is the spinner itself, the other
// a marker that only appears once the turn is already dead. Every other
// state must hold for turnSettle consecutive polls first: the gap between
// Enter and the first spinner frame reads idle, and a single idle frame
// must not flip the phone to "no reply".
func (t Turn) settled(polls int) bool {
	return t.State == TurnRunning || t.State == TurnErrored || polls >= turnSettle
}

// turnSettle is how many consecutive identical polls a quiet state needs.
const turnSettle = 3
