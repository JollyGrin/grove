package tui

// Joy v2 (grove-56): A5 quit farewell, J3 forest strip, J4 planting, J5
// question knock, J6 milestones, A6 first light, A7 night fireflies. Same
// test discipline as joy_test.go: pure table tests first, then render-level
// leak checks proving fx off stays byte-identical.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/state"
)

// pinHour pins the render clock for the duration of a test and restores the
// real one on cleanup — fireflies fly only at night (A7), so an unpinned
// clock makes render assertions flap with the wall clock.
func pinHour(t *testing.T, hour int) {
	t.Helper()
	prev := nowHour
	nowHour = func() int { return hour }
	t.Cleanup(func() { nowHour = prev })
}

// Trap #2 again: every glyph joy v2 introduces must be exactly one cell.
func TestJoyV2GlyphsAreSingleCell(t *testing.T) {
	for _, g := range []string{forestGlyph, plantGlyph, fireflyGlyph} {
		if w := lipgloss.Width(g); w != 1 {
			t.Errorf("glyph %q width = %d, want 1", g, w)
		}
	}
}

// --- A5 ---

func TestFarewellLine(t *testing.T) {
	// A5's silence is part of the fx-off contract.
	if got := farewellLine(fxOff, 3, 10); got != "" {
		t.Errorf("farewell at off = %q, want empty", got)
	}
	// Busy: carries the count; rest: the grove rests/sleeps. Both vary by
	// time-of-day bucket and every bucket is non-empty.
	for hour := 0; hour < 24; hour++ {
		busy := farewellLine(fxCalm, 3, hour)
		if !strings.Contains(busy, "3 trees") {
			t.Errorf("busy farewell at hour %d lost the count: %q", hour, busy)
		}
		if rest := farewellLine(fxFull, 0, hour); strings.TrimSpace(rest) == "" {
			t.Errorf("rest farewell at hour %d is blank", hour)
		}
	}
	if !strings.Contains(farewellLine(fxCalm, 1, 14), "one tree") {
		t.Error("singular farewell should say 'one tree'")
	}
	if farewellLine(fxCalm, 0, 23) != "the grove sleeps" {
		t.Errorf("night rest farewell = %q", farewellLine(fxCalm, 0, 23))
	}
	// The four busy phrasings actually differ across buckets.
	seen := map[string]bool{}
	for _, h := range []int{6, 14, 19, 23} {
		seen[farewellLine(fxCalm, 2, h)] = true
	}
	if len(seen) != 4 {
		t.Errorf("busy farewell has %d distinct time-of-day phrasings, want 4", len(seen))
	}
}

func TestCountWorking(t *testing.T) {
	tasks := []*state.Task{
		{Ticket: "a", Agent: state.AgentWorking},
		{Ticket: "b", Agent: state.AgentSetup},
		{Ticket: "c", Agent: state.AgentWaiting},
	}
	if n := countWorking(tasks); n != 2 {
		t.Errorf("countWorking = %d, want 2 (working + setup)", n)
	}
}

// --- J3 ---

func TestForestStrip(t *testing.T) {
	cases := map[int]string{
		0:  "",
		1:  "♠",
		5:  "♠♠♠♠♠",
		12: strings.Repeat("♠", 12),
		13: "♠×13",
		23: "♠×23",
	}
	for n, want := range cases {
		if got := forestStrip(n); got != want {
			t.Errorf("forestStrip(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestForestStripInHeader(t *testing.T) {
	m := fixtureModel(t)
	m.fx = fxCalm
	m.events = []state.Event{
		{Type: state.EvTaskDone}, {Type: state.EvTaskDone}, {Type: state.EvTaskDone},
	}
	if out := m.viewHeader(); !strings.Contains(out, "♠♠♠") {
		t.Errorf("calm header should carry the forest strip, got:\n%s", out)
	}
	m.fx = fxOff
	if out := m.viewHeader(); strings.Contains(out, "♠") {
		t.Errorf("off header leaked the forest strip:\n%s", out)
	}
}

// The strip must never overflow the header: on a pane too narrow for
// left + gauge + strip + counts the flourish is shed, keeping the line
// within m.width — an overflowing header hard-wraps the whole alt-screen.
func TestForestStripNeverOverflowsHeader(t *testing.T) {
	m := fixtureModel(t)
	m.fx = fxCalm
	for i := 0; i < 12; i++ {
		m.events = append(m.events, state.Event{Type: state.EvTaskDone})
	}
	m.width = 80
	if out := m.viewHeader(); lipgloss.Width(out) > m.width {
		t.Errorf("header is %d cells on an %d-cell pane:\n%s", lipgloss.Width(out), m.width, out)
	}
	// Wide pane: the full 12-tree strip rides along untrimmed.
	m.width = 140
	out := m.viewHeader()
	if !strings.Contains(out, strings.Repeat("♠", 12)) {
		t.Errorf("wide header should carry the full strip:\n%s", out)
	}
	if lipgloss.Width(out) > m.width {
		t.Errorf("wide header overflows: %d > %d", lipgloss.Width(out), m.width)
	}
}

// --- J4 ---

// The planting match keys on feed.go's stable EvTaskCreated string — if that
// wording ever drifts, this catches it before the flourish silently dies.
func TestPlantingMatchesFeed(t *testing.T) {
	items := feedItems([]state.Event{{Type: state.EvTaskCreated, Ticket: "grove-1", Time: time.Now()}})
	if len(items) != 1 || items[0].Text != grabbedText {
		t.Fatalf("feedItems EvTaskCreated text = %q, planting matcher expects %q", items[0].Text, grabbedText)
	}
}

func TestPlantingWindow(t *testing.T) {
	cases := []struct {
		text string
		age  time.Duration
		want bool
	}{
		{grabbedText, 10 * time.Second, true},
		{grabbedText, 59 * time.Second, true},
		{grabbedText, time.Minute, false},    // settled
		{grabbedText, -time.Second, false},   // clock skew — don't flourish
		{"adopted", 10 * time.Second, false}, // only a fresh grab plants
		{"done — cleaned up", 0 * time.Second, false},
	}
	for _, c := range cases {
		if got := planting(c.text, c.age); got != c.want {
			t.Errorf("planting(%q, %v) = %v, want %v", c.text, c.age, got, c.want)
		}
	}
}

func TestPlantingRenders(t *testing.T) {
	m := fixtureModel(t)
	m.fx = fxFull
	m.events = []state.Event{{Type: state.EvTaskCreated, Ticket: "grove-19", Time: time.Now().Add(-10 * time.Second)}}
	out := m.View()
	if !strings.Contains(out, plantText) || strings.Contains(out, grabbedText) {
		t.Errorf("fresh grab at full should read as a planting, got:\n%s", out)
	}
	// After the window it settles back to the plain grabbed line.
	m.events[0].Time = time.Now().Add(-2 * time.Minute)
	if out := m.View(); strings.Contains(out, plantText) {
		t.Errorf("settled grab still reads as a planting:\n%s", out)
	}
	// calm never plants — J4 is a celebration (fxFull).
	m.fx = fxCalm
	m.events[0].Time = time.Now().Add(-10 * time.Second)
	if out := m.View(); strings.Contains(out, plantText) {
		t.Errorf("calm leaked the planting row:\n%s", out)
	}
}

// --- J5 ---

func TestFreshQuestions(t *testing.T) {
	q := &state.Task{Ticket: "grove-1", Agent: state.AgentWaiting}
	w := &state.Task{Ticket: "grove-1", Agent: state.AgentWorking}
	q2 := &state.Task{Ticket: "grove-2", Agent: state.AgentWaiting}
	// A flip working→QUESTION knocks; one already asking stays quiet; a
	// question present at first load (empty prev) knocks once, like J1.
	if got := freshQuestions([]*state.Task{w}, []*state.Task{q}); len(got) != 1 || got[0] != "grove-1" {
		t.Errorf("flip to QUESTION not detected: %v", got)
	}
	if got := freshQuestions([]*state.Task{q}, []*state.Task{q}); len(got) != 0 {
		t.Errorf("steady QUESTION should not re-knock: %v", got)
	}
	if got := freshQuestions(nil, []*state.Task{q, q2}); len(got) != 2 {
		t.Errorf("first-load questions should knock once each: %v", got)
	}
	if got := freshQuestions([]*state.Task{q}, []*state.Task{w}); len(got) != 0 {
		t.Errorf("QUESTION resolving must not knock: %v", got)
	}
}

func TestKnockStyleAlternates(t *testing.T) {
	if !knockStyle(0).GetBold() {
		t.Error("knock even tick should be bold")
	}
	if knockStyle(1).GetBold() {
		t.Error("knock odd tick should be normal weight")
	}
	// Both phases stay amber — only the weight moves.
	if knockStyle(0).GetForeground() != knockStyle(1).GetForeground() {
		t.Error("knock must not change color, only weight")
	}
}

// The Update-level wiring: a refresh that flips a task to QUESTION seeds a
// capped, prefixed celebrations entry at full — and never at calm.
func TestKnockDetectionInUpdate(t *testing.T) {
	m := fixtureModel(t)
	m.fx = fxFull
	m.localTasks = []*state.Task{{Ticket: "grove-9", Agent: state.AgentWorking}}
	m.assemble()
	next, _ := m.Update(refreshMsg{tasks: []*state.Task{{Ticket: "grove-9", Agent: state.AgentWaiting}}, ok: true})
	got := next.(Model)
	if got.celebrations[knockKey("grove-9")] != knockTicks {
		t.Errorf("flip to QUESTION did not seed a knock: %v", got.celebrations)
	}
	if _, clash := got.celebrations["grove-9"]; clash {
		t.Error("knock key collided with the J1 merge-sparkle namespace")
	}

	m = fixtureModel(t)
	m.fx = fxCalm
	m.localTasks = []*state.Task{{Ticket: "grove-9", Agent: state.AgentWorking}}
	m.assemble()
	next, _ = m.Update(refreshMsg{tasks: []*state.Task{{Ticket: "grove-9", Agent: state.AgentWaiting}}, ok: true})
	if got := next.(Model); len(got.celebrations) != 0 {
		t.Errorf("calm must not seed knocks: %v", got.celebrations)
	}
}

// --- J6 ---

func TestDoneRitualFlash(t *testing.T) {
	if got := doneRitualFlash("grove-18", 24); got != "✓ grove-18 shipped — tree #24 this season" {
		t.Errorf("plain ritual = %q", got)
	}
	for _, n := range []int{10, 30, 100} {
		if got := doneRitualFlash("grove-18", n); !strings.Contains(got, "quite the orchard") {
			t.Errorf("tree #%d should be a milestone, got %q", n, got)
		}
	}
	for _, n := range []int{1, 9, 11, 29} {
		if got := doneRitualFlash("grove-18", n); strings.Contains(got, "orchard") {
			t.Errorf("tree #%d must not be a milestone, got %q", n, got)
		}
	}
}

// --- A6 ---

func TestFirstLight(t *testing.T) {
	for hour := 0; hour < 24; hour++ {
		if busy := firstLight(hour, 3); !strings.Contains(busy, "3 trees") {
			t.Errorf("first light at hour %d lost the count: %q", hour, busy)
		}
		if rest := firstLight(hour, 0); strings.TrimSpace(rest) == "" {
			t.Errorf("first light at hour %d is blank", hour)
		}
	}
	if got := firstLight(19, 3); !strings.Contains(got, "the canopy holds for the evening") {
		t.Errorf("evening greeting = %q", got)
	}
	if !strings.Contains(firstLight(8, 2), "morning in the grove") {
		t.Errorf("morning greeting = %q", firstLight(8, 2))
	}
}

// The greeting fires exactly once, at the first SUCCESSFUL refresh, calm+ only.
func TestFirstLightGreetsOnce(t *testing.T) {
	m := fixtureModel(t) // pins nowHour to 10
	m.fx = fxCalm
	next, _ := m.Update(refreshMsg{tasks: m.localTasks, ok: true})
	got := next.(Model)
	if want := firstLight(10, len(m.localTasks)); got.flash != want {
		t.Errorf("first refresh flash = %q, want %q", got.flash, want)
	}
	// A later refresh (flash since cleared) must not greet again.
	got.flash = ""
	next, _ = got.Update(refreshMsg{tasks: m.localTasks, ok: true})
	if again := next.(Model); again.flash != "" {
		t.Errorf("second refresh re-greeted: %q", again.flash)
	}

	// A load-error zero msg (state.Load failed at launch) must neither greet
	// an empty grove that isn't, nor latch — the next good refresh greets.
	m = fixtureModel(t)
	m.fx = fxCalm
	next, _ = m.Update(refreshMsg{})
	bad := next.(Model)
	if bad.flash != "" || bad.greeted {
		t.Errorf("error refresh greeted: flash %q greeted %v", bad.flash, bad.greeted)
	}
	next, _ = bad.Update(refreshMsg{tasks: m.localTasks, ok: true})
	if got := next.(Model); got.flash == "" {
		t.Error("the first good refresh after an error one should still greet")
	}

	// off: silent, but still latched (toggling fx later must not greet).
	m = fixtureModel(t)
	m.fx = fxOff
	next, _ = m.Update(refreshMsg{tasks: m.localTasks, ok: true})
	if got := next.(Model); got.flash != "" || !got.greeted {
		t.Errorf("off greeting: flash %q greeted %v, want silent + latched", got.flash, got.greeted)
	}
}

// --- A7 ---

func TestFireflyPingPongs(t *testing.T) {
	span := 5 // small span: period 8, positions 0,1,2,3,4,3,2,1
	want := []int{0, 1, 2, 3, 4, 3, 2, 1}
	for tick := uint64(0); tick < 16; tick++ {
		if got := fireflyPos(tick, span); got != want[tick%8] {
			t.Errorf("fireflyPos(%d) = %d, want %d", tick, got, want[tick%8])
		}
	}
	// One cell per tick, no jump at either seam.
	for tick := uint64(0); tick < 100; tick++ {
		d := fireflyPos(tick+1, fireflySpan) - fireflyPos(tick, fireflySpan)
		if d < -1 || d > 1 {
			t.Errorf("firefly jumped %d cells between tick %d and %d", d, tick, tick+1)
		}
	}
	if fireflyPos(7, 1) != 0 { // degenerate span stays put
		t.Error("span 1 firefly should hold at 0")
	}
}

func TestFireflyRendersAtNightOnly(t *testing.T) {
	empty := func(fx fxLevel, tick uint64) Model {
		m := New(nil, "", "")
		m.width, m.height = 100, 30
		m.fx = fx
		m.tick = tick
		return m
	}
	pinHour(t, 23) // night bucket
	if out := empty(fxCalm, 3).View(); !strings.Contains(out, fireflyGlyph) {
		t.Errorf("night calm empty state should carry a firefly:\n%s", out)
	}
	// It drifts: two ticks render the glyph in different columns.
	if empty(fxCalm, 3).View() == empty(fxCalm, 4).View() {
		t.Error("firefly did not move between ticks")
	}
	// off: no firefly, and the render is tick-invariant (A7 leak check).
	off3, off4 := empty(fxOff, 3).View(), empty(fxOff, 4).View()
	if strings.Contains(off3, fireflyGlyph) {
		t.Errorf("off render leaked the firefly:\n%s", off3)
	}
	if off3 != off4 {
		t.Error("off empty render changed with the tick")
	}

	pinHour(t, 10) // daytime: fireflies sleep
	if out := empty(fxFull, 3).View(); strings.Contains(out, fireflyGlyph) {
		t.Errorf("daytime empty state must not carry a firefly:\n%s", out)
	}
}

// --- fx off stays byte-identical across every new render site ---

// TestOffIsStatic's sibling for joy v2: a model rich enough to trigger the
// strip, a planting, and a knock renders at off exactly as today — invariant
// to the tick and celebrations, carrying none of the new glyphs or wording.
func TestOffIsStaticJoyV2(t *testing.T) {
	base := fixtureModel(t)
	base.fx = fxOff
	base.localTasks = append(base.localTasks, &state.Task{
		Ticket: "grove-19", Repo: "grove", Agent: state.AgentWaiting, Created: time.Now(),
	})
	base.assemble()
	base.events = []state.Event{
		{Type: state.EvTaskDone, Ticket: "grove-17", Time: time.Now().Add(-time.Hour)},
		{Type: state.EvTaskDone, Ticket: "grove-18", Time: time.Now().Add(-time.Hour)},
		{Type: state.EvTaskCreated, Ticket: "grove-19", Time: time.Now().Add(-5 * time.Second)},
	}
	base.celebrations = map[string]int{knockKey("grove-19"): knockTicks}

	m0 := base
	m0.tick = 0
	r0 := m0.View()
	m1 := base
	m1.tick = 7
	if r1 := m1.View(); r0 != r1 {
		t.Errorf("off render changed with the tick — joy v2 leaked:\n%s\n---\n%s", r0, r1)
	}
	for _, leak := range []string{forestGlyph, plantGlyph, fireflyGlyph, plantText, "orchard"} {
		if strings.Contains(r0, leak) {
			t.Errorf("off render leaked joy v2 flourish %q", leak)
		}
	}
	if !strings.Contains(r0, grabbedText) {
		t.Error("off render should carry the plain grabbed feed line")
	}

	// full on the same model visibly differs and carries the new substance.
	f := base
	f.fx = fxFull
	rf := f.View()
	if rf == r0 {
		t.Error("full render identical to off — joy v2 is not rendering")
	}
	for _, want := range []string{"♠♠", plantText} {
		if !strings.Contains(rf, want) {
			t.Errorf("full render missing joy v2 flourish %q", want)
		}
	}
}
