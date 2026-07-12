package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/resource"
)

func (m Model) View() string {
	if m.width == 0 {
		return "planting…"
	}
	if m.mode == modeDetail {
		return m.viewDetail()
	}
	if m.mode == modeCosts {
		return m.viewCosts()
	}
	if m.mode == modeProfilePick {
		return m.viewProfilePick()
	}

	var b strings.Builder
	b.WriteString(m.viewHeader())
	b.WriteString("\n")
	b.WriteString(m.viewAgents())
	b.WriteString("\n")
	b.WriteString(m.viewActivity())
	b.WriteString("\n")
	b.WriteString(m.viewFooter())
	return b.String()
}

func (m Model) viewHeader() string {
	working, mail, review := countWorking(m.tasks), len(m.mailRows()), len(m.reviewRows())
	scope := "· the canopy"
	if m.label != "" {
		scope = "· " + m.label
	}
	left := sTitle.Render(" ⁂ GROVE ") + sChrome.Render(scope)
	counts := fmt.Sprintf("%s working · %s mail · %s review ",
		sWorking.Render(fmt.Sprint(working)),
		sWaiting.Render(fmt.Sprint(mail)),
		sDelivery.Render(fmt.Sprint(review)))
	// J3 forest strip: one ⸙ per shipped tree in the loaded window, moss
	// green, left of the counts (grove-56).
	strip := ""
	if m.fx >= fxCalm {
		if s := forestStrip(countDone(m.events)); s != "" {
			strip = sForest.Render(s) + " · "
		}
	}
	right := m.memGauge() + strip + counts
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 && strip != "" {
		// A narrow pane sheds the flourish before the data: an overflowing
		// header would hard-wrap the alt-screen, so the strip goes first.
		right = m.memGauge() + counts
		gap = m.width - lipgloss.Width(left) - lipgloss.Width(right)
	}
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + sChrome.Render(right)
}

// memGauge renders `avail GB · N workers`, color-coded against the memory
// floor (green ok · amber drain-before-grabbing · red near the cliff). Empty
// when the read is unusable (non-darwin / failed sysctl) so the header simply
// omits it. Prefixed with a trailing separator to sit ahead of the counts.
func (m Model) memGauge() string {
	if !m.mem.OK() {
		return ""
	}
	st := sWorking // green
	switch m.mem.Level() {
	case resource.Amber:
		st = sWaiting
	case resource.Red:
		st = sBlocked
	}
	// A3 weather gauge: prefix the reading with a sky keyed to the memory
	// level (grove-22) — clear / clouds / storm, in the same level color.
	prefix := ""
	if m.fx >= fxCalm {
		prefix = st.Render(weatherGlyph(m.mem.Level()) + " ")
	}
	return prefix + st.Render(fmt.Sprintf("%.1fG", m.mem.AvailGB())) +
		sChrome.Render(fmt.Sprintf(" avail · %dw   ", m.workers))
}

// ticketColWidth sizes the TICKET column (content + 1-cell gap) to the
// longest active ticket — provider ids share long prefixes (<repo>-<n>),
// so a fixed width truncates them into indistinguishable rows. Clamped so
// one huge id can't push every other column off a narrow screen.
func (m Model) ticketColWidth() int {
	w := 10
	for _, t := range m.tasks {
		if n := len([]rune(t.Ticket)); n > w {
			w = n
		}
	}
	if w > 32 {
		w = 32
	}
	return w + 1
}

// ageColW fixes the AGE column so the trailing TASK-title hint aligns across
// rows; the widest age ("23h59m") is 6 cells, so 7 leaves a one-cell gap.
const ageColW = 7

func (m Model) viewAgents() string {
	w := m.width - 4
	tw := m.ticketColWidth()
	// Cells are padded as plain text FIRST, then styled — ANSI codes inside
	// %-Ns break fmt's width accounting (field-tested on the first render).
	header := "   " + pad("TICKET", tw) + pad("REPO", 11) + pad("STATUS", 11) +
		pad("LIVE", 8) + pad("PR", 8) + pad("CI", 4) + pad("PREVIEW", 9) + pad("AGE", ageColW) + "TASK"
	rows := []string{sHeaderCol.Render(truncPad(header, w))}

	if len(m.tasks) == 0 {
		empty := "  no active tasks — gv grab <ticket> plants one"
		if m.fx >= fxCalm { // A4 living empty state — time-of-day variant
			empty = emptyAgentsLine(nowHour())
		}
		line := sDim.Render(empty)
		if m.fx >= fxCalm && timeOfDay(nowHour()) == 3 {
			// A7: at night one firefly drifts through the dark (grove-56).
			line = truncPad(line+fireflyTrail(m.tick), w)
		}
		rows = append(rows, line)
	}
	for i, t := range m.tasks {
		label := t.Label()
		st := statusStyle(label)
		pr, ci, preview := "—", "—", "—"
		ciStyle := sDim
		if p := m.prs[t.Ticket]; p != nil {
			pr = fmt.Sprintf("#%d", p.Number)
			if p.State == "MERGED" {
				// J1 merge sparkle: shimmer the ⬢ for a few ticks after the PR
				// flips MERGED (grove-22). No spaces — the column is 8 cells.
				if _, ok := m.celebrations[t.Ticket]; ok && m.fx >= fxFull {
					pr += "✦⬢✦"
				} else {
					pr += "⬢"
				}
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
		if i == m.sel {
			cursor = sSelected.Render("▸")
		}
		// A1 the grove breathes: the working ● holds its shape and its green
		// gently ramps on the tick (grove-24). A2 grove verbs: the prominent
		// green "working" status rotates through forest gerunds. Both ambient
		// (fxCalm+) and keyed to the reliable agent status; at off the glyph is
		// a static ● and the status the literal "working".
		glyph := statusGlyph(label)
		glyphStyle := st
		statusText := label
		if m.fx >= fxCalm && label == "working" {
			glyphStyle = breathStyle(m.tick)
			statusText = verbFor(t.Ticket, m.tick)
		}
		// J5 question knock: a freshly flipped QUESTION pulses its ◆
		// bold↔normal for a few ticks (grove-56).
		if m.fx >= fxFull && label == "QUESTION" {
			if _, ok := m.celebrations[knockKey(t.Ticket)]; ok {
				glyphStyle = knockStyle(m.tick)
			}
		}
		live := m.live[t.Ticket]
		line := cursor + glyphStyle.Render(glyph) + " " +
			pad(t.Ticket, tw) +
			pad(trunc(t.Repo, 10), 11) +
			st.Render(pad(statusText, 11)) +
			sDim.Render(pad(live, 8)) +
			sDelivery.Render(pad(pr, 8)) +
			ciStyle.Render(pad(ci, 4)) +
			sDelivery.Render(pad(preview, 9)) +
			sChrome.Render(pad(age(t.Created), ageColW))
		// Dimmed task-title hint in the leftover width — additive, never a
		// formal column. Omit entirely on narrow panes rather than cramp.
		consumed := 3 + tw + 11 + 11 + 8 + 8 + 4 + 9 + ageColW
		if remaining := w - consumed; remaining >= 6 && t.Title != "" {
			line += sChrome.Render(trunc(t.Title, remaining))
		}
		rows = append(rows, truncPad(line, w))
	}

	body := sPanelTitleFocus.Render("AGENTS") + "\n" + strings.Join(rows, "\n")
	return sPanelFocus.Width(m.width - 2).Render(body)
}

// viewActivity renders the newest-first swarm history — the objective
// record of what happened, including while you were away (cockpit §3).
func (m Model) viewActivity() string {
	w := m.width - 4
	items := feedItems(m.events)

	// Fill the space between the agents panel and the footer.
	avail := m.height - (len(m.tasks) + 4) - 6
	if avail < 3 {
		avail = 3
	}
	if avail > len(items) {
		avail = len(items)
	}

	rows := []string{}
	if len(items) == 0 {
		empty := "  nothing has happened yet"
		if m.fx >= fxCalm { // A4 living empty state — time-of-day variant
			empty = emptyActivityLine(nowHour())
		}
		line := sDim.Render(empty)
		if m.fx >= fxCalm && timeOfDay(nowHour()) == 3 {
			// A7: at night one firefly drifts through the dark (grove-56).
			line = truncPad(line+fireflyTrail(m.tick), w)
		}
		rows = append(rows, line)
	}
	tw := m.ticketColWidth()
	for _, it := range items[:avail] {
		glyph, text := statusStyle("").Render(it.Glyph), sDim.Render(it.Text)
		if m.fx >= fxFull && planting(it.Text, time.Since(it.Time)) {
			// J4 planting: a grab younger than a minute reads as a planting —
			// presentation only, the feedItem string is untouched (grove-56).
			glyph, text = sWorking.Render(plantGlyph), sDim.Render(plantText)
		}
		line := fmt.Sprintf(" %s %s  %s %s",
			sChrome.Render(pad(age(it.Time), 7)),
			glyph,
			pad(it.Ticket, tw),
			text)
		rows = append(rows, truncPad(line, w))
	}

	body := sPanelTitle.Render("ACTIVITY") + "\n" + strings.Join(rows, "\n")
	return sPanel.Width(m.width - 2).Render(body)
}

func (m Model) viewFooter() string {
	keys := []string{
		sKey.Render("O") + sFoot.Render(" new chat"),
		sKey.Render(")") + sFoot.Render(" profiled chat"),
		sKey.Render("enter") + sFoot.Render(" reply"),
		sKey.Render("a") + sFoot.Render(" attach"),
		sKey.Render("o") + sFoot.Render(" preview"),
		sKey.Render("p") + sFoot.Render(" PR"),
		sKey.Render("t") + sFoot.Render(" task"),
		sKey.Render("v") + sFoot.Render(" reviewing"),
		sKey.Render("n") + sFoot.Render(" nudge"),
		sKey.Render("d") + sFoot.Render(" done"),
		sKey.Render("X") + sFoot.Render(" park"),
		sKey.Render("$") + sFoot.Render(" costs"),
		sKey.Render("*") + sFoot.Render(" effects"),
		sKey.Render("L") + sFoot.Render(" layout"),
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
	if m.mode == modeConfirmClose {
		// Spell out exactly what park does: which session dies, that N
		// workers + orchestrator + this cockpit stop, that state is saved,
		// and how to bring it back (grove-33).
		prompt := fmt.Sprintf("park %s? stops %d worker(s) + orchestrator + cockpit · state saved on disk · resume: gv, then gv adopt <ticket> ",
			m.sessionName(), len(m.tasks))
		line = " " + sBlocked.Render(prompt) +
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

// viewProfilePick renders the modeProfilePick overlay (grove-45): the sorted
// candidate profile names, each with its `opus` model as a one-line hint, and
// a cursor. Shown only for ≥2 profiles with no default, so `)` needn't guess.
func (m Model) viewProfilePick() string {
	w := m.width - 4

	// Widest name → aligned model-hint column.
	nameW := 8
	for _, n := range m.pickProfiles {
		if l := len([]rune(n)); l > nameW {
			nameW = l
		}
	}

	rows := []string{sHeaderCol.Render(truncPad("   "+pad("PROFILE", nameW+2)+"OPUS MODEL", w))}
	for i, n := range m.pickProfiles {
		opus := "—"
		if p := m.cfg.ModelProfiles[n]; p != nil && p.Opus != "" {
			opus = p.Opus
		}
		cursor := " "
		nameCell := pad(n, nameW+2)
		if i == m.pickSel {
			cursor = sSelected.Render("▸")
			nameCell = sSelected.Render(nameCell)
		}
		line := cursor + " " + nameCell + sChrome.Render(opus)
		rows = append(rows, truncPad(line, w))
	}

	body := sPanelTitleFocus.Render("PICK A PROFILE") +
		sChrome.Render("  (no default set — ≥2 profiles configured)") +
		"\n" + strings.Join(rows, "\n")
	panel := sPanelFocus.Width(m.width - 2).Render(body)

	foot := " " + strings.Join([]string{
		sKey.Render("j/k") + sFoot.Render(" move"),
		sKey.Render("enter") + sFoot.Render(" spawn"),
		sKey.Render("esc") + sFoot.Render(" cancel"),
	}, sDim.Render(" · "))
	if m.flash != "" {
		foot += "   " + sChrome.Render(m.flash)
	}
	return m.viewHeader() + "\n" + panel + "\n" + foot
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
