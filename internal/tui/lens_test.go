package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/feature"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/serve"
)

// lensModel is featureModel(1) with FEATURES focused and the lens open on
// train-0: #100 landed, #101 on the board (a live question), #102 queued.
func lensModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := featureModel(t, 1, w, h)
	m, _ = mustModel(m.handleKey(fkey("tab")))
	m, _ = mustModel(m.handleKey(tea.KeyMsg{Type: tea.KeyEnter}))
	if m.mode != modeLens || m.lensSlug != "train-0" {
		t.Fatalf("enter on a focused feature: mode=%d lens=%q, want the lens on train-0", m.mode, m.lensSlug)
	}
	return m
}

func TestLensRendersFourSections(t *testing.T) {
	m := lensModel(t, 120, 59)
	out := m.View()
	for _, want := range []string{
		"TRAIN", "train-0", "feature/train-0 ▷ main  1/3 landed",
		"#100 landed", "#101 question", "#102 queued", "$1.50", // est per car
		"BRANCH", "base     2 behind main", "PR       none yet", "closes   #100",
		"SERVE", "no run.sh",
		"NEXT", "1. answer #101", "grab #102", "rebase feature/train-0 on main — 2 behind",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lens lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "AGENTS") {
		t.Error("the lens is full-screen: no AGENTS panel")
	}
}

// A landed car renders whole in the dim style.
func TestLensLandedRowsDim(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(1) // termenv.ANSI256: styles emit escapes
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := lensModel(t, 120, 59)
	d := m.feats[0].lens
	landed := lensCarRow(d.cars[0], d.labelW, false, 116)
	if want := sDim.Render(trunc("  "+carGlyphs[feature.CarLanded]+" "+pad("#100", d.labelW)+pad("landed", lensStateW)+pad("$1.50", lensEstW), 116)); landed != want {
		t.Errorf("landed row not dim:\n got %q\nwant %q", landed, want)
	}
	if working := lensCarRow(d.cars[1], d.labelW, false, 116); strings.HasPrefix(working, sDim.Render("  ")) {
		t.Errorf("a live car must not be dim: %q", working)
	}
	// In the frame the cursor sits on #100 — still dim, cursor and all.
	if sel := lensCarRow(d.cars[0], d.labelW, true, 116); !strings.Contains(m.View(), sel) {
		t.Errorf("the frame does not carry the dim landed row %q", sel)
	}
}

func TestLensFits(t *testing.T) {
	for _, sz := range [][2]int{{120, 59}, {80, 24}, {40, 20}, {60, 12}} {
		m := lensModel(t, sz[0], sz[1])
		lines := frameLines(m.View())
		if len(lines) > sz[1] {
			t.Errorf("%dx%d: lens is %d rows", sz[0], sz[1], len(lines))
		}
		for i, l := range lines {
			if lw := lipgloss.Width(l); lw > sz[0] {
				t.Errorf("%dx%d: line %d is %d cells: %q", sz[0], sz[1], i, lw, l)
			}
		}
	}
	// At 59 rows nothing is shed: every section and every car shows.
	out := lensModel(t, 120, 59).View()
	for _, want := range []string{"#100", "#101", "#102", "SERVE", "NEXT"} {
		if !strings.Contains(out, want) {
			t.Errorf("59 rows lacks %q", want)
		}
	}
}

func TestLensEscRestoresList(t *testing.T) {
	m := lensModel(t, 120, 40)
	m, _ = mustModel(m.handleKey(tea.KeyMsg{Type: tea.KeyEsc}))
	if m.mode != modeList || m.lensSlug != "" {
		t.Fatalf("esc: mode=%d lens=%q, want the list", m.mode, m.lensSlug)
	}
	if !strings.Contains(m.View(), "AGENTS") {
		t.Error("esc should restore the list frame")
	}
}

// Row keys on the selected car act as in AGENTS; modals they open return
// to the lens. A landed or queued car has no task.
func TestLensRowKeys(t *testing.T) {
	m := lensModel(t, 120, 40)
	m, _ = mustModel(m.handleKey(fkey("d"))) // cursor on #100, landed
	if m.mode != modeLens || !strings.Contains(m.flash, "no task") {
		t.Fatalf("d on a landed car: mode=%d flash=%q", m.mode, m.flash)
	}
	m, _ = mustModel(m.handleKey(fkey("j")))
	m, _ = mustModel(m.handleKey(fkey("d")))
	if m.mode != modeConfirmDone || m.detail == nil || m.detail.Ticket != "grove-101" {
		t.Fatalf("d on #101 should open done for grove-101 (mode=%d)", m.mode)
	}
	if m.focus != focusFeatures {
		t.Error("acting on a car must not move panel focus")
	}
	m, _ = mustModel(m.handleKey(fkey("x"))) // cancel
	if m.mode != modeLens {
		t.Errorf("cancelled done returns to the lens, mode=%d", m.mode)
	}
	m, _ = mustModel(m.handleKey(tea.KeyMsg{Type: tea.KeyEnter}))
	if m.mode != modeDetail {
		t.Fatalf("enter on #101 opens the reply, mode=%d", m.mode)
	}
	m, _ = mustModel(m.handleKey(tea.KeyMsg{Type: tea.KeyEsc}))
	if m.mode != modeLens {
		t.Errorf("esc from the reply returns to the lens, mode=%d", m.mode)
	}
}

func TestLensFeaturePRKey(t *testing.T) {
	m := lensModel(t, 120, 40)
	m, _ = mustModel(m.handleKey(fkey("m")))
	if !strings.Contains(m.flash, "no feature PR") {
		t.Errorf("m without a feature PR: flash %q", m.flash)
	}
}

// A closed feature drops its lens back to the list on the next refresh.
func TestLensClosesWithFeature(t *testing.T) {
	m := lensModel(t, 120, 40)
	nm, _ := m.Update(refreshMsg{ok: true})
	if got := nm.(Model); got.mode != modeList || got.lensSlug != "" {
		t.Errorf("closed feature: mode=%d lens=%q", got.mode, got.lensSlug)
	}
}

func TestNextActions(t *testing.T) {
	two, zero := 2, 0
	no := false
	cars := func(cs ...feature.Car) []feature.Car { return cs }
	cases := []struct {
		name string
		in   nextInput
		want []string
	}{
		{"empty", nextInput{}, nil},
		{"order: answer, land, review, grab, rebase", nextInput{
			Cars: cars(
				feature.Car{Ticket: "g-1", Number: 1, State: feature.CarLanded},
				feature.Car{Ticket: "g-2", Number: 2, State: feature.CarReady},
				feature.Car{Ticket: "g-3", Number: 3, State: feature.CarReady},
				feature.Car{Ticket: "g-4", Number: 4, State: feature.CarQuestion},
				feature.Car{Ticket: "g-5", Number: 5, State: feature.CarQueued, After: []int{1}},
			),
			Merged: map[string]bool{"g-2": true}, Behind: &two, Branch: "feature/x", Base: "main"},
			[]string{
				"answer #4 — it is waiting on you",
				"land 1 merged car(s) — l",
				"review #3 — merge its PR into feature/x",
				"grab #5 — after #1 landed",
				"rebase feature/x on main — 2 behind",
			}},
		{"queued blocked by an unlanded car on the train", nextInput{Cars: cars(
			feature.Car{Ticket: "g-1", Number: 1, State: feature.CarWorking},
			feature.Car{Ticket: "g-2", Number: 2, State: feature.CarQueued, After: []int{1}},
			feature.Car{Ticket: "g-3", Number: 3, State: feature.CarQueued, After: []int{99}}, // off-train
			feature.Car{Ticket: "g-4", Number: 4, State: feature.CarQueued},
		), Behind: &zero},
			[]string{"grab #3 — after #99 landed", "grab #4"}},
		{"conflicts", nextInput{Cars: cars(feature.Car{Ticket: "g-1", State: feature.CarWorking}),
			Mergeable: &no, Branch: "feature/x", Base: "main"},
			[]string{"resolve feature/x's conflicts with main"}},
		{"all landed, no PR", nextInput{Cars: cars(feature.Car{Ticket: "g-1", Number: 1, State: feature.CarLanded}),
			Branch: "feature/x", Base: "main"},
			[]string{"open the feature PR feature/x → main"}},
		{"all landed, PR open", nextInput{Cars: cars(feature.Car{Ticket: "g-1", Number: 1, State: feature.CarLanded}),
			PR: &feature.FeaturePR{Number: 9, State: "OPEN"}, Branch: "feature/x", Base: "main"},
			[]string{"merge feature PR #9 into main — m opens it"}},
		{"all landed, PR merged", nextInput{Cars: cars(feature.Car{Ticket: "g-1", Number: 1, State: feature.CarLanded}),
			PR: &feature.FeaturePR{Number: 9, State: "MERGED"}}, nil},
	}
	for _, c := range cases {
		got := nextActions(c.in)
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestServeLine(t *testing.T) {
	cases := []struct {
		st   *serve.Status
		want string
	}{
		{nil, "no run.sh"},
		{&serve.Status{State: serve.StateNone}, "no run.sh"},
		{&serve.Status{State: serve.StateUntrusted}, "run.sh changed — review it before it runs"},
		{&serve.Status{State: serve.StateStopped, Port: 4100}, "stopped (last :4100)"},
		{&serve.Status{State: serve.StateRunning, Port: 4100, URL: "http://localhost:4100", Behind: true},
			"running :4100  http://localhost:4100  · behind the branch tip"},
	}
	for _, c := range cases {
		if got := serveLine(c.st); got != c.want {
			t.Errorf("serveLine(%+v) = %q, want %q", c.st, got, c.want)
		}
	}
}

// l opens the land plan as a modal: merged cars land, the rest are
// listed as skipped; cancel runs nothing; y runs the land path once.
func TestLandModal(t *testing.T) {
	calls := 0
	var ran feature.LandPlan
	prev := landFn
	landFn = func(p feature.LandPlan, _ feature.Finisher) feature.LandResult {
		calls++
		ran = p
		return feature.LandResult{Landed: []int{101}}
	}
	t.Cleanup(func() { landFn = prev })

	open := func(t *testing.T) Model {
		m := featureModel(t, 1, 120, 40)
		m.prs = map[string]*github.PR{"grove-101": {Number: 45, State: "MERGED"}}
		m.assemble()
		m, _ = mustModel(m.handleKey(fkey("tab")))
		m, _ = mustModel(m.handleKey(fkey("l")))
		if m.mode != modeConfirmLand {
			t.Fatalf("l on a focused feature: mode=%d, want the land modal", m.mode)
		}
		return m
	}

	m := open(t)
	out := m.View()
	for _, want := range []string{"LAND train-0", "land  grove-101", "PR #45 merged", "skip  grove-102", "queued", "land 1 car(s)?"} {
		if !strings.Contains(out, want) {
			t.Errorf("land modal lacks %q:\n%s", want, out)
		}
	}
	if lines := frameLines(out); len(lines) > 40 {
		t.Errorf("land modal is %d rows", len(lines))
	}

	m, cmd := mustModel(m.handleKey(fkey("n")))
	if cmd != nil || calls != 0 || m.mode != modeList {
		t.Fatalf("cancel: cmd=%v calls=%d mode=%d — must run nothing", cmd != nil, calls, m.mode)
	}

	m = open(t)
	m, cmd = mustModel(m.handleKey(fkey("y")))
	if cmd == nil {
		t.Fatal("confirm returned no command")
	}
	msg := cmd()
	if calls != 1 || len(ran.Land) != 1 || ran.Land[0].Ticket != "grove-101" {
		t.Fatalf("confirm: land path ran %d time(s) with %+v", calls, ran.Land)
	}
	nm, _ := m.Update(msg)
	if f := nm.(Model).flash; !strings.Contains(f, "landed: #101") {
		t.Errorf("flash after land: %q", f)
	}

	// From the lens, l opens the same modal and returns to the lens.
	m = lensModel(t, 120, 40)
	m, _ = mustModel(m.handleKey(fkey("l")))
	if m.mode != modeConfirmLand {
		t.Fatalf("l in the lens: mode=%d", m.mode)
	}
	m, _ = mustModel(m.handleKey(tea.KeyMsg{Type: tea.KeyEsc}))
	if m.mode != modeLens || calls != 1 {
		t.Errorf("cancel from the lens returns to it without landing (mode=%d calls=%d)", m.mode, calls)
	}
}

func TestHelpListsFeatureKeys(t *testing.T) {
	m := New(nil, "", "")
	m.width, m.height, m.mode = 120, 59, modeHelp
	out := m.View()
	for _, want := range []string{"FEATURES", "lens:", "back from the lens", "open the feature PR", "land:"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}
