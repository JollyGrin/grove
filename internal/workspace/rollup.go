package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Rollup is the cheap per-workspace fleet summary the switcher shows —
// counts of non-done tasks by what they need from the human.
type Rollup struct {
	Working int // agent setup|working
	Waiting int // agent waiting|blocked
	Review  int // sentinel done or human reviewing
}

// rollupTask mirrors just the state.Task fields the rollup needs (json
// tags match internal/state/state.go).
type rollupTask struct {
	Agent    string `json:"agent"`
	Sentinel string `json:"sentinel,omitempty"`
	Human    string `json:"human,omitempty"`
	Done     bool   `json:"done"`
}

// ReadRollup parses `<root>/.grove/state/tasks.json` READ-ONLY. It never
// creates directories or writes anything: tasks.json is a derived view
// owned by the state package. A missing or corrupt file yields a zero
// Rollup — the switcher shows "idle" rather than erroring.
func ReadRollup(ws Workspace) Rollup {
	raw, err := os.ReadFile(filepath.Join(ws.Root, ".grove", "state", "tasks.json"))
	if err != nil {
		return Rollup{}
	}
	// state.Load writes the view as a JSON array; accept a ticket→task
	// map too, tolerantly.
	var tasks []rollupTask
	if json.Unmarshal(raw, &tasks) != nil {
		var m map[string]rollupTask
		if json.Unmarshal(raw, &m) != nil {
			return Rollup{}
		}
		for _, t := range m {
			tasks = append(tasks, t)
		}
	}

	var r Rollup
	for _, t := range tasks {
		if t.Done {
			continue
		}
		switch {
		case t.Sentinel == "done" || t.Human == "reviewing":
			r.Review++
		case t.Agent == "waiting" || t.Agent == "blocked":
			r.Waiting++
		case t.Agent == "setup" || t.Agent == "working":
			r.Working++
		}
	}
	return r
}

// SortByActionability orders workspaces for the switcher: waiting desc,
// then review desc, then working desc, then label asc. Pure — the input
// slice is not mutated; rollups is keyed by label.
func SortByActionability(list []Workspace, rollups map[string]Rollup) []Workspace {
	out := append([]Workspace(nil), list...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := rollups[out[i].Label], rollups[out[j].Label]
		if a.Waiting != b.Waiting {
			return a.Waiting > b.Waiting
		}
		if a.Review != b.Review {
			return a.Review > b.Review
		}
		if a.Working != b.Working {
			return a.Working > b.Working
		}
		return out[i].Label < out[j].Label
	})
	return out
}
