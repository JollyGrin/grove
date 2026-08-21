package cost

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestMixCompact(t *testing.T) {
	// Compact form: uppercase family initial + rounded percent, joined by "-".
	// Same USD-vs-token share basis as Mix — construct Totals directly to pin
	// each case's basis (CostKnown + USD decide USD-vs-token).
	cases := []struct {
		name string
		tot  Totals
		want string
	}{
		{
			name: "two models by USD",
			tot: Totals{CostKnown: true, USD: 5.0, Models: []ModelUsage{
				{Model: "claude-opus-4-8", USD: 4.9},
				{Model: "claude-haiku-4-5", USD: 0.1},
			}},
			want: "O98-H2",
		},
		{
			name: "three models by USD",
			tot: Totals{CostKnown: true, USD: 100, Models: []ModelUsage{
				{Model: "claude-opus-4-8", USD: 94},
				{Model: "claude-sonnet-5", USD: 3},
				{Model: "claude-haiku-4-5", USD: 3},
			}},
			want: "O94-S3-H3",
		},
		{
			name: "single model",
			tot: Totals{CostKnown: true, USD: 5.0, Models: []ModelUsage{
				{Model: "claude-fable-5", USD: 5.0},
			}},
			want: "F100",
		},
		{
			name: "empty",
			tot:  Totals{},
			want: "",
		},
		{
			// Unpriced model marks cost unknown → shares fall back to tokens so
			// the unknown model never reads as 0%.
			name: "token-share fallback",
			tot: Totals{CostKnown: false, Models: []ModelUsage{
				{Model: "claude-future-9", Tokens: 750_000},
				{Model: "claude-haiku-4-5", Tokens: 250_000},
			}},
			want: "F75-H25",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tot.MixCompact(); got != tc.want {
				t.Errorf("MixCompact() = %q, want %q", got, tc.want)
			}
		})
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

func TestRateForPrefixMatch(t *testing.T) {
	// Configure an OpenRouter key the way config `cost.pricing` would, so the
	// dated-slug case (transcript records z-ai/glm-5.2-20260616, config keys
	// z-ai/glm-5.2) resolves. Overrides mutates the package table; the keys
	// are distinctive enough not to collide with other tests.
	Overrides(map[string]Rates{
		"z-ai/glm-5.2":     derive(0.6, 2.2),
		"z-ai/glm-4.5-air": derive(0.2, 1.1),
	})
	cases := []struct {
		name  string
		model string
		want  bool
	}{
		{"exact anthropic", "claude-opus-4-8", true},
		{"dated anthropic snapshot", "claude-haiku-4-5-20251001", true},
		{"exact openrouter key", "z-ai/glm-5.2", true},
		{"dated openrouter slug", "z-ai/glm-5.2-20260616", true},
		{"version bump is not a prefix match", "z-ai/glm-5.20", false},
		{"unknown model", "some/unlisted-model", false},
	}
	for _, tc := range cases {
		if _, ok := rateFor(tc.model); ok != tc.want {
			t.Errorf("%s: rateFor(%q) ok = %v, want %v", tc.name, tc.model, ok, tc.want)
		}
	}
	// Longest-wins: a dated 4.8 opus keeps the $5/$25 rate, never sliding onto
	// the bare claude-opus-4 ($15/$75) key that is also a prefix.
	if r, _ := rateFor("claude-opus-4-8-20260101"); r.Input != 5 {
		t.Errorf("dated opus 4.8 Input = %v, want 5 (longest-prefix must beat claude-opus-4)", r.Input)
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

func TestEntriesForEvictsStaleGenerations(t *testing.T) {
	// A live worker's transcript mutates mtime/size continuously — the
	// cache must hold only the newest (mtime,size) generation per path,
	// never accumulate one entry per historical poll.
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")

	c := NewCache()
	line := `{"type":"assistant","requestId":"r1","message":{"id":"m1","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(path, []byte(strings.Repeat(line+"\n", i+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		mtime := time.Now().Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		if _, err := c.entriesFor(path); err != nil {
			t.Fatal(err)
		}
	}

	if got := len(c.byFile); got != 1 {
		t.Errorf("byFile has %d entries after 3 generations of one path, want 1", got)
	}
	if c.parses != 3 {
		t.Errorf("parses = %d, want 3 (re-parse on every mtime/size change)", c.parses)
	}
}

func TestEntriesForCachesDistinctPathsIndependently(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.jsonl")
	pathB := filepath.Join(dir, "b.jsonl")
	line := `{"type":"assistant","requestId":"r1","message":{"id":"m1","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`
	if err := os.WriteFile(pathA, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewCache()
	if _, err := c.entriesFor(pathA); err != nil {
		t.Fatal(err)
	}
	if _, err := c.entriesFor(pathB); err != nil {
		t.Fatal(err)
	}

	if got := len(c.byFile); got != 2 {
		t.Errorf("byFile has %d entries for 2 distinct paths, want 2", got)
	}

	parsesBefore := c.parses
	if _, err := c.entriesFor(pathA); err != nil {
		t.Fatal(err)
	}
	if _, err := c.entriesFor(pathB); err != nil {
		t.Fatal(err)
	}
	if c.parses != parsesBefore {
		t.Errorf("unchanged files reparsed: %d → %d", parsesBefore, c.parses)
	}
	if got := len(c.byFile); got != 2 {
		t.Errorf("byFile has %d entries after re-fetch, want 2", got)
	}
}

// writeSession drops one single-entry session file for wt into its
// transcript project dir and returns the file's path.
func writeSession(t *testing.T, wt, name, reqID string) string {
	t.Helper()
	projDir := transcript.ProjectDir(wt)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","requestId":"` + reqID + `","message":{"id":"m-` + reqID + `","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`
	path := filepath.Join(projDir, name)
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRetainEvictsUnkeptPathsOnly(t *testing.T) {
	// grove-165: entries for two worktrees, Retain with only one's files →
	// internal maps hold exactly the retained path, and a later fetch of the
	// evicted path re-parses cleanly.
	t.Setenv("GV_CLAUDE_CONFIG_DIR", t.TempDir())
	wtA, wtB := t.TempDir(), t.TempDir()
	pathA := writeSession(t, wtA, "sess-a.jsonl", "ra")
	pathB := writeSession(t, wtB, "sess-b.jsonl", "rb")

	c := NewCache()
	seen := map[string]struct{}{}
	if _, err := c.UsageForTaskCollect(wtA, seen); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UsageForTaskCollect(wtB, seen); err != nil {
		t.Fatal(err)
	}
	if _, ok := seen[pathA]; !ok {
		t.Errorf("sweep did not collect %s", pathA)
	}
	if _, ok := seen[pathB]; !ok {
		t.Errorf("sweep did not collect %s", pathB)
	}

	c.Retain(map[string]struct{}{pathA: {}})
	if len(c.byFile) != 1 || len(c.latest) != 1 {
		t.Fatalf("after Retain: byFile=%d latest=%d, want 1/1", len(c.byFile), len(c.latest))
	}
	if _, ok := c.latest[pathA]; !ok {
		t.Fatalf("retained path %s missing from latest", pathA)
	}
	for key := range c.byFile {
		if key.path != pathA {
			t.Fatalf("byFile holds evicted path %s", key.path)
		}
	}

	// The evicted path re-parses cleanly on the next ask.
	parsesBefore := c.parses
	tot, err := c.ForTask(wtB)
	if err != nil {
		t.Fatal(err)
	}
	if tot.Input != 100 || tot.Turns != 1 {
		t.Errorf("re-parse after eviction wrong: %+v", tot)
	}
	if c.parses != parsesBefore+1 {
		t.Errorf("parses = %d, want %d (evicted file must re-parse)", c.parses, parsesBefore+1)
	}
}

func TestPerTaskCallersNeverEvict(t *testing.T) {
	// grove-165: audit's per-task goroutines go through ForTask/UsageForTask,
	// which must never evict — only a full-fleet sweep's explicit Retain may.
	t.Setenv("GV_CLAUDE_CONFIG_DIR", t.TempDir())
	wtA, wtB := t.TempDir(), t.TempDir()
	writeSession(t, wtA, "sess-a.jsonl", "ra")
	writeSession(t, wtB, "sess-b.jsonl", "rb")

	c := NewCache()
	if _, err := c.ForTask(wtA); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ForTask(wtB); err != nil {
		t.Fatal(err)
	}

	// Re-asking about one task must leave the sibling's entries cached.
	parsesBefore := c.parses
	if _, err := c.ForTask(wtA); err != nil {
		t.Fatal(err)
	}
	if len(c.byFile) != 2 || len(c.latest) != 2 {
		t.Errorf("per-task call evicted a sibling: byFile=%d latest=%d, want 2/2", len(c.byFile), len(c.latest))
	}
	if _, err := c.ForTask(wtB); err != nil {
		t.Fatal(err)
	}
	if c.parses != parsesBefore {
		t.Errorf("per-task churn reparsed files: %d → %d", parsesBefore, c.parses)
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
