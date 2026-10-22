package tui

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/account"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/kimi"
	"github.com/JollyGrin/grove/internal/state"
)

// The CLAUDE tab (claude-accounts design §Decision 8): one row per Claude
// account — login first — with its 5-hour and weekly windows as the last
// session on that account saw them. Local files only (the usage files `gv
// statusline` writes), read on tab open and r, never on the 1s tick
// (cockpit RAM rule). A gauge never claims a number it did not observe:
// unknown renders as no data, never 0%, and every row carries its age.

// claudeMsg is the tab's one-shot snapshot. Window states and ages are
// re-derived at render time (claudeClock), never frozen at the read.
// fetched=false while a read is in flight: the held rows only carry the
// selection across it, they never render as current.
type claudeMsg struct {
	fetched bool
	rows    []account.Row // each with Usage set
	err     string
}

// claudeClock is the tab's render clock; reset times read in its
// location. A var so a golden test can pin it.
var claudeClock = time.Now

// claudeUsedMsg reports the u flow's `gv account use`.
type claudeUsedMsg struct {
	name string
	err  error
}

// claudeStore is the account store the tab reads — the global state dir,
// the same one `gv account` uses (accounts are not per-workspace).
func claudeStore() account.Store { return account.Open(config.StateDirPath()) }

// newClaudeCmd builds the tab's read; a var so a test can count the reads
// a key or a tick fires.
var newClaudeCmd = claudeCmd

// claudeCmd reads every account's row and usage file off the tea loop.
func claudeCmd(tasks []*state.Task) tea.Cmd {
	return func() tea.Msg {
		now := time.Now()
		home, _ := os.UserHomeDir()
		s := claudeStore()
		rows, err := s.Rows(home, now, tasks)
		if err != nil {
			return claudeMsg{fetched: true, err: err.Error()}
		}
		for i := range rows {
			u := s.Usage(rows[i].Name, now)
			rows[i].Usage = &u
		}
		return claudeMsg{fetched: true, rows: rows}
	}
}

// claudeUseCmd is `gv account use <name>`, run in-process: the same store
// call and the same rule (an expired token refuses).
func claudeUseCmd(name string) tea.Cmd {
	return func() tea.Msg { return claudeUsedMsg{name: name, err: claudeStore().Use(name)} }
}

// Transient flashes the tab sets while a read or a switch is in flight;
// the landing claudeMsg clears them so they never leak to another tab.
const claudeReadingFlash = "rereading usage…"

func claudeSwitchingFlash(name string) string { return "switching to " + name + "…" }

// claudeRead marks the snapshot pending and fires one read.
func (m Model) claudeRead() (Model, tea.Cmd) {
	m.claude.fetched = false
	return m, newClaudeCmd(m.localTasks)
}

// applyClaude lands a read. The selection follows the account the
// operator picked, by name; a vanished one clamps to the last row, so u
// never hits an account other than the one under the cursor.
func (m Model) applyClaude(msg claudeMsg) Model {
	picked := ""
	if m.claudeSel < len(m.claude.rows) {
		picked = m.claude.rows[m.claudeSel].Name
	}
	m.claude = msg
	m.claudeSel = max(0, min(m.claudeSel, len(msg.rows)-1))
	for i, r := range msg.rows {
		if r.Name == picked {
			m.claudeSel = i
			break
		}
	}
	if m.flash == claudeReadingFlash || strings.HasPrefix(m.flash, "switching to ") {
		m.flash = ""
	}
	return m
}

// handleClaudeKey is the CLAUDE tab's keys; ok=false falls through to the
// costs page's shared keys.
func (m Model) handleClaudeKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	if name := m.claudeUse; name != "" {
		// The armed y/N confirm takes exactly one key: y runs, ctrl+c
		// still quits, anything else cancels.
		m.claudeUse = ""
		switch k.String() {
		case "ctrl+c":
			return m, tea.Quit, true
		case "y":
			m.flash = claudeSwitchingFlash(name)
			return m, claudeUseCmd(name), true
		}
		m.flash = ""
		return m, nil, true
	}
	switch k.String() {
	case "j", "down":
		if m.claudeSel < len(m.claude.rows)-1 {
			m.claudeSel++
		}
	case "k", "up":
		if m.claudeSel > 0 {
			m.claudeSel--
		}
	case "r":
		m.flash = claudeReadingFlash
		next, cmd := m.claudeRead()
		return next, cmd, true
	case "u":
		// Never act on a pending or failed snapshot: the row under the
		// cursor is only what the operator sees once a read has landed.
		if !m.claude.fetched || m.claudeSel >= len(m.claude.rows) {
			return m, nil, true
		}
		r := m.claude.rows[m.claudeSel]
		switch {
		case r.Active:
			m.flash = r.Name + " is already the active account"
		case r.Expired:
			m.flash = r.Name + "'s token EXPIRED — " + account.ExpiredFix(r.Name)
		default:
			m.claudeUse = r.Name
		}
	default:
		return m, nil, false
	}
	return m, nil, true
}

// claudeGauge maps one observed window onto the kimi fuel gauge
// (fuelLine): percent used of a 100 limit, so the bar reads headroom.
func claudeGauge(label string, w *account.UsageWindow, now time.Time) kimi.Window {
	fw := kimi.Window{Label: label, Limit: 100, Used: int64(math.Round(*w.UsedPercentage))}
	switch {
	case w.State == account.StateReset:
		fw.ResetHint = "reset since last seen"
	case w.ResetsAt != nil:
		fw.ResetHint = "resets " + account.ResetWhen(*w.ResetsAt, now, now.Location())
	default:
		fw.ResetHint = "reset time unknown"
	}
	return fw
}

// noDataLine is a gauge slot with nothing observed: a dash and "no data",
// plus why when there is a hint.
func noDataLine(label, hint string, width int) string {
	text := "no data"
	if hint != "" {
		text += " — " + hint
	}
	return truncPad("    "+sChrome.Render(pad(label, 8))+sDim.Render(pad("—", 18)+text), width)
}

// claudeWindowLine renders one window, already re-derived as of now: the
// fuel gauge when observed, a grayed gauge when stale, no data when
// unknown.
func claudeWindowLine(label string, w *account.UsageWindow, now time.Time, width int) string {
	if w == nil || w.State == account.StateUnknown || w.UsedPercentage == nil {
		return noDataLine(label, "", width)
	}
	fw := claudeGauge(label, w, now)
	if w.Stale {
		// A sighting older than the window, with no reset time: it cannot
		// still be current, so it is shown but grayed out.
		frac, _, caption := fuelSpec(fw)
		return truncPad(sDim.Render("    "+pad(label, 8)+gaugeBar(frac, 16)+"  "+caption+" · stale, "+fw.ResetHint), width)
	}
	return fuelLine(fw, width)
}

// col pads s to a column of n cells, always leaving the last one blank so
// a long value never runs into the next column.
func col(s string, n int) string { return pad(s, n-1) + " " }

func (m Model) viewClaude() string {
	w := m.width - 4
	c := m.claude

	sections := []string{m.costsTabBar()}
	switch {
	case !c.fetched:
		sections = append(sections, sDim.Render("reading usage…"))
	case c.err != "":
		sections = append(sections, truncPad(sBlocked.Render("accounts unreadable: ")+sDim.Render(c.err), w))
	default:
		sections = append(sections, m.viewClaudeRows(w))
	}
	sections = append(sections, truncPad(sDim.Render("last values a session on each account saw — never extrapolated"), w))

	body := sPanelFocus.Width(m.width - 2).Render(strings.Join(sections, "\n\n"))
	return m.viewHeader() + "\n" + body + "\n" + m.viewClaudeFooter()
}

func (m Model) viewClaudeRows(w int) string {
	c := m.claude
	now := claudeClock()
	rows := []string{sPanelTitleFocus.Render("ACCOUNTS")}
	for i, r := range c.rows {
		cur := "  "
		if i == m.claudeSel {
			cur = sKey.Render("▸ ")
		}
		dot := "  "
		if r.Active {
			dot = sOK.Render("● ")
		}
		line := cur + dot + col(r.Name, 15) + sChrome.Render(col(fmt.Sprintf("%d pinned", r.Pinned), 11))
		var u *account.Usage
		if r.Usage != nil {
			at := r.Usage.At(now)
			u = &at
		}
		if u != nil && u.SeenAt != nil {
			line += sDim.Render("as of " + account.UsageAge(now.Sub(*u.SeenAt)))
		}
		if r.Expired {
			line += "  " + sBlocked.Render("EXPIRED")
		}
		if r.Email != "" {
			line += "  " + sDim.Render(r.Email)
		}
		rows = append(rows, truncPad(line, w))
		if u == nil || u.SeenAt == nil {
			hint := ""
			if u != nil {
				hint = u.Hint
			}
			rows = append(rows, noDataLine("usage", hint, w))
			continue
		}
		rows = append(rows,
			claudeWindowLine("5h", u.FiveHour, now, w),
			claudeWindowLine("7d", u.SevenDay, now, w))
	}
	return strings.Join(rows, "\n")
}

func (m Model) viewClaudeFooter() string {
	if name := m.claudeUse; name != "" {
		line := " " + sBlocked.Render("make "+name+" the active account? new launches use it · live sessions keep theirs ") +
			sKey.Render("y") + sFoot.Render(" confirm · any other key cancels")
		return truncPad(line, m.width)
	}
	keys := []string{
		sKey.Render("tab") + sFoot.Render(" spend"),
		sKey.Render("j/k") + sFoot.Render(" select"),
		sKey.Render("u") + sFoot.Render(" use account"),
		sKey.Render("r") + sFoot.Render(" reread"),
		sKey.Render("esc") + sFoot.Render(" back"),
	}
	line := " " + strings.Join(keys, sDim.Render(" · "))
	if m.flash != "" {
		line += "   " + sChrome.Render(m.flash)
	}
	return truncPad(line, m.width)
}
