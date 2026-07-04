// Grove additions to the copied state package: read-side helpers for the
// activity feed. New code goes here, not in the byte-comparable state.go.
package state

import (
	"bufio"
	"encoding/json"
	"os"
)

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
