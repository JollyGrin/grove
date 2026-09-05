// Package supervise is the pure transition engine behind DESIGN.md
// principle 4: "the supervisor loop (grab → poll PR/CI → classify STATUS →
// gate) is fixed and enumerable, so it is code." Grove only ever built the
// hook half (agent_status via the Stop hook); this package derives the
// other two dimensions a poller needs — delivery (PR opened/CI
// failing/conflicting/ready/merged) and liveness the hooks cannot see (an
// AskUserQuestion menu, a bare shell after a silent death, a 429 plan cap,
// a sleep-cut turn).
//
// Transitions is pure: given one poll's Observation and the caller's
// Memory, it returns the events.jsonl records to append — it never appends
// them itself, never runs a goroutine or a timer, never touches tmux or gh.
// The poller that drives it (grove-253) owns all of that.
//
// Transitions, never observations: an event is emitted only when the
// derived state differs from the task's folded Delivery/Liveness state, so
// two pollers (or one restarting) re-emit nothing.
package supervise

import (
	"strconv"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
)

// Observation is one poll's snapshot for one task.
type Observation struct {
	Task *state.Task // folded, carries last-known Delivery/Liveness
	// PR is the current PR lookup result; nil = lookup ok, no PR exists.
	PR *github.PR
	// PRKnown is false when the lookup itself failed or timed out — the
	// delivery dimension must emit nothing and change nothing in that
	// case, never mistake "unknown" for "no PR".
	PRKnown bool
	// Live is the pane read (DetectLiveFrom); PaneContent is the bottom-30
	// capture the liveness checks scan.
	Live detect.LiveInfo
	Now  time.Time
}

// Memory is the caller-owned hysteresis: first-seen timestamps per ticket
// for the waiting/vanished debounce windows. In-process only, NEVER
// persisted — a restart simply re-arms the timers, delaying the next
// transition by the hysteresis window rather than ever losing one.
type Memory struct {
	waitingSince   map[string]time.Time
	notClaudeSince map[string]time.Time
}

// NewMemory returns an empty, ready-to-use Memory. A caller may also pass a
// zero-value &Memory{} — Transitions lazily initializes it.
func NewMemory() *Memory {
	return &Memory{waitingSince: map[string]time.Time{}, notClaudeSince: map[string]time.Time{}}
}

func (m *Memory) init() {
	if m.waitingSince == nil {
		m.waitingSince = map[string]time.Time{}
	}
	if m.notClaudeSince == nil {
		m.notClaudeSince = map[string]time.Time{}
	}
}

// forget drops a ticket's hysteresis timers — called whenever the ticket
// leaves liveness scope (done/paused/handed-off/setup) so a later re-entry
// starts its debounce window fresh instead of resuming a stale one.
func (m *Memory) forget(ticket string) {
	delete(m.waitingSince, ticket)
	delete(m.notClaudeSince, ticket)
}

// Transitions derives the events.jsonl records for one Observation.
func Transitions(obs Observation, mem *Memory) []state.Event {
	if obs.Task == nil || mem == nil {
		return nil
	}
	mem.init()
	var evs []state.Event
	evs = append(evs, deliveryTransitions(obs)...)
	evs = append(evs, livenessTransitions(obs, mem)...)
	return evs
}

func mkEvent(evType, ticket string, at time.Time, data map[string]string) state.Event {
	return state.Event{Time: at, Type: evType, Ticket: ticket, Data: data}
}

// --- Delivery ---------------------------------------------------------

func deliveryTransitions(obs Observation) []state.Event {
	if !obs.PRKnown {
		return nil
	}
	t := obs.Task
	prev := state.DeliveryNone
	if t.Delivery != nil {
		prev = t.Delivery.State
	}
	next, evType, data := deriveDelivery(obs.PR, prev)
	if next == prev {
		return nil
	}
	return []state.Event{mkEvent(evType, t.Ticket, obs.Now, data)}
}

// deriveDelivery computes the new Delivery state, the event type to emit,
// and that event's data payload (docs/plugins.md's per-type table). The
// event type is normally the new state's name, except re-entering `opened`
// from anything but none/closed emits `pr_updated` — a fresh push put
// checks back to pending, not a fresh PR — while closed→opened (a reopen)
// still emits `pr_opened`.
func deriveDelivery(pr *github.PR, prev string) (next, evType string, data map[string]string) {
	if pr == nil {
		// No PR known: nothing to derive. In practice this only happens
		// when prev is already none (gh's --state all means an
		// once-opened PR is never un-returned) — returning prev keeps that
		// a guaranteed no-op rather than fabricating a bogus transition.
		return prev, "", nil
	}

	conflicting := pr.State == "OPEN" && (pr.Mergeable == "CONFLICTING" || pr.MergeState == "DIRTY")
	switch {
	case pr.State == "MERGED":
		next = state.DeliveryMerged
	case pr.State == "CLOSED":
		next = state.DeliveryClosed
	case conflicting:
		next = state.DeliveryConflicting
	case pr.CI == "fail":
		next = state.DeliveryCIFailed
	case !pr.Draft && pr.CI == "pass":
		// BLOCKED/BEHIND still count as ready: the human is the missing
		// review, and behind-base is reported via `behind`, not gating.
		next = state.DeliveryReady
	default:
		// Checks pending/none, or draft.
		next = state.DeliveryOpened
	}

	num := strconv.Itoa(pr.Number)
	switch next {
	case state.DeliveryOpened:
		evType = state.EvPRUpdated
		if prev == state.DeliveryNone || prev == state.DeliveryClosed {
			evType = state.EvPROpened
		}
		data = map[string]string{"pr": num}
		if evType == state.EvPROpened {
			data["url"] = pr.URL
			data["draft"] = strconv.FormatBool(pr.Draft)
		}
	case state.DeliveryCIFailed:
		evType = state.EvPRCIFailed
		data = map[string]string{"pr": num, "failing": strings.Join(pr.Failing, ",")}
	case state.DeliveryConflicting:
		evType = state.EvPRConflicting
		data = map[string]string{"pr": num, "merge_state": pr.MergeState}
	case state.DeliveryReady:
		evType = state.EvPRReady
		data = map[string]string{"pr": num, "url": pr.URL, "merge_state": pr.MergeState}
		if pr.MergeState == "BEHIND" {
			data["behind"] = "true"
		}
	case state.DeliveryMerged:
		evType = state.EvPRMerged
		data = map[string]string{"pr": num}
	case state.DeliveryClosed:
		evType = state.EvPRClosed
		data = map[string]string{"pr": num}
	}
	return next, evType, data
}

// --- Liveness -----------------------------------------------------------

const (
	waitingHysteresis  = 10 * time.Second
	vanishedHysteresis = 60 * time.Second
	bootGrace          = 120 * time.Second
)

// livenessTransitions derives worker_waiting/vanished/errored/recovered.
// Only for tasks that are not done, not paused, not handed off, and whose
// agent has left setup — a booting or already-finished task is out of
// scope by definition.
func livenessTransitions(obs Observation, mem *Memory) []state.Event {
	t := obs.Task
	if t.Done || t.Paused || t.HandedOffTo != "" || t.Agent == state.AgentSetup {
		mem.forget(t.Ticket)
		return nil
	}

	prev := state.LivenessOK
	if t.Liveness != nil {
		prev = t.Liveness.State
	}

	// errored — immediate, no hysteresis: a marker in the pane capture
	// means the turn is already dead, waiting out a debounce only delays
	// the alert.
	if reason, line, ok := detectErrorMarker(obs.Live.PaneContent); ok {
		mem.forget(t.Ticket)
		if prev == state.LivenessErrored {
			return nil
		}
		return []state.Event{mkEvent(state.EvWorkerErrored, t.Ticket, obs.Now,
			map[string]string{"reason": reason, "line": line})}
	}

	if !obs.Live.Exists {
		// No window at all is out of scope here (audit's job); leave
		// state as last observed rather than guess.
		return nil
	}

	if obs.Live.Status == detect.StatusWaiting {
		delete(mem.notClaudeSince, t.Ticket)
		since, seen := mem.waitingSince[t.Ticket]
		if !seen {
			since = obs.Now
			mem.waitingSince[t.Ticket] = since
		}
		if obs.Now.Sub(since) < waitingHysteresis {
			return nil // blip — not yet continuous long enough
		}
		if prev == state.LivenessWaiting {
			return nil
		}
		return []state.Event{mkEvent(state.EvWorkerWaiting, t.Ticket, obs.Now,
			map[string]string{"marker": detect.WaitingMarker(obs.Live.PaneContent)})}
	}
	delete(mem.waitingSince, t.Ticket)

	if !obs.Live.HasClaude && obs.Live.Status == detect.StatusUnknown {
		since, seen := mem.notClaudeSince[t.Ticket]
		if !seen {
			since = obs.Now
			mem.notClaudeSince[t.Ticket] = since
		}
		grace := t.LiveSince.IsZero() || obs.Now.Sub(t.LiveSince) >= bootGrace
		if obs.Now.Sub(since) < vanishedHysteresis || !grace {
			return nil
		}
		if prev == state.LivenessVanished {
			return nil
		}
		return []state.Event{mkEvent(state.EvWorkerVanished, t.Ticket, obs.Now, nil)}
	}
	delete(mem.notClaudeSince, t.Ticket)

	// ok — anything else with claude present.
	if prev != state.LivenessOK {
		return []state.Event{mkEvent(state.EvWorkerRecovered, t.Ticket, obs.Now,
			map[string]string{"from": prev})}
	}
	return nil
}

// detectErrorMarker scans the pane capture for the markers that mean the
// turn already died silently — checked line by line so the reported `line`
// is the specific matched line, not the whole capture.
func detectErrorMarker(pane string) (reason, line string, ok bool) {
	for l := range strings.SplitSeq(pane, "\n") {
		switch {
		case strings.Contains(l, "Usage limit reached"), strings.Contains(l, "Request rejected (429)"):
			return "usage_limit", truncateRunes(l), true
		case strings.Contains(l, "computer went to sleep"):
			return "sleep", truncateRunes(l), true
		case strings.Contains(l, "API Error:"):
			return "api_error", truncateRunes(l), true
		}
	}
	return "", "", false
}

// truncateRunes caps the matched line at 200 runes, rune-safe (never
// mid-codepoint — the grove-131 class of bug).
func truncateRunes(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 200 {
		r = r[:200]
	}
	return string(r)
}
