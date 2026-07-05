// Grove additions to the copied state package: read-side helpers for the
// activity feed. New code goes here, not in the byte-comparable state.go.
package state

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

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
