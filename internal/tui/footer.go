package tui

import "strings"

// The list-mode footer legend (grove-60): atomic key+label cells flowed onto
// as many REAL lines as the width requires. Bubble Tea's renderer assumes one
// logical line = one screen row, so the footer must emit its own newlines —
// a terminal-level soft wrap desyncs repaints (#53a).

// hint is one atomic legend cell — a key and its label, never split mid-hint.
type hint struct{ key, label string }

// The legend's semantic groups: row acts on the selected task, spawn creates
// things, global is chrome. Package-level tables — the RAM rule forbids
// rebuilding them per frame.
var (
	rowHints = []hint{
		{"enter", "reply"}, {"a", "attach"}, {"o", "preview"}, {"p", "PR"},
		{"t", "task"}, {"v", "reviewing"}, {"n", "nudge"}, {"d", "done"},
	}
	spawnHints  = []hint{{"O", "new chat"}, {")", "profiled chat"}}
	globalHints = []hint{
		{"?", "help"}, {"L", "layout"}, {"$", "costs"},
		{"*", "effects"}, {"X", "park"}, {"q", "quit"},
	}
)

const (
	intraSep   = " · "   // between hints of one group
	interSep   = "  │  " // heavier break between groups
	minLegendW = 20      // below this, degrade to one truncated line
)

func (h hint) render() string { return sKey.Render(h.key) + sFoot.Render(" "+h.label) }
func (h hint) width() int     { return len([]rune(h.key)) + 1 + len([]rune(h.label)) }

// footerGroups picks the groups for the current fleet: with zero tasks the
// row group is dead weight — the empty-state line already teaches gv grab —
// so it drops entirely and the legend usually stops wrapping at all.
func footerGroups(hasTasks bool) [][]hint {
	if hasTasks {
		return [][]hint{rowHints, spawnHints, globalHints}
	}
	return [][]hint{spawnHints, globalHints}
}

func groupWidth(g []hint) int {
	w := 0
	for i, h := range g {
		if i > 0 {
			w += len([]rune(intraSep))
		}
		w += h.width()
	}
	return w
}

// footerLines flows the legend into lines each ≤ width cells. Hints are
// atomic; a wrap prefers a group boundary when the whole group fits on a
// fresh line, else it breaks between hints. Pure function of its inputs —
// widths are tracked as plain rune cells, styling is applied per cell, so
// the math is ANSI-safe by construction.
func footerLines(width int, hasTasks bool) []string {
	groups := footerGroups(hasTasks)
	if width < minLegendW {
		// Absurdly narrow pane: one truncated line beats eating the screen.
		var all []string
		for _, g := range groups {
			for _, h := range g {
				all = append(all, h.render())
			}
		}
		return []string{truncPad(" "+strings.Join(all, sDim.Render(intraSep)), width)}
	}

	var lines []string
	cur, curW := " ", 1
	flush := func() {
		if curW > 1 {
			lines = append(lines, cur)
		}
		cur, curW = " ", 1
	}
	for _, g := range groups {
		// Prefer the break at the group boundary: if the group can't finish
		// on this line but fits whole on a fresh one, break before it.
		gw := groupWidth(g)
		if curW > 1 && curW+len([]rune(interSep))+gw > width && 1+gw <= width {
			flush()
		}
		for i, h := range g {
			sep, sepW := "", 0
			switch {
			case curW == 1: // line start — no separator
			case i == 0:
				sep, sepW = sDim.Render(interSep), len([]rune(interSep))
			default:
				sep, sepW = sDim.Render(intraSep), len([]rune(intraSep))
			}
			if curW > 1 && curW+sepW+h.width() > width {
				flush()
				sep, sepW = "", 0
			}
			cur += sep + h.render()
			curW += sepW + h.width()
		}
	}
	flush()
	for i, l := range lines {
		lines[i] = truncPad(l, width) // belt and braces: one oversized hint
	}
	return lines
}

// footerHeight is the exact line count viewFooter will emit for the current
// mode/width/fleet — viewActivity yields this many rows so the total render
// never exceeds m.height and scrolls the alt-screen.
func (m Model) footerHeight() int {
	if (m.mode == modeConfirmDone && m.detail != nil) || m.mode == modeConfirmClose {
		return 1
	}
	return len(footerLines(m.width, len(m.tasks) > 0))
}
