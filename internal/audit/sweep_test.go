package audit

import (
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/github"
)

// grove-92: offer-building is a pure map from audited rows to sweep
// offers — merged → done, abandoned → untrack, idle → pause; everything
// else (healthy, disconnected, drifted) yields nothing.
func TestSweepOffers(t *testing.T) {
	rows := []TaskResult{
		{Ticket: "t-merged", Class: Merged, PR: &github.PR{Number: 42, State: "MERGED"}},
		{Ticket: "t-abandoned", Class: Abandoned},
		{Ticket: "t-idle", Class: Idle},
		{Ticket: "t-healthy", Class: Healthy},
		{Ticket: "t-disconnected", Class: Disconnected},
		{Ticket: "t-drifted", Class: Drifted},
	}
	offers := SweepOffers(rows)
	if len(offers) != 3 {
		t.Fatalf("SweepOffers = %d offers, want 3: %+v", len(offers), offers)
	}
	byTicket := map[string]SweepOffer{}
	for _, o := range offers {
		byTicket[o.Ticket] = o
	}
	if o := byTicket["t-merged"]; !strings.HasPrefix(o.Action, "done") || o.Detail != "PR #42 merged" {
		t.Errorf("merged offer = %+v, want done action with PR detail", o)
	}
	if o := byTicket["t-abandoned"]; !strings.HasPrefix(o.Action, "untrack") || o.Detail != "remote branch kept" {
		t.Errorf("abandoned offer = %+v, want untrack action", o)
	}
	if o := byTicket["t-idle"]; !strings.HasPrefix(o.Action, "pause") {
		t.Errorf("idle offer = %+v, want pause action", o)
	}
}

// grove-92 hard rule: a paused task is invisible to sweep — no offer of
// ANY kind. The guard is the paused fact, not the class: Classify's
// precedence lets Merged outrank Paused, so a paused task whose PR merged
// classifies Merged and would otherwise be offered full cleanup.
func TestSweepOffersPausedInvisible(t *testing.T) {
	rows := []TaskResult{
		{Ticket: "t-paused", Class: Paused, Facts: Facts{Paused: true}},
		{Ticket: "t-paused-merged", Class: Merged, Facts: Facts{Paused: true},
			PR: &github.PR{Number: 7, State: "MERGED"}},
	}
	if offers := SweepOffers(rows); len(offers) != 0 {
		t.Fatalf("paused rows must yield zero offers, got %+v", offers)
	}
}

func TestSweepOffersMergedWithoutPR(t *testing.T) {
	offers := SweepOffers([]TaskResult{{Ticket: "t", Class: Merged}})
	if len(offers) != 1 || offers[0].Detail != "" {
		t.Fatalf("merged without PR = %+v, want one offer with empty detail", offers)
	}
}
