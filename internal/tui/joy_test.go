package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/resource"
	"github.com/JollyGrin/grove/internal/state"
)

// Every glyph the flourish introduces must occupy exactly one terminal cell —
// pad/trunc do plain rune-cell math and a double-width glyph desyncs every
// column after it (the battle-scar in viewAgents). Trap #2.
func TestNewGlyphsAreSingleCell(t *testing.T) {
	glyphs := []string{"●", "✦", "⬢", "☼", "☁", "⛆"}
	for _, g := range glyphs {
		if w := lipgloss.Width(g); w != 1 {
			t.Errorf("glyph %q width = %d, want 1", g, w)
		}
	}
}

func TestBreathPingPongs(t *testing.T) {
	// grove-24: the breath is now a COLOR ramp on a stable ●, not a shape swap.
	// The working glyph must never change shape.
	if statusGlyph("working") != "●" {
		t.Fatalf("working glyph = %q, want ●", statusGlyph("working"))
	}
	// One step per tick, ping-ponged dim→bright→dim with no hard seam. For a
	// 5-shade ramp the period is 8 ticks: 0,1,2,3,4,3,2,1 then repeat.
	want := []int{0, 1, 2, 3, 4, 3, 2, 1}
	for tick := uint64(0); tick < 16; tick++ {
		if got := breathPhase(tick); got != want[tick%8] {
			t.Errorf("breathPhase(%d) = %d, want %d", tick, got, want[tick%8])
		}
	}
	// Adjacent phases differ by at most one shade — the brightness change is
	// barely perceptible frame-to-frame (no jump, including across the seam).
	for tick := uint64(0); tick < 16; tick++ {
		if d := breathPhase(tick+1) - breathPhase(tick); d < -1 || d > 1 {
			t.Errorf("breath jumped %d shades between tick %d and %d", d, tick, tick+1)
		}
	}
	// Every shade has a precomputed style (built once, no per-frame alloc).
	if len(breathStyles) != len(breathShades) {
		t.Errorf("breathStyles has %d entries, want %d", len(breathStyles), len(breathShades))
	}
}

func TestVerbForDeterministicAndSlow(t *testing.T) {
	// Same ticket + same tick → same verb (deterministic, no clock in the fn).
	if verbFor("grove-18", 40) != verbFor("grove-18", 40) {
		t.Fatal("verbFor is not deterministic")
	}
	// Stable across a whole 8-tick window (changes ~every 8s, never strobes).
	base := verbFor("grove-18", 16)
	for tick := uint64(16); tick < 24; tick++ {
		if verbFor("grove-18", tick) != base {
			t.Errorf("verb changed mid-window at tick %d (should hold 8s)", tick)
		}
	}
	// The next window advances the verb.
	if verbFor("grove-18", 24) == base {
		t.Error("verb did not advance after an 8-tick window")
	}
	// grove-24: the verb now lives in the prominent 11-cell STATUS column and
	// must fit there WITHOUT truncation (no trailing …).
	for _, v := range groveVerbs {
		if w := lipgloss.Width(v); w > 11 {
			t.Errorf("verb %q is %d cells, too wide for the 11-cell STATUS column", v, w)
		}
	}
}

func TestWeatherGlyph(t *testing.T) {
	cases := []struct {
		lvl  resource.Level
		want string
	}{
		{resource.Green, "☼"},
		{resource.Amber, "☁"},
		{resource.Red, "⛆"},
	}
	for _, c := range cases {
		if got := weatherGlyph(c.lvl); got != c.want {
			t.Errorf("weatherGlyph(%v) = %q, want %q", c.lvl, got, c.want)
		}
	}
}

func TestTimeOfDayAndEmptyLines(t *testing.T) {
	buckets := map[int]int{3: 3, 6: 0, 14: 1, 19: 2, 23: 3, 0: 3}
	for hour, want := range buckets {
		if got := timeOfDay(hour); got != want {
			t.Errorf("timeOfDay(%d) = %d, want %d", hour, got, want)
		}
	}
	// Both empty-state families cover all four buckets and keep the actionable
	// hint (agents) / stay non-empty (activity).
	for hour := 0; hour < 24; hour++ {
		if !strings.Contains(emptyAgentsLine(hour), "gv grab") {
			t.Errorf("agents empty line for hour %d dropped the grab hint: %q", hour, emptyAgentsLine(hour))
		}
		if strings.TrimSpace(emptyActivityLine(hour)) == "" {
			t.Errorf("activity empty line for hour %d is blank", hour)
		}
	}
}

func TestParseCycleAndLabelFx(t *testing.T) {
	cases := map[string]fxLevel{"off": fxOff, "calm": fxCalm, "full": fxFull, "": fxFull, "nonsense": fxFull}
	for in, want := range cases {
		if got := parseFx(in); got != want {
			t.Errorf("parseFx(%q) = %v, want %v", in, got, want)
		}
	}
	// The runtime toggle cycles full → calm → off → full.
	seq := []fxLevel{fxFull, fxCalm, fxOff, fxFull}
	for i := 0; i < len(seq)-1; i++ {
		if got := cycleFx(seq[i]); got != seq[i+1] {
			t.Errorf("cycleFx(%v) = %v, want %v", seq[i], got, seq[i+1])
		}
	}
	for _, fx := range []fxLevel{fxOff, fxCalm, fxFull} {
		if parseFx(fxLabel(fx)) != fx {
			t.Errorf("fxLabel/parseFx not a round-trip for %v (label %q)", fx, fxLabel(fx))
		}
	}
}

// New must honor the config knob, and default (nil cfg or empty value) to full.
func TestNewRespectsConfigEffects(t *testing.T) {
	if New(nil, "", "").fx != fxFull {
		t.Error("nil cfg should default to full")
	}
	cases := map[string]fxLevel{"": fxFull, "off": fxOff, "calm": fxCalm, "full": fxFull}
	for val, want := range cases {
		cfg := &config.Config{}
		cfg.Cockpit.Effects = val
		if got := New(cfg, "", "").fx; got != want {
			t.Errorf("config effects %q → fx %v, want %v", val, got, want)
		}
	}
}

func TestDecayCelebrations(t *testing.T) {
	c := map[string]int{"a": 1, "b": 3}
	decayCelebrations(c)
	if _, ok := c["a"]; ok {
		t.Error("entry at 1 should be deleted after decay")
	}
	if c["b"] != 2 {
		t.Errorf("b = %d, want 2", c["b"])
	}
	decayCelebrations(nil) // nil-safe
}

func TestCountDone(t *testing.T) {
	events := []state.Event{
		{Type: state.EvTaskDone}, {Type: state.EvTaskCreated}, {Type: state.EvTaskDone},
	}
	if n := countDone(events); n != 2 {
		t.Errorf("countDone = %d, want 2", n)
	}
}

// fixtureModel builds a deterministic list-mode Model with one working agent
// and a merged PR, for the render-level assertions below. It pins the render
// clock to daytime — night renders a firefly (A7, grove-56) into empty
// panels, which would make these assertions flap with the wall clock.
func fixtureModel() Model {
	nowHour = func() int { return 10 }
	m := New(nil, "", "")
	m.width, m.height = 120, 40
	m.tasks = []*state.Task{{Ticket: "grove-18", Title: "joy", Repo: "grove", Agent: state.AgentWorking, Created: time.Now()}}
	m.live = map[string]string{"grove-18": "working"}
	m.prs = map[string]*github.PR{"grove-18": {Number: 18, State: "MERGED", CI: "pass"}}
	m.mem = resource.Mem{AvailBytes: 8 << 30, TotalBytes: 16 << 30}
	m.workers = 2
	return m
}

// Trap #4 — the core guarantee: at `off`, the render is exactly today's output.
// We prove it without a time-fragile golden by asserting the off render is
// INVARIANT to the two things flourish reacts to (the tick and the
// celebrations map) and carries none of the new glyphs/verbs — while `full`
// visibly differs. If any flourish leaked into off, one of these would move.
func TestOffIsStatic(t *testing.T) {
	base := fixtureModel()
	base.fx = fxOff

	m0 := base
	m0.tick = 0
	r0 := m0.View()

	m1 := base
	m1.tick = 7 // a different breath/verb phase
	r1 := m1.View()

	if r0 != r1 {
		t.Errorf("off render changed with the tick — flourish leaked:\n%s\n---\n%s", r0, r1)
	}

	// A populated celebrations map must not sparkle at off either.
	mc := base
	mc.tick = 0
	mc.celebrations = map[string]int{"grove-18": celebrationTicks}
	if mc.View() != r0 {
		t.Error("off render changed when celebrations populated — sparkle leaked")
	}

	// The static substance is present, the flourish is absent.
	if !strings.Contains(r0, "working") {
		t.Error("off STATUS column should show the literal 'working'")
	}
	for _, leak := range []string{"◉", "◎", "✦", "☼", "☁", "⛆", "dew on the leaves"} {
		if strings.Contains(r0, leak) {
			t.Errorf("off render leaked flourish %q", leak)
		}
	}
	// No grove verb may replace the literal status at off.
	for _, v := range groveVerbs {
		if strings.Contains(r0, v) {
			t.Errorf("off render leaked grove verb %q", v)
		}
	}

	// full must actually differ (breathing + verb + weather + sparkle).
	full := fixtureModel()
	full.fx = fxFull
	full.tick = 0
	full.celebrations = map[string]int{"grove-18": celebrationTicks}
	rf := full.View()
	if rf == r0 {
		t.Error("full render is identical to off — flourish is not rendering")
	}
	// The breath is a color-only change on ● (untestable via plain-text
	// Contains — colors are stripped without a TTY), so it's covered by
	// TestBreathPingPongs; here we assert the content-bearing flourishes.
	for _, want := range []string{"☼" /*weather*/, "✦" /*sparkle*/} {
		if !strings.Contains(rf, want) {
			t.Errorf("full render missing expected flourish %q", want)
		}
	}
	// A2: the working verb replaces the literal in the prominent STATUS column.
	if !strings.Contains(rf, verbFor("grove-18", 0)) {
		t.Errorf("full render should show grove verb %q", verbFor("grove-18", 0))
	}
}

// calm shows ambient (A1–A4) but never a celebration sparkle (J1/J2 are fxFull).
func TestCalmHasAmbientNotCelebration(t *testing.T) {
	m := fixtureModel()
	m.fx = fxCalm
	m.tick = 0
	m.celebrations = map[string]int{"grove-18": celebrationTicks}
	out := m.View()
	if !strings.Contains(out, "☼") {
		t.Error("calm should render the weather glyph (ambient A3)")
	}
	if !strings.Contains(out, verbFor("grove-18", 0)) {
		t.Error("calm should render the grove verb (ambient A2)")
	}
	if strings.Contains(out, "✦") {
		t.Error("calm must NOT render the merge sparkle (celebration is fxFull only)")
	}
}
