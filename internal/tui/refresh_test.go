package tui

// grove-65: an empty-but-healthy fleet (state.Active returns nil, not an
// empty slice) must still apply the refresh — only a load-error zero msg
// (ok: false) may leave prior state untouched. These pin the msg.ok gate in
// the refreshMsg branch of Update against regressing back to msg.tasks != nil.

import (
	"testing"

	"github.com/JollyGrin/grove/internal/state"
)

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
