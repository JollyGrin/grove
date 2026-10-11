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

// claudeMsg is the tab's one-shot snapshot. now is the read time the
// window states were derived at, so ages render against it; reset times
// read in loc.
type claudeMsg struct {
	fetched bool
	rows    []account.Row // each with Usage set
	now     time.Time
	loc     *time.Location
	err     string
}

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
			return claudeMsg{fetched: true, now: now, loc: time.Local, err: err.Error()}
		}
		for i := range rows {
			u := s.Usage(rows[i].Name, now)
			rows[i].Usage = &u
		}
		return claudeMsg{fetched: true, rows: rows, now: now, loc: time.Local}
	}
}

// claudeUseCmd is `gv account use <name>`, run in-process: the same store
// call and the same rule (an expired token refuses).
func claudeUseCmd(name string) tea.Cmd {
	return func() tea.Msg { return claudeUsedMsg{name: name, err: claudeStore().Use(name)} }
}

// handleClaudeKey is the CLAUDE tab's keys; ok=false falls through to the
// costs page's shared keys.
func (m Model) handleClaudeKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	if name := m.claudeUse; name != "" {
		// The armed y/N confirm takes exactly one key: y runs, anything
		// else cancels.
		m.claudeUse = ""
		if k.String() != "y" {
			m.flash = ""
			return m, nil, true
		}
		m.flash = "switching to " + name + "…"
		return m, claudeUseCmd(name), true
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
		m.flash = "rereading usage…"
		return m, newClaudeCmd(m.localTasks), true
	case "u":
		if m.claudeSel >= len(m.claude.rows) {
			return m, nil, true
		}
		r := m.claude.rows[m.claudeSel]
		if r.Active {
			m.flash = r.Name + " is already the active account"
			return m, nil, true
		}
		m.claudeUse = r.Name
	default:
		return m, nil, false
	}
	return m, nil, true
}

// claudeGauge maps one observed window onto the kimi fuel gauge
// (fuelLine): percent used of a 100 limit, so the bar reads headroom.
func claudeGauge(label string, w *account.UsageWindow, now time.Time, loc *time.Location) kimi.Window {
	fw := kimi.Window{Label: label, Limit: 100, Used: int64(math.Round(*w.UsedPercentage))}
	switch {
	case w.State == account.StateReset:
		fw.ResetHint = "reset since last seen"
	case w.ResetsAt != nil:
		fw.ResetHint = "resets " + account.ResetWhen(*w.ResetsAt, now, loc)
	default:
		fw.ResetHint = "reset time unknown"
	}
	return fw
}

// claudeWindowLine renders one window: the fuel gauge when observed, a
// grayed gauge when stale, a dash with "no data" when unknown.
func claudeWindowLine(label string, w *account.UsageWindow, now time.Time, loc *time.Location, width int) string {
	if w == nil || w.State == account.StateUnknown || w.UsedPercentage == nil {
		return truncPad("    "+sChrome.Render(pad(label, 8))+sDim.Render(pad("—", 18)+"no data"), width)
	}
	fw := claudeGauge(label, w, now, loc)
	if w.Stale {
		// A sighting older than the window, with no reset time: it cannot
		// still be current, so it is shown but grayed out.
		frac, _, caption := fuelSpec(fw)
		return truncPad(sDim.Render("    "+pad(label, 8)+gaugeBar(frac, 16)+"  "+caption+" · stale, "+fw.ResetHint), width)
	}
	return fuelLine(fw, width)
}

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
		line := cur + dot + pad(r.Name, 14) + sChrome.Render(pad(fmt.Sprintf("%d pinned", r.Pinned), 10))
		u := r.Usage
		if u != nil && u.SeenAt != nil {
			line += sDim.Render("as of " + account.UsageAge(c.now.Sub(*u.SeenAt)))
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
			rows = append(rows, truncPad("    "+sChrome.Render(pad("usage", 8))+sDim.Render(pad("—", 18)+"no data — "+hint), w))
			continue
		}
		loc := c.loc
		if loc == nil {
			loc = time.Local
		}
		rows = append(rows,
			claudeWindowLine("5h", u.FiveHour, c.now, loc, w),
			claudeWindowLine("7d", u.SevenDay, c.now, loc, w))
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
