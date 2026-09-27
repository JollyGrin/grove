package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/feature"
	"github.com/JollyGrin/grove/internal/fleet"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/serve"
	"github.com/JollyGrin/grove/internal/state"
)

// The feature lens (feature trains Decision 5, grove-378): `enter` on a
// focused feature opens one train full-screen — TRAIN, BRANCH, SERVE,
// NEXT — and `esc` returns. `l` (list or lens) opens the land plan as a
// confirm modal (Decision 6); `m` in the lens opens the feature PR.
//
// Same data discipline as the rail panel: every lens string is derived in
// assembleFeatures (refresh + PR beat), never in View, and the land plan
// is built from the last refresh's fold and PR poll when `l` is pressed —
// no network, no per-frame work.

// lensCar is one TRAIN row, plain text derived once per assemble.
type lensCar struct {
	ticket string
	state  string
	label  string
	title  string
	est    string
}

// lensData is the lens for one feature. branch/serve/next are plain lines.
type lensData struct {
	head   string // "<branch> ▷ <base>  landed/total  est $X"
	cars   []lensCar
	labelW int
	branch []string
	serve  string
	next   []string
}

// Lens chrome: the four sections and the TRAIN column widths.
const (
	lensStateW = 10
	lensEstW   = 8
)

// landFn is the land path (feature.Land), swappable so a test can prove
// the modal's confirm runs it exactly once and cancel never does.
var landFn = feature.Land

// landDoneMsg is the land run's answer.
type landDoneMsg struct {
	slug string
	res  feature.LandResult
}

// nextInput is everything nextActions reads — table-testable.
type nextInput struct {
	Cars      []feature.Car
	Merged    map[string]bool // ticket → its PR is MERGED (last PR poll)
	Behind    *int
	Mergeable *bool
	PR        *feature.FeaturePR
	Branch    string
	Base      string
}

// nextActions derives the ordered NEXT list for a train: answer questions,
// land merged cars, review ready ones, grab queued cars whose `after`
// cars have landed, rebase when behind, and — once every car is in — the
// feature PR. A queued car's `after N` only blocks while N is a car on
// this train that hasn't landed: the train cannot see an outside issue.
func nextActions(in nextInput) []string {
	var out []string
	onTrain := map[int]bool{}
	landedNum := map[int]bool{}
	landed, merged := 0, 0
	for _, c := range in.Cars {
		if c.Number > 0 {
			onTrain[c.Number] = true
		}
		if c.State == feature.CarLanded {
			landed++
			landedNum[c.Number] = true
		} else if in.Merged[c.Ticket] {
			merged++
		}
	}
	for _, c := range in.Cars {
		if c.State == feature.CarQuestion {
			out = append(out, "answer "+carLabel(c)+" — it is waiting on you")
		}
	}
	if merged > 0 {
		out = append(out, fmt.Sprintf("land %d merged car(s) — l", merged))
	}
	for _, c := range in.Cars {
		if c.State == feature.CarReady && !in.Merged[c.Ticket] {
			out = append(out, "review "+carLabel(c)+" — merge its PR into "+in.Branch)
		}
	}
	for _, c := range in.Cars {
		if c.State != feature.CarQueued {
			continue
		}
		ready, after := true, make([]string, 0, len(c.After))
		for _, n := range c.After {
			if onTrain[n] && !landedNum[n] {
				ready = false
				break
			}
			after = append(after, fmt.Sprintf("#%d", n))
		}
		if !ready {
			continue
		}
		line := "grab " + carLabel(c)
		if len(after) > 0 {
			line += " — after " + strings.Join(after, " ") + " landed"
		}
		out = append(out, line)
	}
	if in.Behind != nil && *in.Behind > 0 {
		out = append(out, fmt.Sprintf("rebase %s on %s — %d behind", in.Branch, in.Base, *in.Behind))
	}
	if in.Mergeable != nil && !*in.Mergeable {
		out = append(out, "resolve "+in.Branch+"'s conflicts with "+in.Base)
	}
	if len(in.Cars) > 0 && landed == len(in.Cars) {
		switch {
		case in.PR == nil || in.PR.State == "CLOSED":
			out = append(out, "open the feature PR "+in.Branch+" → "+in.Base)
		case in.PR.State == "OPEN":
			out = append(out, fmt.Sprintf("merge feature PR #%d into %s — m opens it", in.PR.Number, in.Base))
		}
	}
	return out
}

// serveLine is the SERVE section: the serve status when the status pass
// carries one, else `no run.sh`.
func serveLine(s *serve.Status) string {
	if s == nil {
		return "no run.sh"
	}
	var line string
	switch s.State {
	case serve.StateRunning:
		line = "running"
		if s.Port > 0 {
			line += fmt.Sprintf(" :%d", s.Port)
		}
		if s.URL != "" {
			line += "  " + s.URL
		}
	case serve.StateUntrusted:
		line = "run.sh changed — review it before it runs"
	case serve.StateStopped:
		line = "stopped"
		if s.Port > 0 {
			line += fmt.Sprintf(" (last :%d)", s.Port)
		}
	default:
		return "no run.sh"
	}
	if s.Behind {
		line += "  · behind the branch tip"
	}
	return line
}

// buildLens derives one feature's lens strings once per assemble. sv is
// the cockpit's serve status (grove-381); nil falls back to st.Serve.
func buildLens(f *state.Feature, st *feature.Status, tip string, merged map[string]bool, sv *serve.Status) lensData {
	if sv == nil {
		sv = st.Serve
	}
	d := lensData{
		head:  fmt.Sprintf("%s ▷ %s  %d/%d landed  est $%.2f", f.Branch, f.Base, st.Landed, st.Total, st.EstUSD),
		serve: serveLine(sv),
		next: nextActions(nextInput{Cars: st.Cars, Merged: merged, Behind: st.BehindBase,
			Mergeable: st.Mergeable, PR: st.PR, Branch: f.Branch, Base: f.Base}),
	}
	var closes []string
	for _, c := range st.Cars {
		lc := lensCar{ticket: c.Ticket, state: c.State, label: carLabel(c), title: c.Title, est: "–"}
		if c.EstUSD > 0 {
			lc.est = fmt.Sprintf("$%.2f", c.EstUSD)
		}
		if merged[c.Ticket] && c.State != feature.CarLanded {
			lc.state = "merged"
		}
		if c.State == feature.CarQueued && len(c.After) > 0 {
			after := make([]string, len(c.After))
			for i, n := range c.After {
				after[i] = fmt.Sprintf("#%d", n)
			}
			lc.title = strings.TrimSpace(lc.title + "  (after " + strings.Join(after, " ") + ")")
		}
		if c.State == feature.CarLanded {
			closes = append(closes, carLabel(c))
		}
		if n := len([]rune(lc.label)) + 1; n > d.labelW {
			d.labelW = n
		}
		d.cars = append(d.cars, lc)
	}

	if tip == "" {
		tip = "?"
	} else if len(tip) > 7 {
		tip = tip[:7]
	}
	behind := "? (never fetched)"
	if st.BehindBase != nil {
		behind = fmt.Sprintf("%d behind %s", *st.BehindBase, f.Base)
		if st.Mergeable != nil && !*st.Mergeable {
			behind += " · conflicts"
		}
	}
	pr := "none yet"
	if st.PR != nil {
		pr = fmt.Sprintf("#%d %s", st.PR.Number, st.PR.State)
	}
	cl := "nothing landed yet"
	if len(closes) > 0 {
		cl = strings.Join(closes, " ")
	}
	d.branch = []string{
		"tip      " + tip + "  " + f.Branch,
		"base     " + behind,
		"PR       " + pr,
		"closes   " + cl,
	}
	return d
}

// lensIndex is the lensed feature's index in m.feats, -1 when gone.
func (m Model) lensIndex() int {
	for i := range m.feats {
		if m.feats[i].slug == m.lensSlug {
			return i
		}
	}
	return -1
}

// backMode is where a modal opened from the lens (detail, done, land)
// returns: the lens while one is open, else the list.
func (m Model) backMode() int {
	if m.lensSlug != "" {
		return modeLens
	}
	return modeList
}

// clampLens keeps the lens pointed at a live feature and car after a
// refresh; a closed feature drops the lens back to the list.
func (m *Model) clampLens() {
	if m.lensSlug == "" {
		return
	}
	i := m.lensIndex()
	if i < 0 {
		m.lensSlug, m.lensSel = "", 0
		if m.mode == modeLens {
			m.mode = modeList
		}
		return
	}
	if n := len(m.feats[i].lens.cars); m.lensSel >= n {
		m.lensSel = max(n-1, 0)
	}
}

// openLens enters the lens on the focused feature.
func (m Model) openLens() (tea.Model, tea.Cmd) {
	if m.featSel >= len(m.feats) {
		return m, nil
	}
	m.lensSlug, m.lensSel = m.feats[m.featSel].slug, 0
	m.mode = modeLens
	m.flash = ""
	return m, nil
}

// buildLandPlanFor builds `gv feature land`'s plan from refresh data: the
// local fold and the last PR poll (m.prs). Queued cars come off the last
// status pass. Never per frame — only on `l`.
func (m Model) buildLandPlanFor(slug string) feature.LandPlan {
	tasks := map[string]*state.Task{}
	for _, t := range m.localTasks {
		if t.Feature == slug {
			tasks[t.Ticket] = t
		}
	}
	prs := m.prs
	plan, _ := feature.BuildLandPlan(feature.LandInput{Tasks: tasks, Slug: slug,
		PR: func(t *state.Task) (*github.PR, error) { return prs[t.Ticket], nil }})
	for _, f := range m.feats {
		if f.slug != slug {
			continue
		}
		for _, c := range f.lens.cars {
			if c.state == feature.CarQueued {
				plan.Skipped = append(plan.Skipped, feature.SkipRow{Ticket: c.ticket,
					Number: issueNum(c.label), Reason: feature.SkipQueued})
			}
		}
	}
	return plan
}

// issueNum reads #N back off a car label; 0 for a bare ticket label.
func issueNum(label string) int {
	var n int
	if _, err := fmt.Sscanf(label, "#%d", &n); err != nil {
		return 0
	}
	return n
}

// openLand opens the land confirm modal for slug.
func (m Model) openLand(slug string) (tea.Model, tea.Cmd) {
	m.landSlug = slug
	m.landPlan = m.buildLandPlanFor(slug)
	m.mode = modeConfirmLand
	m.flash = ""
	return m, nil
}

// handleLandKey: y runs the land path (when there is anything to land);
// any other key cancels. Either way the modal returns where it came from.
func (m Model) handleLandKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	plan, slug := m.landPlan, m.landSlug
	m.mode = m.backMode()
	m.landPlan, m.landSlug = feature.LandPlan{}, ""
	if k.String() != "y" || len(plan.Land) == 0 {
		return m, nil
	}
	cfg := m.cfg
	byTicket := map[string]*state.Task{}
	for _, t := range m.localTasks {
		byTicket[t.Ticket] = t
	}
	m.flash = fmt.Sprintf("landing %d car(s) on %s…", len(plan.Land), slug)
	return m, func() tea.Msg {
		res := landFn(plan, func(ticket string) error {
			t, ok := byTicket[ticket]
			if !ok {
				return fmt.Errorf("%s: no longer tracked", ticket)
			}
			return FinishTask(cfg, t, false)
		})
		return landDoneMsg{slug: slug, res: res}
	}
}

// landFlash is the footer line after a land run.
func landFlash(msg landDoneMsg) string {
	nums := make([]string, 0, len(msg.res.Landed))
	for _, n := range msg.res.Landed {
		nums = append(nums, fmt.Sprintf("#%d", n))
	}
	line := "⬢ " + msg.slug + " landed: none"
	if len(nums) > 0 {
		line = "⬢ " + msg.slug + " landed: " + strings.Join(nums, " ")
	}
	if n := len(msg.res.Failed); n > 0 {
		line += fmt.Sprintf(" · %d failed (%s: %s)", n, msg.res.Failed[0].Ticket, msg.res.Failed[0].Error)
	}
	return line
}

// handleLensKey drives the lens: j/k walk the cars, esc returns, m opens
// the feature PR, l opens the land modal, and the AGENTS row keys act on
// the selected car's task exactly as they do in AGENTS.
func (m Model) handleLensKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	i := m.lensIndex()
	if i < 0 {
		m.lensSlug, m.mode = "", modeList
		return m, nil
	}
	f := m.feats[i]
	switch k.String() {
	case "esc":
		m.lensSlug, m.lensSel = "", 0
		m.mode = modeList
		return m, nil
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		if n := len(f.lens.cars); n > 0 {
			m.lensSel = (m.lensSel + 1) % n
		}
		return m, nil
	case "k", "up":
		if n := len(f.lens.cars); n > 0 {
			m.lensSel = (m.lensSel - 1 + n) % n
		}
		return m, nil
	case "m":
		if f.prURL == "" {
			m.flash = f.slug + " has no feature PR yet"
		} else {
			m.flash = openURL(f.prURL)
		}
		return m, nil
	case "l":
		return m.openLand(f.slug)
	case "s": // serve this train (grove-381): same gate as the rail
		return m.serveKey(f.slug)
	case "r":
		m.flash = "refreshing PRs…"
		return m, tea.Batch(prsCmd(m.cfg, m.stateDir, m.localTasks), featuresCmd(m.cfg, m.stateDir, m.features))
	case "enter", "n", "a", "o", "p", "t", "v", "d":
		return m.lensRowKey(k, f)
	}
	return m, nil
}

// lensRowKey hands a row key to the AGENTS handler with the cursor on the
// selected car's local task, so every key behaves exactly as it does
// there; a landed or queued car has no task to act on.
func (m Model) lensRowKey(k tea.KeyMsg, f featRow) (tea.Model, tea.Cmd) {
	if m.lensSel >= len(f.lens.cars) {
		return m, nil
	}
	car := f.lens.cars[m.lensSel]
	idx := -1
	for j, r := range m.board {
		if r.Task != nil && r.Ticket == car.ticket && r.HandedOffTo == "" && !fleet.IsRemote(r.Row) {
			idx = j
			break
		}
	}
	if idx < 0 {
		m.flash = car.label + " is " + car.state + " — no task to act on"
		return m, nil
	}
	focus := m.focus
	m.sel, m.focus, m.mode = idx, focusAgents, modeList
	nm, cmd := m.handleKey(k)
	out := nm.(Model)
	out.focus = focus
	if out.mode == modeList {
		out.mode = modeLens
	}
	return out, cmd
}

// viewLens renders the lensed feature full-screen: header, one panel
// holding TRAIN/BRANCH/SERVE/NEXT, and a one-line foot. Rows are budgeted
// so the frame never exceeds m.height: section gaps go first, then NEXT
// and TRAIN shrink (TRAIN windows around its cursor).
func (m Model) viewLens() string {
	i := m.lensIndex()
	if i < 0 {
		return m.viewHeader()
	}
	f := m.feats[i]
	d := f.lens
	w := m.width - 4
	avail := m.height - 4 // header + panel borders + foot

	const fixed = 4 + 4 + 1 // section titles, BRANCH, SERVE
	trainN, nextN, gaps := len(d.cars), max(len(d.next), 1), 3
	if fixed+gaps+trainN+nextN > avail {
		gaps = 0
	}
	if over := fixed + gaps + trainN + nextN - avail; over > 0 {
		nextN = max(nextN-over, 1)
	}
	if over := fixed + gaps + trainN + nextN - avail; over > 0 {
		trainN = max(trainN-over, 1)
	}
	if len(d.cars) == 0 {
		trainN = 1
	}

	rows := make([]string, 0, avail)
	gap := func() {
		if gaps > 0 {
			rows = append(rows, "")
		}
	}
	section := func(title, rest string) {
		line := sPanelTitleFocus.Render(title)
		if rest != "" {
			line += "  " + sChrome.Render(rest)
		}
		rows = append(rows, truncPad(line, w))
	}

	section("TRAIN", f.slug+"  "+d.head)
	if len(d.cars) == 0 {
		rows = append(rows, truncPad(sDim.Render("  no cars yet — label an issue "+f.label+" or gv grab --feature "+f.slug), w))
	}
	start := 0
	if m.lensSel >= trainN {
		start = m.lensSel - trainN + 1
	}
	for j := start; j < min(start+trainN, len(d.cars)); j++ {
		rows = append(rows, truncPad(lensCarRow(d.cars[j], d.labelW, j == m.lensSel, w), w))
	}
	gap()
	section("BRANCH", "")
	for _, l := range d.branch {
		rows = append(rows, truncPad("  "+sChrome.Render(l), w))
	}
	gap()
	section("SERVE", "")
	rows = append(rows, truncPad("  "+sChrome.Render(d.serve), w))
	gap()
	section("NEXT", "")
	if len(d.next) == 0 {
		rows = append(rows, truncPad(sDim.Render("  nothing needs you — the train is rolling"), w))
	}
	for j := 0; j < min(nextN, len(d.next)); j++ {
		rows = append(rows, truncPad("  "+sQuestion.Render(fmt.Sprintf("%d. %s", j+1, d.next[j])), w))
	}
	if avail < 1 {
		avail = 1
	}
	if len(rows) > avail {
		rows = rows[:avail]
	}
	panel := sPanelFocus.Width(m.width - 2).Render(strings.Join(rows, "\n"))

	foot := " " + sKey.Render("esc") + sFoot.Render(" back") + sDim.Render(" · ") +
		sKey.Render("j/k") + sFoot.Render(" car") + sDim.Render(" · ") +
		sKey.Render("enter") + sFoot.Render(" reply") + sDim.Render(" · ") +
		sKey.Render("m") + sFoot.Render(" feature PR") + sDim.Render(" · ") +
		sKey.Render("l") + sFoot.Render(" land") + sDim.Render(" · ") +
		sKey.Render("s") + sFoot.Render(" serve")
	if m.mode == modeServeStop && m.serveStop != "" {
		foot = " " + sBlocked.Render("stop "+serve.Window(m.serveStop)+"? kills the serve window ") +
			sKey.Render("y") + sFoot.Render(" confirm · any other key cancels")
	} else if m.flash != "" {
		foot += "   " + sChrome.Render(m.flash)
	}
	return m.viewHeader() + "\n" + panel + "\n" + truncPad(foot, m.width)
}

// lensCarRow is one TRAIN row: cursor, glyph, label, state, est, title.
// A landed car renders whole in the dim style — history, not work.
func lensCarRow(c lensCar, labelW int, selected bool, w int) string {
	cursor := "  "
	if selected {
		cursor = "▸ "
	}
	glyphState := c.state
	if glyphState == "merged" {
		glyphState = feature.CarReady
	}
	rest := pad(c.label, labelW) + pad(c.state, lensStateW) + pad(c.est, lensEstW) + c.title
	if c.state == feature.CarLanded {
		return sDim.Render(trunc(cursor+carGlyphs[glyphState]+" "+rest, w))
	}
	if selected {
		cursor = sSelected.Render("▸") + " "
	}
	return cursor + carStyles[glyphState].Render(carGlyphs[glyphState]) + " " + rest
}

// viewLand renders the land confirm modal (mockup F, without the issue
// lines): the cars that land, the cars left alone and why.
func (m Model) viewLand() string {
	w := m.width - 4
	p := m.landPlan
	rows := []string{truncPad(sPanelTitleFocus.Render("LAND "+m.landSlug)+"  "+
		sChrome.Render("finish every car whose PR is merged — the issues stay open for the orchestrator"), w)}
	if len(p.Land) == 0 {
		rows = append(rows, truncPad(sDim.Render("  nothing merged to land yet"), w))
	}
	for _, r := range p.Land {
		rows = append(rows, truncPad("  "+sOK.Render(pad("land", 6))+pad(r.Ticket, 16)+sChrome.Render(fmt.Sprintf("PR #%d merged", r.PR)), w))
	}
	for _, r := range p.Skipped {
		rows = append(rows, truncPad("  "+sDim.Render(pad("skip", 6)+pad(r.Ticket, 16)+r.Reason), w))
	}
	if maxRows := m.height - 4; len(rows) > maxRows {
		maxRows = max(maxRows, 1)
		rows = rows[:maxRows]
		rows[len(rows)-1] = sDim.Render(truncPad("  … a taller pane shows the rest", w))
	}
	panel := sPanelFocus.Width(m.width - 2).Render(strings.Join(rows, "\n"))

	var foot string
	if len(p.Land) == 0 {
		foot = " " + sFoot.Render("any key closes")
	} else {
		foot = " " + sBlocked.Render(fmt.Sprintf("land %d car(s)? runs gv done on each ", len(p.Land))) +
			sKey.Render("y") + sFoot.Render(" confirm · any other key cancels")
	}
	return m.viewHeader() + "\n" + panel + "\n" + truncPad(foot, m.width)
}
