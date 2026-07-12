package tui

// The list-mode footer legend (grove-72): ALWAYS exactly one line. Instead of
// wrapping, hints drop by keep-priority until the remainder fits the pane —
// every hidden hint stays a live key, fully documented behind ?. Bubble Tea's
// renderer assumes one logical line = one screen row (#53a); a single clamped
// line can never desync repaints.

// hint is one atomic legend cell — a key and its label, never split mid-hint.
type hint struct{ key, label string }

// The legend's semantic groups: row acts on the selected task, spawn creates
// things, global is chrome. Package-level tables — the RAM rule forbids
// rebuilding them per frame.
var (
	rowHints    = []hint{{"enter", "reply"}}
	spawnHints  = []hint{{"O", "new chat"}, {")", "profiled chat"}}
	globalHints = []hint{
		{"?", "help"}, {"L", "layout"}, {"$", "costs"},
		{"*", "effects"}, {"X", "park"}, {"q", "quit"},
	}
)

// keepRank orders hints by how long they survive a shrinking pane — lower
// rank survives longer. Ranks below minKeep are the operator-specified
// minimum trio (O, ), ?): they never drop, they only shed their labels and,
// below the bare keys, truncate.
var keepRank = map[string]int{
	"?": 0, "O": 1, ")": 2, "enter": 3, "L": 4, "$": 5, "*": 6, "X": 7, "q": 8,
}

const (
	intraSep = " · "   // between hints of one group
	interSep = "  │  " // heavier break between groups
	minKeep  = 3       // ranks below this never drop: the O/)/? trio
)

func (h hint) render() string { return sKey.Render(h.key) + sFoot.Render(" "+h.label) }
func (h hint) width() int     { return len([]rune(h.key)) + 1 + len([]rune(h.label)) }

// footerGroups picks the groups for the current fleet: with zero tasks the
// row group is dead weight — the empty-state line already teaches gv grab —
// so it drops entirely, regardless of width.
func footerGroups(hasTasks bool) [][]hint {
	if hasTasks {
		return [][]hint{rowHints, spawnHints, globalHints}
	}
	return [][]hint{spawnHints, globalHints}
}

// legendLine renders the hints ranked below cut, in canonical group order,
// with the usual separators and empty groups' separators dropped; hints
// ranked at or above bareFrom render as their bare key. Returns the styled
// line and its plain-cell width — styling is applied per cell, widths are
// plain rune counts, so the math is ANSI-safe by construction.
func legendLine(groups [][]hint, cut, bareFrom int) (string, int) {
	line, w, first := " ", 1, true
	for _, g := range groups {
		gFirst := true
		for _, h := range g {
			r, ok := keepRank[h.key]
			if !ok {
				r = 0 // unranked hints never drop — dropping would hide a live key
			}
			if r >= cut {
				continue
			}
			switch {
			case first:
			case gFirst:
				line += sDim.Render(interSep)
				w += len([]rune(interSep))
			default:
				line += sDim.Render(intraSep)
				w += len([]rune(intraSep))
			}
			if r >= bareFrom {
				line += sKey.Render(h.key)
				w += len([]rune(h.key))
			} else {
				line += h.render()
				w += h.width()
			}
			first, gFirst = false, false
		}
	}
	return line, w
}

// footerLegend is the one-line legend for the given width. Hints keep their
// keepRank while the whole line fits and drop lowest-priority-first when it
// doesn't; below the full trio the trio sheds labels lowest-priority-first.
// The bare trio is the floor: below its width (~12 cells) the returned line
// still carries all three keys and may exceed width — the caller clamps to
// the pane (truncate, never wrap), so a flash reservation can never evict
// the trio, only shrink the flash.
func footerLegend(width int, hasTasks bool) string {
	groups := footerGroups(hasTasks)
	for cut := len(keepRank); cut > minKeep; cut-- {
		if line, w := legendLine(groups, cut, cut); w <= width {
			return line
		}
	}
	for bareFrom := minKeep; bareFrom > 0; bareFrom-- {
		if line, w := legendLine(groups, minKeep, bareFrom); w <= width {
			return line
		}
	}
	line, _ := legendLine(groups, minKeep, 0)
	return line
}

// footerHeight is the exact line count viewFooter will emit — one, always,
// since grove-72. It stays a method so the layout math (rowBudgets, the
// ACTIVITY budget) keeps reading a single source of truth.
func (m Model) footerHeight() int { return 1 }
