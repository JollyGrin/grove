package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/feature"
	"github.com/JollyGrin/grove/internal/serve"
	"github.com/JollyGrin/grove/internal/state"
)

// The FEATURES rail panel (feature trains Decision 5, grove-377): one
// block per open feature between the header and AGENTS. It exists only
// while the workspace has an open feature — with none, every frame is
// byte-identical to the pre-feature cockpit (testdata goldens).
//
// Data discipline (cockpit RAM rule): status is computed in assemble(),
// never in View. The local half (car states off the fold) runs on every
// refresh; the network half (queued issues, feature PR, behind/mergeable,
// est) runs in featuresCmd only where PR refresh already runs — the 30s
// PR beat and `r` — plus once when the open-feature set changes. No new
// poll, goroutine, timer or cache.

// Panel focus: tab toggles while a feature is open.
const (
	focusAgents = iota
	focusFeatures
)

// Height budget (Decision 5): at most maxExpanded features get their three
// lines; below collapseBelow spare rows every feature is one title line.
const (
	maxExpanded   = 3
	collapseBelow = 12
)

// statusesFn is feature.Statuses, swappable so a test can prove View
// never computes status.
var statusesFn = feature.Statuses

// Car glyphs and styles — package-level tables, never rebuilt per frame.
var (
	carGlyphs = map[string]string{
		feature.CarQueued:   "·",
		feature.CarWorking:  "●",
		feature.CarQuestion: "◆",
		feature.CarReady:    "✓",
		feature.CarLanded:   "⬢",
	}
	carStyles = map[string]lipgloss.Style{
		feature.CarQueued:   sIdle,
		feature.CarWorking:  sWorking,
		feature.CarQuestion: sWaiting,
		feature.CarReady:    sOK,
		feature.CarLanded:   sDelivery,
	}
)

// featuresMsg carries the PR-cadence pass: nil statuses = the pass failed
// to read its inputs, and the last good answer stands.
type featuresMsg struct {
	statuses map[string]*feature.Status
	tips     map[string]string       // slug → origin/<branch> sha, local rev-parse (the lens)
	serves   map[string]serve.Status // grove-381; nil = unknown, the last answer stands
}

// featuresCmd runs the network half of feature status. nil when no
// feature is open, so a featureless workspace pays nothing.
func featuresCmd(cfg *config.Config, stateDir string, features map[string]*state.Feature) tea.Cmd {
	if len(features) == 0 {
		return nil
	}
	return func() tea.Msg {
		serves := ServeStatuses(featureList(features))
		in, err := feature.LiveInput(cfg, stateDir, features, true, true)
		if err != nil {
			return featuresMsg{serves: serves}
		}
		st, _ := statusesFn(in) // lookup failures leave fields empty
		// The lens's tip: one local rev-parse per feature, no fetch.
		tips := map[string]string{}
		for slug, f := range features {
			if cfg == nil {
				break
			}
			if r, ok := cfg.Repos[f.Repo]; ok {
				tips[slug] = serve.BranchTip(r.Path, f.Branch)
			}
		}
		return featuresMsg{statuses: st, tips: tips, serves: serves}
	}
}

// openFeatures filters a folded feature view to the open ones; nil when
// none is open.
func openFeatures(all map[string]*state.Feature) map[string]*state.Feature {
	var out map[string]*state.Feature
	for slug, f := range all {
		if f.Closed {
			continue
		}
		if out == nil {
			out = map[string]*state.Feature{}
		}
		out[slug] = f
	}
	return out
}

// sameSlugs reports whether two open-feature views name the same slugs —
// a change fires one featuresCmd off the refresh beat.
func sameSlugs(a, b map[string]*state.Feature) bool {
	if len(a) != len(b) {
		return false
	}
	for slug := range a {
		if _, ok := b[slug]; !ok {
			return false
		}
	}
	return true
}

// carCell is one car on the rail: its state and its label (#N).
type carCell struct {
	ticket string // scene trellis: matches the car to its live plant
	state  string
	label  string
}

// featRow is one feature, fully derived in assemble: View only styles it.
type featRow struct {
	slug  string
	title string // plain title-line text after the slug
	base  string
	cars  []carCell // every car, landed included (the scene trellis)
	rail  []carCell // the cars the rail draws one glyph each: all but landed
	// landed is the rail's leading `⬢N ▸` token (grove-397), padded to
	// its label cell; "" when nothing landed.
	landed    string
	landedTok string // the same token unpadded + one space: the one-line form
	dash      string // rail filler after each glyph (cellW-1 × ─)
	labels    string // plain label line: the landed range cell, then one cellW cell per rail car
	hint      string // what needs the operator (the selected feature's line)
	// trellis is the scene bracket's label, `<slug> landed/total`
	// (grove-379) — built here so the scene never formats per frame.
	trellis string
	label   string
	prURL   string   // the feature PR, "" when none (lens `m`)
	lens    lensData // the full-screen lens (grove-378)
}

// mergeStatus overlays the refresh beat's live car states onto the last
// PR-cadence answer: slow owns the landed and queued cars, the branch
// fields and the estimates; live owns every active car's state. A car
// grabbed since the slow pass is inserted ahead of the queued tail.
func mergeStatus(live, slow *feature.Status) *feature.Status {
	if live == nil {
		live = &feature.Status{}
	}
	if slow == nil {
		return live
	}
	out := &feature.Status{BehindBase: slow.BehindBase, Mergeable: slow.Mergeable, PR: slow.PR, Serve: live.Serve}
	if out.Serve == nil {
		out.Serve = slow.Serve
	}
	liveBy := make(map[string]feature.Car, len(live.Cars))
	for _, c := range live.Cars {
		liveBy[c.Ticket] = c
	}
	used := map[string]bool{}
	var queued []feature.Car
	for _, c := range slow.Cars {
		if lc, ok := liveBy[c.Ticket]; ok {
			// Local wins (grove-398): a car the slow pass saw on a host
			// and this host now tracks drops its @host.
			c.State, c.PR, c.Host = lc.State, lc.PR, lc.Host
			used[c.Ticket] = true
		}
		if c.State == feature.CarQueued {
			queued = append(queued, c)
			continue
		}
		out.Cars = append(out.Cars, c)
	}
	for _, c := range live.Cars {
		if !used[c.Ticket] {
			out.Cars = append(out.Cars, c)
		}
	}
	out.Cars = append(out.Cars, queued...)
	for _, c := range out.Cars {
		out.EstUSD += c.EstUSD
		if c.State == feature.CarLanded {
			out.Landed++
		}
	}
	out.Total = len(out.Cars)
	return out
}

func carLabel(c feature.Car) string {
	if c.Number > 0 {
		return fmt.Sprintf("#%d", c.Number)
	}
	return c.Ticket
}

// featureHint names the one thing on this train that needs the operator,
// most urgent first.
func featureHint(f *state.Feature, st *feature.Status) string {
	var question, next string
	ready, working := 0, 0
	for _, c := range st.Cars {
		switch c.State {
		case feature.CarQuestion:
			if question == "" {
				question = carLabel(c)
			}
		case feature.CarReady:
			ready++
		case feature.CarWorking:
			working++
		case feature.CarQueued:
			if next == "" {
				next = carLabel(c)
			}
		}
	}
	switch {
	case question != "":
		return "◆ " + question + " is waiting on you — tab to AGENTS to answer"
	case ready > 0:
		return fmt.Sprintf("✓ %d car(s) ready — review and merge into %s", ready, f.Branch)
	case st.Mergeable != nil && !*st.Mergeable:
		return "✗ " + f.Branch + " conflicts with " + f.Base + " — rebase the feature branch"
	case st.Total > 0 && st.Landed == st.Total:
		return "⬢ every car landed — the feature PR into " + f.Base + " is next"
	case working == 0 && next != "":
		return "· next up: " + next + " — grab it to start the next car"
	}
	return "nothing needs you — the train is rolling"
}

// buildFeatRow derives one feature's plain strings once per assemble —
// the rail panel's and the lens's. tip is the branch sha ("" unknown);
// merged marks the cars whose PR the last poll saw MERGED; sv/svOK the
// serve status (grove-381, svOK false = unknown).
func buildFeatRow(f *state.Feature, st *feature.Status, tip string, merged map[string]bool, sv serve.Status, svOK bool) featRow {
	var svp *serve.Status
	if svOK {
		svp = &sv
	}
	behind := "?"
	if st.BehindBase != nil {
		behind = fmt.Sprint(*st.BehindBase)
	}
	r := featRow{
		slug:    f.Slug,
		base:    f.Base,
		title:   fmt.Sprintf("%d/%d  ↓%s %s  %s  est $%.2f", st.Landed, st.Total, behind, f.Base, serveLabel(sv, svOK), st.EstUSD),
		hint:    featureHint(f, st),
		trellis: fmt.Sprintf("%s %d/%d", f.Slug, st.Landed, st.Total),
		label:   f.Label,
		lens:    buildLens(f, st, tip, merged, svp),
	}
	if st.PR != nil {
		r.prURL = st.PR.URL
	}
	cellW := 3
	var landed []int
	for _, c := range st.Cars {
		l := carLabel(c)
		cell := carCell{ticket: c.Ticket, state: c.State, label: l}
		r.cars = append(r.cars, cell)
		if c.State == feature.CarLanded {
			landed = append(landed, c.Number)
			continue
		}
		r.rail = append(r.rail, cell)
		if n := len([]rune(l)) + 1; n > cellW {
			cellW = n
		}
	}
	r.dash = strings.Repeat("─", cellW-1)
	var lb strings.Builder
	if len(landed) > 0 {
		token, label := fmt.Sprintf("⬢%d ▸", len(landed)), landedLabel(landed)
		w := max(len([]rune(token)), len([]rune(label))) + 1
		r.landed, r.landedTok = pad(token, w), token+" "
		lb.WriteString(pad(label, w))
	}
	for _, c := range r.rail {
		lb.WriteString(pad(c.label, cellW))
	}
	r.labels = lb.String()
	return r
}

// landedLabel names the collapsed landed cars under their rail token:
// `#361` for one, `361-363` when the issue numbers run consecutively,
// `3 landed` otherwise.
func landedLabel(nums []int) string {
	if len(nums) == 1 && nums[0] > 0 {
		return fmt.Sprintf("#%d", nums[0])
	}
	sorted := append([]int(nil), nums...)
	sort.Ints(sorted)
	run := sorted[0] > 0
	for i := 1; run && i < len(sorted); i++ {
		run = sorted[i] == sorted[i-1]+1
	}
	if run {
		return fmt.Sprintf("%d-%d", sorted[0], sorted[len(sorted)-1])
	}
	return fmt.Sprintf("%d landed", len(nums))
}

// assembleFeatures rebuilds m.feats from the fold (every refresh) and the
// last PR-cadence answer. Called only from assemble — never per frame.
func (m *Model) assembleFeatures() {
	m.feats = m.feats[:0]
	if len(m.features) == 0 {
		m.focus, m.featSel, m.trainW = focusAgents, 0, 0
		m.clampLens()
		return
	}
	tasks := map[string]*state.Task{}
	for _, t := range m.localTasks {
		if t.Feature != "" {
			tasks[t.Ticket] = t
		}
	}
	live, _ := statusesFn(feature.StatusInput{Features: m.features, Tasks: tasks, SkipQueued: true})

	slugs := make([]string, 0, len(m.features))
	for slug := range m.features {
		slugs = append(slugs, slug)
	}
	sort.Slice(slugs, func(i, j int) bool {
		a, b := m.features[slugs[i]], m.features[slugs[j]]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return slugs[i] < slugs[j]
	})
	var merged map[string]bool
	for ticket := range tasks {
		if pr := m.prs[ticket]; pr != nil && pr.State == "MERGED" {
			if merged == nil {
				merged = map[string]bool{}
			}
			merged[ticket] = true
		}
	}
	m.trainW = len("TRAIN")
	for _, slug := range slugs {
		sv, svOK := m.serves[slug]
		m.feats = append(m.feats, buildFeatRow(m.features[slug], mergeStatus(live[slug], m.featSlow[slug]), m.featTips[slug], merged, sv, svOK))
		if n := len([]rune(slug)); n > m.trainW {
			m.trainW = n
		}
	}
	if m.trainW > 16 {
		m.trainW = 16
	}
	m.trainW++ // one-cell gap
	if m.featSel >= len(m.feats) {
		m.featSel = len(m.feats) - 1
	}
	m.clampLens()
}

// featLayout is how the FEATURES panel spends its rows this frame.
type featLayout struct {
	height   int  // total panel rows incl. border; 0 = no panel
	expanded bool // three lines per feature (+ hint) vs one title line
	start    int  // first shown feature
	shown    int
	more     int // features behind the `+N more` line
}

// featureLayout budgets the panel against the rows ACTIVITY and the scene
// would otherwise get — ACTIVITY shrinks first. Pure arithmetic over the
// model: no allocation, safe per frame.
func (m Model) featureLayout() featLayout {
	n := len(m.feats)
	if n == 0 {
		return featLayout{}
	}
	spare := m.height - (len(m.board) + 4) - 5 - m.footerHeight()
	lay := featLayout{shown: min(n, maxExpanded), expanded: true}
	lay.more = n - lay.shown
	lay.height = 3 + lay.shown*3 + 1 // border+title, 3 lines each, the hint
	if lay.more > 0 {
		lay.height++
	}
	if spare < collapseBelow || lay.height > spare {
		room := spare - 3 // content rows left after border+title
		if room < 1 {
			return featLayout{}
		}
		lay = featLayout{expanded: false, shown: min(n, room)}
		if lay.shown < n && room > 1 {
			lay.shown = room - 1 // leave the +N more line
		}
		lay.more = n - lay.shown
		lay.height = 3 + lay.shown
		if lay.more > 0 && room > lay.shown {
			lay.height++
		}
	}
	if m.featSel >= lay.shown {
		lay.start = m.featSel - lay.shown + 1
	}
	return lay
}

// viewFeatures renders the panel for the layout featureLayout chose.
func (m Model) viewFeatures(lay featLayout) string {
	w := m.width - 4
	rows := make([]string, 0, lay.height)
	for i := lay.start; i < lay.start+lay.shown; i++ {
		f := m.feats[i]
		cursor := " "
		if i == m.featSel {
			cursor = sSelected.Render("▸")
		}
		title := cursor + sTitle.Render(f.slug) + "  " + sChrome.Render(f.title)
		if !lay.expanded {
			var strip strings.Builder
			if f.landedTok != "" {
				strip.WriteString(carStyles[feature.CarLanded].Render(f.landedTok))
			}
			for _, c := range f.rail {
				strip.WriteString(carStyles[c.state].Render(carGlyphs[c.state]))
			}
			rows = append(rows, truncPad(title+"  "+strip.String()+sDim.Render(" ▷ ")+sChrome.Render(f.base), w))
			continue
		}
		var rail strings.Builder
		rail.WriteString("  ")
		if f.landed != "" {
			rail.WriteString(carStyles[feature.CarLanded].Render(f.landed))
		}
		for _, c := range f.rail {
			rail.WriteString(carStyles[c.state].Render(carGlyphs[c.state]))
			rail.WriteString(sDim.Render(f.dash))
		}
		rail.WriteString(sDim.Render("▷ ") + sChrome.Render(f.base))
		rows = append(rows, truncPad(title, w), truncPad(rail.String(), w), truncPad("  "+sChrome.Render(f.labels), w))
		if i == m.featSel {
			rows = append(rows, truncPad("  "+sQuestion.Render(f.hint), w))
		}
	}
	if lay.more > 0 && len(rows)+3 < lay.height {
		rows = append(rows, truncPad(sDim.Render(fmt.Sprintf("  +%d more", lay.more)), w))
	}
	titleStyle, border := sPanelTitle, sPanel
	if m.focus == focusFeatures {
		titleStyle, border = sPanelTitleFocus, sPanelFocus
	}
	body := titleStyle.Render("FEATURES") + "\n" + strings.Join(rows, "\n")
	return border.Width(m.width - 2).Render(body)
}
