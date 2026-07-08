package tui

// Cockpit joy (grove-22): calm ambient flourish + one-shot celebrations,
// layered OVER the working cockpit, never into it. Everything here is pure
// and driven by the existing 1s refresh tick (Model.tick) — no new timers,
// no goroutines, no per-frame allocation (all tables are package-level
// consts). See docs/plans/2026-07-08-cockpit-joy-design.md.

import (
	"time"

	"github.com/JollyGrin/grove/internal/resource"
	"github.com/JollyGrin/grove/internal/state"
)

// fxLevel is the effects knob. Ordered so `fx >= fxCalm` gates ambient life
// and `fx >= fxFull` gates event celebrations; `off` renders today's exact
// output (byte-identical, asserted by TestOffIsStatic).
type fxLevel int

const (
	fxOff  fxLevel = iota // today's exact render — no flourish
	fxCalm                // ambient only (A1–A4)
	fxFull                // ambient + celebrations (J1, J2)
)

// parseFx maps the config string to a level. Empty/missing/unknown = full
// (default is joy; a typo must never break the cockpit).
func parseFx(s string) fxLevel {
	switch s {
	case "off":
		return fxOff
	case "calm":
		return fxCalm
	default:
		return fxFull
	}
}

// fxLabel names a level for the runtime-toggle flash.
func fxLabel(fx fxLevel) string {
	switch fx {
	case fxOff:
		return "off"
	case fxCalm:
		return "calm"
	default:
		return "full"
	}
}

// cycleFx advances the runtime toggle: full → calm → off → full.
func cycleFx(fx fxLevel) fxLevel {
	return (fx + 2) % 3
}

// --- A1: the grove breathes ---

// breathFrames is a slow 4-beat breath, not a spinner: at 1 frame/second the
// working glyph inhales and exhales over 4s. Every frame is width-1.
var breathFrames = [...]string{"◉", "●", "◎", "●"}

func breathFrame(tick uint64) string {
	return breathFrames[tick%uint64(len(breathFrames))]
}

// --- A2: grove verbs ---

// groveVerbs replaces the static "working" in the LIVE column with a rotating
// gerund — the forest dialect of Claude Code's spinner verbs. Each entry must
// stay short enough to read once truncated to the 8-cell LIVE column.
var groveVerbs = [...]string{
	"photosynthesizing", "grafting", "pruning", "pollinating",
	"rooting", "mulching", "leafing", "budding",
}

// verbFor is deterministic per agent: hash(ticket) fixes the starting verb so
// two agents rarely share one, and tick/8 advances it ~every 8s — slow enough
// never to strobe. Callers truncate to the column width (pad does this).
func verbFor(ticket string, tick uint64) string {
	idx := (hashTicket(ticket) + tick/8) % uint64(len(groveVerbs))
	return groveVerbs[idx]
}

// hashTicket is a small FNV-1a over the ticket id — stable across runs, no
// allocation, good enough to spread agents across the verb table.
func hashTicket(s string) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

// --- A3: weather gauge ---

// weatherGlyph keys the memory metaphor to a sky: clear when there's headroom,
// clouds gathering as it tightens, storm near the cliff. All width-1.
func weatherGlyph(l resource.Level) string {
	switch l {
	case resource.Amber:
		return "☁"
	case resource.Red:
		return "⛆"
	default:
		return "☼"
	}
}

// --- A4: living empty states ---

// timeOfDay buckets an hour into morning / day / evening / night for the
// empty-state variants. Pure lookup — no clock cost beyond the Hour() read.
func timeOfDay(hour int) int {
	switch {
	case hour >= 5 && hour < 12:
		return 0 // morning
	case hour >= 12 && hour < 17:
		return 1 // day
	case hour >= 17 && hour < 21:
		return 2 // evening
	default:
		return 3 // night
	}
}

var emptyAgentsLines = [...]string{
	"  dew on the leaves — gv grab <ticket> plants one",
	"  a clearing in the canopy — gv grab <ticket> plants one",
	"  long shadows — gv grab <ticket> plants one",
	"  the grove sleeps — gv grab <ticket> plants one",
}

var emptyActivityLines = [...]string{
	"  the grove wakes — nothing stirring yet",
	"  a still afternoon — nothing has happened yet",
	"  settling in for the evening — nothing has happened yet",
	"  the grove sleeps — nothing has happened yet",
}

func emptyAgentsLine(hour int) string   { return emptyAgentsLines[timeOfDay(hour)] }
func emptyActivityLine(hour int) string { return emptyActivityLines[timeOfDay(hour)] }

// --- J1/J2 celebration state decay ---

const celebrationTicks = 6 // ~6s of merge shimmer
const maxCelebrations = 16 // cap: no unbounded growth

// decayCelebrations decrements every entry and deletes those hitting zero.
// Runs once per tick on the map the Model carries (maps are references, so it
// survives Update's value-copy). Nil-safe.
func decayCelebrations(c map[string]int) {
	for k, v := range c {
		if v <= 1 {
			delete(c, k)
		} else {
			c[k] = v - 1
		}
	}
}

// countDone tallies EvTaskDone events for the "tree #N this season" ritual.
// Counts only what's loaded (the 200-event window) — precision doesn't matter.
func countDone(events []state.Event) int {
	n := 0
	for _, e := range events {
		if e.Type == state.EvTaskDone {
			n++
		}
	}
	return n
}

// nowHour is the current hour, isolated so tests reason about time-of-day via
// the pure timeOfDay/emptyLine functions rather than the clock.
func nowHour() int { return time.Now().Hour() }
