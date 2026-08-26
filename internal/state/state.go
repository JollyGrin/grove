// Package state is the event-sourced task store. events.jsonl is the source
// of truth (O_APPEND + flock — concurrent hook invocations only ever
// append); tasks.json is a derived view rebuilt by folding events.
package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/JollyGrin/grove/internal/schema"
)

// Event types.
const (
	EvTaskCreated    = "task_created"
	EvSessionStarted = "session_started"
	EvAgentStatus    = "agent_status"
	EvNotification   = "notification"
	EvSessionEnded   = "session_ended"
	EvAttached       = "attached"
	EvAnswered       = "answered"
	EvHumanStatus    = "human_status"
	EvTaskDone       = "task_done"
	EvTaskUntracked  = "task_untracked"
	EvTaskAdopted    = "task_adopted"
	EvTaskPaused     = "task_paused"
	// EvTaskHandedOff (grove-177) is the forwarding tombstone `gv handoff`
	// writes after the remote adopt succeeded: data {host, branch}, the
	// event time is `at`. Folds like an untrack (leaves Active) but keeps
	// the host on the task so `gv ls --json` can show handed_off_to.
	EvTaskHandedOff = "task_handed_off"
)

// Human states (the `human` dimension): "" (untouched) · reviewing ·
// changes-sent. approved/done are implicit in merge + EvTaskDone.
const (
	HumanReviewing   = "reviewing"
	HumanChangesSent = "changes-sent"
)

// Agent states (the `agent` dimension; `delivery` is computed live from gh,
// `human` arrives with the Phase 2 TUI).
const (
	AgentSetup   = "setup"
	AgentWorking = "working"
	AgentWaiting = "waiting"
	AgentBlocked = "blocked"
	AgentIdle    = "idle"
	AgentDead    = "dead"
)

type Event struct {
	Time   time.Time         `json:"time"`
	Type   string            `json:"type"`
	Ticket string            `json:"ticket"`
	Data   map[string]string `json:"data,omitempty"`
	// V is the record's plugin-contract version (docs/plugins.md), stamped
	// by Append. Records written before the field existed read as v1 —
	// use Version(), never V directly.
	V int `json:"v,omitempty"`
}

type Task struct {
	Ticket      string `json:"ticket"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Repo        string `json:"repo"`
	Branch      string `json:"branch"`
	Worktree    string `json:"worktree"`
	TmuxSession string `json:"tmux_session"`
	TmuxWindow  string `json:"tmux_window"`
	// ModelProfile is the named non-Anthropic backend a worker runs on
	// (grove-36); empty = the operator's own Claude sub. Additive & optional:
	// events predating the field simply lack it and fold to "".
	ModelProfile string `json:"model_profile,omitempty"`
	SessionID    string `json:"claude_session_id,omitempty"`
	Agent        string `json:"agent"`
	Sentinel     string `json:"sentinel,omitempty"` // question | blocked | done | none
	Question     string `json:"question,omitempty"`
	LastMessage  string `json:"last_message,omitempty"`
	Human        string `json:"human,omitempty"`
	Attached     bool   `json:"attached"`
	Done         bool   `json:"done"`
	// Paused marks a deliberately parked worker (grove-90): its tmux window
	// was killed to free CPU, but worktree, branch, and session transcript
	// all survive — `gv adopt` resumes the stored session and clears the
	// flag. A bookmark, never trash: paused tasks stay in Active().
	// Additive & optional: events predating the field fold to false.
	Paused bool `json:"paused,omitempty"`
	// HandedOffTo (grove-177) names the remote grove host this task was
	// handed to — a forwarding tombstone. Set by task_handed_off (which
	// also untracks), cleared by a local task_created/task_adopted (the
	// task came back). Additive & optional.
	HandedOffTo string    `json:"handed_off_to,omitempty"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
}

func eventsPath(dir string) string { return filepath.Join(dir, "events.jsonl") }

// Append writes one event under an exclusive flock.
func Append(stateDir string, ev Event) error {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	if ev.V == 0 {
		ev.V = schema.Version
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(eventsPath(stateDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_, err = f.Write(append(line, '\n'))
	return err
}

// Load folds events.jsonl into the task map and refreshes the derived
// tasks.json view. A complete-but-malformed line is skipped and folding
// continues, matching Folder.consume — a crash-torn line buried mid-file
// (not just at the end) must not silently drop every event after it. This
// is a deliberate divergence from ovs's break-on-error Load; see
// docs/seed-manifest.md.
func Load(stateDir string) (map[string]*Task, error) {
	tasks, err := Peek(stateDir)
	if err != nil {
		return nil, err
	}
	if view, err := json.MarshalIndent(tasksSlice(tasks), "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(stateDir, "tasks.json"), view, 0o644)
	}
	return tasks, nil
}

// Peek folds events.jsonl like Load but never rewrites the derived
// tasks.json view — for polling loops (gv handoff's idle wait) that would
// otherwise rewrite the same bytes every tick.
func Peek(stateDir string) (map[string]*Task, error) {
	f, err := os.Open(eventsPath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*Task{}, nil
		}
		return nil, err
	}
	defer f.Close()

	tasks := map[string]*Task{}
	r := bufio.NewReader(f)
	for {
		line, readErr := r.ReadBytes('\n')
		if len(line) > 0 {
			var ev Event
			if json.Unmarshal(line, &ev) == nil {
				fold(tasks, ev)
			}
		}
		if readErr != nil {
			break
		}
	}
	return tasks, nil
}

// EventCounts tallies events of one type per ticket across the whole log —
// e.g. answered-events as a steering count for cost analysis. Read-only.
func EventCounts(stateDir, eventType string) (map[string]int, error) {
	f, err := os.Open(eventsPath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]int{}, nil
		}
		return nil, err
	}
	defer f.Close()

	counts := map[string]int{}
	dec := json.NewDecoder(f)
	for dec.More() {
		var ev Event
		if err := dec.Decode(&ev); err != nil {
			break
		}
		if ev.Type == eventType {
			counts[ev.Ticket]++
		}
	}
	return counts, nil
}

func fold(tasks map[string]*Task, ev Event) {
	t := tasks[ev.Ticket]
	if t == nil {
		if ev.Type != EvTaskCreated && ev.Type != EvTaskAdopted {
			return // event for an unknown (or pruned) task
		}
		t = &Task{Ticket: ev.Ticket, Created: ev.Time}
		tasks[ev.Ticket] = t
	}
	t.Updated = ev.Time
	d := ev.Data
	switch ev.Type {
	case EvTaskCreated:
		t.Title, t.URL, t.Repo = d["title"], d["url"], d["repo"]
		t.Branch, t.Worktree = d["branch"], d["worktree"]
		t.TmuxSession, t.TmuxWindow = d["tmux_session"], d["tmux_window"]
		t.ModelProfile = d["model_profile"] // "" for unprofiled + pre-field events
		t.Agent = AgentSetup
		t.Done = false
		t.Paused = false
		t.HandedOffTo = ""
	case EvSessionStarted:
		t.SessionID = d["session_id"]
		if t.Agent == AgentSetup || t.Agent == AgentDead {
			t.Agent = AgentWorking
		}
		t.Paused = false // any live session un-pauses (mirrors ParkedTickets)
	case EvAgentStatus:
		t.Agent = d["status"]
		t.Sentinel = d["sentinel"]
		t.Question = d["question"]
		if m := d["message"]; m != "" {
			t.LastMessage = m
		}
	case EvAnswered:
		t.Agent = AgentWorking // optimistic flip; scraper corrects if it didn't take
		t.Question = ""
	case EvHumanStatus:
		t.Human = d["status"]
	case EvSessionEnded:
		t.Agent = AgentDead
	case EvAttached:
		t.Attached = true
	case EvTaskDone:
		t.Done = true
	case EvTaskUntracked:
		t.Done = true // leaves Active(); the event type keeps the distinction in history
		// Also the tombstone's terminal path: `gv untrack` on a handed-off
		// row drops the forwarding pointer without resurrecting a worker.
		t.HandedOffTo = ""
	case EvTaskHandedOff:
		t.Done = true // untracked here; the host carries the forwarding pointer
		t.HandedOffTo = d["host"]
		// The transcript is stale the moment another host works the
		// branch: a later local adopt must start from the PR handoff, not
		// resume a session that never saw the remote's commits.
		t.SessionID = ""
		// The tombstone row still shows in `gv ls --json`: a phantom open
		// question or working glyph would point orchestrators at a task
		// `gv answer` can no longer reach.
		t.Agent = AgentIdle
		t.Sentinel, t.Question = "", ""
		if b := d["branch"]; b != "" {
			t.Branch = b
		}
	case EvTaskPaused:
		// Deliberate park (grove-90). Agent normalizes to idle so a paused
		// worker never ghosts the working counts — the window kill that
		// follows the append may or may not deliver a session_ended.
		// Sentinel/question/last_message survive: paused is a bookmark.
		t.Paused = true
		t.Agent = AgentIdle
	case EvTaskAdopted:
		// Refresh only the fields the event carries — an adopt may reuse
		// the existing worktree/window, and title/url/repo survive from
		// the original task_created.
		for k, v := range d {
			switch k {
			case "title":
				t.Title = v
			case "url":
				t.URL = v
			case "repo":
				t.Repo = v
			case "branch":
				t.Branch = v
			case "worktree":
				t.Worktree = v
			case "tmux_session":
				t.TmuxSession = v
			case "tmux_window":
				t.TmuxWindow = v
			case "model_profile":
				t.ModelProfile = v
			case "session_id":
				t.SessionID = v
			}
		}
		t.Done = false
		t.Paused = false
		t.HandedOffTo = ""
		t.Agent = AgentSetup
		t.Sentinel, t.Question = "", ""
	}
}

// Label is the single most-actionable status string for table rows.
func (t *Task) Label() string {
	switch {
	case t.Done:
		return "done"
	case t.Paused:
		return "paused"
	case t.Agent == AgentWaiting:
		return "QUESTION"
	case t.Agent == AgentBlocked:
		return "BLOCKED"
	case t.Agent == AgentDead:
		return "dead"
	case t.Agent == AgentIdle && t.Sentinel == "done":
		return "idle ✓"
	case t.Agent == AgentIdle:
		return "stalled?"
	default:
		return t.Agent
	}
}

// Glyph is the one-rune live-status marker pushed into a worker's tmux
// window name so `Ctrl-b w` reads as a fleet board (grove-29 P3). It mirrors
// Label's actionability buckets; the setup/blocked runes are shared with the
// cockpit AGENTS list (grove-47) so both surfaces read as one vocabulary:
//
//	◌ booting     setup, spinning up
//	● live        actively working
//	⏸ needs you   waiting on a question / plan approval
//	⚠ blocked     BLOCKED sentinel, needs a decision
//	✔ done        agent reports done (PR likely following)
//	✗ stalled     dead/crashed, or idle with no STATUS sentinel
func Glyph(agent, sentinel string) string {
	switch {
	case agent == AgentWaiting:
		return "⏸"
	case agent == AgentBlocked:
		return "⚠"
	case agent == AgentDead:
		return "✗"
	case agent == AgentIdle && sentinel == "done":
		return "✔"
	case agent == AgentIdle:
		return "✗"
	case agent == AgentSetup:
		return "◌"
	default: // working
		return "●"
	}
}

// SortRank orders by actionability: things needing a human float up.
func (t *Task) SortRank() int {
	if t.Paused {
		return 4 // parked on purpose (grove-90) — calm, sorts with setup
	}
	switch t.Agent {
	case AgentWaiting:
		return 0
	case AgentBlocked:
		return 1
	case AgentDead:
		return 2
	case AgentIdle:
		return 3
	case AgentSetup:
		return 4
	default: // working
		return 5
	}
}

// HandedOff returns the forwarding tombstones (grove-177): tasks that left
// Active() via task_handed_off and have not been re-tracked locally since.
// Sorted by Updated (the handoff time) so the newest handoff lists last.
func HandedOff(tasks map[string]*Task) []*Task {
	var out []*Task
	for _, t := range tasks {
		if t.Done && t.HandedOffTo != "" {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.Before(out[j].Updated) })
	return out
}

// Active returns non-done tasks, actionability-sorted.
func Active(tasks map[string]*Task) []*Task {
	var out []*Task
	for _, t := range tasks {
		if !t.Done {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortRank() != out[j].SortRank() {
			return out[i].SortRank() < out[j].SortRank()
		}
		return out[i].Created.Before(out[j].Created)
	})
	return out
}

func tasksSlice(tasks map[string]*Task) []*Task {
	var out []*Task
	for _, t := range tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// FindByCwd maps a hook payload cwd to an active task by worktree path,
// comparing realpaths (hook cwd arrives symlink-resolved: /tmp → /private/tmp).
func FindByCwd(tasks map[string]*Task, cwd string) *Task {
	rcwd := realpath(cwd)
	for _, t := range tasks {
		if !t.Done && realpath(t.Worktree) == rcwd {
			return t
		}
	}
	return nil
}

func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
