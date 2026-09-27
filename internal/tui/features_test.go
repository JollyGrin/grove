package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/feature"
	"github.com/JollyGrin/grove/internal/state"
)

// featureModel is a clock-pinned board carrying n open features, each
// with one active car on the board plus landed/queued cars from a slow
// pass. Feature i is "train-i" off main.
func featureModel(t *testing.T, n, w, h int) Model {
	t.Helper()
	pinHour(t, 10)
	now := time.Now()
	m := New(nil, "", "golden")
	m.width, m.height, m.fx = w, h, fxOff
	m.features = map[string]*state.Feature{}
	m.featSlow = map[string]*feature.Status{}
	for i := 0; i < n; i++ {
		slug := fmt.Sprintf("train-%d", i)
		m.features[slug] = &state.Feature{Slug: slug, Branch: "feature/" + slug, Base: "main", Label: slug,
			CreatedAt: now.Add(time.Duration(i) * time.Minute)}
		behind := 2
		m.featSlow[slug] = &feature.Status{
			Cars: []feature.Car{
				{Ticket: fmt.Sprintf("grove-%d", 100+i*10), Number: 100 + i*10, State: feature.CarLanded, EstUSD: 1.5},
				{Ticket: fmt.Sprintf("grove-%d", 101+i*10), Number: 101 + i*10, State: feature.CarWorking, EstUSD: 0.5},
				{Ticket: fmt.Sprintf("grove-%d", 102+i*10), Number: 102 + i*10, State: feature.CarQueued},
			},
			BehindBase: &behind,
		}
	}
	m.localTasks = []*state.Task{
		{Ticket: "grove-18", Title: "off the train", Repo: "grove", Agent: state.AgentWorking, Created: now},
	}
	if n > 0 {
		m.localTasks = append(m.localTasks, &state.Task{Ticket: "grove-101", Title: "on the train", Repo: "grove",
			Agent: state.AgentWaiting, Question: "which?", Feature: "train-0", Created: now})
	}
	m.assemble()
	return m
}

func frameLines(s string) []string { return strings.Split(s, "\n") }

func TestMergeStatusOverlaysLiveCars(t *testing.T) {
	slow := &feature.Status{Cars: []feature.Car{
		{Ticket: "g-1", State: feature.CarLanded, EstUSD: 1},
		{Ticket: "g-2", State: feature.CarWorking, EstUSD: 2},
		{Ticket: "g-3", State: feature.CarQueued},
		{Ticket: "g-4", State: feature.CarQueued},
	}}
	live := &feature.Status{Cars: []feature.Car{
		{Ticket: "g-2", State: feature.CarQuestion}, // re-stated
		{Ticket: "g-3", State: feature.CarWorking},  // grabbed since
		{Ticket: "g-9", State: feature.CarWorking},  // new, unknown to slow
	}}
	got := mergeStatus(live, slow)
	var order []string
	for _, c := range got.Cars {
		order = append(order, c.Ticket+":"+c.State)
	}
	want := "g-1:landed g-2:question g-3:working g-9:working g-4:queued"
	if s := strings.Join(order, " "); s != want {
		t.Errorf("cars = %s, want %s", s, want)
	}
	if got.Landed != 1 || got.Total != 5 || got.EstUSD != 3 {
		t.Errorf("landed/total/est = %d/%d/%.1f, want 1/5/3", got.Landed, got.Total, got.EstUSD)
	}
	if only := mergeStatus(live, nil); only != live {
		t.Error("no slow pass yet: the live status stands alone")
	}
}

func TestFeatureHintPriority(t *testing.T) {
	f := &state.Feature{Slug: "s", Branch: "feature/s", Base: "main"}
	no := false
	cases := []struct {
		st   feature.Status
		want string
	}{
		{feature.Status{Cars: []feature.Car{{Number: 7, State: feature.CarReady}, {Number: 8, State: feature.CarQuestion}}}, "◆ #8"},
		{feature.Status{Cars: []feature.Car{{Number: 7, State: feature.CarReady}}}, "✓ 1 car(s) ready"},
		{feature.Status{Mergeable: &no, Cars: []feature.Car{{State: feature.CarWorking}}}, "✗ feature/s conflicts"},
		{feature.Status{Landed: 2, Total: 2, Cars: []feature.Car{{State: feature.CarLanded}, {State: feature.CarLanded}}}, "⬢ every car landed"},
		{feature.Status{Cars: []feature.Car{{Number: 9, State: feature.CarQueued}}}, "· next up: #9"},
		{feature.Status{Cars: []feature.Car{{State: feature.CarWorking}, {Number: 9, State: feature.CarQueued}}}, "nothing needs you"},
	}
	for i, c := range cases {
		if got := featureHint(f, &c.st); !strings.HasPrefix(got, c.want) {
			t.Errorf("case %d: hint %q, want prefix %q", i, got, c.want)
		}
	}
}

// One feature: title, rail, labels and the selected feature's amber hint.
func TestFeaturesPanelOneFeature(t *testing.T) {
	m := featureModel(t, 1, 120, 40)
	out := m.View()
	for _, want := range []string{
		"FEATURES",
		"train-0  1/3  ↓2 main  serve –  est $2.00", // title line
		"⬢────◆────·────▷ main",                     // rail: grove-101's live QUESTION wins
		"#100 #101 #102",           // labels, one cell per car
		"◆ #101 is waiting on you", // the selected feature's hint
	} {
		if !strings.Contains(out, want) {
			t.Errorf("frame lacks %q:\n%s", want, out)
		}
	}
	// FEATURES sits between the header and AGENTS.
	fi, ai := strings.Index(out, "FEATURES"), strings.Index(out, "AGENTS")
	if fi < 0 || ai < 0 || fi > ai {
		t.Errorf("FEATURES must precede AGENTS (at %d vs %d)", fi, ai)
	}
}

func TestFeaturesPanelCapsExpandedAtThree(t *testing.T) {
	m := featureModel(t, 4, 120, 59)
	out := m.View()
	if !strings.Contains(out, "+1 more") {
		t.Errorf("4 features should show +1 more:\n%s", out)
	}
	for i := 0; i < 3; i++ {
		if !strings.Contains(out, fmt.Sprintf("train-%d  ", i)) {
			t.Errorf("train-%d should be expanded", i)
		}
	}
	if strings.Contains(out, "train-3  ") {
		t.Error("train-3 is behind +1 more")
	}
	if n := strings.Count(out, "▷ main"); n != 3 {
		t.Errorf("rail lines = %d, want 3 expanded", n)
	}
	// Selecting the 4th feature scrolls it into view.
	m.focus = focusFeatures
	m.move(3)
	if out := m.View(); !strings.Contains(out, "train-3  ") || !strings.Contains(out, "+1 more") {
		t.Errorf("selected train-3 must be visible:\n%s", out)
	}
}

func TestFeaturesPanelCollapsesOnShortPane(t *testing.T) {
	m := featureModel(t, 2, 120, 22) // spare < 12
	lay := m.featureLayout()
	if lay.expanded || lay.height == 0 {
		t.Fatalf("short pane should collapse, got %+v", lay)
	}
	out := m.View()
	for _, ln := range frameLines(out) {
		if strings.Contains(ln, "serve –") && !strings.Contains(ln, "▷ main") {
			t.Errorf("collapsed title line lacks the inline strip: %q", ln)
		}
	}
	if strings.Contains(out, "#100") {
		t.Error("collapsed features have no label line")
	}
	if !strings.Contains(out, "train-0  1/3  ↓2 main  serve –  est $2.00  ⬢◆· ▷ main") {
		t.Errorf("collapsed title + strip missing:\n%s", out)
	}
}

// Every line fits the width and the frame fits the height, at the 59-row
// cockpit and at small panes, for 0..5 features and every fx level.
func TestFeaturesFrameFits(t *testing.T) {
	sizes := [][2]int{{120, 59}, {80, 24}, {60, 16}, {40, 12}, {100, 30}}
	for _, sz := range sizes {
		for n := 0; n <= 5; n++ {
			for _, fx := range []fxLevel{fxOff, fxCalm, fxFull} {
				m := featureModel(t, n, sz[0], sz[1])
				m.fx = fx
				lines := frameLines(m.View())
				if len(lines) > m.height {
					t.Errorf("%dx%d n=%d fx=%d: %d lines > height %d", sz[0], sz[1], n, fx, len(lines), m.height)
				}
				for i, ln := range lines {
					if lw := lipgloss.Width(ln); lw > m.width {
						t.Errorf("%dx%d n=%d fx=%d: line %d is %d cells", sz[0], sz[1], n, fx, i, lw)
					}
				}
			}
		}
	}
}

func TestTrainColumnOnlyWithFeatures(t *testing.T) {
	with := featureModel(t, 1, 140, 40)
	agents := with.viewAgents()
	if !strings.Contains(agents, "TRAIN") {
		t.Errorf("AGENTS lacks TRAIN with a feature open:\n%s", agents)
	}
	if !strings.Contains(agents, "grove-101  grove      train-0") {
		t.Errorf("the train car's row should name its slug:\n%s", agents)
	}
	without := featureModel(t, 0, 140, 40)
	if strings.Contains(without.viewAgents(), "TRAIN") {
		t.Error("TRAIN column must not exist without an open feature")
	}
}

// fkey is a key press, tab included (remote_test's key is runes only).
func fkey(s string) tea.KeyMsg {
	if s == "tab" {
		return tea.KeyMsg{Type: tea.KeyTab}
	}
	return key(s)
}

func TestTabTogglesFocus(t *testing.T) {
	m := featureModel(t, 2, 120, 40)
	if m.focus != focusAgents {
		t.Fatal("AGENTS holds focus by default")
	}
	nm, _ := m.handleKey(fkey("tab"))
	m = nm.(Model)
	if m.focus != focusFeatures {
		t.Fatal("tab should focus FEATURES")
	}
	sel := m.sel
	nm, _ = m.handleKey(fkey("j"))
	m = nm.(Model)
	if m.featSel != 1 || m.sel != sel {
		t.Errorf("j with FEATURES focused moves the feature cursor only (featSel=%d sel=%d)", m.featSel, m.sel)
	}
	nm, _ = m.handleKey(fkey("d"))
	m = nm.(Model)
	if m.mode != modeList || !strings.Contains(m.flash, "tab to AGENTS") {
		t.Errorf("a row key on FEATURES must not act on a task (mode=%d flash=%q)", m.mode, m.flash)
	}
	nm, _ = m.handleKey(fkey("tab"))
	m = nm.(Model)
	if m.focus != focusAgents {
		t.Error("tab again returns to AGENTS")
	}
	nm, _ = m.handleKey(fkey("j"))
	if nm.(Model).sel == sel {
		t.Error("j with AGENTS focused moves the task cursor")
	}

	// No open feature: tab is inert.
	none := featureModel(t, 0, 120, 40)
	nm, _ = none.handleKey(fkey("tab"))
	if nm.(Model).focus != focusAgents {
		t.Error("tab without a feature must not move focus")
	}
}

// View computes no status: the only caller of the status function is
// assemble, on refresh.
func TestViewComputesNoStatus(t *testing.T) {
	calls := 0
	prev := statusesFn
	statusesFn = func(in feature.StatusInput) (map[string]*feature.Status, error) {
		calls++
		return prev(in)
	}
	t.Cleanup(func() { statusesFn = prev })

	m := featureModel(t, 3, 120, 59)
	if calls == 0 {
		t.Fatal("assemble should compute status")
	}
	calls = 0
	for _, fx := range []fxLevel{fxOff, fxCalm, fxFull} {
		m.fx = fx
		for i := 0; i < 5; i++ {
			m.tick = uint64(i)
			_ = m.View()
		}
	}
	if calls != 0 {
		t.Errorf("View made %d status computations, want 0", calls)
	}
}

func TestFooterAndHelpListTab(t *testing.T) {
	m := featureModel(t, 1, 80, 24) // an ordinary pane keeps it
	if !strings.Contains(m.viewFooter(), "tab focus") {
		t.Errorf("footer lacks tab with a feature open: %q", m.viewFooter())
	}
	if strings.Contains(featureModel(t, 0, 160, 40).viewFooter(), "tab") {
		t.Error("footer lists tab with no feature open")
	}
	m.mode = modeHelp
	if !strings.Contains(m.View(), "tab") {
		t.Error("help screen lacks tab")
	}
}

func TestFeaturesCmdFreeWithoutFeatures(t *testing.T) {
	if featuresCmd(nil, "", nil) != nil {
		t.Error("no open feature: no status pass, no goroutine")
	}
	if openFeatures(map[string]*state.Feature{"x": {Slug: "x", Closed: true}}) != nil {
		t.Error("closed features are not open")
	}
}

// A newly opened feature fires one status pass off the refresh beat; an
// unchanged set does not.
func TestRefreshFiresFeaturePassOnNewFeature(t *testing.T) {
	m := featureModel(t, 0, 120, 40)
	feats := map[string]*state.Feature{"a": {Slug: "a", Base: "main"}}
	nm, cmd := m.Update(refreshMsg{ok: true, features: feats})
	m = nm.(Model)
	if len(m.feats) != 1 || cmd == nil {
		t.Fatalf("new feature: feats=%d cmd=%v", len(m.feats), cmd)
	}
	_, cmd = m.Update(refreshMsg{ok: true, features: feats})
	if cmd != nil {
		t.Error("an unchanged feature set must not fire another pass")
	}
	nm, _ = m.Update(refreshMsg{ok: true})
	if len(nm.(Model).feats) != 0 || nm.(Model).trainW != 0 {
		t.Error("closing the last feature removes the panel and TRAIN column")
	}
}
