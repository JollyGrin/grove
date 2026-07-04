package state

import (
	"os"
	"path/filepath"
	"testing"
)

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
