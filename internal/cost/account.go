package cost

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/transcript"
)

// WindowSums buckets spend points into the calendar windows the ACCOUNT tab
// compares against OpenRouter's counters (grove-87): the UTC day, ISO week
// (Monday start), month, and year containing now — the same UTC bucketing
// the SPEND chart uses (grove-51).
func WindowSums(points []Point, now time.Time) (day, week, month, year float64) {
	n := now.UTC()
	dayStart := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	weekStart := dayStart.AddDate(0, 0, -((int(dayStart.Weekday()) + 6) % 7))
	monthStart := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.UTC)
	yearStart := time.Date(n.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	for _, p := range points {
		t := p.Time.UTC()
		if t.Before(yearStart) || t.After(n) {
			continue
		}
		year += p.USD
		if !t.Before(monthStart) {
			month += p.USD
		}
		if !t.Before(weekStart) {
			week += p.USD
		}
		if !t.Before(dayStart) {
			day += p.USD
		}
	}
	return day, week, month, year
}

// ModelSpend is one model's estimated dollars within a window — the ACCOUNT
// tab's per-model totals (per-model time series stay in SPEND, grove-49).
type ModelSpend struct {
	Model string
	USD   float64
}

// SpendByModel sums per-model estimated dollars for entries at or after
// since, keyed by the human-facing short name (ShortModel) so transcript
// contributions merge with the ledger's mix vocabulary. Unparseable
// timestamps and unpriced entries are skipped — under-report, never guess.
func SpendByModel(entries []transcript.UsageEntry, since time.Time) map[string]float64 {
	out := map[string]float64{}
	for _, e := range entries {
		at, err := time.Parse(time.RFC3339, e.Timestamp)
		if err != nil || at.Before(since) {
			continue
		}
		usd, ok := entryUSD(e)
		if !ok || usd == 0 {
			continue
		}
		out[ShortModel(e.Model)] += usd
	}
	return out
}

// MixShares parses a Totals.Mix string ("fable 92% · haiku 8%") back into
// normalized fractional shares — how ledger rows, which only carry the mix,
// contribute to per-model totals after transcripts prune. Unparseable input
// returns nil.
func MixShares(mix string) map[string]float64 {
	shares := map[string]float64{}
	total := 0.0
	for _, part := range strings.Split(mix, "·") {
		fields := strings.Fields(part)
		if len(fields) < 2 {
			continue
		}
		pctField := fields[len(fields)-1]
		if !strings.HasSuffix(pctField, "%") {
			continue
		}
		pct, err := strconv.ParseFloat(strings.TrimSuffix(pctField, "%"), 64)
		if err != nil || pct < 0 {
			continue
		}
		name := strings.Join(fields[:len(fields)-1], " ")
		shares[name] += pct
		total += pct
	}
	if total <= 0 {
		return nil
	}
	for name := range shares {
		shares[name] /= total
	}
	return shares
}

// SortedModelSpend flattens a per-model USD map to a slice, biggest spend
// first (ties by name for deterministic rendering).
func SortedModelSpend(byModel map[string]float64) []ModelSpend {
	var out []ModelSpend
	for m, usd := range byModel {
		out = append(out, ModelSpend{Model: m, USD: usd})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].USD != out[j].USD {
			return out[i].USD > out[j].USD
		}
		return out[i].Model < out[j].Model
	})
	return out
}
