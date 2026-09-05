package supervise

import (
	"time"

	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
)

// RepoLookup resolves a task's repo name to the repo directory FetchAll
// needs to shell out to `gh` — the caller supplies it from config.Repos so
// this package never imports internal/config. ok=false (no repo mapping)
// means the task is skipped for the delivery dimension entirely, exactly
// like `gv ls`'s existing PR-refresh path.
type RepoLookup func(repo string) (dir string, ok bool)

// PollDeps are the poll loop's tmux/gh seams — injected so tests never
// touch a real tmux server or shell out to gh. In production these are
// tmux.SnapshotSession, detect.DetectLiveFrom, and github.FetchAll.
type PollDeps struct {
	StateDir   string
	RepoLookup RepoLookup
	SnapFor    func(session string) (*tmux.SessionSnapshot, error)
	DetectFrom func(snap *tmux.SessionSnapshot, base string) detect.LiveInfo
	FetchAll   func(lookups map[string][2]string) (prs map[string]*github.PR, unknown map[string]error)
}

// Poll is one workspace-wide supervise pass: fold state (read-only —
// state.Peek, never state.Load's tasks.json rewrite), snapshot every
// distinct tmux session exactly once (the grove-149 shape), fetch every
// active task's PR in one github.FetchAll round-trip, derive transitions
// per task via Transitions, and append whatever fired. Returns the events
// actually appended, oldest first.
func Poll(deps PollDeps, mem *Memory, now time.Time) ([]state.Event, error) {
	tasks, err := state.Peek(deps.StateDir)
	if err != nil {
		return nil, err
	}
	active := state.Active(tasks)

	lookups := map[string][2]string{}
	for _, t := range active {
		if dir, ok := deps.RepoLookup(t.Repo); ok {
			lookups[t.Ticket] = [2]string{dir, t.Branch}
		}
	}
	prs, unknown := deps.FetchAll(lookups)

	snapCache := map[string]*tmux.SessionSnapshot{}
	snapFor := func(session string) *tmux.SessionSnapshot {
		if snap, ok := snapCache[session]; ok {
			return snap
		}
		snap, _ := deps.SnapFor(session)
		snapCache[session] = snap
		return snap
	}

	var evs []state.Event
	for _, t := range active {
		_, bad := unknown[t.Ticket]
		obs := Observation{
			Task:    t,
			PR:      prs[t.Ticket],
			PRKnown: !bad,
			Live:    deps.DetectFrom(snapFor(t.TmuxSession), t.TmuxWindow),
			Now:     now,
		}
		for _, ev := range Transitions(obs, mem) {
			if err := state.Append(deps.StateDir, ev); err != nil {
				return evs, err
			}
			evs = append(evs, ev)
		}
	}
	return evs, nil
}
