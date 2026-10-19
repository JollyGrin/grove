package transcript

import (
	"os"
	"testing"
)

func loadContextFixture(t *testing.T) []Line {
	t.Helper()
	f, err := os.Open("testdata/context.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return ParseLines(f)
}

func TestParseLinesBlocks(t *testing.T) {
	lines := loadContextFixture(t)
	if len(lines) != 12 {
		t.Fatalf("lines = %d, want 12", len(lines))
	}

	// Two assistant lines share one message id — a multi-block message
	// split across JSONL lines.
	if lines[1].MessageID != "msg-1" || lines[2].MessageID != "msg-1" {
		t.Fatalf("multi-block message ids: %q, %q", lines[1].MessageID, lines[2].MessageID)
	}
	if lines[1].RequestID != "req-1" || lines[2].RequestID != "req-1" {
		t.Fatalf("multi-block message requestIds: %q, %q", lines[1].RequestID, lines[2].RequestID)
	}
	if len(lines[1].Blocks) != 1 || lines[1].Blocks[0].Kind != "text" {
		t.Fatalf("line 1 blocks = %+v, want one text block", lines[1].Blocks)
	}

	// tool_use Read, then its string-content tool_result resolves the name.
	if len(lines[2].Blocks) != 1 || lines[2].Blocks[0].Kind != "tool_use" || lines[2].Blocks[0].Name != "Read" {
		t.Fatalf("line 2 blocks = %+v, want one Read tool_use", lines[2].Blocks)
	}
	if got := lines[2].Blocks[0].Input["file_path"]; got != "internal/x.go" {
		t.Errorf("tool_use input file_path = %v, want internal/x.go", got)
	}
	readResult := lines[3].Blocks[0]
	if readResult.Kind != "tool_result" || readResult.Name != "Read" {
		t.Fatalf("read result block = %+v, want tool_result named Read", readResult)
	}
	wantText := "package main\n\nfunc main() {}\n"
	if readResult.Chars != len(wantText) {
		t.Errorf("read result chars = %d, want %d", readResult.Chars, len(wantText))
	}
	if readResult.Lines != 3 {
		t.Errorf("read result lines = %d, want 3", readResult.Lines)
	}

	// Bash tool_use + list-content tool_result.
	bashUse := lines[4].Blocks[0]
	if bashUse.Kind != "tool_use" || bashUse.Name != "Bash" {
		t.Fatalf("bash tool_use = %+v", bashUse)
	}
	bashResult := lines[5].Blocks[0]
	if bashResult.Kind != "tool_result" || bashResult.Name != "Bash" {
		t.Fatalf("bash result block = %+v, want tool_result named Bash", bashResult)
	}
	wantBash := "package y\n\nfunc Y() {}\n"
	if bashResult.Chars != len(wantBash) {
		t.Errorf("bash result chars = %d, want %d", bashResult.Chars, len(wantBash))
	}
	if bashResult.Lines != 3 {
		t.Errorf("bash result lines = %d, want 3", bashResult.Lines)
	}
}

func TestParseLinesCompact(t *testing.T) {
	lines := loadContextFixture(t)
	c := lines[6]
	if c.Type != "system" || c.Subtype != "compact_boundary" {
		t.Fatalf("compact line = %+v", c)
	}
	if c.Compact == nil {
		t.Fatal("Compact is nil")
	}
	if c.Compact.Trigger != "auto" || c.Compact.PreTokens != 150000 || c.Compact.PostTokens != 9000 {
		t.Errorf("compact = %+v, want {auto 150000 9000}", *c.Compact)
	}
}

func TestParseLinesSyntheticKept(t *testing.T) {
	lines := loadContextFixture(t)
	s := lines[7]
	if s.Model != "<synthetic>" {
		t.Fatalf("line 7 model = %q, want <synthetic>", s.Model)
	}
	if s.Usage == nil {
		t.Fatal("synthetic line's usage was dropped — ParseLines must keep it (cost dedups later)")
	}
	if s.Usage.Input != 10 || s.Usage.Output != 5 {
		t.Errorf("synthetic usage = %+v, want input 10 output 5", *s.Usage)
	}
}
