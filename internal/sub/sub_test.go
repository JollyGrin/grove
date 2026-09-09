package sub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMissingFileIsEmptyNotError(t *testing.T) {
	recs, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read on a missing sub.jsonl returned an error: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("recs = %v, want none", recs)
	}
}

func TestReadSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	body := `{"time":"2026-09-06T10:00:00Z","v":1,"ticket":"grove-289","lane":"x","model":"y","mode":"raw","input_chars":10,"input_tokens":3,"output_tokens":1,"cached_tokens":0,"turns":0,"ms":5,"exit":0,"prompt_head":"p"}
not json at all
{"time":"2026-09-06T10:01:00Z","v":1,"ticket":"grove-290","lane":"x","model":"y","mode":"agentic","exit":1}
`
	if err := os.WriteFile(filepath.Join(dir, "sub.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	recs, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("recs = %d, want 2 (the malformed line must be skipped, not fatal)", len(recs))
	}
	if recs[0].Ticket != "grove-289" || recs[0].Mode != "raw" || recs[0].InputChars != 10 {
		t.Errorf("recs[0] = %+v", recs[0])
	}
	if recs[1].Ticket != "grove-290" || recs[1].Mode != "agentic" || recs[1].Exit != 1 {
		t.Errorf("recs[1] = %+v", recs[1])
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
