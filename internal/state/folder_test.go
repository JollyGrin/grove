package state

// grove-126: the cockpit's incremental events.jsonl reader. These pin the
// Folder's contract: parity with Load+ReadEvents on any log, O(new-bytes)
// incremental pickup, torn-write hold-back, and the dirty-flagged
// tasks.json rewrite.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func seedEvents(t *testing.T, dir string, evs ...Event) {
	t.Helper()
	for _, ev := range evs {
		if err := Append(dir, ev); err != nil {
			t.Fatal(err)
		}
	}
}

func created(ticket, title string) Event {
	return Event{Type: EvTaskCreated, Ticket: ticket, Data: map[string]string{
		"title": title, "repo": "grove",
	}}
}

// A fresh Folder's first Refresh must answer exactly what Load + ReadEvents
// answer on the same log.
func TestFolderMatchesLoadAndReadEvents(t *testing.T) {
	dir := t.TempDir()
	seedEvents(t, dir,
		created("grove-1", "one"),
		Event{Type: EvSessionStarted, Ticket: "grove-1", Data: map[string]string{"session_id": "s1"}},
		created("grove-2", "two"),
		Event{Type: EvTaskDone, Ticket: "grove-1"},
	)

	f := NewFolder(dir, 200)
	got, tail, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	want, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("folder tasks diverge from Load:\n got %+v\nwant %+v", got, want)
	}
	wantTail, err := ReadEvents(dir, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tail, wantTail) {
		t.Errorf("folder tail diverges from ReadEvents:\n got %+v\nwant %+v", tail, wantTail)
	}
}

// After the initial fold, a Refresh consumes only appended bytes — and still
// converges on what a full Load would say.
func TestFolderIncrementalPickup(t *testing.T) {
	dir := t.TempDir()
	seedEvents(t, dir, created("grove-1", "one"))

	f := NewFolder(dir, 200)
	if _, _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	offsetAfterFirst := f.offset

	seedEvents(t, dir,
		Event{Type: EvAgentStatus, Ticket: "grove-1", Data: map[string]string{"status": AgentWaiting, "sentinel": "question", "question": "which?"}},
		created("grove-2", "two"),
	)
	got, tail, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if f.offset <= offsetAfterFirst {
		t.Error("offset did not advance over the appended events")
	}
	want, _ := Load(dir)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("incremental fold diverges from Load:\n got %+v\nwant %+v", got, want)
	}
	if len(tail) != 3 {
		t.Errorf("tail = %d events, want 3", len(tail))
	}
	if got["grove-1"].Agent != AgentWaiting || got["grove-1"].Question != "which?" {
		t.Errorf("appended agent_status not folded: %+v", got["grove-1"])
	}
}

// A no-change Refresh must not re-read consumed bytes (offset stable) and
// must keep answering the same state.
func TestFolderNoNewBytesIsCheap(t *testing.T) {
	dir := t.TempDir()
	seedEvents(t, dir, created("grove-1", "one"))
	f := NewFolder(dir, 200)
	if _, _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	off := f.offset
	got, tail, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if f.offset != off {
		t.Errorf("offset moved on an unchanged log: %d → %d", off, f.offset)
	}
	if len(got) != 1 || len(tail) != 1 {
		t.Errorf("unchanged log answered %d tasks / %d tail events", len(got), len(tail))
	}
}

// The feed tail stays capped — the cockpit RAM rule.
func TestFolderTailCap(t *testing.T) {
	dir := t.TempDir()
	seedEvents(t, dir, created("grove-1", "one"))
	for i := 0; i < 10; i++ {
		seedEvents(t, dir, Event{Type: EvNotification, Ticket: "grove-1", Data: map[string]string{"message": "m"}})
	}
	f := NewFolder(dir, 5)
	_, tail, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 5 {
		t.Errorf("tail = %d events, want cap 5", len(tail))
	}
	if tail[len(tail)-1].Type != EvNotification {
		t.Errorf("tail should keep the newest events, last = %s", tail[len(tail)-1].Type)
	}
}

// A torn append (no trailing newline yet) is left unconsumed; once the
// writer finishes the line, the event folds.
func TestFolderTornWriteHeldBack(t *testing.T) {
	dir := t.TempDir()
	seedEvents(t, dir, created("grove-1", "one"))
	f := NewFolder(dir, 200)
	if _, _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	off := f.offset

	path := filepath.Join(dir, "events.jsonl")
	half := `{"type":"task_done","ticket":"grove-1"`
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(half); err != nil {
		t.Fatal(err)
	}
	file.Close()

	got, _, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if f.offset != off {
		t.Error("offset advanced over a torn (newline-less) append")
	}
	if got["grove-1"].Done {
		t.Error("torn task_done folded before the line was complete")
	}

	file, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("}\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()

	got, _, err = f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if !got["grove-1"].Done {
		t.Error("completed line did not fold on the next refresh")
	}
}

// A complete-but-malformed line is skipped (like ReadEvents), and folding
// continues past it.
func TestFolderMalformedLineSkipped(t *testing.T) {
	dir := t.TempDir()
	seedEvents(t, dir, created("grove-1", "one"))
	path := filepath.Join(dir, "events.jsonl")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{garbage\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	seedEvents(t, dir, created("grove-2", "two"))

	f := NewFolder(dir, 200)
	got, tail, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("fold stopped at the malformed line: %d tasks, want 2", len(got))
	}
	if len(tail) != 2 {
		t.Errorf("tail = %d events, want 2 (garbage skipped)", len(tail))
	}
}

// events.jsonl is append-only; a shrink means the file was replaced —
// refold from scratch rather than serving ghosts.
func TestFolderShrinkRefolds(t *testing.T) {
	dir := t.TempDir()
	seedEvents(t, dir, created("grove-1", "one"), created("grove-2", "two"))
	f := NewFolder(dir, 200)
	if _, _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(dir, "events.jsonl")); err != nil {
		t.Fatal(err)
	}
	seedEvents(t, dir, created("grove-9", "fresh log"))

	got, tail, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["grove-9"] == nil {
		t.Errorf("replaced log did not refold: %+v", got)
	}
	if len(tail) != 1 {
		t.Errorf("tail after refold = %d events, want 1", len(tail))
	}
}

// A missing log answers an empty fleet (and clears any prior state), same
// shape as Load.
func TestFolderMissingLog(t *testing.T) {
	dir := t.TempDir()
	seedEvents(t, dir, created("grove-1", "one"))
	f := NewFolder(dir, 200)
	if _, _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "events.jsonl")); err != nil {
		t.Fatal(err)
	}
	got, tail, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || len(tail) != 0 {
		t.Errorf("missing log should answer empty, got %d tasks / %d events", len(got), len(tail))
	}
}

// The derived view is written on the first refresh and on change — but an
// unchanged fold must NOT rewrite tasks.json every tick (grove-126 item 2:
// the whole point).
func TestFolderViewWriteDirtyFlagged(t *testing.T) {
	dir := t.TempDir()
	seedEvents(t, dir, created("grove-1", "one"))
	f := NewFolder(dir, 200)
	if _, _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	view := filepath.Join(dir, "tasks.json")
	if _, err := os.Stat(view); err != nil {
		t.Fatalf("first refresh did not write the derived view: %v", err)
	}

	// Plant a sentinel: an unchanged fold must leave it alone.
	if err := os.WriteFile(view, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(view)
	if string(raw) != "sentinel" {
		t.Error("no-change refresh rewrote tasks.json")
	}

	// A folded event rewrites it.
	seedEvents(t, dir, Event{Type: EvTaskDone, Ticket: "grove-1"})
	if _, _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(view)
	if string(raw) == "sentinel" {
		t.Error("changed fold did not rewrite tasks.json")
	}
	tasks := ReadTasks(dir)
	if tasks["grove-1"] == nil || !tasks["grove-1"].Done {
		t.Errorf("rewritten view stale: %+v", tasks["grove-1"])
	}
}

// A ticket-less feed event (orchestrator_closed) folds to a no-op — the
// view bytes are identical, so even though the log grew, tasks.json is not
// rewritten.
func TestFolderNoopFoldSkipsViewWrite(t *testing.T) {
	dir := t.TempDir()
	seedEvents(t, dir, created("grove-1", "one"))
	f := NewFolder(dir, 200)
	if _, _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	view := filepath.Join(dir, "tasks.json")
	if err := os.WriteFile(view, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedEvents(t, dir, Event{Type: EvOrchestratorClosed, Data: map[string]string{"ticket": "grove-1"}})
	_, tail, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 2 {
		t.Errorf("feed event should land in the tail, got %d", len(tail))
	}
	raw, _ := os.ReadFile(view)
	if string(raw) != "sentinel" {
		t.Error("no-op fold rewrote tasks.json")
	}
}

// Refresh returns copies: the render loop holds a result across ticks while
// the folder keeps mutating its internal state — later folds must not write
// through pointers the caller already has, and caller mutations must not
// leak back in.
func TestFolderReturnsCopies(t *testing.T) {
	dir := t.TempDir()
	seedEvents(t, dir, created("grove-1", "one"))
	f := NewFolder(dir, 200)
	first, _, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}

	seedEvents(t, dir, Event{Type: EvTaskDone, Ticket: "grove-1"})
	second, _, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if first["grove-1"].Done {
		t.Error("later fold mutated a previously returned task")
	}
	second["grove-1"].Title = "caller scribble"
	third, _, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if third["grove-1"].Title != "one" {
		t.Error("caller mutation leaked into folder state")
	}
}
