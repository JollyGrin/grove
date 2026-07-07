// Package cost turns transcript usage entries into per-ticket token and
// dollar estimates, plus the deterministic analysis flags the orchestrator
// interprets. Estimates only: on a Max plan the $ figure is notional — a
// relative-effort signal, never a bill.
package cost

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/JollyGrin/grove/internal/transcript"
)

// Rates are USD per million tokens. Cache rates derive from input (docs:
// read ≈0.1×, 5m write ≈1.25×, 1h write =2×) — kept explicit so a config
// override can set any of them.
type Rates struct {
	Input        float64 `yaml:"input"`
	Output       float64 `yaml:"output"`
	CacheRead    float64 `yaml:"cache_read"`
	CacheWrite5m float64 `yaml:"cache_write_5m"`
	CacheWrite1h float64 `yaml:"cache_write_1h"`
}

func derive(input, output float64) Rates {
	return Rates{
		Input: input, Output: output,
		CacheRead: 0.1 * input, CacheWrite5m: 1.25 * input, CacheWrite1h: 2 * input,
	}
}

// defaultRates: current Anthropic pricing (claude-api reference, cached
// 2026-06-24). Explicit per model — no prefix matching: Opus 4.0/4.1 were
// $15/$75 while 4.5+ is $5/$25, so prefixes would misprice. Overridable
// via config `cost.pricing`.
var defaultRates = map[string]Rates{
	"claude-fable-5":    derive(10, 50),
	"claude-mythos-5":   derive(10, 50),
	"claude-opus-4-8":   derive(5, 25),
	"claude-opus-4-7":   derive(5, 25),
	"claude-opus-4-6":   derive(5, 25),
	"claude-opus-4-5":   derive(5, 25),
	"claude-opus-4-1":   derive(15, 75),
	"claude-opus-4-0":   derive(15, 75),
	"claude-opus-4":     derive(15, 75),
	"claude-sonnet-5":   derive(3, 15),
	"claude-sonnet-4-6": derive(3, 15),
	"claude-sonnet-4-5": derive(3, 15),
	"claude-sonnet-4-0": derive(3, 15),
	"claude-sonnet-4":   derive(3, 15),
	"claude-haiku-4-5":  derive(1, 5),
}

// Overrides merges config-provided pricing on top of the defaults.
func Overrides(pricing map[string]Rates) {
	for model, r := range pricing {
		defaultRates[model] = r
	}
}

var datedSuffix = regexp.MustCompile(`-\d{8}$`)

func rateFor(model string) (Rates, bool) {
	if r, ok := defaultRates[model]; ok {
		return r, true
	}
	r, ok := defaultRates[datedSuffix.ReplaceAllString(model, "")]
	return r, ok
}

// Totals is the aggregate for one ticket (all sessions + subagents).
type Totals struct {
	Input         int          `json:"input_tokens"`
	Output        int          `json:"output_tokens"`
	CacheCreate5m int          `json:"cache_create_5m_tokens"`
	CacheCreate1h int          `json:"cache_create_1h_tokens"`
	CacheRead     int          `json:"cache_read_tokens"`
	Turns         int          `json:"turns"`
	USD           float64      `json:"est_usd"`
	CostKnown     bool         `json:"cost_known"` // false when any entry's model has no pricing
	Models        []ModelUsage `json:"models,omitempty"`
}

// ModelUsage is the token/dollar subtotal for one model within a ticket —
// the routing-verification signal: which models a ticket actually burned.
// Full model IDs are kept (not shortened) so the breakdown stays precise;
// Mix does the human-facing shortening.
type ModelUsage struct {
	Model     string  `json:"model"`
	Tokens    int     `json:"tokens"`     // input + output + all cache tokens
	USD       float64 `json:"est_usd"`    // 0 when CostKnown is false
	CostKnown bool    `json:"cost_known"` // false when this model has no pricing
}

// CacheReadShare is the fraction of all input-side tokens served from
// cache — a low share on a long task indicates context thrash.
func (t Totals) CacheReadShare() float64 {
	denom := t.Input + t.CacheCreate5m + t.CacheCreate1h + t.CacheRead
	if denom == 0 {
		return 0
	}
	return float64(t.CacheRead) / float64(denom)
}

// Mix renders a compact per-model share, dominant model first, e.g.
// "fable 92% · haiku 8%". Share is by USD when the ticket's dollars are
// known (routing is about spend); it falls back to tokens when any model
// is unpriced, so an unknown model never silently reads as free. Returns
// "" when there is nothing to split. Estimates, not billing.
func (t Totals) Mix() string {
	if len(t.Models) == 0 {
		return ""
	}
	byUSD := t.CostKnown && t.USD > 0
	var denom float64
	for _, m := range t.Models {
		if byUSD {
			denom += m.USD
		} else {
			denom += float64(m.Tokens)
		}
	}
	if denom <= 0 {
		return ""
	}
	var parts []string
	for _, m := range t.Models {
		var share float64
		if byUSD {
			share = m.USD / denom
		} else {
			share = float64(m.Tokens) / denom
		}
		parts = append(parts, fmt.Sprintf("%s %.0f%%", ShortModel(m.Model), 100*share))
	}
	return strings.Join(parts, " · ")
}

// ShortModel is the human-facing family label for a model id:
// "claude-fable-5" → "fable", "claude-opus-4-8" → "opus",
// "claude-haiku-4-5-20251001" → "haiku". Ids that don't fit the
// claude-<family>-… shape pass through unchanged (never guess).
func ShortModel(model string) string {
	rest, ok := strings.CutPrefix(model, "claude-")
	if !ok {
		return model
	}
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// Dedup collapses duplicate billing entries by (MessageID, RequestID) —
// resumed sessions re-emit prior messages into new files, and the guard
// also absorbs any future CC change that duplicates subagent usage into
// the parent transcript (dedup runs across a ticket's whole file set).
func Dedup(entries []transcript.UsageEntry) []transcript.UsageEntry {
	seen := make(map[[2]string]bool, len(entries))
	out := entries[:0:0]
	for _, e := range entries {
		key := [2]string{e.MessageID, e.RequestID}
		if key != [2]string{} && seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}

// Total sums deduped entries in "auto" cost mode: a per-entry costUSD
// (old CC versions) is used verbatim; otherwise cost is computed from the
// pricing table. Unknown models still count tokens but mark the dollar
// figure unknown — never zero-and-confident.
func Total(entries []transcript.UsageEntry) Totals {
	tot := Totals{CostKnown: true}
	// Per-model accumulation runs alongside the grand total — the data is
	// already in each entry's Model, so don't aggregate it away.
	type acc struct {
		tokens    int
		usd       float64
		costKnown bool
	}
	byModel := map[string]*acc{}
	var order []string
	get := func(model string) *acc {
		a, ok := byModel[model]
		if !ok {
			a = &acc{costKnown: true}
			byModel[model] = a
			order = append(order, model)
		}
		return a
	}
	for _, e := range entries {
		tot.Turns++
		tot.Input += e.Input
		tot.Output += e.Output
		tot.CacheCreate5m += e.CacheCreate5m
		tot.CacheCreate1h += e.CacheCreate1h
		tot.CacheRead += e.CacheRead
		m := get(e.Model)
		m.tokens += e.Input + e.Output + e.CacheCreate5m + e.CacheCreate1h + e.CacheRead
		if e.CostUSD != nil {
			tot.USD += *e.CostUSD
			m.usd += *e.CostUSD
			continue
		}
		r, ok := rateFor(e.Model)
		if !ok {
			tot.CostKnown = false
			m.costKnown = false
			continue
		}
		usd := (r.Input*float64(e.Input) +
			r.Output*float64(e.Output) +
			r.CacheWrite5m*float64(e.CacheCreate5m) +
			r.CacheWrite1h*float64(e.CacheCreate1h) +
			r.CacheRead*float64(e.CacheRead)) / 1e6
		tot.USD += usd
		m.usd += usd
	}
	for _, model := range order {
		a := byModel[model]
		tot.Models = append(tot.Models, ModelUsage{
			Model: model, Tokens: a.tokens, USD: a.usd, CostKnown: a.costKnown,
		})
	}
	// Biggest share first so mix strings read "dominant model first". Order
	// by the same basis Mix uses: USD when the ticket's dollars are known,
	// tokens otherwise (an unpriced model has USD 0 and must not sink below
	// a small priced one).
	byUSD := tot.CostKnown && tot.USD > 0
	sort.Slice(tot.Models, func(i, j int) bool {
		a, b := tot.Models[i], tot.Models[j]
		if byUSD && a.USD != b.USD {
			return a.USD > b.USD
		}
		if a.Tokens != b.Tokens {
			return a.Tokens > b.Tokens
		}
		return a.Model < b.Model
	})
	return tot
}

// Cache parses transcripts at most once per (path, mtime, size) within a
// process — the TUI polls repeatedly and transcripts reach MBs. One-shot
// CLI invocations start cold by design (an on-disk cache is IDEAS.md
// territory if latency ever bites).
type Cache struct {
	mu     sync.Mutex
	byFile map[fileKey][]transcript.UsageEntry
	parses int // test hook: how many files were actually parsed
}

type fileKey struct {
	path  string
	mtime int64
	size  int64
}

func NewCache() *Cache { return &Cache{byFile: map[fileKey][]transcript.UsageEntry{}} }

// ForTask aggregates every session + subagent transcript for a worktree
// (cwd-scan discovery — grab/adopt/resume sessions all share the path).
func (c *Cache) ForTask(worktreePath string) (Totals, error) {
	all, err := c.UsageForTask(worktreePath)
	if err != nil {
		return Totals{CostKnown: true}, err
	}
	return Total(all), nil
}

// UsageForTask returns the deduped usage entries behind ForTask — the
// timestamped form the spend chart buckets.
func (c *Cache) UsageForTask(worktreePath string) ([]transcript.UsageEntry, error) {
	files, err := transcript.SessionFiles(worktreePath)
	if err != nil {
		return nil, err
	}
	var all []transcript.UsageEntry
	for _, path := range files {
		entries, err := c.entriesFor(path)
		if err != nil {
			continue // unreadable file degrades that file, not the ticket
		}
		all = append(all, entries...)
	}
	return Dedup(all), nil
}

func (c *Cache) entriesFor(path string) ([]transcript.UsageEntry, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	key := fileKey{path, fi.ModTime().UnixNano(), fi.Size()}
	c.mu.Lock()
	cached, ok := c.byFile[key]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries := transcript.ParseUsage(f)
	c.mu.Lock()
	c.byFile[key] = entries
	c.parses++
	c.mu.Unlock()
	return entries, nil
}

// --- analysis flags (Warren-style thresholds, deterministic, no LLM) ---

// StuckFlag: many turns with no delivery movement — the strongest cheap
// agent-is-stuck signal.
func StuckFlag(turns, threshold int, deliveryChanged bool) bool {
	return !deliveryChanged && threshold > 0 && turns > threshold
}

// SteeringAnomaly: more than 25% of turns needed a human answer.
func SteeringAnomaly(steers, turns int) bool {
	return turns > 0 && float64(steers)/float64(turns) > 0.25
}

// CostOutlier: ≥2× the median cost of merged tickets.
func CostOutlier(usd, medianMerged float64) bool {
	return medianMerged > 0 && usd >= 2*medianMerged
}
