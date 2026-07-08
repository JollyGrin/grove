package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGlyph(t *testing.T) {
	cases := []struct {
		agent, sentinel, want string
	}{
		{AgentWaiting, "question", "⏸"},
		{AgentBlocked, "blocked", "⏸"},
		{AgentDead, "", "✗"},
		{AgentIdle, "done", "✔"},
		{AgentIdle, "none", "✗"},
		{AgentWorking, "", "●"},
		{AgentSetup, "", "●"},
	}
	for _, tc := range cases {
		if got := Glyph(tc.agent, tc.sentinel); got != tc.want {
			t.Errorf("Glyph(%q, %q) = %q, want %q", tc.agent, tc.sentinel, got, tc.want)
		}
	}
}

func TestReadEvents(t *testing.T) {
	dir := t.TempDir()
	for _, ticket := range []string{"t-1", "t-2", "t-3"} {
		if err := Append(dir, Event{Type: EvTaskCreated, Ticket: ticket, Data: map[string]string{
			"title": "x", "repo": "r", "branch": "b", "worktree": "/w/" + ticket,
			"tmux_session": "s", "tmux_window": "w",
		}}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := ReadEvents(dir, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("ReadEvents(0) = %d events, %v; want 3", len(all), err)
	}
	if all[0].Ticket != "t-1" || all[2].Ticket != "t-3" {
		t.Errorf("order wrong: %v", all)
	}

	last, err := ReadEvents(dir, 2)
	if err != nil || len(last) != 2 {
		t.Fatalf("ReadEvents(2) = %d events, %v; want 2", len(last), err)
	}
	if last[0].Ticket != "t-2" || last[1].Ticket != "t-3" {
		t.Errorf("limit must keep the most recent, got %v", last)
	}
}

func TestReadEventsMissingAndMalformed(t *testing.T) {
	dir := t.TempDir()
	if evs, err := ReadEvents(dir, 10); err != nil || evs != nil {
		t.Errorf("missing file: %v, %v", evs, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"),
		[]byte("not json\n{\"type\":\"task_created\",\"ticket\":\"ok\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evs, err := ReadEvents(dir, 10)
	if err != nil || len(evs) != 1 || evs[0].Ticket != "ok" {
		t.Errorf("malformed lines must be skipped: %v, %v", evs, err)
	}
}

func TestReadTasks(t *testing.T) {
	dir := t.TempDir()
	for _, ticket := range []string{"t-1", "t-2"} {
		if err := Append(dir, Event{Type: EvTaskCreated, Ticket: ticket, Data: map[string]string{
			"title": "x", "repo": "r", "branch": "b", "worktree": "/w/" + ticket,
			"tmux_session": "s", "tmux_window": "w",
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Load(dir); err != nil { // materialize the derived tasks.json
		t.Fatal(err)
	}
	viewPath := filepath.Join(dir, "tasks.json")
	before, err := os.Stat(viewPath)
	if err != nil {
		t.Fatal(err)
	}

	tasks := ReadTasks(dir)
	if len(tasks) != 2 || tasks["t-1"] == nil || tasks["t-2"] == nil {
		t.Fatalf("ReadTasks = %v, want t-1 and t-2", tasks)
	}
	if tasks["t-1"].Worktree != "/w/t-1" || tasks["t-1"].Agent != AgentSetup {
		t.Errorf("task fields not parsed: %+v", tasks["t-1"])
	}

	after, err := os.Stat(viewPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("ReadTasks must not rewrite tasks.json (contrast with Load)")
	}
}

func TestReadTasksMissingAndCorrupt(t *testing.T) {
	// Missing state dir: empty map, and the dir must NOT be created —
	// hook receivers probe every registered workspace on every turn.
	ghost := filepath.Join(t.TempDir(), "never-created")
	if tasks := ReadTasks(ghost); len(tasks) != 0 {
		t.Errorf("missing dir: got %v, want empty map", tasks)
	}
	if _, err := os.Stat(ghost); !os.IsNotExist(err) {
		t.Error("ReadTasks created the state dir; it must be read-only")
	}

	// Present dir, missing file: empty map, no file created.
	dir := t.TempDir()
	if tasks := ReadTasks(dir); len(tasks) != 0 {
		t.Errorf("missing file: got %v, want empty map", tasks)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks.json")); !os.IsNotExist(err) {
		t.Error("ReadTasks created tasks.json; it must be read-only")
	}

	// Corrupt view: empty map, bytes untouched.
	if err := os.WriteFile(filepath.Join(dir, "tasks.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tasks := ReadTasks(dir); len(tasks) != 0 {
		t.Errorf("corrupt file: got %v, want empty map", tasks)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "tasks.json"))
	if err != nil || string(raw) != "{not json" {
		t.Errorf("corrupt view must be left as-is: %q, %v", raw, err)
	}
}
