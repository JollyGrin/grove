package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlyph(t *testing.T) {
	cases := []struct {
		agent, sentinel, want string
	}{
		{AgentSetup, "", "◌"},
		{AgentWorking, "", "●"},
		{AgentWaiting, "question", "⏸"},
		{AgentBlocked, "blocked", "⚠"},
		{AgentBlocked, "done", "⚠"}, // sentinel never overrides a blocked agent
		{AgentIdle, "done", "✔"},
		{AgentIdle, "none", "✗"},
		{AgentIdle, "", "✗"},
		{AgentDead, "", "✗"},
	}
	for _, tc := range cases {
		if got := Glyph(tc.agent, tc.sentinel); got != tc.want {
			t.Errorf("Glyph(%q, %q) = %q, want %q", tc.agent, tc.sentinel, got, tc.want)
		}
	}

	// The two collisions grove-47 splits must stay split.
	if Glyph(AgentSetup, "") == Glyph(AgentWorking, "") {
		t.Error("setup and working must map to distinct glyphs")
	}
	if Glyph(AgentBlocked, "") == Glyph(AgentWaiting, "") {
		t.Error("blocked and waiting must map to distinct glyphs")
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

// SeenOpID (grove-186) is the receipt a retried relayed answer/nudge
// dedups against: an event already carrying the op id ⇒ already applied.
func TestSeenOpID(t *testing.T) {
	dir := t.TempDir()
	if seen, err := SeenOpID(dir, "abc"); err != nil || seen {
		t.Fatalf("missing log: SeenOpID = %v, %v; want false, nil", seen, err)
	}
	if err := Append(dir, Event{Type: EvTaskCreated, Ticket: "t-1"}); err != nil {
		t.Fatal(err)
	}
	// The empty op id is never seen — legacy relays carry no id and must
	// always run, even against a log full of id-less answered events.
	if err := Append(dir, Event{Type: EvAnswered, Ticket: "t-1"}); err != nil {
		t.Fatal(err)
	}
	if seen, err := SeenOpID(dir, ""); err != nil || seen {
		t.Fatalf(`SeenOpID("") = %v, %v; want false, nil`, seen, err)
	}
	if err := Append(dir, Event{Type: EvAnswered, Ticket: "t-1", Data: map[string]string{"op_id": "abc"}}); err != nil {
		t.Fatal(err)
	}
	if seen, err := SeenOpID(dir, "abc"); err != nil || !seen {
		t.Fatalf("SeenOpID(abc) = %v, %v; want true, nil", seen, err)
	}
	if seen, err := SeenOpID(dir, "other"); err != nil || seen {
		t.Fatalf("SeenOpID(other) = %v, %v; want false, nil", seen, err)
	}
}

func TestParkedTickets(t *testing.T) {
	mk := func(evs ...Event) []Event { return evs }

	t.Run("park marks every active task", func(t *testing.T) {
		got := ParkedTickets(mk(
			Event{Type: EvTaskCreated, Ticket: "a"},
			Event{Type: EvTaskCreated, Ticket: "b"},
			Event{Type: EvWorkspaceParked},
		))
		if !got["a"] || !got["b"] {
			t.Errorf("both tasks should be parked, got %v", got)
		}
	})

	t.Run("adopt after park clears just that ticket", func(t *testing.T) {
		got := ParkedTickets(mk(
			Event{Type: EvTaskCreated, Ticket: "a"},
			Event{Type: EvTaskCreated, Ticket: "b"},
			Event{Type: EvWorkspaceParked},
			Event{Type: EvTaskAdopted, Ticket: "a"},
		))
		if got["a"] {
			t.Errorf("adopted ticket a should not be parked, got %v", got)
		}
		if !got["b"] {
			t.Errorf("un-adopted ticket b should stay parked, got %v", got)
		}
	})

	t.Run("session start after park clears the mark", func(t *testing.T) {
		got := ParkedTickets(mk(
			Event{Type: EvTaskCreated, Ticket: "a"},
			Event{Type: EvWorkspaceParked},
			Event{Type: EvSessionStarted, Ticket: "a"},
		))
		if got["a"] {
			t.Errorf("revived ticket should not be parked, got %v", got)
		}
	})

	t.Run("task created after park is not parked", func(t *testing.T) {
		got := ParkedTickets(mk(
			Event{Type: EvTaskCreated, Ticket: "a"},
			Event{Type: EvWorkspaceParked},
			Event{Type: EvTaskCreated, Ticket: "b"},
		))
		if got["b"] {
			t.Errorf("task grabbed after park should not be parked, got %v", got)
		}
	})

	t.Run("done task drops out of the parked set", func(t *testing.T) {
		got := ParkedTickets(mk(
			Event{Type: EvTaskCreated, Ticket: "a"},
			Event{Type: EvWorkspaceParked},
			Event{Type: EvTaskDone, Ticket: "a"},
		))
		if got["a"] {
			t.Errorf("done ticket must not remain parked, got %v", got)
		}
	})

	t.Run("no park event means nothing parked", func(t *testing.T) {
		got := ParkedTickets(mk(
			Event{Type: EvTaskCreated, Ticket: "a"},
			Event{Type: EvSessionStarted, Ticket: "a"},
		))
		if got["a"] {
			t.Errorf("no park event: got %v, want none parked", got)
		}
	})
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

// grove-75: every appended record carries the contract stamp, and
// pre-stamp records (no v key) fold identically and read as v1.
func TestEventVersionStamp(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Event{Type: EvTaskCreated, Ticket: "T-1", Data: map[string]string{"title": "x"}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"v":1`) {
		t.Errorf("appended record missing v stamp: %s", raw)
	}

	// A legacy record without v still folds, and reads as v1.
	legacy := `{"time":"2026-01-01T00:00:00Z","type":"task_created","ticket":"T-old","data":{"title":"old"}}` + "\n"
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(legacy); err != nil {
		t.Fatal(err)
	}
	f.Close()

	tasks, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if tasks["T-old"] == nil || tasks["T-1"] == nil {
		t.Fatalf("mixed-version log did not fold both tasks: %v", tasks)
	}
	evs, err := ReadEvents(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if ev.Version() != 1 {
			t.Errorf("event %s/%s Version() = %d, want 1", ev.Type, ev.Ticket, ev.Version())
		}
	}
}

// TestAnsweredEventByteShape is grove-186's additive-by-construction
// guard: an answered event minted WITHOUT an op id must stay byte-for-byte
// today's record — Data nil ⇒ `omitempty` drops the key entirely, so no
// plugin parsing events.jsonl sees any change. Only a relayed hop's event
// carries data.op_id.
func TestAnsweredEventByteShape(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Event{Type: EvAnswered, Ticket: "task-1"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, Event{Type: EvAnswered, Ticket: "task-1", Data: map[string]string{"op_id": "abc"}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 records, got %d", len(lines))
	}
	if strings.Contains(lines[0], "data") {
		t.Errorf("a local relay's event must carry no data key at all: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"data":{"op_id":"abc"}`) {
		t.Errorf("a relayed hop's event must stamp data.op_id: %s", lines[1])
	}
}
