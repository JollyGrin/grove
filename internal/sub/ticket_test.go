package sub

import "testing"

func TestReadMissingFileIsEmptyNotError(t *testing.T) {
	recs, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read on a missing sub.jsonl returned an error: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("recs = %v, want none", recs)
	}
}

func TestForTicketFilters(t *testing.T) {
	recs := []Record{
		{Ticket: "grove-289"},
		{Ticket: "grove-290"},
		{Ticket: "grove-289"},
	}
	got := ForTicket(recs, "grove-289")
	if len(got) != 2 {
		t.Fatalf("filtered = %d, want 2", len(got))
	}
	for _, r := range got {
		if r.Ticket != "grove-289" {
			t.Errorf("leaked record for %q", r.Ticket)
		}
	}
}
