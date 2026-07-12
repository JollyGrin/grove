package tui

// The living grove (grove-63): the landscape between ACTIVITY and the
// footer, rendered only in modeList. Pure function of (tasks, prs, events,
// celebrations, tick, width, rows, hour, fx, focused) — always returns
// exactly `rows` lines, each truncPad-ed to width. See
// docs/plans/2026-07-12-living-grove-design.md for the locked glyph
// vocabulary and algorithms this file implements.

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
	sceneStrip sceneTier = iota // 3-5 rows: canopy, soil, label — no trunk
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
			canopy: forestGlyph, style: sForest,
			label: fmt.Sprintf("%s×%d", forestGlyph, remainder),
		})
	}
	for _, tk := range done[n-individualN:] {
		plots = append(plots, scenePlot{
			ticket: tk, orchard: true,
			canopy: forestGlyph, style: sForest,
			label: ticketLabel(tk) + " ⬢",
		})
	}
	return plots
}

// buildTaskPlot renders one active task's plant. Priority mirrors the S1
// table: dead > merged PR > setup > the age-bucketed growth stage, with
// QUESTION/BLOCKED/idle overlaying a marker or style on that stage glyph.
func buildTaskPlot(t *state.Task, pr *github.PR, fx fxLevel, tick uint64, celebrations map[string]int) scenePlot {
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
	p := scenePlot{ticket: t.Ticket, canopy: canopy, trunk: trunk, style: sWorking}

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
		p.style = sIdle
		p.label = tl + " ✓"
	default:
		p.label = tl + " " + age(t.Created)
	}
	return p
}

// fitPlots is the overflow ladder: drop the condensed orchard remainder,
// then collapse the rest of the orchard to one tile, then shrink plotW
// (8, then 6), and finally keep the leftmost tiles that fit at 6, relabeling
// the last as "+K more". Never truncates mid-plot.
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
		last.label = fmt.Sprintf("+%d more", dropped)
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

// renderPlotRows lays out the canopy/soil/label rows as plain strings (no
// overlay ever touches them) and the trunk/sky rows as sceneGrids — S2's
// cast overlays (pawn/queen/walk-off/fairy) mutate those grids before the
// caller stringifies them.
func renderPlotRows(layouts []plotLayout, pw int, hasTrunk bool) (canopy, soil, label string, trunkGrid, skyGrid *sceneGrid) {
	width := len(layouts) * pw
	trunkGrid = newSceneGrid(width)
	skyGrid = newSceneGrid(width)

	var cb, sb, lb strings.Builder
	col := 0
	for _, l := range layouts {
		right := pw - l.left - len([]rune(l.cluster))
		if right < 0 {
			right = 0
		}
		cb.WriteString(l.plot.style.Render(strings.Repeat(" ", l.left) + l.cluster + strings.Repeat(" ", right)))

		if hasTrunk && l.plot.trunk != "" {
			trunkGrid.set(col+l.pos, []rune(l.plot.trunk)[0], l.plot.style)
		}
		if l.plot.marker != "" {
			skyGrid.set(col+l.pos, []rune(l.plot.marker)[0], l.plot.markerSt)
		}

		sb.WriteString(sChrome.Render(strings.Repeat("▁", pw)))

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
	return cb.String(), sb.String(), lb.String(), trunkGrid, skyGrid
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
// onto the trunk/sky grids — fxFull only, purely derived from tasks/events/
// celebrations/focused, no new state beyond the existing capped map.
func applyCast(layouts []plotLayout, ticketCol map[string]int, pw int, tasks []*state.Task, events []state.Event, celebrations map[string]int, tick uint64, focused string, hasTrunk bool, trunkGrid, skyGrid *sceneGrid) {
	if hasTrunk {
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
			trunkGrid.set(pos, '♟', sWorking)
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
			trunkGrid.set(pos, '♟', sIdle)
		}

		if focused != "" {
			if l, ok := findLayout(layouts, focused); ok {
				c := ticketCol[focused]
				pos := clampCol(c+l.pos-pawnSide(focused, tick), c, c+pw-1)
				trunkGrid.set(pos, '♛', sWaiting)
			}
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
		skyGrid.set(center+fairyOffset(tick), '✧', sDelivery)
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

	plots := buildOrchardPlots(events)
	for _, t := range tasks {
		plots = append(plots, buildTaskPlot(t, prs[t.Ticket], fx, tick, celebrations))
	}

	if len(plots) == 0 {
		// Empty grove: sky + soil only (no canopy/trunk/label rows at all).
		out := make([]string, rows)
		for i := range out {
			out[i] = strings.Repeat(" ", width)
		}
		out[rows-1] = truncPad(sChrome.Render(strings.Repeat("▁", width)), width)
		if timeOfDay(hour) == 3 {
			out[0] = truncPad(fireflyTrail(tick), width)
		}
		return out
	}

	tier := sceneTierFor(rows)
	hasTrunk := tier != sceneStrip
	baseRows := 3
	if hasTrunk {
		baseRows = 4
	}
	if baseRows > rows {
		baseRows = rows
	}
	topRows := rows - baseRows

	fitted, pw := fitPlots(plots, width)
	layouts := layoutPlots(fitted, pw)
	canopyRow, soilRow, labelRow, trunkGrid, skyGrid := renderPlotRows(layouts, pw, hasTrunk)

	ticketCol := map[string]int{}
	col := 0
	for _, l := range layouts {
		if l.plot.ticket != "" {
			ticketCol[l.plot.ticket] = col
		}
		col += pw
	}
	if fx >= fxFull {
		applyCast(layouts, ticketCol, pw, tasks, events, celebrations, tick, focused, hasTrunk, trunkGrid, skyGrid)
	}
	trunkRow, skyRow := trunkGrid.String(), skyGrid.String()

	out := make([]string, 0, rows)
	for i := 0; i < topRows; i++ {
		line := strings.Repeat(" ", width)
		if i == topRows-1 { // the sky row closest to the canopy carries markers/fairy
			line = skyRow
		}
		out = append(out, truncPad(line, width))
	}
	out = append(out, truncPad(canopyRow, width))
	if hasTrunk {
		out = append(out, truncPad(trunkRow, width))
	}
	out = append(out, truncPad(soilRow, width))
	out = append(out, truncPad(labelRow, width))
	return out
}

// rowBudgets splits the leftover height between ACTIVITY and the scene. At
// fxOff it is byte-identical to the pre-scene sizing (sceneRows always 0);
// at fxCalm+ the scene takes what the feed doesn't need, yielding to the
// feed down to 0 rather than forcing a strip that doesn't fit.
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
	if sceneRows < 3 && leftover >= 7 {
		sceneRows = 3
	}
	if sceneRows < 3 {
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
