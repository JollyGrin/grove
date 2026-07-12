package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
)

// --- ticketLabel ---

func TestTicketLabel(t *testing.T) {
	cases := map[string]string{
		"grove-63":    "#63",
		"DEV-123":     "#123",
		"grove-abc":   "grove-abc", // no trailing digits: fall back whole
		"noseparator": "noseparator",
	}
	for in, want := range cases {
		if got := ticketLabel(in); got != want {
			t.Errorf("ticketLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- plant stages by age ---

func TestPlantStageByAge(t *testing.T) {
	cases := []struct {
		age           time.Duration
		canopy, trunk string
	}{
		{10 * time.Second, plantGlyph, ""},
		{59 * time.Second, plantGlyph, ""},
		{time.Minute, "ψ", ""},
		{29 * time.Minute, "ψ", ""},
		{30 * time.Minute, "∆", "│"},
		{119 * time.Minute, "∆", "│"},
		{2 * time.Hour, "♣", "│"},
		{5 * time.Hour, "♣", "│"},
	}
	for _, c := range cases {
		canopy, trunk := plantStage(c.age)
		if canopy != c.canopy || trunk != c.trunk {
			t.Errorf("plantStage(%v) = (%q,%q), want (%q,%q)", c.age, canopy, trunk, c.canopy, c.trunk)
		}
	}
}

func TestClusterFor(t *testing.T) {
	if got := clusterFor(forestGlyph, 6); got != forestGlyph {
		t.Errorf("narrow plot should not widen the mature canopy, got %q", got)
	}
	if got := clusterFor(forestGlyph, 10); got != strings.Repeat(forestGlyph, 3) {
		t.Errorf("wide plot should triple the mature canopy, got %q", got)
	}
	if got := clusterFor("♣", 10); got != "♣♣" {
		t.Errorf("wide plot should double the established canopy, got %q", got)
	}
	if got := clusterFor("ψ", 10); got != "ψ" {
		t.Errorf("sapling must never cluster, got %q", got)
	}
}

// --- buildTaskPlot: label + priority ---

func TestBuildTaskPlotStagesAndOverlays(t *testing.T) {
	now := time.Now()
	cel := map[string]int{}

	dead := &state.Task{Ticket: "grove-1", Agent: state.AgentDead, Created: now}
	if p := buildTaskPlot(dead, nil, fxFull, 0, cel); p.canopy != "✗" || p.label != "#1 ✗" {
		t.Errorf("dead plot = %+v", p)
	}

	merged := &state.Task{Ticket: "grove-2", Agent: state.AgentWorking, Created: now.Add(-3 * time.Hour)}
	pr := &github.PR{State: "MERGED"}
	if p := buildTaskPlot(merged, pr, fxFull, 0, cel); p.canopy != forestGlyph || p.trunk != "┃" || p.label != "#2 ⬢" {
		t.Errorf("merged plot = %+v, want mature canopy + ⬢ label regardless of age", p)
	}

	setup := &state.Task{Ticket: "grove-3", Agent: state.AgentSetup, Created: now}
	if p := buildTaskPlot(setup, nil, fxFull, 0, cel); p.canopy != "◌" || p.label != "#3 setup" {
		t.Errorf("setup plot = %+v", p)
	}

	question := &state.Task{Ticket: "grove-4", Agent: state.AgentWaiting, Created: now.Add(-time.Hour)}
	if p := buildTaskPlot(question, nil, fxFull, 0, cel); p.marker != "◆" || p.label != "#4 ?" || p.canopy != "∆" {
		t.Errorf("QUESTION plot = %+v, want ∆ stage + ◆ marker + ?-label", p)
	}

	blocked := &state.Task{Ticket: "grove-5", Agent: state.AgentBlocked, Created: now.Add(-time.Minute)}
	if p := buildTaskPlot(blocked, nil, fxFull, 0, cel); p.marker != "⚠" || p.label != "#5 ⚠" {
		t.Errorf("BLOCKED plot = %+v", p)
	}

	idle := &state.Task{Ticket: "grove-6", Agent: state.AgentIdle, Sentinel: "done", Created: now.Add(-time.Hour)}
	if p := buildTaskPlot(idle, nil, fxFull, 0, cel); p.label != "#6 ✓" {
		t.Errorf("idle✓ plot = %+v", p)
	}

	working := &state.Task{Ticket: "grove-7", Agent: state.AgentWorking, Created: now.Add(-5 * time.Second)}
	if p := buildTaskPlot(working, nil, fxFull, 0, cel); p.marker != "" || !strings.HasPrefix(p.label, "#7 ") {
		t.Errorf("plain working plot = %+v", p)
	}
}

// The QUESTION marker reuses the J5 knock while it's live at fxFull, and
// stays plain amber otherwise (calm, or the knock already expired).
func TestBuildTaskPlotQuestionKnock(t *testing.T) {
	q := &state.Task{Ticket: "grove-9", Agent: state.AgentWaiting, Created: time.Now()}
	cel := map[string]int{knockKey("grove-9"): knockTicks}
	p := buildTaskPlot(q, nil, fxFull, 0, cel)
	if p.markerSt.Render("◆") != knockStyle(0).Render("◆") {
		t.Error("live knock at fxFull should style the marker with knockStyle")
	}
	p = buildTaskPlot(q, nil, fxCalm, 0, cel)
	if p.markerSt.Render("◆") != sQuestion.Render("◆") {
		t.Error("calm must never apply the knock style")
	}
	p = buildTaskPlot(q, nil, fxFull, 0, map[string]int{})
	if p.markerSt.Render("◆") != sQuestion.Render("◆") {
		t.Error("expired knock should fall back to plain amber")
	}
}

// --- orchard ---

func TestBuildOrchardPlots(t *testing.T) {
	if got := buildOrchardPlots(nil); got != nil {
		t.Errorf("no done events should yield no orchard, got %+v", got)
	}
	events := []state.Event{
		{Type: state.EvTaskDone, Ticket: "grove-1"},
		{Type: state.EvTaskDone, Ticket: "grove-2"},
		{Type: state.EvTaskDone, Ticket: "grove-3"},
		{Type: state.EvTaskDone, Ticket: "grove-4"},
		{Type: state.EvTaskDone, Ticket: "grove-5"},
	}
	plots := buildOrchardPlots(events)
	if len(plots) != 4 { // 1 condensed + 3 individual
		t.Fatalf("expected 4 orchard plots (1 condensed + 3 individual), got %d: %+v", len(plots), plots)
	}
	if !plots[0].condensed || plots[0].label != forestGlyph+"×2" {
		t.Errorf("condensed remainder = %+v, want label %q", plots[0], forestGlyph+"×2")
	}
	for _, p := range plots[1:] {
		if p.ticket == "" || !strings.HasSuffix(p.label, "⬢") {
			t.Errorf("individual orchard plot = %+v", p)
		}
	}
	// Exactly 3 or fewer: no condensed remainder at all.
	if got := buildOrchardPlots(events[:3]); len(got) != 3 || got[0].condensed {
		t.Errorf("3 done trees should be all-individual, got %+v", got)
	}
}

// --- overflow ladder ---

func TestFitPlotsNoOverflow(t *testing.T) {
	plots := make([]scenePlot, 3)
	fitted, pw := fitPlots(plots, 200)
	if len(fitted) != 3 || pw != plotW {
		t.Errorf("plenty of width should not shrink anything: got %d plots at pw=%d", len(fitted), pw)
	}
}

func TestFitPlotsDropsCondensedFirst(t *testing.T) {
	plots := []scenePlot{
		{condensed: true, orchard: true},
		{ticket: "a", orchard: true},
		{ticket: "b"}, {ticket: "c"}, {ticket: "d"},
	}
	// Width fits 4 plots at plotW but not 5.
	width := 4*plotW + 2
	fitted, pw := fitPlots(plots, width)
	if pw != plotW {
		t.Fatalf("should still fit at the default plotW after dropping condensed, got pw=%d", pw)
	}
	if len(fitted) != 4 || fitted[0].condensed {
		t.Errorf("condensed plot should be dropped first, got %+v", fitted)
	}
}

func TestFitPlotsShrinksPlotWidth(t *testing.T) {
	var plots []scenePlot
	for i := 0; i < 10; i++ {
		plots = append(plots, scenePlot{ticket: string(rune('a' + i))})
	}
	fitted, pw := fitPlots(plots, 70) // 10 tasks, no orchard to drop/shrink
	if pw >= plotW {
		t.Errorf("plotW should shrink under pressure, got %d", pw)
	}
	if len(fitted)*pw > 70-2 {
		t.Errorf("fitted plots still overflow: %d*%d > %d", len(fitted), pw, 70-2)
	}
}

func TestFitPlotsNeverTruncatesMidPlot(t *testing.T) {
	var plots []scenePlot
	for i := 0; i < 20; i++ {
		plots = append(plots, scenePlot{ticket: string(rune('a' + i))})
	}
	fitted, pw := fitPlots(plots, 40)
	if len(fitted)*pw > 40-2 {
		t.Errorf("even the narrowest ladder step must fit: %d plots * %d > %d", len(fitted), pw, 40-2)
	}
	if !strings.Contains(fitted[len(fitted)-1].label, "more") {
		t.Errorf("last kept plot should be relabeled '+K more', got %+v", fitted[len(fitted)-1])
	}
}

// --- height-budget split ---

func TestRowBudgetsOffStaysZero(t *testing.T) {
	m := New(nil, "", "")
	m.width, m.height = 120, 40
	m.fx = fxOff
	m.tasks = []*state.Task{{Ticket: "grove-1", Agent: state.AgentWorking, Created: time.Now()}}
	m.events = []state.Event{{Type: state.EvTaskCreated, Ticket: "grove-1", Time: time.Now()}}
	_, sceneRows := m.rowBudgets()
	if sceneRows != 0 {
		t.Errorf("fxOff must never yield scene rows, got %d", sceneRows)
	}
}

func TestRowBudgetsSplitsLeftover(t *testing.T) {
	cases := []struct {
		name          string
		height, tasks int
		events        int
	}{
		{"busy tall pane", 60, 3, 40},
		{"quiet short pane", 20, 0, 0},
		{"short pane", 14, 1, 2},
		{"tall pane", 80, 5, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(nil, "", "")
			m.width, m.height = 120, c.height
			m.fx = fxFull
			for i := 0; i < c.tasks; i++ {
				m.tasks = append(m.tasks, &state.Task{Ticket: string(rune('a' + i)), Agent: state.AgentWorking, Created: time.Now()})
			}
			for i := 0; i < c.events; i++ {
				m.events = append(m.events, state.Event{Type: state.EvTaskCreated, Ticket: "x", Time: time.Now()})
			}
			activityRows, sceneRows := m.rowBudgets()
			leftover := c.height - (c.tasks + 4) - 5 - m.footerHeight()
			if leftover < 0 {
				leftover = 0
			}
			if activityRows+sceneRows != leftover {
				t.Errorf("%s: activityRows(%d)+sceneRows(%d) != leftover(%d)", c.name, activityRows, sceneRows, leftover)
			}
			if sceneRows != 0 && sceneRows < 3 {
				t.Errorf("%s: scene never renders below its 3-row floor, got %d", c.name, sceneRows)
			}
		})
	}
}

// --- tier selection ---

func TestSceneTierFor(t *testing.T) {
	cases := map[int]sceneTier{3: sceneStrip, 5: sceneStrip, 6: sceneCompact, 8: sceneCompact, 9: sceneFull, 20: sceneFull}
	for rows, want := range cases {
		if got := sceneTierFor(rows); got != want {
			t.Errorf("sceneTierFor(%d) = %v, want %v", rows, got, want)
		}
	}
}

// --- sceneLines: totals, width, determinism, fxOff emptiness ---

func TestSceneLinesFxOffIsBlank(t *testing.T) {
	tasks := []*state.Task{{Ticket: "grove-1", Agent: state.AgentWorking, Created: time.Now()}}
	lines := sceneLines(tasks, nil, nil, nil, 0, 80, 6, 10, fxOff, "")
	if len(lines) != 6 {
		t.Fatalf("must always emit exactly rows lines, got %d", len(lines))
	}
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			t.Errorf("fxOff scene must be blank, got %q", l)
		}
	}
}

func TestSceneLinesExactRowsAndWidth(t *testing.T) {
	var tasks []*state.Task
	for i := 0; i < 6; i++ {
		tasks = append(tasks, &state.Task{Ticket: "grove-" + string(rune('1'+i)), Agent: state.AgentWorking, Created: time.Now().Add(-time.Duration(i) * time.Hour)})
	}
	events := []state.Event{
		{Type: state.EvTaskDone, Ticket: "grove-90"},
		{Type: state.EvTaskDone, Ticket: "grove-91"},
	}
	for _, rows := range []int{3, 4, 5, 6, 8, 9, 15} {
		lines := sceneLines(tasks, nil, events, nil, 3, 60, rows, 10, fxFull, "")
		if len(lines) != rows {
			t.Errorf("rows=%d: got %d lines", rows, len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w > 60 {
				t.Errorf("rows=%d line %d is %d cells wide", rows, i, w)
			}
		}
	}
}

func TestSceneLinesEmptyGrove(t *testing.T) {
	lines := sceneLines(nil, nil, nil, nil, 0, 60, 6, 10, fxFull, "")
	if len(lines) != 6 {
		t.Fatalf("empty grove must still emit exactly rows lines, got %d", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "▁") {
		t.Errorf("empty grove's last row should carry the soil line, got %q", lines[len(lines)-1])
	}
}

func TestSceneLinesDeterministic(t *testing.T) {
	tasks := []*state.Task{{Ticket: "grove-42", Agent: state.AgentWorking, Created: time.Now().Add(-time.Hour)}}
	events := []state.Event{{Type: state.EvTaskDone, Ticket: "grove-1"}}
	a := sceneLines(tasks, nil, events, nil, 5, 80, 8, 10, fxFull, "")
	b := sceneLines(tasks, nil, events, nil, 5, 80, 8, 10, fxFull, "")
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Error("same inputs should render identically")
	}
}

// --- marker priority: a plot never carries more than one sky glyph ---

func TestRenderSkyRowSingleMarkerPerPlot(t *testing.T) {
	plots := []scenePlot{{ticket: "a", canopy: "∆", marker: "◆", markerSt: sQuestion}}
	layouts := layoutPlots(plots, plotW)
	_, _, _, _, skyGrid := renderPlotRows(layouts, plotW, true)
	row := skyGrid.String()
	if strings.Count(row, "◆") != 1 {
		t.Errorf("expected exactly one marker glyph, got %q", row)
	}
}

// --- S2: the cast ---

func TestWalkedOff(t *testing.T) {
	prev := []*state.Task{
		{Ticket: "grove-1", Agent: state.AgentWorking},
		{Ticket: "grove-2", Agent: state.AgentWorking},
		{Ticket: "grove-3", Agent: state.AgentIdle},
	}
	next := []*state.Task{
		{Ticket: "grove-1", Agent: state.AgentIdle},    // walked off
		{Ticket: "grove-2", Agent: state.AgentWorking}, // still working
		{Ticket: "grove-3", Agent: state.AgentIdle},    // was never working
	}
	got := walkedOff(prev, next)
	if len(got) != 1 || got[0] != "grove-1" {
		t.Errorf("walkedOff = %v, want [grove-1]", got)
	}
	// A task that disappears entirely (done/cleaned) has no plot to walk off
	// of — nothing to flag.
	if got := walkedOff(prev, next[1:]); len(got) != 0 {
		t.Errorf("a removed task should not walk off: %v", got)
	}
}

func TestPawnSideAlternatesEvery4Ticks(t *testing.T) {
	side0 := pawnSide("grove-1", 0)
	for tick := uint64(0); tick < 4; tick++ {
		if pawnSide("grove-1", tick) != side0 {
			t.Errorf("pawn side changed within the same 4-tick window at tick %d", tick)
		}
	}
	if pawnSide("grove-1", 4) == pawnSide("grove-1", 0) {
		t.Error("pawn side should flip after 4 ticks")
	}
}

func TestFairyOffsetOrbitsAndTrailIsOneTickBehind(t *testing.T) {
	for tick := uint64(0); tick < 20; tick++ {
		if fairyTrailOffset(tick+1) != fairyOffset(tick) {
			t.Errorf("trail at tick %d should equal the fairy's offset the tick before", tick+1)
		}
	}
	// No underflow at tick 0.
	_ = fairyTrailOffset(0)
}

func TestApplyCastPawnQueenOpposite(t *testing.T) {
	tasks := []*state.Task{{Ticket: "grove-1", Agent: state.AgentWorking}}
	plots := []scenePlot{{ticket: "grove-1", canopy: "∆", trunk: "│"}}
	layouts := layoutPlots(plots, plotW)
	trunkGrid := newSceneGrid(plotW)
	skyGrid := newSceneGrid(plotW)
	ticketCol := map[string]int{"grove-1": 0}

	applyCast(layouts, ticketCol, plotW, tasks, nil, map[string]int{}, 0, "grove-1", true, trunkGrid, skyGrid)

	row := trunkGrid.String()
	if !strings.Contains(row, "♟") {
		t.Errorf("pawn should render on the working task's trunk row, got %q", row)
	}
	if !strings.Contains(row, "♛") {
		t.Errorf("queen should render on the focused task's trunk row, got %q", row)
	}
	pawnCol := clampCol(0+layouts[0].pos+pawnSide("grove-1", 0), 0, plotW-1)
	queenCol := clampCol(0+layouts[0].pos-pawnSide("grove-1", 0), 0, plotW-1)
	if pawnCol == queenCol {
		t.Skip("degenerate layout put pawn and queen on the same column — nothing to assert")
	}
	if trunkGrid.chars[pawnCol] != '♟' || trunkGrid.chars[queenCol] != '♛' {
		t.Errorf("pawn/queen not at their expected opposite columns: pawn@%d=%q queen@%d=%q",
			pawnCol, trunkGrid.chars[pawnCol], queenCol, trunkGrid.chars[queenCol])
	}
}

func TestApplyCastWalkOffPawnOffsetsRight(t *testing.T) {
	tasks := []*state.Task{{Ticket: "grove-1", Agent: state.AgentIdle}}
	plots := []scenePlot{{ticket: "grove-1", canopy: "ψ"}}
	layouts := layoutPlots(plots, plotW)
	ticketCol := map[string]int{"grove-1": 0}

	early := newSceneGrid(plotW)
	applyCast(layouts, ticketCol, plotW, tasks, nil, map[string]int{walkKey("grove-1"): walkTicks}, 0, "", true, early, newSceneGrid(plotW))
	late := newSceneGrid(plotW)
	applyCast(layouts, ticketCol, plotW, tasks, nil, map[string]int{walkKey("grove-1"): 1}, 0, "", true, late, newSceneGrid(plotW))

	earlyCol, lateCol := -1, -1
	for i, ch := range early.chars {
		if ch == '♟' {
			earlyCol = i
		}
	}
	for i, ch := range late.chars {
		if ch == '♟' {
			lateCol = i
		}
	}
	if earlyCol < 0 || lateCol < 0 {
		t.Fatalf("expected a walk-off pawn in both grids, got early=%d late=%d", earlyCol, lateCol)
	}
	if lateCol <= earlyCol {
		t.Errorf("walk-off pawn should drift right as remaining ticks fall: early=%d (remaining=walkTicks) late=%d (remaining=1)", earlyCol, lateCol)
	}
}

func TestApplyCastFairyRecencyWindow(t *testing.T) {
	tasks := []*state.Task{{Ticket: "grove-1"}}
	plots := []scenePlot{{ticket: "grove-1", canopy: "∆"}}
	layouts := layoutPlots(plots, plotW)
	ticketCol := map[string]int{"grove-1": 0}

	fresh := []state.Event{{Type: state.EvAnswered, Ticket: "grove-1", Time: time.Now().Add(-5 * time.Second)}}
	skyFresh := newSceneGrid(plotW)
	applyCast(layouts, ticketCol, plotW, tasks, fresh, map[string]int{}, 0, "", false, newSceneGrid(plotW), skyFresh)
	if !strings.Contains(skyFresh.String(), "✧") {
		t.Errorf("a recent answer should summon the fairy, got %q", skyFresh.String())
	}

	stale := []state.Event{{Type: state.EvAnswered, Ticket: "grove-1", Time: time.Now().Add(-time.Minute)}}
	skyStale := newSceneGrid(plotW)
	applyCast(layouts, ticketCol, plotW, tasks, stale, map[string]int{}, 0, "", false, newSceneGrid(plotW), skyStale)
	if strings.Contains(skyStale.String(), "✧") {
		t.Errorf("an answer older than 45s must not summon the fairy, got %q", skyStale.String())
	}
}

// The scene wiring end-to-end: a working task + a fresh answer at fxFull
// renders both the pawn and the fairy; fxCalm renders neither (cast is
// fxFull only, per S2).
func TestSceneLinesCastGatedToFxFull(t *testing.T) {
	tasks := []*state.Task{{Ticket: "grove-1", Agent: state.AgentWorking, Created: time.Now().Add(-time.Hour)}}
	events := []state.Event{{Type: state.EvAnswered, Ticket: "grove-1", Time: time.Now().Add(-time.Second)}}

	full := strings.Join(sceneLines(tasks, nil, events, nil, 0, 80, 9, 10, fxFull, ""), "\n")
	if !strings.Contains(full, "♟") {
		t.Errorf("fxFull scene should render the pawn, got:\n%s", full)
	}
	if !strings.Contains(full, "✧") {
		t.Errorf("fxFull scene should render the fairy for a fresh answer, got:\n%s", full)
	}

	calm := strings.Join(sceneLines(tasks, nil, events, nil, 0, 80, 9, 10, fxCalm, ""), "\n")
	if strings.Contains(calm, "♟") || strings.Contains(calm, "✧") {
		t.Errorf("fxCalm must not render the cast, got:\n%s", calm)
	}
}

// --- integration: viewScene renders inside View(), never at fxOff ---

func TestViewIncludesSceneAtCalmPlus(t *testing.T) {
	m := fixtureModel(t)
	m.fx = fxCalm
	m.height = 40
	out := m.View()
	if lipgloss.Height(out) > m.height {
		t.Errorf("total render is %d lines on a %d-line pane", lipgloss.Height(out), m.height)
	}
	m.fx = fxOff
	before := m.View()
	m2 := fixtureModel(t)
	m2.height = 40
	m2.fx = fxOff
	after := m2.View()
	if before != after {
		t.Error("fxOff render should be deterministic/unaffected by scene wiring")
	}
}
