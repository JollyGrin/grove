package state

import (
	"os"
	"path/filepath"
	"testing"
)

// gv pause (grove-90): task_paused bookmarks a deliberately parked worker;
// adopt (or any live session) clears it. Paused tasks stay Active.
func TestFoldPause(t *testing.T) {
	tasks := map[string]*Task{}
	fold(tasks, Event{Type: EvTaskCreated, Ticket: "gr-1", Data: map[string]string{
		"title": "Park me", "repo": "dummy", "branch": "gr-1-park",
		"worktree": "/tmp/wt/gr-1-park", "tmux_session": "grove-dummy", "tmux_window": "gr-1-park",
	}})
	fold(tasks, Event{Type: EvSessionStarted, Ticket: "gr-1", Data: map[string]string{"session_id": "s-1"}})
	fold(tasks, Event{Type: EvAgentStatus, Ticket: "gr-1", Data: map[string]string{
		"status": AgentIdle, "sentinel": "none", "message": "midway through the refactor",
	}})

	fold(tasks, Event{Type: EvTaskPaused, Ticket: "gr-1"})
	task := tasks["gr-1"]
	if !task.Paused {
		t.Fatal("task_paused did not set Paused")
	}
	if task.Label() != "paused" {
		t.Errorf("Label = %q, want paused", task.Label())
	}
	if task.Agent != AgentIdle {
		t.Errorf("Agent = %q, want idle (pause normalizes a possibly-unreported agent)", task.Agent)
	}
	if task.SessionID != "s-1" || task.LastMessage != "midway through the refactor" {
		t.Errorf("pause must not lose session/message: %+v", task)
	}
	if len(Active(tasks)) != 1 {
		t.Error("a paused task must stay active — it is a bookmark, not trash")
	}

	// The window kill may still deliver a session_ended — paused survives it.
	fold(tasks, Event{Type: EvSessionEnded, Ticket: "gr-1"})
	if !task.Paused || task.Label() != "paused" {
		t.Errorf("session_ended after pause must not unpause: %+v", task)
	}

	// Adopt resumes and clears the bookmark.
	fold(tasks, Event{Type: EvTaskAdopted, Ticket: "gr-1", Data: map[string]string{"tmux_window": "gr-1-park"}})
	if task.Paused {
		t.Error("task_adopted did not clear Paused")
	}

	// A live session also un-pauses (self-heal, mirrors ParkedTickets).
	fold(tasks, Event{Type: EvTaskPaused, Ticket: "gr-1"})
	fold(tasks, Event{Type: EvSessionStarted, Ticket: "gr-1", Data: map[string]string{"session_id": "s-2"}})
	if task.Paused {
		t.Error("session_started did not clear Paused")
	}
}

func TestFoldLifecycle(t *testing.T) {
	dir := t.TempDir()
	evs := []Event{
		{Type: EvTaskCreated, Ticket: "DEV-1", Data: map[string]string{
			"title": "Fix filters", "repo": "monorepo", "branch": "DEV-1-fix-filters",
			"worktree": "/tmp/wt/DEV-1-fix-filters", "tmux_session": "pr-monorepo", "tmux_window": "DEV-1-fix-filters",
		}},
		{Type: EvSessionStarted, Ticket: "DEV-1", Data: map[string]string{"session_id": "abc-123"}},
		{Type: EvAgentStatus, Ticket: "DEV-1", Data: map[string]string{
			"status": AgentWaiting, "sentinel": "question", "question": "Tabs or spaces?",
		}},
	}
	for _, ev := range evs {
		if err := Append(dir, ev); err != nil {
			t.Fatal(err)
		}
	}

	tasks, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	task := tasks["DEV-1"]
	if task == nil {
		t.Fatal("task not folded")
	}
	if task.SessionID != "abc-123" || task.Agent != AgentWaiting || task.Question != "Tabs or spaces?" {
		t.Errorf("fold mismatch: %+v", task)
	}
	if task.Label() != "QUESTION" {
		t.Errorf("Label = %q, want QUESTION", task.Label())
	}

	// Answer flips optimistically; done removes from Active.
	if err := Append(dir, Event{Type: EvAnswered, Ticket: "DEV-1"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, Event{Type: EvTaskDone, Ticket: "DEV-1"}); err != nil {
		t.Fatal(err)
	}
	tasks, _ = Load(dir)
	if !tasks["DEV-1"].Done {
		t.Error("task should be done")
	}
	if len(Active(tasks)) != 0 {
		t.Error("done task should not be active")
	}

	// Derived view exists and is rebuildable.
	if _, err := os.Stat(filepath.Join(dir, "tasks.json")); err != nil {
		t.Error("tasks.json view not written")
	}
}

func TestFoldModelProfile(t *testing.T) {
	dir := t.TempDir()
	// A profiled grab persists the field; an unprofiled grab and a pre-field
	// event both fold to "" (additive, no migration — grove-36 T2).
	evs := []Event{
		{Type: EvTaskCreated, Ticket: "DEV-30", Data: map[string]string{
			"title": "glm", "repo": "grove", "model_profile": "openrouter-glm",
		}},
		{Type: EvTaskCreated, Ticket: "DEV-31", Data: map[string]string{
			"title": "plain", "repo": "grove", // no model_profile key at all
		}},
	}
	for _, ev := range evs {
		if err := Append(dir, ev); err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := tasks["DEV-30"].ModelProfile; got != "openrouter-glm" {
		t.Errorf("profiled task ModelProfile = %q, want openrouter-glm", got)
	}
	if got := tasks["DEV-31"].ModelProfile; got != "" {
		t.Errorf("unprofiled task ModelProfile = %q, want empty", got)
	}
	// Adopt refreshes the field when the event carries it.
	if err := Append(dir, Event{Type: EvTaskAdopted, Ticket: "DEV-31", Data: map[string]string{
		"model_profile": "openrouter-glm",
	}}); err != nil {
		t.Fatal(err)
	}
	tasks, _ = Load(dir)
	if got := tasks["DEV-31"].ModelProfile; got != "openrouter-glm" {
		t.Errorf("adopted task ModelProfile = %q, want openrouter-glm", got)
	}
}

func TestFoldSurvivesTornFinalLine(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Event{Type: EvTaskCreated, Ticket: "DEV-2", Data: map[string]string{"title": "x"}}); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"time":"2026-06-10T12:00:00Z","type":"agent_st`) // torn write
	f.Close()

	tasks, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if tasks["DEV-2"] == nil {
		t.Fatal("intact prefix should still fold")
	}
}

func TestLoadSkipsMalformedLineMidFile(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Event{Type: EvTaskCreated, Ticket: "DEV-4", Data: map[string]string{"title": "before"}}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// A complete but corrupt line buried mid-file (e.g. a crash-torn write
	// later padded out by a subsequent append), not the final line.
	if _, err := f.WriteString("{not valid json}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := Append(dir, Event{Type: EvTaskCreated, Ticket: "DEV-5", Data: map[string]string{"title": "after"}}); err != nil {
		t.Fatal(err)
	}

	tasks, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if tasks["DEV-4"] == nil {
		t.Error("event before the torn line should fold")
	}
	if tasks["DEV-5"] == nil {
		t.Error("event after the torn line should still fold — Load must skip-and-continue like Folder.consume")
	}
}

func TestFindByCwdRealpath(t *testing.T) {
	dir := t.TempDir()
	wt := filepath.Join(dir, "wt")
	os.Mkdir(wt, 0o755)
	link := filepath.Join(dir, "link")
	os.Symlink(wt, link)

	tasks := map[string]*Task{
		"DEV-3": {Ticket: "DEV-3", Worktree: link}, // task recorded via symlink path
	}
	real, _ := filepath.EvalSymlinks(wt)
	if FindByCwd(tasks, real) == nil { // hook cwd arrives realpath'd
		t.Error("realpath'd cwd should match symlinked worktree path")
	}
}

func TestActiveSortsByActionability(t *testing.T) {
	tasks := map[string]*Task{
		"A": {Ticket: "A", Agent: AgentWorking},
		"B": {Ticket: "B", Agent: AgentWaiting},
		"C": {Ticket: "C", Agent: AgentIdle},
		"D": {Ticket: "D", Agent: AgentBlocked},
	}
	got := Active(tasks)
	want := []string{"B", "D", "C", "A"}
	for i, w := range want {
		if got[i].Ticket != w {
			t.Fatalf("order[%d] = %s, want %s", i, got[i].Ticket, w)
		}
	}
}

func TestFoldUntracked(t *testing.T) {
	dir := t.TempDir()
	evs := []Event{
		{Type: EvTaskCreated, Ticket: "DEV-10", Data: map[string]string{
			"title": "x", "repo": "monorepo", "branch": "DEV-10-x",
			"worktree": "/tmp/wt/DEV-10-x", "tmux_session": "pr-monorepo", "tmux_window": "DEV-10-x",
		}},
		{Type: EvTaskUntracked, Ticket: "DEV-10"},
	}
	for _, ev := range evs {
		if err := Append(dir, ev); err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !tasks["DEV-10"].Done {
		t.Error("untracked task should fold Done=true")
	}
	if len(Active(tasks)) != 0 {
		t.Error("untracked task should not be active")
	}
}

func TestFoldAdoptedRevive(t *testing.T) {
	dir := t.TempDir()
	evs := []Event{
		{Type: EvTaskCreated, Ticket: "DEV-11", Data: map[string]string{
			"title": "y", "repo": "monorepo", "branch": "DEV-11-y",
			"worktree": "/tmp/wt/DEV-11-y", "tmux_session": "pr-monorepo", "tmux_window": "DEV-11-y",
		}},
		{Type: EvAgentStatus, Ticket: "DEV-11", Data: map[string]string{
			"status": AgentWaiting, "sentinel": "question", "question": "stale?",
		}},
		{Type: EvTaskUntracked, Ticket: "DEV-11"},
		{Type: EvTaskAdopted, Ticket: "DEV-11", Data: map[string]string{
			"branch": "DEV-11-y", "worktree": "/tmp/wt2/DEV-11-y",
			"tmux_session": "pr-monorepo", "tmux_window": "DEV-11-y",
		}},
	}
	for _, ev := range evs {
		if err := Append(dir, ev); err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	task := tasks["DEV-11"]
	if task.Done {
		t.Error("adopted task should fold Done=false")
	}
	if task.Agent != AgentSetup {
		t.Errorf("adopted task Agent = %q, want setup", task.Agent)
	}
	if task.Worktree != "/tmp/wt2/DEV-11-y" {
		t.Errorf("adopted task Worktree = %q, want refreshed path", task.Worktree)
	}
	if task.Question != "" || task.Sentinel != "" {
		t.Errorf("adopted task should clear stale question/sentinel, got %q/%q", task.Question, task.Sentinel)
	}
	if task.Title != "y" {
		t.Errorf("adopt must not clobber fields absent from event data: Title = %q", task.Title)
	}
	if len(Active(tasks)) != 1 {
		t.Error("adopted task should be active again")
	}
}

func TestFoldAdoptedCold(t *testing.T) {
	dir := t.TempDir()
	ev := Event{Type: EvTaskAdopted, Ticket: "DEV-12", Data: map[string]string{
		"title": "z", "url": "https://linear.app/x/DEV-12", "repo": "discovery",
		"branch": "DEV-12-z", "worktree": "/tmp/wt/DEV-12-z",
		"tmux_session": "pr-discovery", "tmux_window": "DEV-12-z",
	}}
	if err := Append(dir, ev); err != nil {
		t.Fatal(err)
	}
	tasks, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	task := tasks["DEV-12"]
	if task == nil {
		t.Fatal("cold adopt should create the task")
	}
	if task.Done || task.Agent != AgentSetup || task.Repo != "discovery" || task.Title != "z" {
		t.Errorf("cold adopt fold mismatch: %+v", task)
	}
}

func TestFoldUntrackThenRecreate(t *testing.T) {
	dir := t.TempDir()
	evs := []Event{
		{Type: EvTaskCreated, Ticket: "DEV-13", Data: map[string]string{"title": "old", "worktree": "/tmp/a"}},
		{Type: EvTaskUntracked, Ticket: "DEV-13"},
		{Type: EvTaskCreated, Ticket: "DEV-13", Data: map[string]string{"title": "new", "worktree": "/tmp/b"}},
	}
	for _, ev := range evs {
		if err := Append(dir, ev); err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	task := tasks["DEV-13"]
	if task.Done || task.Title != "new" || task.Worktree != "/tmp/b" {
		t.Errorf("re-grab after untrack should fold cleanly: %+v", task)
	}
	if len(Active(tasks)) != 1 {
		t.Error("re-grabbed task should be active")
	}
}

func TestEventCounts(t *testing.T) {
	dir := t.TempDir()
	evs := []Event{
		{Type: EvTaskCreated, Ticket: "DEV-20", Data: map[string]string{"title": "x"}},
		{Type: EvAnswered, Ticket: "DEV-20"},
		{Type: EvAnswered, Ticket: "DEV-20"},
		{Type: EvAnswered, Ticket: "DEV-21"},
	}
	for _, ev := range evs {
		if err := Append(dir, ev); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := EventCounts(dir, EvAnswered)
	if err != nil {
		t.Fatal(err)
	}
	if counts["DEV-20"] != 2 || counts["DEV-21"] != 1 {
		t.Errorf("counts = %v", counts)
	}
}

// gv handoff (grove-177): task_handed_off is an untrack that keeps a
// forwarding pointer; a local re-adopt clears it.
func TestFoldHandedOff(t *testing.T) {
	tasks := map[string]*Task{}
	fold(tasks, Event{Type: EvTaskCreated, Ticket: "gr-1", Data: map[string]string{
		"title": "Move me", "repo": "dummy", "branch": "gr-1-move",
	}})
	fold(tasks, Event{Type: EvSessionStarted, Ticket: "gr-1", Data: map[string]string{"session_id": "s-1"}})
	fold(tasks, Event{Type: EvTaskHandedOff, Ticket: "gr-1", Data: map[string]string{"host": "pc", "branch": "gr-1-move"}})
	task := tasks["gr-1"]
	if task.SessionID != "" {
		t.Error("handoff must drop the stored session id (the transcript goes stale on the other host)")
	}
	if !task.Done || task.HandedOffTo != "pc" {
		t.Fatalf("handed off: Done=%v HandedOffTo=%q", task.Done, task.HandedOffTo)
	}
	if len(Active(tasks)) != 0 {
		t.Error("a handed-off task must leave Active()")
	}
	if ho := HandedOff(tasks); len(ho) != 1 || ho[0].Ticket != "gr-1" {
		t.Errorf("HandedOff = %+v, want gr-1", ho)
	}
	fold(tasks, Event{Type: EvTaskAdopted, Ticket: "gr-1", Data: map[string]string{"tmux_window": "gr-1-move"}})
	if task.Done || task.HandedOffTo != "" {
		t.Errorf("task_adopted must clear the tombstone: %+v", task)
	}
	if len(HandedOff(tasks)) != 0 {
		t.Error("re-adopted task still listed as handed off")
	}
}
