package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The ? help overlay (grove-60 §5): a static, grouped cheat sheet — every
// footer key with a one-line plain-language description. The footer stays
// terse because depth lives one keypress away. All content is package-level;
// the overlay costs nothing at runtime.

type helpEntry struct{ key, desc string }

var (
	helpRow = []helpEntry{
		{"j/k", "move the selection up/down the fleet"},
		{"enter", "reply: open the task and type straight into its agent's pane"},
		{"a", "attach: switch your tmux client to the task's window"},
		{"o", "preview: open the PR's preview deploy (falls back to PR, then ticket)"},
		{"p", "PR: open the task's pull request in the browser"},
		{"t", "task: open the ticket/issue in the browser"},
		{"v", "mark you're reviewing: silences re-pings while you read the PR (toggle)"},
		{"n", "nudge: send the agent a wake-up message"},
		{"d", "done: merged-check + full cleanup of the task — asks to confirm first"},
	}
	helpSpawn = []helpEntry{
		{"O", "new chat: spawn an orchestrator chat pane (0 works too)"},
		{")", "profiled chat: orchestrator on a configured model profile"},
	}
	helpGlobal = []helpEntry{
		{"?", "this cheat sheet"},
		{"L", "layout: cycle the cockpit pane split (persisted on the session)"},
		{"$", "costs: per-ticket spend, ledger history, spend-over-time chart"},
		{"*", "effects: cycle ambient effects off → calm → full (runtime only)"},
		{"g", "gardens: the almanac — one garden per day, h/l walk days"},
		{"X", "park: stop workers + orchestrator + cockpit; state stays on disk"},
		{"r", "refresh PR states now (also polled every 30s)"},
		{"q", "quit the cockpit — workers keep running"},
	}
	helpSections = []struct {
		title   string
		entries []helpEntry
	}{
		{"ROW — act on the selected task", helpRow},
		{"SPAWN", helpSpawn},
		{"GLOBAL", helpGlobal},
	}
)

func (m Model) handleHelpKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "?":
		m.mode = modeList
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) viewHelp() string {
	w := m.width - 4

	var rows []string
	for i, s := range helpSections {
		if i > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, sPanelTitleFocus.Render(truncPad(s.title, w)))
		for _, e := range s.entries {
			rows = append(rows, truncPad("  "+sKey.Render(pad(e.key, 7))+sFoot.Render(e.desc), w))
		}
	}
	// Same height discipline as the footer itself (grove-60): the overlay
	// must fit m.height — header(1) + panel borders(2) + foot(1) leave
	// height-4 content rows; anything past that is trimmed behind a hint,
	// never soft-scrolled by the alt-screen.
	if maxRows := m.height - 4; len(rows) > maxRows {
		if maxRows < 1 {
			maxRows = 1
		}
		rows = rows[:maxRows]
		rows[len(rows)-1] = sDim.Render(truncPad("  … a taller pane shows the rest", w))
	}
	panel := sPanelFocus.Width(m.width - 2).Render(strings.Join(rows, "\n"))

	foot := " " + sKey.Render("esc") + sFoot.Render(" or ") +
		sKey.Render("?") + sFoot.Render(" close")
	return m.viewHeader() + "\n" + panel + "\n" + truncPad(foot, m.width)
}
