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

// truncate maps a time to its bucket start in loc (weeks start Monday).
// Boundaries are built from wall-clock components in loc, not
// t.Truncate(time.Hour) — Truncate rounds against absolute time-since-zero
// (effectively UTC), so it lands on the wrong hour in fractional-offset
// zones (e.g. +5:30). Storage stays UTC-canonical; this is display only.
func (u BucketUnit) truncate(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	switch u {
	case Daily:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	case Weekly:
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		back := (int(d.Weekday()) + 6) % 7 // Monday=0 … Sunday=6
		return d.AddDate(0, 0, -back)
	default:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
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

// Label renders a bucket start for the chart's axis, formatted in loc so the
// operator sees wall-clock labels (a bucket keyed on a loc boundary already
// carries that location, but callers pass loc explicitly to be safe).
func (u BucketUnit) Label(t time.Time, loc *time.Location) string {
	t = t.In(loc)
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
// Buckets keys its map[time.Time]int on loc-truncated starts. time.Time map
// equality compares wall-clock AND the *time.Location pointer, so the axis
// truncation and the per-point truncation must share the SAME loc value —
// passing a freshly LoadLocation'd copy for either would make no key match and
// every bar read $0.
func Buckets(points []Point, unit BucketUnit, n int, now time.Time, loc *time.Location) []Bucket {
	out := make([]Bucket, n)
	start := unit.truncate(now, loc)
	for i := n - 1; i >= 0; i-- {
		out[i] = Bucket{Start: start}
		start = unit.prev(start)
	}
	idx := map[time.Time]int{}
	for i, b := range out {
		idx[b.Start] = i
	}
	for _, p := range points {
		if i, ok := idx[unit.truncate(p.Time, loc)]; ok {
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
