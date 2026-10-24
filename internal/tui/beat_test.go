package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/state"
)

// beatFetches runs cmd (recursing into batches) and counts the data
// fetches it made: refreshMsgs produced and chat passes taken. A command
// still running after a moment is the beat's timer and is left alone.
func beatFetches(t *testing.T, cmd tea.Cmd) (refreshes, chats int) {
	t.Helper()
	prev := CockpitChats
	CockpitChats = func(string, bool) []ChatRow {
		chats++
		return nil
	}
	defer func() { CockpitChats = prev }()
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		select {
		case msg := <-done:
			switch msg := msg.(type) {
			case tea.BatchMsg:
				for _, sub := range msg {
					walk(sub)
				}
			case refreshMsg:
				refreshes++
			}
		case <-time.After(500 * time.Millisecond):
		}
	}
	walk(cmd)
	return refreshes, chats
}

// AC (grove-579): two beats with no reply between them dispatch exactly
// one refresh and one chat pass; the clock still advances on both. A
// reply frees the slot, and the stale valve re-opens it on its own.
func TestBeatSkipsInFlightFetch(t *testing.T) {
	m := chatsModel(t, fxOff, 120, 40, chatShownRow(1))
	m.stateDir = t.TempDir()
	m.folder = state.NewFolder(m.stateDir, feedTail)

	next, cmd1 := m.Update(tickMsg{})
	m = next.(Model)
	next, cmd2 := m.Update(tickMsg{})
	m = next.(Model)
	if m.tick != 2 {
		t.Fatalf("tick = %d after two beats, want 2 — the clock must never wait on a fetch", m.tick)
	}
	r1, c1 := beatFetches(t, cmd1)
	r2, c2 := beatFetches(t, cmd2)
	if r1+r2 != 1 || c1+c2 != 1 {
		t.Errorf("two unanswered beats made %d refreshes and %d chat passes, want 1 and 1", r1+r2, c1+c2)
	}
	if r1 != 1 || c1 != 1 {
		t.Errorf("first beat made %d refreshes, %d chat passes — the FIRST beat fetches, the second skips", r1, c1)
	}

	// The replies land: the next beat fetches again.
	next, _ = m.Update(refreshMsg{ok: true})
	next, _ = next.(Model).Update(chatsMsg{})
	m = next.(Model)
	next, cmd3 := m.Update(tickMsg{})
	m = next.(Model)
	if r, c := beatFetches(t, cmd3); r != 1 || c != 1 || m.tick != 3 {
		t.Errorf("beat after the replies made %d refreshes, %d chat passes at tick %d, want 1, 1, 3", r, c, m.tick)
	}

	// No reply for beatStale beats: the valve dispatches anyway, so a lost
	// reply can delay the board but never freeze it.
	// (The dispatch stamps are the cheap witness here: a beat that fetches
	// moves them to its own tick; walking 29 timers would cost 15s.)
	at := m.refreshAt
	for i := 0; i < beatStale-1; i++ {
		next, _ := m.Update(tickMsg{})
		m = next.(Model)
		if m.refreshAt != at || m.chatsAt != at {
			t.Fatalf("beat %d inside the stale window fetched (stamps %d/%d, want %d)", i, m.refreshAt, m.chatsAt, at)
		}
	}
	next, cmd := m.Update(tickMsg{})
	m = next.(Model)
	if r, c := beatFetches(t, cmd); r != 1 || c != 1 {
		t.Errorf("beat past the stale valve made %d refreshes, %d chat passes, want 1 and 1", r, c)
	}
}

// An ad-hoc refreshMsg (answer, review, done) frees the slot too — the
// guard tracks "a reply is owed", not which caller asked.
func TestInFlight(t *testing.T) {
	if inFlight(0, 5) {
		t.Error("0 is idle")
	}
	if !inFlight(1, 2) || !inFlight(1, beatStale) {
		t.Error("an unanswered fetch holds its slot inside the window")
	}
	if inFlight(1, 1+beatStale) {
		t.Error("the valve opens after beatStale beats")
	}
}
