package cost

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/JollyGrin/grove/internal/transcript"
)

func fe(msgID, reqID, model string, in, out, c5m, c1h, read int) transcript.UsageEntry {
	return transcript.UsageEntry{
		MessageID: msgID, RequestID: reqID, Model: model,
		Input: in, Output: out, CacheCreate5m: c5m, CacheCreate1h: c1h, CacheRead: read,
	}
}

func TestDedup(t *testing.T) {
	entries := []transcript.UsageEntry{
		fe("msg_A", "req_1", "claude-opus-4-8", 100, 10, 0, 0, 0),
		fe("msg_A", "req_1", "claude-opus-4-8", 100, 10, 0, 0, 0), // resume re-emitted
		fe("msg_B", "req_2", "claude-opus-4-8", 50, 5, 0, 0, 0),
	}
	got := Dedup(entries)
	if len(got) != 2 {
		t.Fatalf("Dedup = %d entries, want 2", len(got))
	}
}

func TestTotalComputed(t *testing.T) {
	// Opus 4.8: $5 in / $25 out per MTok; cache read 0.1×in, 5m write
	// 1.25×in, 1h write 2×in.
	entries := []transcript.UsageEntry{
		fe("m1", "r1", "claude-opus-4-8", 1_000_000, 1_000_000, 1_000_000, 1_000_000, 1_000_000),
	}
	tot := Total(entries)
	want := 5.0 + 25.0 + (1.25 * 5.0) + (2.0 * 5.0) + (0.1 * 5.0)
	if math.Abs(tot.USD-want) > 1e-9 {
		t.Errorf("USD = %v, want %v", tot.USD, want)
	}
	if !tot.CostKnown {
		t.Error("cost should be known for a priced model")
	}
	if tot.Turns != 1 || tot.Input != 1_000_000 || tot.CacheRead != 1_000_000 {
		t.Errorf("totals: %+v", tot)
	}
}

func TestTotalCostUSDPreferred(t *testing.T) {
	c := 0.42
	e := fe("m1", "r1", "claude-sonnet-5", 100, 10, 0, 0, 0)
	e.CostUSD = &c
	tot := Total([]transcript.UsageEntry{e})
	if math.Abs(tot.USD-0.42) > 1e-9 {
		t.Errorf("USD = %v, want the entry's own costUSD 0.42", tot.USD)
	}
}

func TestTotalUnknownModel(t *testing.T) {
	tot := Total([]transcript.UsageEntry{
		fe("m1", "r1", "claude-future-9", 100, 10, 0, 0, 0),
	})
	if tot.CostKnown {
		t.Error("unknown model must mark cost unknown, not zero-and-confident")
	}
	if tot.Input != 100 || tot.Output != 10 {
		t.Error("tokens must still be counted for unknown models")
	}
}

func TestTotalPerModelSubtotals(t *testing.T) {
	// Two models in one ticket: opus (dominant) + haiku. Subtotals must be
	// kept per model, dominant-by-USD first, and sum back to the grand total.
	entries := []transcript.UsageEntry{
		fe("m1", "r1", "claude-opus-4-8", 1_000_000, 0, 0, 0, 0), // $5
		fe("m2", "r2", "claude-haiku-4-5", 100_000, 0, 0, 0, 0),  // $0.1
	}
	tot := Total(entries)
	if len(tot.Models) != 2 {
		t.Fatalf("want 2 model subtotals, got %d (%+v)", len(tot.Models), tot.Models)
	}
	if tot.Models[0].Model != "claude-opus-4-8" {
		t.Errorf("dominant model first: got %q", tot.Models[0].Model)
	}
	if math.Abs(tot.Models[0].USD-5.0) > 1e-9 || math.Abs(tot.Models[1].USD-0.1) > 1e-9 {
		t.Errorf("per-model USD wrong: %+v", tot.Models)
	}
	if tot.Models[0].Tokens != 1_000_000 || tot.Models[1].Tokens != 100_000 {
		t.Errorf("per-model tokens wrong: %+v", tot.Models)
	}
	var sum float64
	for _, m := range tot.Models {
		sum += m.USD
	}
	if math.Abs(sum-tot.USD) > 1e-9 {
		t.Errorf("subtotals %v don't sum to grand total %v", sum, tot.USD)
	}
	if got := tot.Mix(); got != "opus 98% · haiku 2%" {
		t.Errorf("Mix() = %q, want %q", got, "opus 98% · haiku 2%")
	}
}

func TestMixFallsBackToTokensWhenCostUnknown(t *testing.T) {
	// An unpriced model marks cost unknown; the mix must still split by
	// tokens so the unknown model never silently reads as 0%.
	entries := []transcript.UsageEntry{
		fe("m1", "r1", "claude-future-9", 750_000, 0, 0, 0, 0),
		fe("m2", "r2", "claude-haiku-4-5", 250_000, 0, 0, 0, 0),
	}
	tot := Total(entries)
	if tot.CostKnown {
		t.Fatal("unknown model should mark cost unknown")
	}
	if got := tot.Mix(); got != "future 75% · haiku 25%" {
		t.Errorf("Mix() = %q, want token-share %q", got, "future 75% · haiku 25%")
	}
}

func TestMixEmptyWhenNoModels(t *testing.T) {
	if got := (Totals{}).Mix(); got != "" {
		t.Errorf("Mix() with no models = %q, want empty", got)
	}
}

func TestShortModel(t *testing.T) {
	cases := map[string]string{
		"claude-fable-5":            "fable",
		"claude-opus-4-8":           "opus",
		"claude-haiku-4-5-20251001": "haiku",
		"claude-sonnet-5":           "sonnet",
		"gpt-4o":                    "gpt-4o", // non-claude id passes through
	}
	for in, want := range cases {
		if got := ShortModel(in); got != want {
			t.Errorf("ShortModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRateLookupNormalizesDatedIDs(t *testing.T) {
	if _, ok := rateFor("claude-haiku-4-5-20251001"); !ok {
		t.Error("dated snapshot id should resolve via suffix normalization")
	}
	if _, ok := rateFor("claude-opus-4-8"); !ok {
		t.Error("bare alias should resolve")
	}
}

func TestForTaskSumsAllSessionsWithCache(t *testing.T) {
	// Two-session fixture (grab + adopt shape) — the undercount regression
	// case: both sessions' usage must be summed. Also asserts the
	// mtime+size cache: a second call must not reparse unchanged files.
	wt := t.TempDir()
	projDir := transcript.ProjectDir(wt)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line1 := `{"type":"assistant","requestId":"r1","message":{"id":"m1","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`
	line2 := `{"type":"assistant","requestId":"r2","message":{"id":"m2","model":"claude-opus-4-8","usage":{"input_tokens":200,"output_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`
	os.WriteFile(filepath.Join(projDir, "sess-1.jsonl"), []byte(line1+"\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "sess-2.jsonl"), []byte(line2+"\n"), 0o644)

	c := NewCache()
	tot, err := c.ForTask(wt)
	if err != nil {
		t.Fatal(err)
	}
	if tot.Input != 300 || tot.Output != 30 || tot.Turns != 2 {
		t.Fatalf("two-session sum wrong: %+v", tot)
	}

	parsesBefore := c.parses
	if _, err := c.ForTask(wt); err != nil {
		t.Fatal(err)
	}
	if c.parses != parsesBefore {
		t.Errorf("unchanged files reparsed: %d → %d", parsesBefore, c.parses)
	}
}

func TestStuckFlag(t *testing.T) {
	if !StuckFlag(35, 30, false) {
		t.Error("over-threshold turns with no delivery should flag")
	}
	if StuckFlag(35, 30, true) {
		t.Error("delivery movement should clear the flag")
	}
	if StuckFlag(10, 30, false) {
		t.Error("under threshold should not flag")
	}
}

func TestAnalyzeFlags(t *testing.T) {
	if !SteeringAnomaly(10, 30) {
		t.Error("10 steers over 30 turns (33%) should flag (>25%)")
	}
	if SteeringAnomaly(2, 30) {
		t.Error("2/30 should not flag")
	}
	if SteeringAnomaly(1, 0) {
		t.Error("zero turns should never flag")
	}
	if !CostOutlier(10.0, 4.0) {
		t.Error("2.5× median should flag (≥2×)")
	}
	if CostOutlier(6.0, 4.0) {
		t.Error("1.5× median should not flag")
	}
	if CostOutlier(10.0, 0) {
		t.Error("zero median should never flag")
	}
}
