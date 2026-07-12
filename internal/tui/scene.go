package tui

// The living grove (grove-63): the landscape between ACTIVITY and the
// footer, rendered only in modeList. Pure function of (tasks, prs, events,
// celebrations, tick, width, rows, hour, fx, focused) — always returns
// exactly `rows` lines, each truncPad-ed to width. See
// docs/plans/2026-07-12-living-grove-design.md for the locked glyph
// vocabulary and algorithms this file implements.
//
// grove-71: the scene renders bottom-up from the soil. Every glyph column
// touches the ground — trunks root ON the ground row (the row directly
// above the soil), canopies stack above them, and height encodes age:
// merged ♠ gets a 2-row canopy at full tier, ♣/∆ a 1-row canopy over a
// trunk at compact+, and saplings/blossoms/seeds always sit bare on the
// ground row. At strip tier NOTHING has a trunk — the whole cast of plants
// sits uniformly on the soil.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
)

// plotW is the default cell width of one plot (tree + soil + label); the
// overflow ladder shrinks it (8, then 6) when the fleet doesn't fit.
const plotW = 10

// sceneTier gates which rows a plot occupies — purely a function of the row
// budget the height split hands the scene.
type sceneTier int

const (
	sceneStrip sceneTier = iota // 3-5 rows: ground, soil, label — no trunks anywhere
	sceneCompact
	sceneFull
)

func sceneTierFor(rows int) sceneTier {
	switch {
	case rows <= 5:
		return sceneStrip
	case rows <= 8:
		return sceneCompact
	default:
		return sceneFull
	}
}

// plantRowsFor is the height of the plant region (the rows between the soil
// and the sky) per tier — height encodes age, degrading with the squeeze:
// full fits a 2-row ♠ canopy over its trunk, compact a 1-row canopy over a
// trunk, strip just the bare glyphs on the ground row.
func plantRowsFor(tier sceneTier) int {
	switch tier {
	case sceneStrip:
		return 1
	case sceneCompact:
		return 2
	default:
		return 3
	}
}

// --- S3: day cycle ---
//
// scenePalette tints the scene's ambient (non-semantic) surfaces by
// time-of-day bucket (morning/day/evening/night, timeOfDay's buckets):
// the "growing" plant stages' canopy/trunk color and the soil row. Status
// colors (QUESTION amber, BLOCKED rust, dead fog, the merged/orchard moss)
// are semantic and never shift with the clock. Built once at package load —
// the RAM rule forbids per-frame style construction.
type scenePalette struct {
	canopy lipgloss.Style
	soil   lipgloss.Style
}

var scenePalettes = [4]scenePalette{
	{ // morning
		canopy: lipgloss.NewStyle().Foreground(lipgloss.Color("#76b053")),
		soil:   lipgloss.NewStyle().Foreground(cMoss),
	},
	{ // day — unchanged from the pre-S3 look
		canopy: lipgloss.NewStyle().Foreground(cCanopy),
		soil:   lipgloss.NewStyle().Foreground(cMoss),
	},
	{ // evening
		canopy: lipgloss.NewStyle().Foreground(lipgloss.Color("#a8b454")),
		soil:   lipgloss.NewStyle().Foreground(lipgloss.Color("#6b5d3a")),
	},
	{ // night
		canopy: lipgloss.NewStyle().Foreground(lipgloss.Color("#3f5d3a")),
		soil:   lipgloss.NewStyle().Foreground(cFog),
	},
}

// sDew/sMoon/sSun/sSceneFirefly are the ambient sky accent's precomputed
// styles — package-level, built once.
var (
	sDew        = lipgloss.NewStyle().Foreground(cSky)
	sMoon       = lipgloss.NewStyle().Foreground(cSky)
	sSun        = lipgloss.NewStyle().Foreground(cAmber)
	sSceneFly   = lipgloss.NewStyle().Foreground(cAmber)
	fireflyRune = []rune(fireflyGlyph)[0]
)

// sIdlePlant renders an idle task's plant in dim moss — still alive, just
// resting (grove-71). Grey (sDim/fog) stays reserved for dead ✗.
var sIdlePlant = lipgloss.NewStyle().Foreground(cMoss)

// chromeBorderStyles/chromeTitleStyles: the panel-border and ⁂ GROVE title
// tint per time-of-day bucket, fxFull only (S3's "chrome tint"). Unlike
// scenePalette these never touch data styles — only the AGENTS panel border
// and the header's branding title. Built once at package load.
var chromeTintColors = [4]lipgloss.Color{
	cMoss,                     // morning
	cMoss,                     // day — unchanged
	lipgloss.Color("#8a7a4a"), // evening
	lipgloss.Color("#3a4a6a"), // night
}

var chromeBorderStyles = [4]lipgloss.Style{
	sPanelFocus.BorderForeground(chromeTintColors[0]),
	sPanelFocus.BorderForeground(chromeTintColors[1]),
	sPanelFocus.BorderForeground(chromeTintColors[2]),
	sPanelFocus.BorderForeground(chromeTintColors[3]),
}

var chromeTitleStyles = [4]lipgloss.Style{
	lipgloss.NewStyle().Bold(true).Foreground(chromeTintColors[0]),
	lipgloss.NewStyle().Bold(true).Foreground(chromeTintColors[1]),
	lipgloss.NewStyle().Bold(true).Foreground(chromeTintColors[2]),
	lipgloss.NewStyle().Bold(true).Foreground(chromeTintColors[3]),
}

// chromeBorder/chromeTitle pick the AGENTS panel border and the header
// title style: today's fixed sPanelFocus/sTitle below fxFull, the
// time-of-day tint at fxFull (S3's hard constraint — chrome tint is
// fxFull-only; data styles never shift).
func (m Model) chromeBorder() lipgloss.Style {
	if m.fx < fxFull {
		return sPanelFocus
	}
	return chromeBorderStyles[timeOfDay(nowHour())]
}

func (m Model) chromeTitle() lipgloss.Style {
	if m.fx < fxFull {
		return sTitle
	}
	return chromeTitleStyles[timeOfDay(nowHour())]
}

// applyAmbientSky paints the day-cycle's sky accent, lowest priority (never
// overwrites a QUESTION/BLOCKED marker or the fairy — the hard priority
// order is ◆/⚠ > fairy > ambient). morning's dew is sparse dots across the
// row; day/evening place a sun; night places a moon plus two drifting
// fireflies. The moon/sun keep a 1-cell right margin — the last column sits
// at the clipping boundary (grove-71: the ☾ was visibly cut in the live
// scene). The caller hands this the TOPMOST sky row when more than one
// exists, so fireflies drift strictly above the tallest canopy instead of
// reading as tree ornaments.
func applyAmbientSky(hour int, tick uint64, skyGrid *sceneGrid) {
	width := len(skyGrid.chars)
	if width == 0 {
		return
	}
	switch timeOfDay(hour) {
	case 0: // morning: sparse dew dots
		for x := 0; x < width; x++ {
			if x%7 == 0 && !skyGrid.occupied(x) {
				skyGrid.set(x, '·', sDew)
			}
		}
	case 1: // day: sun top-right, 1-cell margin
		if x := width - 2; !skyGrid.occupied(x) {
			skyGrid.set(x, '☼', sSun)
		}
	case 2: // evening: sun low-left
		if !skyGrid.occupied(0) {
			skyGrid.set(0, '☼', sSun)
		}
	case 3: // night: moon top-right (1-cell margin) + two drifting fireflies
		if x := width - 2; !skyGrid.occupied(x) {
			skyGrid.set(x, '☾', sMoon)
		}
		span := fireflySpan
		if span > width {
			span = width
		}
		if span > 0 {
			if x := fireflyPos(tick, span); !skyGrid.occupied(x) {
				skyGrid.set(x, fireflyRune, sSceneFly)
			}
			if x := fireflyPos(tick+7, span); !skyGrid.occupied(x) {
				skyGrid.set(x, fireflyRune, sSceneFly)
			}
		}
	}
}

// scenePlot is one tile of the landscape: either an orchard tile (a done
// tree, ticket-less when condensed) or a live task's plant.
type scenePlot struct {
	ticket    string // "" for the condensed orchard remainder
	orchard   bool   // done tree, not a live task
	condensed bool   // the single "♠×K" orchard remainder tile
	canopy    string // single glyph; clusterFor widens it at plotW>=8
	trunk     string // "┃" / "│" / ""
	marker    string // "◆" / "⚠" hover marker, rendered in the sky row above
	label     string // soil label, trunc-ed to plotW-1 at render time
	style     lipgloss.Style
	markerSt  lipgloss.Style
}

func (p scenePlot) jitterKey() string {
	if p.ticket != "" {
		return p.ticket
	}
	return "orchard-condensed"
}

// clusterFor widens a single canopy glyph into its multi-glyph form once the
// plot has room (plotW>=8) — mature/merged trees to a 3-wide canopy, an
// established plant to 2-wide. Every other stage glyph stays single (the
// locked vocabulary never doubles ∆/ψ/✿/◌/✗).
func clusterFor(canopy string, pw int) string {
	if pw < 8 {
		return canopy
	}
	switch canopy {
	case forestGlyph:
		return strings.Repeat(canopy, 3)
	case "♣":
		return strings.Repeat(canopy, 2)
	default:
		return canopy
	}
}

// ticketLabel derives the soil label from a ticket id: the trailing digits
// after the last "-" (grove-63 -> #63), falling back to the full ticket when
// there are no trailing digits (DEV vs a non-numeric id).
func ticketLabel(ticket string) string {
	i := strings.LastIndex(ticket, "-")
	if i < 0 || i == len(ticket)-1 {
		return ticket
	}
	suffix := ticket[i+1:]
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return ticket
		}
	}
	return "#" + suffix
}

// plantStage buckets a working task's canopy/trunk by age. The label-based
// overlay (QUESTION/BLOCKED/idle/dead/merged) is layered on top by
// buildTaskPlot — the stage glyph underneath doesn't change.
func plantStage(age time.Duration) (canopy, trunk string) {
	switch {
	case age < plantWindow:
		return plantGlyph, ""
	case age < 30*time.Minute:
		return "ψ", ""
	case age < 2*time.Hour:
		return "∆", "│"
	default:
		return "♣", "│"
	}
}

// buildOrchardPlots derives the merged/done orchard from the loaded events
// window: the newest 3 EvTaskDone tickets get their own labeled tile, any
// remainder condenses into one "♠×K" tile (leftmost, oldest-first reading).
func buildOrchardPlots(events []state.Event) []scenePlot {
	var done []string
	for _, ev := range events {
		if ev.Type == state.EvTaskDone {
			done = append(done, ev.Ticket)
		}
	}
	n := len(done)
	if n == 0 {
		return nil
	}
	individualN := n
	if individualN > 3 {
		individualN = 3
	}
	remainder := n - individualN

	var plots []scenePlot
	if remainder > 0 {
		plots = append(plots, scenePlot{
			orchard: true, condensed: true,
			canopy: forestGlyph, trunk: "┃", style: sForest,
			label: fmt.Sprintf("%s×%d", forestGlyph, remainder),
		})
	}
	for _, tk := range done[n-individualN:] {
		plots = append(plots, scenePlot{
			ticket: tk, orchard: true,
			canopy: forestGlyph, trunk: "┃", style: sForest,
			label: ticketLabel(tk) + " ⬢",
		})
	}
	return plots
}

// buildTaskPlot renders one active task's plant. Priority mirrors the S1
// table: dead > merged PR > setup > the age-bucketed growth stage, with
// QUESTION/BLOCKED/idle overlaying a marker or style on that stage glyph.
func buildTaskPlot(t *state.Task, pr *github.PR, fx fxLevel, tick uint64, celebrations map[string]int, pal scenePalette) scenePlot {
	tl := ticketLabel(t.Ticket)
	label := t.Label()

	switch {
	case label == "dead":
		return scenePlot{ticket: t.Ticket, canopy: "✗", style: sDim, label: tl + " ✗"}
	case pr != nil && pr.State == "MERGED":
		return scenePlot{ticket: t.Ticket, canopy: forestGlyph, trunk: "┃", style: sForest, label: tl + " ⬢"}
	case label == "setup":
		return scenePlot{ticket: t.Ticket, canopy: "◌", style: sSetup, label: tl + " setup"}
	}

	canopy, trunk := plantStage(time.Since(t.Created))
	p := scenePlot{ticket: t.Ticket, canopy: canopy, trunk: trunk, style: pal.canopy}

	switch label {
	case "QUESTION":
		p.marker = "◆"
		p.markerSt = sQuestion
		if fx >= fxFull {
			if _, ok := celebrations[knockKey(t.Ticket)]; ok {
				p.markerSt = knockStyle(tick)
			}
		}
		p.label = tl + " ?"
	case "BLOCKED":
		p.marker = "⚠"
		p.markerSt = sBlocked
		p.label = tl + " ⚠"
	case "idle ✓":
		p.style = sIdlePlant // idle ≠ dead: dim moss, never bark/fog grey
		p.label = tl + " ✓"
	default:
		p.label = tl + " " + age(t.Created)
	}
	return p
}

// fitPlots is the overflow ladder: drop the condensed orchard remainder,
// then collapse the rest of the orchard to one tile, then shrink plotW
// (8, then 6), and finally keep the leftmost tiles that fit at 6, relabeling
// the last as "+K…" (grove-71: "+K more" truncated confusingly at pw=6).
// Never truncates mid-plot.
func fitPlots(plots []scenePlot, width int) ([]scenePlot, int) {
	fits := func(n, w int) bool { return n*w <= width-2 }

	cur := plots
	if fits(len(cur), plotW) {
		return cur, plotW
	}
	if len(cur) > 0 && cur[0].condensed {
		cur = cur[1:]
		if fits(len(cur), plotW) {
			return cur, plotW
		}
	}
	cur = shrinkOrchardToOne(cur)
	if fits(len(cur), plotW) {
		return cur, plotW
	}
	for _, w := range []int{8, 6} {
		if fits(len(cur), w) {
			return cur, w
		}
	}
	w := 6
	n := (width - 2) / w
	if n < 1 {
		n = 1
	}
	if n < len(cur) {
		dropped := len(cur) - n
		cur = append([]scenePlot(nil), cur[:n]...)
		last := cur[len(cur)-1]
		last.label = fmt.Sprintf("+%d…", dropped)
		cur[len(cur)-1] = last
	}
	return cur, w
}

// shrinkOrchardToOne condenses every orchard tile (individual + any already-
// condensed remainder) into a single "♠×K" tile, leaving task plots
// untouched. A no-op when the orchard is already ≤1 tile.
func shrinkOrchardToOne(plots []scenePlot) []scenePlot {
	orchardCount := 0
	var rest []scenePlot
	for _, p := range plots {
		if p.orchard {
			orchardCount++
		} else {
			rest = append(rest, p)
		}
	}
	if orchardCount <= 1 {
		return plots
	}
	combined := scenePlot{
		orchard: true, condensed: true, canopy: forestGlyph, style: sForest,
		label: fmt.Sprintf("%s×%d", forestGlyph, orchardCount),
	}
	return append([]scenePlot{combined}, rest...)
}

// plotLayout is a plot's precomputed column geometry within its plotW cell —
// shared by every row renderer so the canopy, trunk, marker, and label rows
// agree on where the tree sits.
type plotLayout struct {
	plot    scenePlot
	cluster string
	left    int // canopy left-padding within the cell
	pos     int // trunk/marker column within the cell
}

func layoutPlots(plots []scenePlot, pw int) []plotLayout {
	out := make([]plotLayout, len(plots))
	for i, p := range plots {
		cluster := clusterFor(p.canopy, pw)
		cellW := len([]rune(cluster))
		padTotal := pw - cellW
		if padTotal < 0 {
			padTotal = 0
		}
		jitter := int(hashTicket(p.jitterKey())%3) - 1
		left := padTotal/2 + jitter
		if left < 0 {
			left = 0
		}
		if left > padTotal {
			left = padTotal
		}
		out[i] = plotLayout{plot: p, cluster: cluster, left: left, pos: left + cellW/2}
	}
	return out
}

// sceneGrid is a rune-per-column canvas with per-column styling — used for
// the trunk and sky rows, the only two that need cell-granular overlay
// (pawn/queen/walk-off/fairy on top of the base trunk glyph or marker).
type sceneGrid struct {
	chars  []rune
	styles []lipgloss.Style
}

func newSceneGrid(width int) *sceneGrid {
	g := &sceneGrid{chars: make([]rune, width), styles: make([]lipgloss.Style, width)}
	for i := range g.chars {
		g.chars[i] = ' '
	}
	return g
}

func (g *sceneGrid) occupied(col int) bool {
	return col >= 0 && col < len(g.chars) && g.chars[col] != ' '
}

func (g *sceneGrid) set(col int, ch rune, st lipgloss.Style) {
	if col < 0 || col >= len(g.chars) {
		return
	}
	g.chars[col] = ch
	g.styles[col] = st
}

func (g *sceneGrid) String() string {
	var b strings.Builder
	for i, ch := range g.chars {
		b.WriteString(g.styles[i].Render(string(ch)))
	}
	return b.String()
}

// clampCol keeps a cast member's column inside its own plot's cell — a pawn
// or queen never wanders into the neighboring plot.
func clampCol(c, lo, hi int) int {
	if c < lo {
		return lo
	}
	if c > hi {
		return hi
	}
	return c
}

// soilTuft returns the sparse grass tuft rune for a soil column, or 0 —
// roughly 1 in 7 columns, deterministic in the column index alone (never the
// tick: the ground doesn't shimmer).
func soilTuft(x int) rune {
	h := uint64(x)*1099511628211 + 14695981039346656037
	h ^= h >> 7
	if h%7 != 0 {
		return 0
	}
	if (h>>3)%2 == 0 {
		return ','
	}
	return '.'
}

// renderPlotRows lays out the plant region bottom-up from the soil
// (grove-71): plantGrids[0] is the GROUND row — the row directly above the
// soil, where every plot touches down. A plot with a trunk roots it there
// and lifts its canopy to plantGrids[1]; a merged ♠ adds a second canopy
// row at plantGrids[2] when the tier grants it; trunkless glyphs
// (ψ/✿/◌/✗ — and everything at strip, where plantRows is 1) sit bare on
// the ground row. The label row stays a plain string (no overlay ever
// touches it); the ground/soil/sky rows are sceneGrids so S2's cast
// (pawn/queen/walk-off/fairy) can mutate them before stringifying. The
// soil row runs the full scene width, textured with sparse tufts between
// the plots' footprints.
func renderPlotRows(layouts []plotLayout, pw, width, plantRows int, soilStyle lipgloss.Style) (plantGrids []*sceneGrid, label string, soilGrid, skyGrid *sceneGrid) {
	if plantRows < 1 {
		plantRows = 1
	}
	plantGrids = make([]*sceneGrid, plantRows)
	for i := range plantGrids {
		plantGrids[i] = newSceneGrid(width)
	}
	soilGrid = newSceneGrid(width)
	skyGrid = newSceneGrid(width)
	ground := plantGrids[0]
	footprint := make([]bool, width)

	var lb strings.Builder
	col := 0
	for _, l := range layouts {
		cluster := []rune(l.cluster)
		if plantRows == 1 || l.plot.trunk == "" {
			for j, r := range cluster {
				ground.set(col+l.left+j, r, l.plot.style)
			}
		} else {
			ground.set(col+l.pos, []rune(l.plot.trunk)[0], l.plot.style)
			for j, r := range cluster {
				plantGrids[1].set(col+l.left+j, r, l.plot.style)
			}
			if plantRows >= 3 && l.plot.canopy == forestGlyph {
				plantGrids[2].set(col+l.pos, []rune(forestGlyph)[0], l.plot.style)
			}
		}
		if l.plot.marker != "" {
			skyGrid.set(col+l.pos, []rune(l.plot.marker)[0], l.plot.markerSt)
		}
		for j := range cluster {
			if x := col + l.left + j; x >= 0 && x < width {
				footprint[x] = true
			}
		}
		if x := col + l.pos; x >= 0 && x < width {
			footprint[x] = true
		}

		lbl := trunc(l.plot.label, pw-1)
		lPad := pw - len([]rune(lbl))
		if lPad < 0 {
			lPad = 0
		}
		lLeft := lPad / 2
		lRight := lPad - lLeft
		lb.WriteString(sChrome.Render(strings.Repeat(" ", lLeft) + lbl + strings.Repeat(" ", lRight)))

		col += pw
	}
	for x := 0; x < width; x++ {
		if r := soilTuft(x); r != 0 && !footprint[x] {
			soilGrid.set(x, r, sDim)
		} else {
			soilGrid.set(x, '▁', soilStyle)
		}
	}
	return plantGrids, lb.String(), soilGrid, skyGrid
}

// --- S2: the cast (fxFull only) ---

// walkTicks is how long a pawn lingers, walking off its now-idle plot, after
// its task leaves AgentWorking.
const walkTicks = 8

// walkKey namespaces a walk-off in the celebrations map so it can never
// collide with a J1 merge-sparkle entry (bare ticket) or a J5 knock ("?"
// prefix).
func walkKey(ticket string) string { return "w" + ticket }

// walkedOff returns tickets whose Agent flipped away from AgentWorking
// between the prior and fresh task snapshots — still present, just no
// longer working. Same diff-in-Update shape as J5's freshQuestions.
func walkedOff(prev, next []*state.Task) []string {
	wasWorking := map[string]bool{}
	for _, t := range prev {
		if t.Agent == state.AgentWorking {
			wasWorking[t.Ticket] = true
		}
	}
	var out []string
	for _, t := range next {
		if wasWorking[t.Ticket] && t.Agent != state.AgentWorking {
			out = append(out, t.Ticket)
		}
	}
	return out
}

// pawnSide alternates a worker's pawn every 4 ticks: -1 left, +1 right.
// hashTicket fixes the starting side so two agents rarely mirror each other.
func pawnSide(ticket string, tick uint64) int {
	if (tick/4+hashTicket(ticket))%2 == 0 {
		return -1
	}
	return 1
}

// orbitOffsets is the fairy's 8-tick loop around a plot's center column.
var orbitOffsets = [8]int{-2, -1, 0, 1, 2, 1, 0, -1}

func fairyOffset(tick uint64) int { return orbitOffsets[tick%8] }

// fairyTrailOffset is one tick behind fairyOffset — (tick+7)%8 rather than
// tick-1 so it never underflows at tick 0.
func fairyTrailOffset(tick uint64) int { return orbitOffsets[(tick+7)%8] }

// fairyTrailRune alternates the dim trail rune by tick parity.
func fairyTrailRune(tick uint64) rune {
	if tick%2 == 0 {
		return '˙'
	}
	return '·'
}

// fairyWindow: an EvAnswered older than this no longer summons the fairy.
const fairyWindow = 45 * time.Second

// latestAnswered maps ticket -> time of its most recent EvAnswered event.
// events is oldest-first, so the last write per ticket wins.
func latestAnswered(events []state.Event) map[string]time.Time {
	out := map[string]time.Time{}
	for _, ev := range events {
		if ev.Type == state.EvAnswered {
			out[ev.Ticket] = ev.Time
		}
	}
	return out
}

// findLayout locates a ticket's plot layout, if any (orchard tiles and
// dropped-by-overflow tickets have none).
func findLayout(layouts []plotLayout, ticket string) (plotLayout, bool) {
	for _, l := range layouts {
		if l.plot.ticket == ticket {
			return l, true
		}
	}
	return plotLayout{}, false
}

// applyCast overlays the pawn(s), the queen, walk-off pawns, and the fairy
// onto the ground/sky grids — fxFull only, purely derived from tasks/
// events/celebrations/focused, no new state beyond the existing capped map.
// Figures stand on the GROUND row beside the trunk base at every tier
// (grove-71: grove-66's strip-tier fallback is now the universal rule — the
// old trunk-row placement is gone); the fairy stays sky-only.
func applyCast(layouts []plotLayout, ticketCol map[string]int, pw int, tasks []*state.Task, events []state.Event, celebrations map[string]int, tick uint64, focused string, groundGrid, skyGrid *sceneGrid) {
	for _, t := range tasks {
		if t.Agent != state.AgentWorking {
			continue
		}
		l, ok := findLayout(layouts, t.Ticket)
		if !ok {
			continue
		}
		c := ticketCol[t.Ticket]
		pos := clampCol(c+l.pos+pawnSide(t.Ticket, tick), c, c+pw-1)
		groundGrid.set(pos, '♟', sWorking)
	}

	for _, t := range tasks {
		remaining, ok := celebrations[walkKey(t.Ticket)]
		if !ok {
			continue
		}
		l, ok := findLayout(layouts, t.Ticket)
		if !ok {
			continue
		}
		c := ticketCol[t.Ticket]
		pos := clampCol(c+l.pos+(walkTicks-remaining), c, c+pw-1)
		groundGrid.set(pos, '♟', sIdle)
	}

	if focused != "" {
		if l, ok := findLayout(layouts, focused); ok {
			c := ticketCol[focused]
			pos := clampCol(c+l.pos-pawnSide(focused, tick), c, c+pw-1)
			groundGrid.set(pos, '♛', sWaiting)
		}
	}

	now := time.Now()
	for ticket, at := range latestAnswered(events) {
		if now.Sub(at) >= fairyWindow || now.Sub(at) < 0 {
			continue
		}
		l, ok := findLayout(layouts, ticket)
		if !ok {
			continue
		}
		center := ticketCol[ticket] + l.pos
		trail := center + fairyTrailOffset(tick)
		if !skyGrid.occupied(trail) {
			skyGrid.set(trail, fairyTrailRune(tick), sDim)
		}
		if fairyCol := center + fairyOffset(tick); !skyGrid.occupied(fairyCol) {
			skyGrid.set(fairyCol, '✧', sDelivery)
		}
	}
}

// sceneLines is the scene's pure render function: always exactly `rows`
// lines, each truncPad-ed to width. fx<fxCalm renders a blank scene (the
// caller never invokes it there since sceneRows is 0 at fxOff, but staying
// total here costs nothing).
func sceneLines(tasks []*state.Task, prs map[string]*github.PR, events []state.Event, celebrations map[string]int, tick uint64, width, rows, hour int, fx fxLevel, focused string) []string {
	if rows <= 0 {
		return nil
	}
	blank := func() []string {
		out := make([]string, rows)
		for i := range out {
			out[i] = truncPad("", width)
		}
		return out
	}
	if fx < fxCalm {
		return blank()
	}

	pal := scenePalettes[timeOfDay(hour)]

	plots := buildOrchardPlots(events)
	for _, t := range tasks {
		plots = append(plots, buildTaskPlot(t, prs[t.Ticket], fx, tick, celebrations, pal))
	}

	if len(plots) == 0 {
		// Empty grove: sky + soil only (no canopy/trunk/label rows at all).
		out := make([]string, rows)
		for i := range out {
			out[i] = strings.Repeat(" ", width)
		}
		out[rows-1] = truncPad(pal.soil.Render(strings.Repeat("▁", width)), width)
		if timeOfDay(hour) == 3 {
			out[0] = truncPad(fireflyTrail(tick), width)
		}
		return out
	}

	tier := sceneTierFor(rows)
	plantRows := plantRowsFor(tier)
	if plantRows > rows-2 {
		plantRows = rows - 2
	}
	if plantRows < 1 {
		plantRows = 1
	}
	topRows := rows - (plantRows + 2)
	if topRows < 0 {
		topRows = 0
	}

	fitted, pw := fitPlots(plots, width)
	layouts := layoutPlots(fitted, pw)
	plantGrids, labelRow, soilGrid, skyGrid := renderPlotRows(layouts, pw, width, plantRows, pal.soil)

	ticketCol := map[string]int{}
	col := 0
	for _, l := range layouts {
		if l.plot.ticket != "" {
			ticketCol[l.plot.ticket] = col
		}
		col += pw
	}
	if fx >= fxFull {
		applyCast(layouts, ticketCol, pw, tasks, events, celebrations, tick, focused, plantGrids[0], skyGrid)
	}
	// S3: the day-cycle sky accent is the lowest-priority sky glyph — it
	// never overwrites a QUESTION/BLOCKED marker or the fairy. grove-71 sky
	// discipline: with more than one sky row it paints the TOPMOST one, so
	// fireflies/moon drift strictly above the tallest canopy instead of
	// hugging the treetops like ornaments.
	var ambientRow string
	if topRows >= 2 {
		ambientGrid := newSceneGrid(width)
		applyAmbientSky(hour, tick, ambientGrid)
		ambientRow = ambientGrid.String()
	} else if topRows == 1 {
		applyAmbientSky(hour, tick, skyGrid)
	}
	skyRow, soilRow := skyGrid.String(), soilGrid.String()

	out := make([]string, 0, rows)
	for i := 0; i < topRows; i++ {
		line := strings.Repeat(" ", width)
		switch {
		case i == 0 && topRows >= 2:
			line = ambientRow
		case i == topRows-1: // the sky row closest to the plants carries markers/fairy
			line = skyRow
		}
		out = append(out, truncPad(line, width))
	}
	for i := plantRows - 1; i >= 0; i-- {
		out = append(out, truncPad(plantGrids[i].String(), width))
	}
	out = append(out, truncPad(soilRow, width))
	out = append(out, truncPad(labelRow, width))
	if len(out) > rows { // rows 1-2: keep the rooted bottom, shed the sky
		out = out[len(out)-rows:]
	}
	return out
}

// sceneHasLife reports whether the cast has anything to show right now: a
// working-agent pawn, a live walk-off pawn, a queen (focused task), or a
// fairy inside its answer window (grove-66). rowBudgets uses this to decide
// whether the scene's floor is worth raising above strip tier — no life
// means the trunk row would sit empty anyway.
func sceneHasLife(tasks []*state.Task, events []state.Event, celebrations map[string]int, focused string) bool {
	for _, t := range tasks {
		if t.Agent == state.AgentWorking {
			return true
		}
		if _, ok := celebrations[walkKey(t.Ticket)]; ok {
			return true
		}
	}
	if focused != "" {
		return true
	}
	now := time.Now()
	for _, at := range latestAnswered(events) {
		if now.Sub(at) < fairyWindow && now.Sub(at) >= 0 {
			return true
		}
	}
	return false
}

// rowBudgets splits the leftover height between ACTIVITY and the scene. At
// fxOff it is byte-identical to the pre-scene sizing (sceneRows always 0);
// at fxCalm+ the scene takes what the feed doesn't need, yielding to the
// feed down to 0 rather than forcing a strip that doesn't fit. grove-66:
// when the cast has life to show (fxFull only — the cast itself is
// fxFull-gated), the floor rises from strip (3) to compact (6) so the
// trunk row exists, as long as ACTIVITY can still keep its own 4-row floor
// (leftover >= 10). No life, or fxCalm, leaves the floor at strip.
func (m Model) rowBudgets() (activityRows, sceneRows int) {
	leftover := m.height - (len(m.tasks) + 4) - 5 - m.footerHeight()
	if leftover < 0 {
		leftover = 0
	}
	items := len(feedItems(m.events))
	activityRows = items
	if activityRows > leftover {
		activityRows = leftover
	}
	if m.fx == fxOff {
		return activityRows, 0
	}
	sceneRows = leftover - activityRows

	floor := 3
	if m.fx >= fxFull && leftover >= 10 && sceneHasLife(m.tasks, m.events, m.celebrations, m.focused) {
		floor = 6
	}
	if sceneRows < floor && leftover >= floor+4 {
		sceneRows = floor
	}
	if sceneRows < floor {
		sceneRows = 0
	}
	activityRows = leftover - sceneRows
	return activityRows, sceneRows
}

// viewScene renders the scene for the row budget rowBudgets handed it.
func (m Model) viewScene(rows int) string {
	lines := sceneLines(m.tasks, m.prs, m.events, m.celebrations, m.tick, m.width, rows, nowHour(), m.fx, m.focused)
	return strings.Join(lines, "\n")
}
