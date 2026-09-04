package cost

import (
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/transcript"
)

func TestPoints(t *testing.T) {
	entries := []transcript.UsageEntry{
		{Model: "claude-sonnet-5", Output: 1_000_000, Timestamp: "2026-07-07T10:00:00.000Z"},
		{Model: "claude-sonnet-5", Output: 1_000_000, Timestamp: "not-a-time"},         // skipped
		{Model: "who-knows", Output: 1_000_000, Timestamp: "2026-07-07T11:00:00.000Z"}, // unknown model: no $ → skipped
	}
	pts := Points(entries)
	if len(pts) != 1 {
		t.Fatalf("points = %d, want 1 (%+v)", len(pts), pts)
	}
	if pts[0].USD != 10 { // sonnet-5 output $10/MTok
		t.Errorf("USD = %v, want 10", pts[0].USD)
	}
	want := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	if !pts[0].Time.Equal(want) {
		t.Errorf("time = %v, want %v", pts[0].Time, want)
	}
}

func TestPointsPreferEntryCost(t *testing.T) {
	c := 0.5
	pts := Points([]transcript.UsageEntry{
		{Model: "who-knows", CostUSD: &c, Timestamp: "2026-07-07T10:00:00Z"},
	})
	if len(pts) != 1 || pts[0].USD != 0.5 {
		t.Fatalf("per-entry costUSD must be used verbatim: %+v", pts)
	}
}

func TestBuckets(t *testing.T) {
	now := time.Date(2026, 7, 7, 14, 30, 0, 0, time.UTC)
	pts := []Point{
		{Time: now.Add(-10 * time.Minute), USD: 1},              // current hour
		{Time: now.Add(-1 * time.Hour), USD: 2},                 // previous hour
		{Time: now.Add(-1*time.Hour - 5*time.Minute), USD: 0.5}, // same previous hour
		{Time: now.Add(-100 * time.Hour), USD: 99},              // out of range: dropped
	}
	b := Buckets(pts, Hourly, 6, now, time.UTC)
	if len(b) != 6 {
		t.Fatalf("buckets = %d, want 6", len(b))
	}
	if !b[0].Start.Before(b[5].Start) {
		t.Error("buckets must be oldest-first")
	}
	last := b[5]
	if !last.Start.Equal(time.Date(2026, 7, 7, 14, 0, 0, 0, time.UTC)) {
		t.Errorf("last bucket start = %v", last.Start)
	}
	if last.USD != 1 {
		t.Errorf("current-hour USD = %v, want 1", last.USD)
	}
	if b[4].USD != 2.5 {
		t.Errorf("previous-hour USD = %v, want 2.5", b[4].USD)
	}
	if b[0].USD != 0 {
		t.Errorf("empty bucket must be zero-filled, got %v", b[0].USD)
	}
}

func TestBucketsDailyAndWeekly(t *testing.T) {
	now := time.Date(2026, 7, 7, 14, 30, 0, 0, time.UTC) // a Tuesday
	pts := []Point{
		{Time: time.Date(2026, 7, 6, 23, 0, 0, 0, time.UTC), USD: 3}, // yesterday (Monday)
		{Time: now, USD: 1},
	}
	d := Buckets(pts, Daily, 3, now, time.UTC)
	if d[2].USD != 1 || d[1].USD != 3 {
		t.Errorf("daily: today=%v yesterday=%v, want 1 and 3", d[2].USD, d[1].USD)
	}
	w := Buckets(pts, Weekly, 2, now, time.UTC)
	// Both points fall in the week starting Monday 2026-07-06.
	if w[1].USD != 4 {
		t.Errorf("weekly current = %v, want 4", w[1].USD)
	}
	if !w[1].Start.Equal(time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("week must start Monday, got %v", w[1].Start)
	}
}

func TestBucketUnitLabels(t *testing.T) {
	at := time.Date(2026, 7, 7, 14, 0, 0, 0, time.UTC)
	if got := Hourly.Label(at, time.UTC); got != "14:00" {
		t.Errorf("hourly label = %q", got)
	}
	if got := Daily.Label(at, time.UTC); got != "07-07" {
		t.Errorf("daily label = %q", got)
	}
	if got := Weekly.Label(at, time.UTC); got != "07-07" {
		t.Errorf("weekly label = %q", got)
	}
	if Hourly.String() != "hourly" || Daily.String() != "daily" || Weekly.String() != "weekly" {
		t.Error("unit names")
	}
}

// TestBucketsLocalTimezone proves buckets key on the operator's wall clock, not
// UTC: this is the grove-51 fix. A fixed zone (never time.Local) keeps it
// deterministic on any CI box.
func TestBucketsLocalTimezone(t *testing.T) {
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	// Reproduction case: 14:00Z in summer (+2 CEST) is local 16:00. now must
	// sit past the point's bucket so the window includes it.
	now := time.Date(2026, 7, 7, 18, 0, 0, 0, time.UTC)
	pt := Point{Time: time.Date(2026, 7, 7, 14, 0, 0, 0, time.UTC), USD: 5}
	b := Buckets([]Point{pt}, Hourly, 6, now, ams)
	var found *Bucket
	for i := range b {
		if b[i].USD > 0 {
			found = &b[i]
		}
	}
	if found == nil {
		t.Fatal("spend point landed in no bucket")
	}
	if got := Hourly.Label(found.Start, ams); got != "16:00" {
		t.Errorf("14:00Z (+2) bucket label = %q, want 16:00", got)
	}
}

// TestBucketsDailyLocalBoundary: a point at 23:00Z Monday is local Tuesday in
// Amsterdam (+2) and must bucket into the local Tuesday, not UTC Monday.
func TestBucketsDailyLocalBoundary(t *testing.T) {
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC) // local Tue afternoon
	pt := Point{Time: time.Date(2026, 7, 6, 23, 0, 0, 0, time.UTC), USD: 3}
	d := Buckets([]Point{pt}, Daily, 3, now, ams)
	want := time.Date(2026, 7, 7, 0, 0, 0, 0, ams) // local Tuesday midnight
	last := d[len(d)-1]
	if !last.Start.Equal(want) {
		t.Errorf("newest daily bucket start = %v, want local Tue %v", last.Start, want)
	}
	if last.USD != 3 {
		t.Errorf("23:00Z Monday must count as local Tuesday: today=%v, want 3", last.USD)
	}
}

// TestBucketsFractionalOffset proves the time.Date-vs-Truncate distinction:
// India is +5:30, so UTC hour boundaries do not align with local hour
// boundaries. time.Truncate(time.Hour) would key on a :30 wall-clock start.
func TestBucketsFractionalOffset(t *testing.T) {
	kol, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	// 06:10Z is 11:40 IST → local 11:00 hour.
	pt := Point{Time: time.Date(2026, 7, 7, 6, 10, 0, 0, time.UTC), USD: 2}
	b := Buckets([]Point{pt}, Hourly, 12, now, kol)
	var found *Bucket
	for i := range b {
		if b[i].USD > 0 {
			found = &b[i]
		}
	}
	if found == nil {
		t.Fatal("spend point landed in no bucket")
	}
	local := found.Start.In(kol)
	if local.Minute() != 0 {
		t.Errorf("bucket start not on a local hour boundary: %v (min=%d)", local, local.Minute())
	}
	if got := Hourly.Label(found.Start, kol); got != "11:00" {
		t.Errorf("06:10Z (+5:30) bucket label = %q, want 11:00", got)
	}
}

func TestBar(t *testing.T) {
	if got := Bar(0, 10, 20); got != "" {
		t.Errorf("zero bar = %q, want empty", got)
	}
	if got := Bar(10, 10, 20); len([]rune(got)) != 20 {
		t.Errorf("max bar width = %d, want 20", len([]rune(got)))
	}
	got := Bar(0.001, 10, 20)
	if len([]rune(got)) != 1 {
		t.Errorf("tiny nonzero value must still show one cell: %q", got)
	}
	if strings.Trim(got, "▇") != "" {
		t.Errorf("bar uses unexpected runes: %q", got)
	}
	if Bar(5, 0, 20) != "" {
		t.Error("max=0 must not divide by zero")
	}
}
