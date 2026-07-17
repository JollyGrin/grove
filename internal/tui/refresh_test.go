package tui

// grove-65: an empty-but-healthy fleet (state.Active returns nil, not an
// empty slice) must still apply the refresh — only a load-error zero msg
// (ok: false) may leave prior state untouched. These pin the msg.ok gate in
// the refreshMsg branch of Update against regressing back to msg.tasks != nil.

import (
	"testing"

	"github.com/JollyGrin/grove/internal/state"
)

// Producer side of the grove-35 fix: refreshCmd must set ok:true even when
// state.Active folds to a nil slice (every task done). This is the real
// load→Active→construct path the visible bug rode on — the two Update-handler
// tests below hand-build messages and never exercise it. Without ok:true here
// the swept last row would linger no matter how the consumer gates.
//
// The session arg is a bogus, non-existent name: refreshCmd's one tmux touch
// (tmux.ActiveWindow) is a read-only list-windows that errors out inertly on
// an unknown target, and the live-detect loop never runs at zero active tasks.
func TestRefreshCmdOkAtZeroActive(t *testing.T) {
	dir := t.TempDir()
	evs := []state.Event{
		{Type: state.EvTaskCreated, Ticket: "grove-35", Data: map[string]string{
			"title": "swept task lingers", "repo": "grove",
		}},
		{Type: state.EvTaskDone, Ticket: "grove-35"},
	}
	for _, ev := range evs {
		if err := state.Append(dir, ev); err != nil {
			t.Fatal(err)
		}
	}

	msg, ok := refreshCmd(dir, "grove-nonexistent-test-session")().(refreshMsg)
	if !ok {
		t.Fatalf("refreshCmd returned %T, want refreshMsg", msg)
	}
	if !msg.ok {
		t.Error("successful load must set ok:true so the handler applies the empty fleet")
	}
	if len(msg.tasks) != 0 {
		t.Errorf("all-done fleet should yield zero active tasks, got %d", len(msg.tasks))
	}
	if n := countDone(msg.events); n != 1 {
		t.Errorf("done event should survive into the refresh, countDone = %d, want 1", n)
	}
}

// A healthy refresh at zero active tasks (tasks: nil, ok: true) must still
// land the events — the orchard/strip/feed all read off m.events, not
// m.tasks, once every task is done.
func TestRefreshAppliesOnOkEvenWithNilTasks(t *testing.T) {
	m := fixtureModel(t)
	events := []state.Event{
		{Type: state.EvTaskDone, Ticket: "grove-9"},
		{Type: state.EvTaskDone, Ticket: "grove-10"},
	}
	next, _ := m.Update(refreshMsg{tasks: nil, events: events, ok: true})
	got := next.(Model)

	if got.events == nil || len(got.events) != 2 {
		t.Errorf("healthy empty-fleet refresh dropped events: %v", got.events)
	}
	if n := countDone(got.events); n != 2 {
		t.Errorf("countDone off the retained events = %d, want 2", n)
	}
	if got.tasks != nil {
		t.Errorf("empty fleet should clear stale tasks, got %v", got.tasks)
	}
}

// A load-error zero msg (state.Load failed) must never clobber a
// previously-populated refresh — the whole reason msg.ok exists.
func TestRefreshErrorMsgRetainsPriorState(t *testing.T) {
	m := fixtureModel(t)
	staleTasks := []*state.Task{{Ticket: "grove-18", Agent: state.AgentWorking}}
	staleEvents := []state.Event{{Type: state.EvTaskDone, Ticket: "grove-18"}}
	next, _ := m.Update(refreshMsg{tasks: staleTasks, events: staleEvents, ok: true})
	populated := next.(Model)

	next, _ = populated.Update(refreshMsg{})
	got := next.(Model)

	if len(got.tasks) != 1 || got.tasks[0].Ticket != "grove-18" {
		t.Errorf("load-error refresh clobbered tasks: %v", got.tasks)
	}
	if len(got.events) != 1 {
		t.Errorf("load-error refresh clobbered events: %v", got.events)
	}
}
