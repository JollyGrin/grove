package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/feature"
	"github.com/JollyGrin/grove/internal/state"
)

// trainSpec is one open train for trainsModel: its cars as the slow pass
// reports them and how far its branch is behind base.
type trainSpec struct {
	slug   string
	behind int
	cars   []feature.Car
}

// trainsModel is a clock-pinned board with no tasks carrying the given
// trains, in order (grove-490).
func trainsModel(t *testing.T, w, h int, trains ...trainSpec) Model {
	t.Helper()
	pinHour(t, 10)
	now := time.Now()
	m := New(nil, "", "golden")
	m.width, m.height, m.fx = w, h, fxOff
	if len(trains) > 0 {
		m.features = map[string]*state.Feature{}
		m.featSlow = map[string]*feature.Status{}
	}
	for i, tr := range trains {
		m.features[tr.slug] = &state.Feature{Slug: tr.slug, Branch: "feature/" + tr.slug, Base: "main", Label: tr.slug,
			CreatedAt: now.Add(time.Duration(i) * time.Minute)}
		behind := tr.behind
		m.featSlow[tr.slug] = &feature.Status{Cars: tr.cars, BehindBase: &behind}
	}
	m.assemble()
	return m
}

// carsOf builds cars #base.. with the given states; prs marks which
// indexes carry an open PR.
func carsOf(base int, states []string, prs ...int) []feature.Car {
	pr := map[int]bool{}
	for _, i := range prs {
		pr[i] = true
	}
	cars := make([]feature.Car, len(states))
	for i, st := range states {
		cars[i] = feature.Car{Ticket: fmt.Sprintf("grove-%d", base+i), Number: base + i, State: st}
		if pr[i] {
			cars[i].PR = 900 + i
		}
	}
	return cars
}

func repeatState(st string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = st
	}
	return out
}

// sevenCars: 3 landed, one working car with an open PR, one working, two
// queued — ticket case (a).
func sevenCars(base int) []feature.Car {
	L, W, Q := feature.CarLanded, feature.CarWorking, feature.CarQueued
	return carsOf(base, []string{L, L, L, W, W, Q, Q}, 3)
}

// Golden TRAINS boxes (grove-490 acceptance a, b, c, e): one line per
// train, its waiting line only under a train with a car in question.
func TestTrainsBoxGolden(t *testing.T) {
	L, Q := feature.CarLanded, feature.CarQueued
	question := sevenCars(200)
	question[4].State = feature.CarQuestion
	cases := []struct {
		name   string
		trains []trainSpec
	}{
		{"a-one-train", []trainSpec{{"focus-mode", 3, sevenCars(200)}}},
		{"b-question", []trainSpec{{"focus-mode", 3, question}}},
		{"c-two-trains", []trainSpec{{"focus-mode", 3, question}, {"boot-stats", 0, carsOf(300, []string{L, Q, Q})}}},
		{"e-fourteen-cars", []trainSpec{{"keys", 1, carsOf(400, append(repeatState(L, 5), repeatState(Q, 9)...))}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := trainsModel(t, 80, 40, c.trains...)
			got := m.viewFeatures(m.featureLayout())
			path := filepath.Join("testdata", "trains-"+c.name+".golden")
			if *updateGolden {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("TRAINS box drifted:\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// (a) the row's content, (b) the question copy is today's exact one,
// (e) the bar caps at 12 cells and the count carries the rest.
func TestTrainsRowContent(t *testing.T) {
	m := trainsModel(t, 80, 40, trainSpec{"focus-mode", 3, sevenCars(200)})
	out := m.viewFeatures(m.featureLayout())
	if want := "▸focus-mode  ▰▰▰▰▱▱▱ 3/7 · ↓3 main · serve –"; !strings.Contains(out, want) {
		t.Errorf("row lacks %q:\n%s", want, out)
	}
	if strings.Contains(out, "waiting on you") {
		t.Error("no car in question: no waiting line")
	}

	q := sevenCars(200)
	q[4].State = feature.CarQuestion
	m = trainsModel(t, 80, 40, trainSpec{"focus-mode", 3, q})
	if out := m.viewFeatures(m.featureLayout()); !strings.Contains(out, "◆ #204 is waiting on you — tab to AGENTS to answer") {
		t.Errorf("question train lacks today's waiting copy:\n%s", out)
	}

	cars := carsOf(400, append(repeatState(feature.CarLanded, 5), repeatState(feature.CarQueued, 9)...))
	m = trainsModel(t, 80, 40, trainSpec{"keys", 1, cars})
	bar := m.feats[0].bar
	if n := strings.Count(bar, "▰") + strings.Count(bar, "▱"); n != maxBarCells {
		t.Errorf("14 cars draw %d cells, want %d: %q", n, maxBarCells, bar)
	}
	if !strings.Contains(m.feats[0].meta, "5/14") {
		t.Errorf("meta %q lacks 5/14", m.feats[0].meta)
	}
}

// A car with an open PR is a filled cell in its glyph colour; landed in
// the landed colour; the rest dim and empty.
func TestTrainBarColours(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(1) // termenv.ANSI256: styles emit escapes
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	cars := carsOf(1, []string{feature.CarLanded, feature.CarReady, feature.CarWorking, feature.CarQueued}, 1)
	want := carStyles[feature.CarLanded].Render("▰") + carStyles[feature.CarReady].Render("▰") +
		sDim.Render("▱") + sDim.Render("▱")
	if got := trainBar(cars); got != want {
		t.Errorf("bar = %q, want %q", got, want)
	}
	if trainBar(nil) != "" {
		t.Error("no cars: no bar")
	}
}

// (d) no open train: no TRAINS box, the frame as before (the no-feature
// goldens pin the bytes).
func TestTrainsBoxAbsentWithoutTrains(t *testing.T) {
	m := trainsModel(t, 120, 40)
	if lay := m.featureLayout(); lay.height != 0 {
		t.Errorf("no trains: layout %+v, want no panel", lay)
	}
	if strings.Contains(m.View(), "TRAINS") {
		t.Error("no trains: the box must be absent")
	}
}

// ACTIVITY gains the rows the old per-car list used: a train is one row
// (plus its waiting line), whatever its car count — the old box spent
// three rows per expanded train plus the selected train's hint.
func TestTrainsRowBudgetFeedsActivity(t *testing.T) {
	q := sevenCars(200)
	q[4].State = feature.CarQuestion
	for _, tr := range []trainSpec{
		{"focus-mode", 3, sevenCars(200)}, // no question: one row
		{"focus-mode", 3, q},              // question: row + waiting line
	} {
		m := trainsModel(t, 120, 40, tr)
		m.events = make([]state.Event, 0, 40)
		for i := 0; i < 40; i++ {
			m.events = append(m.events, state.Event{Type: state.EvTaskCreated, Ticket: fmt.Sprintf("grove-%d", i), Time: time.Now()})
		}
		feed := len(feedItems(m.events))
		lay := m.featureLayout()
		lines := 1
		if m.feats[0].waiting != "" {
			lines++
		}
		if lay.height != 3+lines {
			t.Errorf("%d-line train: panel height %d, want %d", lines, lay.height, 3+lines)
		}
		withTrain, _ := m.rowBudgets(feed, nil)
		bare := m
		bare.feats = nil
		withoutTrain, _ := bare.rowBudgets(feed, nil)
		if withoutTrain-withTrain != lay.height {
			t.Errorf("ACTIVITY pays %d rows for the box, want exactly its %d", withoutTrain-withTrain, lay.height)
		}
		oldHeight := 3 + 3 + 1 // border+title, title/rail/labels, the selected train's hint
		if gained := oldHeight - lay.height; gained < 1 {
			t.Errorf("ACTIVITY gains %d rows over the old box, want > 0", gained)
		}
	}
	// Car count no longer costs rows.
	many := trainsModel(t, 120, 40, trainSpec{"keys", 1, carsOf(400, repeatState(feature.CarQueued, 14))})
	if h := many.featureLayout().height; h != 4 {
		t.Errorf("14-car train: panel height %d, want 4", h)
	}
}

// More trains than rows: the selection scrolls into view, `+N more`
// names the rest.
func TestTrainsScrollToSelection(t *testing.T) {
	var trains []trainSpec
	for i := 0; i < 8; i++ {
		trains = append(trains, trainSpec{fmt.Sprintf("train-%d", i), 0, sevenCars(100 + 10*i)})
	}
	m := trainsModel(t, 80, 18, trains...)
	lay := m.featureLayout()
	if lay.more == 0 || lay.shown == 0 {
		t.Fatalf("short pane with 8 trains should cut: %+v", lay)
	}
	m.focus = focusFeatures
	m.featSel = 7
	out := m.viewFeatures(m.featureLayout())
	if !strings.Contains(out, "train-7") || !strings.Contains(out, "more") {
		t.Errorf("selected train-7 must show, with +N more:\n%s", out)
	}
}
