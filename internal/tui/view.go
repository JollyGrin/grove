package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/state"
)

func (m Model) View() string {
	if m.width == 0 {
		return "planting…"
	}
	if m.mode == modeDetail {
		return m.viewDetail()
	}

	var b strings.Builder
	b.WriteString(m.viewHeader())
	b.WriteString("\n")
	b.WriteString(m.viewAgents())
	b.WriteString("\n")
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, m.viewMail(), m.viewReview()))
	b.WriteString("\n")
	b.WriteString(m.viewFooter())
	return b.String()
}

func (m Model) viewHeader() string {
	working, mail, review := 0, len(m.mailRows()), len(m.reviewRows())
	for _, t := range m.tasks {
		if t.Agent == state.AgentWorking || t.Agent == state.AgentSetup {
			working++
		}
	}
	left := sTitle.Render(" ❉ GROVE ") + sChrome.Render("· the canopy")
	right := fmt.Sprintf("%s working · %s mail · %s review ",
		sWorking.Render(fmt.Sprint(working)),
		sWaiting.Render(fmt.Sprint(mail)),
		sDelivery.Render(fmt.Sprint(review)))
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + sChrome.Render(right)
}

func (m Model) viewAgents() string {
	w := m.width - 4
	// Cells are padded as plain text FIRST, then styled — ANSI codes inside
	// %-Ns break fmt's width accounting (field-tested on the first render).
	header := "   " + pad("TICKET", 11) + pad("REPO", 11) + pad("STATUS", 11) +
		pad("LIVE", 8) + pad("PR", 8) + pad("CI", 4) + pad("PREVIEW", 9) + "AGE"
	rows := []string{sHeaderCol.Render(truncPad(header, w))}

	if len(m.tasks) == 0 {
		rows = append(rows, sDim.Render("  no active tasks — gv grab <ticket> plants one"))
	}
	for i, t := range m.tasks {
		label := t.Label()
		st := statusStyle(label)
		pr, ci, preview := "—", "—", "—"
		ciStyle := sDim
		if p := m.prs[t.Ticket]; p != nil {
			pr = fmt.Sprintf("#%d", p.Number)
			if p.State == "MERGED" {
				pr += "⬢"
			}
			switch p.CI {
			case "pass":
				ci, ciStyle = "✓", sOK
			case "fail":
				ci, ciStyle = "✗", sFail
			case "pending":
				ci, ciStyle = "◌", sDelivery
			}
			if p.PreviewURL != "" {
				preview = "⬡ up"
			}
		}
		cursor := " "
		if m.focus == panelAgents && i == m.sel[panelAgents] {
			cursor = sSelected.Render("▸")
		}
		line := cursor + st.Render(statusGlyph(label)) + " " +
			pad(t.Ticket, 11) +
			pad(trunc(t.Repo, 10), 11) +
			st.Render(pad(label, 11)) +
			sDim.Render(pad(m.live[t.Ticket], 8)) +
			sDelivery.Render(pad(pr, 8)) +
			ciStyle.Render(pad(ci, 4)) +
			sDelivery.Render(pad(preview, 9)) +
			sChrome.Render(age(t.Created))
		rows = append(rows, truncPad(line, w))
	}

	title := sPanelTitle
	style := sPanel
	if m.focus == panelAgents {
		title, style = sPanelTitleFocus, sPanelFocus
	}
	body := title.Render("AGENTS") + "\n" + strings.Join(rows, "\n")
	return style.Width(m.width - 2).Render(body)
}

func (m Model) viewMail() string {
	w := (m.width - 4) / 2
	rows := []string{}
	mail := m.mailRows()
	if len(mail) == 0 {
		rows = append(rows, sDim.Render(" canopy is quiet"))
	}
	for i, t := range mail {
		glyph := statusGlyph(t.Label())
		text := t.Question
		if text == "" {
			text = firstLine(t.LastMessage)
		}
		if text == "" {
			text = "(no message — attach to inspect)"
		}
		line := fmt.Sprintf("%s %s  %s",
			statusStyle(t.Label()).Render(glyph), t.Ticket, sQuestion.Render(text))
		if m.focus == panelMail && i == m.sel[panelMail] {
			line = sSelected.Render("▸") + line
		} else {
			line = " " + line
		}
		rows = append(rows, truncPad(line, w-2))
	}

	title := sPanelTitle
	style := sPanel
	if m.focus == panelMail {
		title, style = sPanelTitleFocus, sPanelFocus
	}
	heading := fmt.Sprintf("MAIL (%d)", len(mail))
	return style.Width(w).Render(title.Render(heading) + "\n" + strings.Join(rows, "\n"))
}

func (m Model) viewReview() string {
	w := m.width - 2 - ((m.width - 4) / 2) - 2
	rows := []string{}
	review := m.reviewRows()
	if len(review) == 0 {
		rows = append(rows, sDim.Render(" nothing to review yet"))
	}
	for i, t := range review {
		mark, markStyle := "·", sDim // fresh, awaiting eyes
		if t.Human == state.HumanReviewing {
			mark, markStyle = "◉", sDelivery
		}
		pr, tail := "", sBlurb.Render(doneBlurb(t))
		if p := m.prs[t.Ticket]; p != nil {
			pr = fmt.Sprintf("#%d ", p.Number)
			if p.State == "MERGED" {
				mark, markStyle = "⬢", sOK
				tail = sOK.Render("merged — press d to clean up")
			} else if p.PreviewURL != "" {
				pr += sDelivery.Render("⬡ ")
			}
		}
		line := fmt.Sprintf("%s %s %s%s", markStyle.Render(mark), t.Ticket, pr, tail)
		if m.focus == panelReview && i == m.sel[panelReview] {
			line = sSelected.Render("▸") + line
		} else {
			line = " " + line
		}
		rows = append(rows, truncPad(line, w-2))
	}

	title := sPanelTitle
	style := sPanel
	if m.focus == panelReview {
		title, style = sPanelTitleFocus, sPanelFocus
	}
	heading := fmt.Sprintf("REVIEW QUEUE (%d)", len(review))
	return style.Width(w).Render(title.Render(heading) + "\n" + strings.Join(rows, "\n"))
}

func (m Model) viewFooter() string {
	keys := []string{
		sKey.Render("enter") + sFoot.Render(" reply"),
		sKey.Render("a") + sFoot.Render(" attach"),
		sKey.Render("o") + sFoot.Render(" preview"),
		sKey.Render("p") + sFoot.Render(" PR"),
		sKey.Render("t") + sFoot.Render(" ticket"),
		sKey.Render("v") + sFoot.Render(" reviewing"),
		sKey.Render("n") + sFoot.Render(" nudge"),
		sKey.Render("d") + sFoot.Render(" done"),
		sKey.Render("tab") + sFoot.Render(" panel"),
		sKey.Render("q") + sFoot.Render(" quit"),
	}
	line := " " + strings.Join(keys, sDim.Render(" · "))
	if m.flash != "" {
		line += "   " + sChrome.Render(trunc(m.flash, m.width-lipgloss.Width(line)-4))
	}
	if m.mode == modeConfirmDone && m.detail != nil {
		line = " " + sBlocked.Render("done "+m.detail.Ticket+"? merged-check + full cleanup ") +
			sKey.Render("y") + sFoot.Render(" confirm · any other key cancels")
	}
	return line
}

func (m Model) viewDetail() string {
	t := m.detail
	w := m.width - 4

	label := t.Label()
	head := fmt.Sprintf("%s %s %s  %s",
		statusStyle(label).Render(statusGlyph(label)),
		sTitle.Render(t.Ticket),
		sChrome.Render(trunc(t.Title, w-30)),
		statusStyle(label).Render(label))

	var sections []string
	sections = append(sections, head, "")

	if t.Question != "" {
		sections = append(sections, sQuestion.Render("◆ "+t.Question), "")
	}
	if t.LastMessage != "" && t.Question == "" {
		sections = append(sections, sChrome.Render(wrap(firstLines(t.LastMessage, 6), w)), "")
	}

	if m.paneTail != "" {
		sections = append(sections, sPanelTitle.Render("PANE"))
		for _, l := range strings.Split(strings.TrimRight(m.paneTail, "\n"), "\n") {
			sections = append(sections, sDim.Render(trunc(l, w)))
		}
		sections = append(sections, "")
	}

	verb := "ANSWER"
	if m.nudging {
		verb = "NUDGE"
	}
	sections = append(sections,
		sPanelTitleFocus.Render(verb)+sChrome.Render("  (enter sends · esc backs out · single char = raw key)"),
		m.input.View())

	body := sPanelFocus.Width(m.width - 2).Render(strings.Join(sections, "\n"))
	return m.viewHeader() + "\n" + body
}

// --- text helpers ---

func age(t time.Time) string {
	d := time.Since(t).Round(time.Minute)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// pad right-pads plain text to n cells (must run before styling).
func pad(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		return trunc(s, n)
	}
	return s + strings.Repeat(" ", n-len(r))
}

func trunc(s string, n int) string {
	if n <= 1 {
		return ""
	}
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func truncPad(s string, n int) string {
	if lipgloss.Width(s) > n {
		return lipgloss.NewStyle().MaxWidth(n).Render(s)
	}
	return s
}

func firstLine(s string) string {
	return strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func wrap(s string, w int) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		for len([]rune(line)) > w {
			out = append(out, string([]rune(line)[:w]))
			line = string([]rune(line)[w:])
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
