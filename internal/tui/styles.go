package tui

import "github.com/charmbracelet/lipgloss"

// Canopy palette: forest greens carry calm state, amber demands attention,
// rust flags blockers. One accent per semantic, applied everywhere.
var (
	cCanopy = lipgloss.Color("#87c95f") // healthy green
	cMoss   = lipgloss.Color("#5a7d4a") // dim green chrome
	cAmber  = lipgloss.Color("#ffb454") // questions / attention
	cRust   = lipgloss.Color("#e06c60") // blocked / dead / fail
	cSky    = lipgloss.Color("#7fb4ca") // delivery (PR/CI/preview)
	cBark   = lipgloss.Color("#8a8a85") // secondary text
	cFog    = lipgloss.Color("#4a4a45") // borders, dividers
	cInk    = lipgloss.Color("#1c1c1a") // selection bg
)

var (
	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(cCanopy)
	sChrome = lipgloss.NewStyle().Foreground(cBark)
	sDim    = lipgloss.NewStyle().Foreground(cFog)

	sPanel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cFog).
		Padding(0, 1)

	sPanelFocus = sPanel.BorderForeground(cMoss)

	sPanelTitle      = lipgloss.NewStyle().Foreground(cMoss).Bold(true)
	sPanelTitleFocus = lipgloss.NewStyle().Foreground(cCanopy).Bold(true)

	sHeaderCol = lipgloss.NewStyle().Foreground(cFog).Bold(true)

	sSelected = lipgloss.NewStyle().Background(cInk).Bold(true)

	sWorking = lipgloss.NewStyle().Foreground(cCanopy)
	sWaiting = lipgloss.NewStyle().Foreground(cAmber).Bold(true)
	sBlocked = lipgloss.NewStyle().Foreground(cRust).Bold(true)
	sIdle    = lipgloss.NewStyle().Foreground(cBark)
	sDead    = lipgloss.NewStyle().Foreground(cRust)
	sSetup   = lipgloss.NewStyle().Foreground(cSky)

	sDelivery = lipgloss.NewStyle().Foreground(cSky)
	sOK       = lipgloss.NewStyle().Foreground(cCanopy)
	sFail     = lipgloss.NewStyle().Foreground(cRust)

	sKey  = lipgloss.NewStyle().Foreground(cAmber)
	sFoot = lipgloss.NewStyle().Foreground(cBark)

	sQuestion = lipgloss.NewStyle().Foreground(cAmber)
	sBlurb    = lipgloss.NewStyle().Foreground(cBark).Italic(true)
)

func statusStyle(label string) lipgloss.Style {
	switch label {
	case "QUESTION":
		return sWaiting
	case "BLOCKED":
		return sBlocked
	case "dead":
		return sDead
	case "working":
		return sWorking
	case "setup", "queued":
		return sSetup
	default:
		return sIdle
	}
}

func statusGlyph(label string) string {
	switch label {
	case "QUESTION":
		return "◆"
	case "BLOCKED":
		return "⚠"
	case "dead":
		return "✗"
	case "working":
		return "●"
	case "setup", "queued":
		return "◌"
	case "idle ✓":
		return "✓"
	default:
		return "○"
	}
}
