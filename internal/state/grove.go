// Grove additions to the copied state package: read-side helpers for the
// activity feed. New code goes here, not in the byte-comparable state.go.
package state

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// EvOrchestratorClosed records a fire-and-forget orchestrator chat
// dismissing its own pane after a clean dispatch. Activity-feed only: it
// carries an empty Event.Ticket (the dispatched ticket rides in Data so the
// derived task view is never touched), so fold ignores it for free — no
// change to the byte-comparable state.go.
const EvOrchestratorClosed = "orchestrator_closed"

// EvWorkspaceParked records the operator parking a workspace: the shared
// grove-<label> tmux session (cockpit + orchestrator + every worker) is
// killed to free memory, with all state left on disk so bare `gv` +
// `gv adopt` resume it. Like EvOrchestratorClosed it is a workspace-level,
// ticket-less event that fold ignores — so state.go stays byte-comparable
// with ovs. The parked-ness of individual tasks is derived from the log by
// ParkedTickets, not stored on Task.
const EvWorkspaceParked = "workspace_parked"

// Version returns an event record's plugin-contract version
// (docs/plugins.md). Records written before the v field existed
// (pre-grove-75) carry no stamp and read as v1.
func (e Event) Version() int {
	if e.V == 0 {
		return 1
	}
	return e.V
}

// ParkedTickets derives, from the whole event log (oldest-first), the set of
// tickets that are currently parked: a workspace_parked event marks every
// then-active task, and any later revival of that ticket — a session start,
// an adopt, or a fresh grab reusing the id — clears the mark. A task leaving
// Active() (done / untracked) drops out entirely. Read-only; audit uses it
// so a parked task is never mistaken for abandoned.
func ParkedTickets(events []Event) map[string]bool {
	parked := map[string]bool{}
	known := map[string]bool{} // active (non-done) tickets seen so far
	for _, ev := range events {
		switch ev.Type {
		case EvWorkspaceParked:
			for tk := range known {
				parked[tk] = true
			}
		case EvTaskCreated, EvTaskAdopted, EvSessionStarted:
			if ev.Ticket != "" {
				known[ev.Ticket] = true
				parked[ev.Ticket] = false
			}
		case EvTaskDone, EvTaskUntracked:
			delete(known, ev.Ticket)
			delete(parked, ev.Ticket)
		}
	}
	return parked
}

// ReadTasks is the read-only counterpart of Load: it parses the derived
// tasks.json view without folding events, creating directories, or
// rewriting anything. Hook receivers call this once per candidate
// workspace on every live turn — a missing or corrupt file is an empty
// fleet, never an error and never a write.
func ReadTasks(stateDir string) map[string]*Task {
	raw, err := os.ReadFile(filepath.Join(stateDir, "tasks.json"))
	if err != nil {
		return map[string]*Task{}
	}
	var list []*Task
	if json.Unmarshal(raw, &list) != nil {
		return map[string]*Task{}
	}
	tasks := make(map[string]*Task, len(list))
	for _, t := range list {
		if t != nil && t.Ticket != "" {
			tasks[t.Ticket] = t
		}
	}
	return tasks
}

// ReadEvents returns up to limit most-recent events from events.jsonl,
// oldest-first (callers reverse for a newest-first feed). Malformed lines
// are skipped — the feed is a render of the log, never a gate on it. A
// missing file is an empty feed, not an error.
func ReadEvents(stateDir string, limit int) ([]Event, error) {
	f, err := os.Open(eventsPath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var events []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		events = append(events, ev)
		if limit > 0 && len(events) > limit {
			events = events[1:]
		}
	}
	return events, sc.Err()
}
