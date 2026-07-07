package cost

import (
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/transcript"
)

// Point is one timestamped spend amount — from a transcript entry (precise)
// or a ledger snapshot delta (coarse) — the common input to Buckets.
type Point struct {
	Time time.Time
	USD  float64
}

// Points converts usage entries to chartable spend points. Entries with an
// unparseable timestamp or no computable cost (unknown model, no per-entry
// costUSD) are skipped: the chart under-reports rather than guessing.
func Points(entries []transcript.UsageEntry) []Point {
	var pts []Point
	for _, e := range entries {
		at, err := time.Parse(time.RFC3339, e.Timestamp)
		if err != nil {
			continue
		}
		usd, ok := entryUSD(e)
		if !ok || usd == 0 {
			continue
		}
		pts = append(pts, Point{Time: at, USD: usd})
	}
	return pts
}

// entryUSD prices one entry the same way Total does: per-entry costUSD
// verbatim when present, else the pricing table; unknown models are !ok.
func entryUSD(e transcript.UsageEntry) (float64, bool) {
	if e.CostUSD != nil {
		return *e.CostUSD, true
	}
	r, ok := rateFor(e.Model)
	if !ok {
		return 0, false
	}
	return (r.Input*float64(e.Input) +
		r.Output*float64(e.Output) +
		r.CacheWrite5m*float64(e.CacheCreate5m) +
		r.CacheWrite1h*float64(e.CacheCreate1h) +
		r.CacheRead*float64(e.CacheRead)) / 1e6, true
}

// BucketUnit is the chart granularity toggle: hourly / daily / weekly.
type BucketUnit int

const (
	Hourly BucketUnit = iota
	Daily
	Weekly
)

func (u BucketUnit) String() string {
	switch u {
	case Daily:
		return "daily"
	case Weekly:
		return "weekly"
	default:
		return "hourly"
	}
}

// Next cycles hourly → daily → weekly → hourly (the cockpit's b key).
func (u BucketUnit) Next() BucketUnit { return (u + 1) % 3 }

// truncate maps a time to its bucket start (UTC; weeks start Monday).
func (u BucketUnit) truncate(t time.Time) time.Time {
	t = t.UTC()
	switch u {
	case Daily:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case Weekly:
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		back := (int(d.Weekday()) + 6) % 7 // Monday=0 … Sunday=6
		return d.AddDate(0, 0, -back)
	default:
		return t.Truncate(time.Hour)
	}
}

// prev steps one bucket back from a bucket start.
func (u BucketUnit) prev(t time.Time) time.Time {
	switch u {
	case Daily:
		return t.AddDate(0, 0, -1)
	case Weekly:
		return t.AddDate(0, 0, -7)
	default:
		return t.Add(-time.Hour)
	}
}

// Label renders a bucket start for the chart's axis.
func (u BucketUnit) Label(t time.Time) string {
	if u == Hourly {
		return t.Format("15:04")
	}
	return t.Format("01-02")
}

// Bucket is one chart bar: all spend points whose time falls in
// [Start, next bucket).
type Bucket struct {
	Start time.Time
	USD   float64
}

// Buckets sums points into the n most recent buckets ending at now's
// bucket, oldest first and zero-filled. Points older than the window are
// dropped — the ledger keeps the full history; the chart shows the recent
// window.
func Buckets(points []Point, unit BucketUnit, n int, now time.Time) []Bucket {
	out := make([]Bucket, n)
	start := unit.truncate(now)
	for i := n - 1; i >= 0; i-- {
		out[i] = Bucket{Start: start}
		start = unit.prev(start)
	}
	idx := map[time.Time]int{}
	for i, b := range out {
		idx[b.Start] = i
	}
	for _, p := range points {
		if i, ok := idx[unit.truncate(p.Time)]; ok {
			out[i].USD += p.USD
		}
	}
	return out
}

// Bar renders a value as a proportional run of block cells. Zero is empty;
// any nonzero value shows at least one cell so small spend stays visible.
func Bar(v, max float64, width int) string {
	if v <= 0 || max <= 0 || width <= 0 {
		return ""
	}
	n := int(v / max * float64(width))
	if n < 1 {
		n = 1
	}
	if n > width {
		n = width
	}
	return strings.Repeat("▇", n)
}
