package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/state"
)

// A fire-and-forget dismissal must surface as one ACTIVITY row, attributed
// to the ticket it dispatched — the durable trail the operator sees after the pane
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

// grove-403: a hide and a show are one feed line each, whoever ran them —
// the cockpit key, the CLI, or `!gv chat hide` inside the chat all append
// the same grove-401 event.
func TestFeedRendersChatHiddenAndShown(t *testing.T) {
	data := map[string]string{"session": "grove-chat-grove-2", "pane": "%7", "workspace": "grove"}
	items := feedItems([]state.Event{
		{Type: state.EvChatHidden, Time: time.Unix(1, 0), Data: data},
		{Type: state.EvChatShown, Time: time.Unix(2, 0), Data: data},
	})
	if len(items) != 2 {
		t.Fatalf("got %d feed items, want 2", len(items))
	}
	// newest-first: the show leads.
	if got := items[0].Glyph + " " + items[0].Text; got != "▣ showed grove-chat-grove-2" {
		t.Errorf("show line = %q", got)
	}
	if got := items[1].Glyph + " " + items[1].Text; got != "○ hid grove-chat-grove-2" {
		t.Errorf("hide line = %q", got)
	}
	if items[0].Ticket != "" || !items[1].Time.Equal(time.Unix(1, 0)) {
		t.Errorf("chat lines are ticket-less and carry the event's time: %+v", items)
	}
}
