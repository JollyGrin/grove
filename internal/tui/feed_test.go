package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/state"
)

// A fire-and-forget dismissal must surface as one ACTIVITY row, attributed
// to the ticket it dispatched — the durable trail Dean sees after the pane
// is gone.
func TestFeedRendersOrchestratorClosed(t *testing.T) {
	events := []state.Event{
		{Type: state.EvTaskCreated, Ticket: "DEV-42", Time: time.Unix(1, 0)},
		// Ticket rides in Data, not Event.Ticket — that's how the dismissal
		// stays out of the derived task view.
		{Type: state.EvOrchestratorClosed, Time: time.Unix(2, 0), Data: map[string]string{"reason": "dispatched", "ticket": "DEV-42"}},
	}
	items := feedItems(events)
	if len(items) != 2 {
		t.Fatalf("got %d feed items, want 2", len(items))
	}
	// newest-first: dismissal leads.
	top := items[0]
	if top.Ticket != "DEV-42" {
		t.Errorf("dismissal ticket = %q, want DEV-42", top.Ticket)
	}
	if !strings.Contains(top.Text, "dismissed") || !strings.Contains(top.Text, "dispatched") {
		t.Errorf("dismissal text = %q, want it to mention dismissed + reason", top.Text)
	}
}

func TestOrchCloseReasonDefaults(t *testing.T) {
	if got := orchCloseReason(""); got != "dispatched" {
		t.Errorf("orchCloseReason(\"\") = %q, want dispatched", got)
	}
	if got := orchCloseReason("manual"); got != "manual" {
		t.Errorf("orchCloseReason(manual) = %q, want manual", got)
	}
}
