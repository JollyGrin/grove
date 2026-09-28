package transcript

import (
	"os"
	"strings"
	"testing"
)

func openBootFixture(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open("testdata/boot.jsonl")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestParseBootStopsAtFirstCall(t *testing.T) {
	first, lines := ParseBoot(openBootFixture(t))

	if first == nil {
		t.Fatal("expected a billable first call, got nil")
	}
	if first.MessageID != "msg_real1" {
		t.Errorf("first.MessageID = %q, want msg_real1 (sidechain/synthetic lines must be skipped)", first.MessageID)
	}
	if first.Input != 5000 || first.CacheCreate5m != 800 || first.CacheCreate1h != 200 || first.CacheRead != 2000 {
		t.Errorf("first usage = %+v, unexpected", first)
	}

	// The fixture has 9 pre-call lines (7 attachments + 2 user lines); the
	// sidechain and synthetic assistant lines are consumed by the scan but
	// never appended to lines, and the second assistant line is never reached.
	if len(lines) != 9 {
		t.Fatalf("len(lines) = %d, want 9 (sidechain/synthetic must not be treated as the first call, nor appended as boot lines)", len(lines))
	}
	for _, l := range lines {
		if l.Type == "assistant" {
			t.Errorf("assistant line leaked into boot lines: %+v", l)
		}
	}
}

func TestBootImageNotCounted(t *testing.T) {
	_, lines := ParseBoot(openBootFixture(t))

	var listLine *BootLine
	for i := range lines {
		if lines[i].Type == "user" && lines[i].HasImage {
			listLine = &lines[i]
		}
	}
	if listLine == nil {
		t.Fatal("expected a user line with an image block")
	}
	if listLine.UserChars != len("RRRRR") {
		t.Errorf("UserChars = %d, want %d (text block only, image must never be counted)", listLine.UserChars, len("RRRRR"))
	}
	if !listLine.HasImage {
		t.Error("HasImage = false, want true")
	}
}

func TestBootTornLine(t *testing.T) {
	body := `{"type":"attachment","attachment":{"type":"skill_listing","content":"ABC","skillCount":1}}
{"type":"assistant","message":{"id":"msg1","model":"claude-sonnet-5","usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}},"requestId":"req1","timestamp":"t1"}
{"type":"attachment","attachment":{"type":"skill_lis`

	first, lines := ParseBoot(strings.NewReader(body))
	if first == nil {
		t.Fatal("expected a billable first call despite the torn trailing line")
	}
	if first.MessageID != "msg1" {
		t.Errorf("first.MessageID = %q, want msg1", first.MessageID)
	}
	if len(lines) != 1 {
		t.Fatalf("len(lines) = %d, want 1", len(lines))
	}
}

func TestParseBootNoCall(t *testing.T) {
	body := `{"type":"attachment","attachment":{"type":"skill_listing","content":"ABC","skillCount":1}}
{"type":"user","message":{"content":"hello"}}
`
	first, lines := ParseBoot(strings.NewReader(body))
	if first != nil {
		t.Errorf("first = %+v, want nil (no billable assistant line in transcript)", first)
	}
	if len(lines) != 2 {
		t.Fatalf("len(lines) = %d, want 2", len(lines))
	}
}
