package cost

import (
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/transcript"
)

func TestWindowSums(t *testing.T) {
	// A Wednesday: day starts 07-15, week (Monday) 07-13, month 07-01, year 01-01.
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	points := []Point{
		{Time: time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC), USD: 1},   // today
		{Time: time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC), USD: 2},   // this week
		{Time: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC), USD: 4},    // this month
		{Time: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), USD: 8},    // this year
		{Time: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), USD: 16}, // last year — dropped
		{Time: now.Add(time.Hour), USD: 32},                            // future — dropped
	}
	day, week, month, year := WindowSums(points, now)
	if day != 1 || week != 3 || month != 7 || year != 15 {
		t.Errorf("got day=%v week=%v month=%v year=%v, want 1/3/7/15", day, week, month, year)
	}
}

func TestSpendByModel(t *testing.T) {
	usd := 0.5
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	entries := []transcript.UsageEntry{
		{Model: "claude-fable-5", Output: 1_000_000, Timestamp: "2026-07-10T00:00:00Z"},     // priced: $50
		{Model: "claude-fable-5", CostUSD: &usd, Timestamp: "2026-07-11T00:00:00Z"},         // verbatim: $0.50
		{Model: "moonshotai/kimi-k3", Output: 1_000_000, Timestamp: "2026-07-11T00:00:00Z"}, // unpriced: skipped
		{Model: "claude-haiku-4-5", Output: 1_000_000, Timestamp: "2026-06-01T00:00:00Z"},   // before since
		{Model: "claude-haiku-4-5", Output: 1_000_000, Timestamp: "not-a-time"},             // unparseable
	}
	got := SpendByModel(entries, since)
	if len(got) != 1 {
		t.Fatalf("got %v, want only fable", got)
	}
	if v := got["fable"]; v < 50.49 || v > 50.51 {
		t.Errorf("fable = %v, want 50.50", v)
	}
}

func TestMixShares(t *testing.T) {
	got := MixShares("fable 92% · haiku 8%")
	if len(got) != 2 || got["fable"] < 0.919 || got["fable"] > 0.921 || got["haiku"] < 0.079 || got["haiku"] > 0.081 {
		t.Errorf("shares = %v", got)
	}
	if got := MixShares(""); got != nil {
		t.Errorf("empty mix = %v, want nil", got)
	}
	if got := MixShares("garbage"); got != nil {
		t.Errorf("garbage mix = %v, want nil", got)
	}
	// Round-trip: what Mix renders, MixShares must parse.
	tot := Total([]transcript.UsageEntry{
		{Model: "claude-fable-5", Output: 900_000},
		{Model: "claude-haiku-4-5", Output: 100_000},
	})
	rt := MixShares(tot.Mix())
	if len(rt) != 2 || rt["fable"] <= rt["haiku"] {
		t.Errorf("round-trip shares = %v (mix %q)", rt, tot.Mix())
	}
}

func TestSortedModelSpend(t *testing.T) {
	got := SortedModelSpend(map[string]float64{"a": 1, "b": 5, "c": 1})
	want := []ModelSpend{{"b", 5}, {"a", 1}, {"c", 1}}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("got %v, want %v", got, want)
	}
}
