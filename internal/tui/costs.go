package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/cost"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/ledger"
	"github.com/JollyGrin/grove/internal/openrouter"
	"github.com/JollyGrin/grove/internal/provider"
	"github.com/JollyGrin/grove/internal/state"
)

// costsRow is one live ticket on the costs page.
type costsRow struct {
	ticket, title string
	tot           cost.Totals
}

type costsMsg struct {
	rows      []costsRow   // active tickets with transcripts, spend-sorted
	hist      []ledger.Row // final/latest ledger snapshot per non-active ticket, newest first
	points    []cost.Point // live transcript points + ledger deltas for pruned tickets
	recording bool
}

// bucketCounts: how many bars each granularity shows.
var bucketCounts = map[cost.BucketUnit]int{cost.Hourly: 12, cost.Daily: 14, cost.Weekly: 8}

// costsCmd assembles the costs page off the tea loop: live totals via the
// mtime-keyed cache, history from the ledger, chart points from both.
// snapshot=true (page open, record toggled on) appends a ledger row per
// active ticket — deliberately NOT on every refresh tick: cumulative
// snapshots once per visit keep the ledger readable; `gv done` writes the
// final row that history depends on.
func costsCmd(cfg *config.Config, stateDir string, tasks []*state.Task, prs map[string]*github.PR, cache *cost.Cache, snapshot bool) tea.Cmd {
	return func() tea.Msg {
		recording := ledger.Enabled(stateDir, cfg != nil && cfg.Cost.Record)

		var rows []costsRow
		live := map[string]bool{}
		active := map[string]bool{}
		var points []cost.Point
		for _, t := range tasks {
			active[t.Ticket] = true
			entries, err := cache.UsageForTask(t.Worktree)
			if err != nil || len(entries) == 0 {
				continue
			}
			live[t.Ticket] = true
			tot := cost.Total(entries)
			rows = append(rows, costsRow{ticket: t.Ticket, title: t.Title, tot: tot})
			points = append(points, cost.Points(entries)...)

			if snapshot && recording {
				outcome := "none"
				if pr := prs[t.Ticket]; pr != nil {
					outcome = ledger.Outcome(pr.State)
				}
				_ = ledger.Append(stateDir, ledger.Row{
					Time: time.Now(), Ticket: t.Ticket, Title: t.Title,
					Desc: ledger.Snip(provider.BestEffortDescription(cfg, t.Repo, t.Ticket), 200),
					Repo: t.Repo, Branch: t.Branch, Outcome: outcome,
					Input: tot.Input, Output: tot.Output,
					CacheCreate: tot.CacheCreate5m + tot.CacheCreate1h,
					CacheRead:   tot.CacheRead, Turns: tot.Turns, USD: tot.USD,
					Models: tot.Mix(),
				})
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].tot.USD > rows[j].tot.USD })

		all, _ := ledger.Read(stateDir)
		var hist []ledger.Row
		for ticket, r := range ledger.Latest(all) {
			if !active[ticket] {
				hist = append(hist, r)
			}
		}
		sort.Slice(hist, func(i, j int) bool { return hist[i].Time.After(hist[j].Time) })
		points = append(points, ledger.DeltaPoints(all, live)...)

		return costsMsg{rows: rows, hist: hist, points: points, recording: recording}
	}
}

func (m Model) handleCostsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "$", "c":
		m.mode = modeList
		return m, nil
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.costsTab = (m.costsTab + 1) % 2
		if m.costsTab == costsTabAccount {
			// One-shot fetch on tab open (the costsCmd pattern) — the 1s tick
			// never re-fires it; r refetches manually.
			return m, accountCmd(m.cfg, m.stateDir, m.tasks, m.costCache)
		}
		return m, nil
	case "o":
		if m.costsTab == costsTabAccount {
			m.flash = openURL(openrouter.CreditsURL)
		}
		return m, nil
	case "p":
		if m.costsTab == costsTabAccount && m.account.fetched && m.account.keyMasked == "" {
			m.flash = "reading clipboard…"
			return m, pasteKeyCmd()
		}
		return m, nil
	case "b":
		m.bucketUnit = m.bucketUnit.Next()
		return m, nil
	case "r":
		if m.costsTab == costsTabAccount {
			m.flash = "refreshing account…"
			return m, accountCmd(m.cfg, m.stateDir, m.tasks, m.costCache)
		}
		on := !m.costs.recording
		if err := ledger.SetRecording(m.stateDir, on); err != nil {
			m.flash = err.Error()
			return m, nil
		}
		m.costs.recording = on
		if on {
			m.flash = "recording on — snapshotting active tickets"
			return m, costsCmd(m.cfg, m.stateDir, m.tasks, m.prs, m.costCache, true)
		}
		m.flash = "recording off"
		return m, nil
	}
	return m, nil
}

func (m Model) viewCosts() string {
	if m.costsTab == costsTabAccount {
		return m.viewAccount()
	}
	w := m.width - 4

	sections := []string{m.costsTabBar()}

	// Active tickets, live from transcripts.
	rows := []string{sHeaderCol.Render(truncPad("   "+pad("TICKET", m.ticketColWidth())+pad("EST $", 9)+pad("TURNS", 7)+pad("IN", 9)+pad("OUT", 9)+pad("CACHE%", 8)+pad("MODELS", 12)+"TITLE", w))}
	if len(m.costs.rows) == 0 {
		rows = append(rows, sDim.Render("  no active tickets with transcripts"))
	}
	for _, r := range m.costs.rows {
		line := "   " + pad(r.ticket, m.ticketColWidth()) +
			sWorking.Render(pad(fmtCostUSD(r.tot), 9)) +
			pad(fmt.Sprint(r.tot.Turns), 7) +
			pad(fmtTokens(r.tot.Input), 9) +
			pad(fmtTokens(r.tot.Output), 9) +
			pad(fmt.Sprintf("%.0f%%", 100*r.tot.CacheReadShare()), 8) +
			sDelivery.Render(pad(r.tot.MixCompact(), 12)) +
			sChrome.Render(r.title)
		rows = append(rows, truncPad(line, w))
	}
	sections = append(sections, sPanelTitleFocus.Render("ACTIVE")+"\n"+strings.Join(rows, "\n"))

	// History: read from the ledger alone, so it survives transcript
	// pruning and worktree removal.
	hist := []string{sHeaderCol.Render(truncPad("   "+pad("TICKET", m.ticketColWidth())+pad("EST $", 9)+pad("TURNS", 7)+pad("OUTCOME", 9)+"TITLE · DESCRIPTION", w))}
	if len(m.costs.hist) == 0 {
		hist = append(hist, sDim.Render("  ledger empty — press r to start recording; gv done writes each ticket's final row"))
	}
	histMax := 8
	if len(m.costs.hist) < histMax {
		histMax = len(m.costs.hist)
	}
	for _, r := range m.costs.hist[:histMax] {
		blurb := r.Title
		if r.Desc != "" {
			blurb += " · " + r.Desc
		}
		oc := sDim
		if r.Outcome == "merged" {
			oc = sOK
		}
		line := "   " + pad(r.Ticket, m.ticketColWidth()) +
			sWorking.Render(pad(fmt.Sprintf("$%.2f", r.USD), 9)) +
			pad(fmt.Sprint(r.Turns), 7) +
			oc.Render(pad(r.Outcome, 9)) +
			sChrome.Render(blurb)
		hist = append(hist, truncPad(line, w))
	}
	sections = append(sections, sPanelTitle.Render("HISTORY")+sChrome.Render("  (from the ledger — survives transcript pruning)")+"\n"+strings.Join(hist, "\n"))

	// Spend-over-time bars.
	n := bucketCounts[m.bucketUnit]
	buckets := cost.Buckets(m.costs.points, m.bucketUnit, n, time.Now(), time.Local)
	var maxUSD, totUSD float64
	for _, b := range buckets {
		totUSD += b.USD
		if b.USD > maxUSD {
			maxUSD = b.USD
		}
	}
	chart := []string{}
	barW := w - 22
	if barW > 48 {
		barW = 48
	}
	for _, b := range buckets {
		bar := cost.Bar(b.USD, maxUSD, barW)
		amount := ""
		if b.USD > 0 {
			amount = fmt.Sprintf(" $%.2f", b.USD)
		}
		chart = append(chart, truncPad(fmt.Sprintf("   %s %s%s",
			sChrome.Render(pad(m.bucketUnit.Label(b.Start, time.Local), 6)),
			sDelivery.Render(bar),
			sChrome.Render(amount)), w))
	}
	title := fmt.Sprintf("SPEND · %s (last %d · $%.2f)", m.bucketUnit, n, totUSD)
	sections = append(sections, sPanelTitle.Render(title)+"\n"+strings.Join(chart, "\n"))

	body := sPanelFocus.Width(m.width - 2).Render(strings.Join(sections, "\n\n"))
	return m.viewHeader() + "\n" + body + "\n" + m.viewCostsFooter()
}

func (m Model) viewCostsFooter() string {
	rec := sDim.Render("○ off")
	if m.costs.recording {
		rec = sOK.Render("● on")
	}
	keys := []string{
		sKey.Render("tab") + sFoot.Render(" account"),
		sKey.Render("r") + sFoot.Render(" record ") + rec,
		sKey.Render("b") + sFoot.Render(" buckets: "+m.bucketUnit.String()),
		sKey.Render("esc") + sFoot.Render(" back"),
		sKey.Render("q") + sFoot.Render(" quit"),
		sDim.Render("estimates, not billing"),
	}
	line := " " + strings.Join(keys, sDim.Render(" · "))
	if m.flash != "" {
		line += "   " + sChrome.Render(m.flash)
	}
	// grove-53: clamp so a narrow pane never hard-wraps this bare line and
	// desyncs the alt-screen renderer. truncPad is ANSI-aware and a no-op
	// passthrough when the line already fits.
	return truncPad(line, m.width)
}

// fmtCostUSD mirrors gv cost's table: a ? marks tickets whose model had no
// pricing (tokens counted, dollars unknown).
func fmtCostUSD(t cost.Totals) string {
	s := fmt.Sprintf("$%.2f", t.USD)
	if !t.CostKnown {
		s += "?"
	}
	return s
}

func fmtTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1_000)
	default:
		return fmt.Sprint(n)
	}
}
