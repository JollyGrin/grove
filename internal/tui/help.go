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

	var sections []string
	for _, s := range helpSections {
		rows := []string{sPanelTitleFocus.Render(truncPad(s.title, w))}
		for _, e := range s.entries {
			rows = append(rows, truncPad("  "+sKey.Render(pad(e.key, 7))+sFoot.Render(e.desc), w))
		}
		sections = append(sections, strings.Join(rows, "\n"))
	}
	panel := sPanelFocus.Width(m.width - 2).Render(strings.Join(sections, "\n\n"))

	foot := " " + sKey.Render("esc") + sFoot.Render(" or ") +
		sKey.Render("?") + sFoot.Render(" close")
	return m.viewHeader() + "\n" + panel + "\n" + truncPad(foot, m.width)
}
