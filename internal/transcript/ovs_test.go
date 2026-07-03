package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixture lines mirror live shapes captured 2026-07-02 (DEV-4733's
// transcript): usage under message.usage, cache_creation breakdown object,
// requestId at top level, no costUSD on current CC versions.
const (
	assistantLine = `{"type":"assistant","sessionId":"s-1","requestId":"req_A","timestamp":"2026-07-01T10:00:00Z","version":"2.1.197","isSidechain":false,"message":{"id":"msg_A","model":"claude-opus-4-8","role":"assistant","usage":{"input_tokens":9868,"cache_creation_input_tokens":22022,"cache_read_input_tokens":17707,"output_tokens":171,"cache_creation":{"ephemeral_1h_input_tokens":22022,"ephemeral_5m_input_tokens":0}}}}`
	// Older shape: flat cache_creation_input_tokens only, plus a
	// pre-computed costUSD.
	oldStyleLine  = `{"type":"assistant","sessionId":"s-1","requestId":"req_B","costUSD":0.42,"message":{"id":"msg_B","model":"claude-sonnet-5","usage":{"input_tokens":100,"cache_creation_input_tokens":50,"cache_read_input_tokens":200,"output_tokens":30}}}`
	userLine      = `{"type":"user","sessionId":"s-1","message":{"role":"user","content":"do the thing"}}`
	apiErrorLine  = `{"type":"assistant","isApiErrorMessage":true,"requestId":"req_C","message":{"id":"msg_C","model":"<synthetic>","usage":{"input_tokens":5,"output_tokens":1}}}`
	syntheticLine = `{"type":"assistant","requestId":"req_D","message":{"id":"msg_D","model":"<synthetic>","usage":{"input_tokens":5,"output_tokens":1}}}`
)

func TestParseUsageAssistant(t *testing.T) {
	entries := ParseUsage(strings.NewReader(assistantLine + "\n"))
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.MessageID != "msg_A" || e.RequestID != "req_A" || e.Model != "claude-opus-4-8" {
		t.Errorf("identity fields: %+v", e)
	}
	if e.Input != 9868 || e.Output != 171 || e.CacheRead != 17707 {
		t.Errorf("token fields: %+v", e)
	}
	// New breakdown wins: all 22022 creation tokens are 1h.
	if e.CacheCreate1h != 22022 || e.CacheCreate5m != 0 {
		t.Errorf("cache breakdown: 1h=%d 5m=%d", e.CacheCreate1h, e.CacheCreate5m)
	}
	if e.CostUSD != nil {
		t.Error("no costUSD in current shape")
	}
}

func TestParseUsageOldStyleFlatCache(t *testing.T) {
	entries := ParseUsage(strings.NewReader(oldStyleLine + "\n"))
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	// Flat cache_creation_input_tokens maps to the 5m bucket.
	if e.CacheCreate5m != 50 || e.CacheCreate1h != 0 {
		t.Errorf("flat cache mapping: 5m=%d 1h=%d", e.CacheCreate5m, e.CacheCreate1h)
	}
	if e.CostUSD == nil || *e.CostUSD != 0.42 {
		t.Errorf("costUSD = %v, want 0.42", e.CostUSD)
	}
}

func TestParseUsageSkipsNonBilling(t *testing.T) {
	input := userLine + "\n" + apiErrorLine + "\n" + syntheticLine + "\n" + assistantLine + "\n"
	entries := ParseUsage(strings.NewReader(input))
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (user/apiError/synthetic skipped)", len(entries))
	}
}

func TestParseUsageToleratesGarbage(t *testing.T) {
	input := assistantLine + "\n" + `{"torn":"lin` + "\n" + "not json at all\n"
	entries := ParseUsage(strings.NewReader(input))
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (garbage skipped, prefix kept)", len(entries))
	}
}

func TestSessionFiles(t *testing.T) {
	// Two sessions (grab + adopt shape), each with a subagent file — the
	// undercount regression case: everything must be found.
	wt := t.TempDir()
	projDir := ProjectDir(wt)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel string) {
		t.Helper()
		p := filepath.Join(projDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("sess-1.jsonl")
	write("sess-2.jsonl")
	write("sess-1/subagents/agent-aaa.jsonl")
	write("sess-1/subagents/agent-aaa.meta.json") // must be ignored
	write("sess-2/subagents/agent-bbb.jsonl")

	files, err := SessionFiles(wt)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, strings.TrimPrefix(f, projDir+string(os.PathSeparator)))
	}
	want := map[string]bool{
		"sess-1.jsonl": true, "sess-2.jsonl": true,
		filepath.Join("sess-1", "subagents", "agent-aaa.jsonl"): true,
		filepath.Join("sess-2", "subagents", "agent-bbb.jsonl"): true,
	}
	if len(files) != len(want) {
		t.Fatalf("files = %v, want %d entries", names, len(want))
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected file %s", n)
		}
	}
}

func TestSessionFilesMissingDir(t *testing.T) {
	files, err := SessionFiles(filepath.Join(t.TempDir(), "never-a-worktree"))
	if err != nil {
		t.Fatalf("missing project dir must not error, got %v", err)
	}
	if len(files) != 0 {
		t.Errorf("files = %v, want none", files)
	}
}
