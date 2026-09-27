package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/feature"
	"github.com/JollyGrin/grove/internal/serve"
	"github.com/JollyGrin/grove/internal/state"
)

// Trellis (grove-379): a feature's trees stand together under a bracket
// labelled `<slug> landed/total`; queued cars are seeds.

// trellisFixture: feature gv-keys has a landed-and-done car (#361, in the
// orchard's events), a live working car (#364) and a queued car (#366);
// feature pi has a queued car (#370) and a live car (#371); #359 is loose.
func trellisFixture() ([]*state.Task, []state.Event, []featRow) {
	now := time.Now()
	tasks := []*state.Task{
		{Ticket: "grove-359", Agent: state.AgentWorking, Created: now.Add(-3 * time.Minute)},
		{Ticket: "grove-364", Agent: state.AgentWorking, Feature: "gv-keys", Created: now.Add(-time.Hour)},
		{Ticket: "grove-371", Agent: state.AgentWorking, Feature: "pi", Created: now.Add(-9 * time.Minute)},
	}
	events := []state.Event{{Type: state.EvTaskDone, Ticket: "grove-361"}}
	feats := []featRow{
		{slug: "gv-keys", trellis: "gv-keys 1/3", cars: []carCell{
			{ticket: "grove-361", state: feature.CarLanded, label: "#361"},
			{ticket: "grove-364", state: feature.CarWorking, label: "#364"},
			{ticket: "grove-366", state: feature.CarQueued, label: "#366"},
		}},
		{slug: "pi", trellis: "pi 0/2", cars: []carCell{
			{ticket: "grove-371", state: feature.CarWorking, label: "#371"},
			{ticket: "grove-370", state: feature.CarQueued, label: "#370"},
		}},
	}
	return tasks, events, feats
}

// rowWith returns the index of the first line containing sub, or -1.
func rowWith(lines []string, sub string) int {
	for i, l := range lines {
		if strings.Contains(l, sub) {
			return i
		}
	}
	return -1
}

// runeAt returns the rune in cell x of a plain (unstyled) line.
func runeAt(line string, x int) rune {
	r := []rune(line)
	if x < 0 || x >= len(r) {
		return ' '
	}
	return r[x]
}

func TestTrellisOneFeature(t *testing.T) {
	tasks, events, feats := trellisFixture()
	feats = feats[:1]
	const width, rows = 80, 9
	lines := sceneLines(tasks, nil, events, latestAnswered(events), nil, 0, width, rows, 10, fxCalm, "", feats)

	br := rowWith(lines, "gv-keys 1/3")
	if br < 0 {
		t.Fatalf("no bracket labelled `gv-keys 1/3`:\n%s", strings.Join(lines, "\n"))
	}
	if br >= rows-5 { // above all three plant rows, the soil and the label row
		t.Errorf("bracket must sit above the trees, got row %d:\n%s", br, strings.Join(lines, "\n"))
	}
	// The group is the first three plots (the done car left the orchard for
	// its trellis): the bracket spans cells 1..3*plotW-2, and the loose
	// plot after it has none.
	bracket := lines[br]
	if runeAt(bracket, 1) != trellisBar || runeAt(bracket, 3*plotW-2) != trellisBar {
		t.Errorf("bracket should span cells 1..%d, got %q", 3*plotW-2, bracket)
	}
	for x := 3*plotW - 1; x < 4*plotW; x++ {
		if r := runeAt(bracket, x); r != ' ' {
			t.Errorf("loose plot must stand outside the bracket, cell %d = %q", x, r)
		}
	}

	label := lines[rows-1]
	if strings.Count(label, "#361") != 1 {
		t.Errorf("the landed car stands once — in its trellis, not also the orchard: %q", label)
	}
	i361, i364, i366, i359 := strings.Index(label, "#361"), strings.Index(label, "#364"), strings.Index(label, "#366"), strings.Index(label, "#359")
	if !(i361 >= 0 && i361 < i364 && i364 < i366 && i366 < i359) {
		t.Errorf("group should read #361 #364 #366 in rail order, then the loose #359: %q", label)
	}
}

func TestTrellisTwoFeatures(t *testing.T) {
	tasks, events, feats := trellisFixture()
	const width, rows = 80, 9
	lines := sceneLines(tasks, nil, events, latestAnswered(events), nil, 0, width, rows, 10, fxFull, "", feats)

	br := rowWith(lines, "gv-keys 1/3")
	if br < 0 || !strings.Contains(lines[br], "pi 0/2") {
		t.Fatalf("both brackets belong on one row:\n%s", strings.Join(lines, "\n"))
	}
	// gv-keys takes plots 0-2, pi plots 3-4: the two rules never touch.
	bracket := lines[br]
	if runeAt(bracket, 3*plotW-1) != ' ' || runeAt(bracket, 3*plotW) != ' ' {
		t.Errorf("two features must read as two brackets (a gap at cells %d-%d): %q", 3*plotW-1, 3*plotW, bracket)
	}
	if runeAt(bracket, 3*plotW+1) != trellisBar || runeAt(bracket, 5*plotW-2) != trellisBar {
		t.Errorf("pi's bracket should span cells %d..%d: %q", 3*plotW+1, 5*plotW-2, bracket)
	}
	label := lines[rows-1]
	iPi371, iPi370, i359 := strings.Index(label, "#371"), strings.Index(label, "#370"), strings.Index(label, "#359")
	if !(strings.Index(label, "#366") < iPi371 && iPi371 < iPi370 && iPi370 < i359) {
		t.Errorf("features in rail order, each contiguous, loose tasks last: %q", label)
	}
}

func TestTrellisQueuedCarsAreSeeds(t *testing.T) {
	_, _, feats := trellisFixture()
	const width, rows = 80, 9
	// No tasks and no orchard at all: the queued cars still plant seeds.
	lines := sceneLines(nil, nil, nil, nil, nil, 0, width, rows, 10, fxCalm, "", feats)
	ground := lines[rows-3]
	if got := strings.Count(ground, seedGlyph); got != 2 {
		t.Errorf("two queued cars should be two seeds on the ground row, got %d: %q", got, ground)
	}
	// #366 is gv-keys's only drawable car (#361 landed-but-untracked is a
	// ♠, #364 has no live task here): the seed sits inside its cell.
	cell := []rune(ground)[plotW : 2*plotW]
	if !strings.Contains(string(cell), seedGlyph) {
		t.Errorf("#366's seed should sit in plot 1, got %q", string(cell))
	}
	for _, l := range []string{"#366", "#370"} {
		if !strings.Contains(lines[rows-1], l) {
			t.Errorf("queued car %s should be labelled: %q", l, lines[rows-1])
		}
	}
}

func TestTrellisFxOffByteIdentical(t *testing.T) {
	tasks, events, feats := trellisFixture()
	for _, rows := range []int{3, 6, 9} {
		with := strings.Join(sceneLines(tasks, nil, events, latestAnswered(events), nil, 0, 80, rows, 10, fxOff, "", feats), "\n")
		without := strings.Join(sceneLines(tasks, nil, events, latestAnswered(events), nil, 0, 80, rows, 10, fxOff, "", nil), "\n")
		if with != without {
			t.Errorf("rows=%d: fx=off must not draw the trellis", rows)
		}
	}
	m := New(nil, "", "")
	m.width, m.height, m.fx = 120, 40, fxOff
	m.localTasks = tasks
	m.assemble()
	m.feats = feats
	if _, sceneRows := m.rowBudgets(0, nil); sceneRows != 0 {
		t.Errorf("fx=off yields no scene rows with features present, got %d", sceneRows)
	}
}

// With no features the scene is byte-identical at every fx level: the
// empty feats list takes no trellis branch (the testdata goldens pin the
// pre-trellis frames themselves).
func TestTrellisNoFeaturesByteIdentical(t *testing.T) {
	tasks, events, _ := trellisFixture()
	for _, fx := range []fxLevel{fxOff, fxCalm, fxFull} {
		for _, rows := range []int{3, 4, 6, 9, 12} {
			a := strings.Join(sceneLines(tasks, nil, events, latestAnswered(events), nil, 2, 80, rows, 10, fx, "", nil), "\n")
			b := strings.Join(sceneLines(tasks, nil, events, latestAnswered(events), nil, 2, 80, rows, 10, fx, "", []featRow{}), "\n")
			if a != b {
				t.Errorf("fx=%v rows=%d: an empty feature list changed the scene", fx, rows)
			}
		}
	}
}

func TestTrellisGlyphsAreLocked(t *testing.T) {
	locked := map[rune]bool{' ': true}
	for _, g := range sceneLockedGlyphs {
		for _, r := range g {
			locked[r] = true
		}
	}
	for _, g := range []string{seedGlyph, string(trellisBar)} {
		if !locked[[]rune(g)[0]] {
			t.Errorf("trellis glyph %q is outside the locked set", g)
		}
	}
	// Every rune the trellis draws — the sky rows it lives in and the
	// ground row its seeds sit on — is a locked glyph or label text.
	tasks, events, feats := trellisFixture()
	text := map[rune]bool{}
	for _, f := range feats {
		for _, r := range f.trellis {
			text[r] = true
		}
	}
	for _, width := range []int{24, 40, 80, 121} {
		for _, rows := range []int{4, 5, 6, 9, 12} {
			for _, hour := range []int{2, 8, 14, 19} {
				lines := sceneLines(tasks, nil, events, latestAnswered(events), nil, 3, width, rows, hour, fxFull, "", feats)
				plant := plantRowsFor(sceneTierFor(rows))
				drawn := append(append([]string(nil), lines[:rows-2-plant]...), lines[rows-3])
				for _, l := range drawn {
					for _, r := range l {
						if !locked[r] && !text[r] {
							t.Errorf("w=%d rows=%d h=%d: trellis row emitted %q, outside the locked set: %q", width, rows, hour, r, l)
						}
					}
				}
			}
		}
	}
}

// The trellis keeps the scene's contract: exactly rows lines, none wider
// than the width, at the existing scene test sizes.
func TestTrellisRespectsWidthAndHeight(t *testing.T) {
	tasks, events, feats := trellisFixture()
	for _, width := range []int{20, 24, 28, 32, 36, 40, 55, 80, 121} {
		for _, rows := range []int{3, 4, 5, 6, 7, 8, 9, 12, 20} {
			for _, hour := range []int{2, 8, 14, 19} {
				for _, fx := range []fxLevel{fxCalm, fxFull} {
					lines := sceneLines(tasks, nil, events, latestAnswered(events), nil, 5, width, rows, hour, fx, "grove-364", feats)
					if len(lines) != rows {
						t.Fatalf("w=%d rows=%d: got %d lines", width, rows, len(lines))
					}
					for i, l := range lines {
						if w := lipgloss.Width(l); w > width {
							t.Errorf("w=%d rows=%d h=%d fx=%v: line %d is %d cells wide: %q", width, rows, hour, fx, i, w, l)
						}
					}
				}
			}
		}
	}
}

// The bracket's label degrades with its span: whole, then the tally, then
// a bare rule — never a truncation ellipsis (outside the locked set).
func TestPaintBracketDegrades(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{16, "▁ gv-keys 1/3 ▁▁"},
		{8, "▁ 1/3 ▁▁"},
		{5, "▁▁▁▁▁"},
	}
	for _, c := range cases {
		g := newSceneGrid(c.n)
		paintBracket(g, 0, c.n-1, "gv-keys 1/3")
		if got := string(g.chars); got != c.want {
			t.Errorf("n=%d: got %q, want %q", c.n, got, c.want)
		}
	}
}

// Sharing a row with the ambient sky (2 sky rows: compact tier), the
// bracket's label gaps stay blank — morning dew never lands in them.
func TestTrellisOutranksAmbient(t *testing.T) {
	tasks, events, feats := trellisFixture()
	for _, rows := range []int{4, 6} {
		lines := sceneLines(tasks, nil, events, latestAnswered(events), nil, 0, 80, rows, 8, fxCalm, "", feats)
		if rowWith(lines, "▁ gv-keys 1/3 ▁") < 0 || rowWith(lines, "▁ pi 0/2 ▁") < 0 {
			t.Errorf("rows=%d: the ambient sky crept into the bracket label:\n%s", rows, strings.Join(lines, "\n"))
		}
	}
}

// assemble's featRow carries the trellis label and each car's ticket, so
// the scene formats nothing per frame.
func TestBuildFeatRowTrellis(t *testing.T) {
	f := &state.Feature{Slug: "gv-keys", Base: "main"}
	st := &feature.Status{Landed: 1, Total: 3, Cars: []feature.Car{
		{Ticket: "grove-361", Number: 361, State: feature.CarLanded},
		{Ticket: "grove-364", Number: 364, State: feature.CarWorking},
		{Ticket: "grove-366", Number: 366, State: feature.CarQueued},
	}}
	r := buildFeatRow(f, st, "", nil, serve.Status{}, false)
	if r.trellis != "gv-keys 1/3" {
		t.Errorf("trellis = %q, want %q", r.trellis, "gv-keys 1/3")
	}
	if r.cars[2].ticket != "grove-366" || r.cars[2].label != "#366" {
		t.Errorf("car cell = %+v, want ticket grove-366 labelled #366", r.cars[2])
	}
}
