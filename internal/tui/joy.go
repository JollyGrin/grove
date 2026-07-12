package tui

// Cockpit joy (grove-22): calm ambient flourish + one-shot celebrations,
// layered OVER the working cockpit, never into it. Everything here is pure
// and driven by the existing 1s refresh tick (Model.tick) — no new timers,
// no goroutines, no per-frame allocation (all tables are package-level
// consts). See docs/plans/2026-07-08-cockpit-joy-design.md.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

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

// --- A1: the grove breathes (grove-24: color, not shape) ---
//
// The working dot holds ONE stable ● — only its green LIGHTNESS ramps, a
// barely-perceptible dim→bright→dim. The old ◉/●/◎ shape-swap read as flicker
// (an arcade); a same-hue brightness pulse reads as breathing (a forest).

// breathShades is the up-ramp of near-identical greens, dim → full canopy
// (#87c95f). Same hue; only lightness moves. The breath ping-pongs across it.
var breathShades = []lipgloss.Color{
	"#649646", "#6da34c", "#76b053", "#7ebc59", "#87c95f",
}

// breathStyles precomputes one foreground style per shade ONCE, at package
// load — the render loop indexes this table and allocates nothing per frame
// (the RAM constraint: no per-frame allocation, palette built as a table).
var breathStyles = func() []lipgloss.Style {
	ss := make([]lipgloss.Style, len(breathShades))
	for i, c := range breathShades {
		ss[i] = lipgloss.NewStyle().Foreground(c)
	}
	return ss
}()

// breathPhase maps the animation tick to a shade index, ping-ponged so the
// brightness eases dim→bright→dim with no hard jump at the seam. One step per
// tick; a full inhale+exhale spans 2*(len-1) ticks (~8s at the 1s beat).
func breathPhase(tick uint64) int {
	n := len(breathShades)
	period := 2*n - 2
	p := int(tick % uint64(period))
	if p >= n {
		p = period - p // mirror the down-ramp
	}
	return p
}

// breathStyle is the working dot's green shade at this tick. The caller gates
// on fx and keeps the glyph a static ● at every level; only the color moves.
func breathStyle(tick uint64) lipgloss.Style {
	return breathStyles[breathPhase(tick)]
}

// --- A2: grove verbs ---

// groveVerbs rotates through the prominent green AGENT-status column in place
// of the static "working" (grove-24: moved here from the dim LIVE column — the
// eye lands on the green status, and the reliable agent state, not the often-
// empty transcript LIVE string, triggers it). Each gerund is short enough to
// read in the 11-cell STATUS column without truncation.
var groveVerbs = [...]string{
	"growing", "sprouting", "grafting", "pruning",
	"rooting", "budding", "leafing", "flowering",
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
// the pure timeOfDay/emptyLine functions rather than the clock. A var, not a
// func: render tests pin it (fireflies only fly at night) — grove-56.
var nowHour = func() int { return time.Now().Hour() }

// --- grove-56: joy v2 (A5–A7, J3–J6) ---

// trees phrases a count in the grove dialect — "one tree" / "3 trees" — for
// the farewell and first-light lines.
func trees(n int) string {
	if n == 1 {
		return "one tree"
	}
	return fmt.Sprintf("%d trees", n)
}

// countWorking tallies agents actually at work (working or still in setup) —
// the header count, the farewell count.
func countWorking(tasks []*state.Task) int {
	n := 0
	for _, t := range tasks {
		if t.Agent == state.AgentWorking || t.Agent == state.AgentSetup {
			n++
		}
	}
	return n
}

// --- A5: quit farewell ---
//
// One dim line printed by cmd/gv AFTER the alt-screen closes — the grove's
// goodbye. Phrasing follows the time of day; silent at off.

var farewellBusy = [...]string{
	"morning tends the grove — %s still working",
	"the grove keeps growing — %s still working",
	"the grove works into the evening — %s still working",
	"the grove tends itself tonight — %s still working",
}

var farewellRest = [...]string{
	"the grove rests in the morning light",
	"the grove rests",
	"the grove rests for the evening",
	"the grove sleeps",
}

// farewellLine is the unstyled farewell — empty at off (A5's silence is part
// of the fx-off contract), a rest line at zero working, else the busy line
// with the tree count.
func farewellLine(fx fxLevel, working, hour int) string {
	if fx < fxCalm {
		return ""
	}
	if working <= 0 {
		return farewellRest[timeOfDay(hour)]
	}
	return fmt.Sprintf(farewellBusy[timeOfDay(hour)], trees(working))
}

// --- J3: the forest strip ---
//
// One ♠ per shipped tree (EvTaskDone in the loaded event window), in the
// header left of the counts. Past the cap it condenses to ♠×N — the orchard
// keeps its size without eating the header.

const forestGlyph = "♠" // grove-63: ⸙ rendered as tofu in the operator's font
const forestStripCap = 12

// sForest renders the strip in moss green — grown wood, not fresh canopy.
var sForest = lipgloss.NewStyle().Foreground(cMoss)

func forestStrip(done int) string {
	if done <= 0 {
		return ""
	}
	if done <= forestStripCap {
		return strings.Repeat(forestGlyph, done)
	}
	return fmt.Sprintf("%s×%d", forestGlyph, done)
}

// --- J4: planting ---
//
// A freshly grabbed task's ACTIVITY row reads as a planting for its first
// minute, then settles to the normal line. Presentation-layer only: the
// match is on the stable feedItems string (asserted against feed.go by
// TestPlantingMatchesFeed), never a rewrite of it.

const plantWindow = time.Minute
const plantGlyph = "✿"
const plantText = "planted — roots taking"

// grabbedText mirrors feed.go's EvTaskCreated row byte-for-byte.
const grabbedText = "grabbed — worktree up"

func planting(text string, age time.Duration) bool {
	return text == grabbedText && age >= 0 && age < plantWindow
}

// --- J5: question knock ---
//
// When a task's label first flips to QUESTION its amber ◆ pulses bold↔normal
// for a few ticks — the knock that pulls the eye to the thing that needs a
// human. Detection diffs prior vs fresh tasks in Update (the J1 pattern);
// state rides in the existing capped celebrations map under a prefixed key.

const knockTicks = 4

// knockKey namespaces a knock in the celebrations map so it can never
// collide with a J1 merge-sparkle entry (keyed by the bare ticket).
func knockKey(ticket string) string { return "?" + ticket }

// knockStyle alternates the QUESTION glyph bold↔normal on the tick — same
// amber both phases, only the weight moves.
func knockStyle(tick uint64) lipgloss.Style {
	if tick%2 == 0 {
		return sWaiting
	}
	return sQuestion
}

// freshQuestions returns tickets whose label flipped to QUESTION between the
// prior and fresh task snapshots. A question already knocking last poll stays
// quiet; one present at first load knocks once (matching J1's first-poll
// sparkle — the eye should land on it).
func freshQuestions(prev, next []*state.Task) []string {
	old := make(map[string]bool, len(prev))
	for _, t := range prev {
		if t.Label() == "QUESTION" {
			old[t.Ticket] = true
		}
	}
	var out []string
	for _, t := range next {
		if t.Label() == "QUESTION" && !old[t.Ticket] {
			out = append(out, t.Ticket)
		}
	}
	return out
}

// --- J6: milestones ---

// doneRitualFlash is the J2 done ritual, upgraded every 10th tree. n is the
// season's shipped count including the tree just done.
func doneRitualFlash(ticket string, n int) string {
	if n > 0 && n%10 == 0 {
		return fmt.Sprintf("✓ %s shipped — that's %d this season — quite the orchard", ticket, n)
	}
	return fmt.Sprintf("✓ %s shipped — tree #%d this season", ticket, n)
}

// --- A6: first light ---
//
// The footer flash greets once, at the first data refresh after launch; the
// next real flash naturally overwrites it. standing = active tasks (trees in
// the ground, working or not).

var firstLightBusy = [...]string{
	"morning in the grove — %s standing",
	"the canopy is full — %s standing",
	"the canopy holds for the evening — %s standing",
	"night in the grove — %s standing watch",
}

var firstLightRest = [...]string{
	"morning in the grove — ground ready for planting",
	"a quiet day in the grove",
	"the canopy holds for the evening",
	"the grove sleeps — the fireflies keep watch",
}

func firstLight(hour, standing int) string {
	if standing <= 0 {
		return firstLightRest[timeOfDay(hour)]
	}
	return fmt.Sprintf(firstLightBusy[timeOfDay(hour)], trees(standing))
}

// --- A7: night fireflies ---
//
// In the night-bucket empty states one ✦ drifts a cell per tick through the
// dark to the right of the line, ping-ponged (breathPhase's mirror trick) so
// it never jumps at the seam. Deterministic from the tick — no state.

const fireflyGlyph = "✦"

// fireflySpan is the drift width in cells; a full out-and-back crossing takes
// 2*(span-1) ticks (~46s) — a firefly, not a cursor.
const fireflySpan = 24

var sFirefly = lipgloss.NewStyle().Foreground(cAmber)

// fireflyPos ping-pongs over [0, span) at one cell per tick, mirrored on the
// return leg exactly like breathPhase.
func fireflyPos(tick uint64, span int) int {
	if span <= 1 {
		return 0
	}
	period := 2*span - 2
	p := int(tick % uint64(period))
	if p >= span {
		p = period - p
	}
	return p
}

// fireflyTrail is the styled tail appended to a night empty-state line: the
// gap to the firefly's current cell, then the glyph.
func fireflyTrail(tick uint64) string {
	return strings.Repeat(" ", 2+fireflyPos(tick, fireflySpan)) + sFirefly.Render(fireflyGlyph)
}
