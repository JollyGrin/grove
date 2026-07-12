package tui

// THE ALMANAC (grove-63 S4): one garden per LOCAL calendar day, browsable
// history built from the full events.jsonl. Data is read once on mode
// entry (a one-shot tea.Cmd, not on the 1s tick) — reopening the mode
// re-reads.

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/state"
)

// almanacDay is one calendar day's garden. date is LOCAL midnight — the
// bucketing key. Empty days (no events at all) still appear with a zero
// value so h/l can walk a contiguous calendar.
type almanacDay struct {
	date      time.Time
	shipped   []string // tickets of EvTaskDone that day, oldest-first
	planted   int      // EvTaskCreated
	answered  int      // EvAnswered
	questions int      // EvAgentStatus(sentinel=question) + EvNotification
	parked    int      // EvWorkspaceParked
}

// almanacMsg is the one-shot snapshot from almanacCmd: oldest → newest,
// contiguous by calendar day.
type almanacMsg struct{ days []almanacDay }

// almanacMaxDays clamps the contiguous range to the most recent year —
// events older than that still count in a day already inside the window,
// but don't stretch the calendar further back.
const almanacMaxDays = 365

// localMidnight is the bucketing key: LOCAL calendar day, not UTC — grove-51
// shipped a UTC-bucketing bug in the spend chart that made evening work
// look untracked. Do not repeat it here.
func localMidnight(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// buildAlmanacDays folds the full event log into a contiguous, oldest-first
// calendar of days from the earliest event's LOCAL day through "now"'s
// LOCAL day, empty days included, clamped to the most recent
// almanacMaxDays. now is injected so tests don't depend on the wall clock.
func buildAlmanacDays(events []state.Event, now time.Time) []almanacDay {
	if len(events) == 0 {
		return nil
	}
	byDay := map[time.Time]*almanacDay{}
	earliest := localMidnight(events[0].Time)
	for _, ev := range events {
		d := localMidnight(ev.Time)
		if d.Before(earliest) {
			earliest = d
		}
		day := byDay[d]
		if day == nil {
			day = &almanacDay{date: d}
			byDay[d] = day
		}
		switch ev.Type {
		case state.EvTaskDone:
			day.shipped = append(day.shipped, ev.Ticket)
		case state.EvTaskCreated:
			day.planted++
		case state.EvAnswered:
			day.answered++
		case state.EvAgentStatus:
			if ev.Data["sentinel"] == "question" {
				day.questions++
			}
		case state.EvNotification:
			day.questions++
		case state.EvWorkspaceParked:
			day.parked++
		}
	}

	today := localMidnight(now)
	if floor := today.AddDate(0, 0, -(almanacMaxDays - 1)); earliest.Before(floor) {
		earliest = floor
	}
	if earliest.After(today) {
		earliest = today
	}

	var days []almanacDay
	for d := earliest; !d.After(today); d = d.AddDate(0, 0, 1) {
		if day, ok := byDay[d]; ok {
			days = append(days, *day)
		} else {
			days = append(days, almanacDay{date: d})
		}
	}
	return days
}

// almanacCmd is the one-shot read: state.ReadEvents with limit 0 (the full
// file), off the tea loop. events.jsonl is read-only here — the almanac
// never appends.
func almanacCmd(stateDir string) tea.Cmd {
	return func() tea.Msg {
		events, _ := state.ReadEvents(stateDir, 0)
		return almanacMsg{days: buildAlmanacDays(events, time.Now())}
	}
}

// almanacStatLine composes "N shipped · N planted · N answered · N
// questions", omitting zero categories, with a trailing "· parked" on a
// day that saw a workspace park.
func almanacStatLine(day almanacDay) string {
	var parts []string
	if n := len(day.shipped); n > 0 {
		parts = append(parts, fmt.Sprintf("%d shipped", n))
	}
	if day.planted > 0 {
		parts = append(parts, fmt.Sprintf("%d planted", day.planted))
	}
	if day.answered > 0 {
		parts = append(parts, fmt.Sprintf("%d answered", day.answered))
	}
	if day.questions > 0 {
		parts = append(parts, fmt.Sprintf("%d questions", day.questions))
	}
	if len(parts) == 0 {
		return ""
	}
	line := strings.Join(parts, " · ")
	if day.parked > 0 {
		line += " · parked"
	}
	return line
}

// almanacPlots renders one mature ♠ tree per shipped ticket — the same plot
// primitives S1 uses for the orchard, condensed past the width exactly like
// the scene (fitPlots/layoutPlots/renderPlotRows).
func almanacPlots(day almanacDay) []scenePlot {
	var plots []scenePlot
	for _, tk := range day.shipped {
		plots = append(plots, scenePlot{
			ticket: tk, canopy: forestGlyph, trunk: "┃", style: sForest,
			label: ticketLabel(tk) + " ⬢",
		})
	}
	return plots
}

func (m Model) handleAlmanacKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "h", "left":
		if m.almSel > 0 {
			m.almSel--
		}
	case "l", "right":
		if m.almSel < len(m.almanac.days)-1 {
			m.almSel++
		}
	case "g", "esc":
		m.mode = modeList
		m.flash = ""
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) viewAlmanac() string {
	w := m.width - 4
	days := m.almanac.days

	title := "THE ALMANAC"
	var body []string
	if len(days) == 0 {
		body = append(body, "", sDim.Render("  no history yet"))
	} else {
		sel := m.almSel
		if sel < 0 {
			sel = 0
		}
		if sel >= len(days) {
			sel = len(days) - 1
		}
		day := days[sel]

		weekday := strings.ToLower(day.date.Format("Monday"))
		title = fmt.Sprintf("THE ALMANAC ── %s · %s", weekday, day.date.Format("2006-01-02"))
		if sel == len(days)-1 {
			title += " · today"
		}

		body = append(body, "")
		if plots := almanacPlots(day); len(plots) > 0 {
			fitted, pw := fitPlots(plots, w)
			layouts := layoutPlots(fitted, pw)
			// Full-tier plant region (3 rows): the garden's ♠s root to the
			// soil with the same bottom-up primitives as the scene (grove-71).
			plantGrids, labelRow, soilGrid, _ := renderPlotRows(layouts, pw, w, 3, sChrome)
			for i := len(plantGrids) - 1; i >= 0; i-- {
				body = append(body, truncPad(plantGrids[i].String(), w))
			}
			body = append(body,
				truncPad(soilGrid.String(), w),
				truncPad(labelRow, w),
			)
		} else {
			line := "  a quiet day in the grove"
			if timeOfDay(nowHour()) == 3 { // fireflies key off nowHour, not the browsed day
				line = sDim.Render(line) + fireflyTrail(m.tick)
			} else {
				line = sDim.Render(line)
			}
			body = append(body,
				truncPad(sChrome.Render(strings.Repeat("▁", w)), w),
				truncPad(line, w),
			)
		}

		body = append(body, "")
		if stat := almanacStatLine(day); stat != "" {
			body = append(body, "  "+sChrome.Render(stat))
		}
	}

	// Same height discipline as the help overlay (grove-60): trim behind a
	// dim hint rather than let the alt-screen soft-scroll. header(1) +
	// border(2) + the panel's own title line(1) + footer(1) leave
	// height-5 rows for the body.
	if maxRows := m.height - 5; len(body) > maxRows {
		if maxRows < 1 {
			maxRows = 1
		}
		body = body[:maxRows]
		body[len(body)-1] = sDim.Render(truncPad("  … a taller pane shows the rest", w))
	}

	panelBody := sPanelTitleFocus.Render(title) + "\n" + strings.Join(body, "\n")
	panel := sPanelFocus.Width(m.width - 2).Render(panelBody)
	return m.viewHeader() + "\n" + panel + "\n" + m.viewAlmanacFooter()
}

func (m Model) viewAlmanacFooter() string {
	line := " " + strings.Join([]string{
		sKey.Render("h/←") + sFoot.Render(" older"),
		sKey.Render("l/→") + sFoot.Render(" newer"),
		sKey.Render("esc") + sFoot.Render(" back"),
		sKey.Render("q") + sFoot.Render(" quit"),
	}, sDim.Render(" · "))
	if m.flash != "" {
		line += "   " + sChrome.Render(m.flash)
	}
	return truncPad(line, m.width)
}
