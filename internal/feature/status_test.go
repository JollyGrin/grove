package feature

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JollyGrin/grove/internal/git"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/ledger"
	"github.com/JollyGrin/grove/internal/provider"
	"github.com/JollyGrin/grove/internal/state"
)

func ev(h int, typ, ticket string, data map[string]string) state.Event {
	return state.Event{Time: t1(h), Type: typ, Ticket: ticket, Data: data}
}

// foldAll folds events the way the real store does, so the task shapes the
// status function sees are real ones.
func foldAll(t *testing.T, events []state.Event) map[string]*state.Task {
	t.Helper()
	dir := t.TempDir()
	for _, e := range events {
		if err := state.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := state.Peek(dir)
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

func onTrain(title string) map[string]string {
	return map[string]string{"title": title, "feature": "trains", "base": "feature/trains"}
}

func trainsOnly() map[string]*state.Feature {
	return map[string]*state.Feature{
		"trains": {Slug: "trains", Repo: "grove", Branch: "feature/trains", Base: "main", Label: "trains", CreatedAt: t1(0)},
		"old":    {Slug: "old", Repo: "grove", Branch: "feature/old", Base: "main", Label: "old", Closed: true},
	}
}

func tickets(cars []Car) []string {
	out := make([]string, len(cars))
	for i, c := range cars {
		out[i] = c.Ticket + ":" + c.State
	}
	return out
}

func TestStatusesOrderingAcrossGroups(t *testing.T) {
	events := []state.Event{
		// grove-20 lands second, grove-21 lands first: landing time orders.
		ev(1, state.EvTaskCreated, "grove-20", onTrain("twenty")),
		ev(2, state.EvTaskCreated, "grove-21", onTrain("twenty-one")),
		ev(3, state.EvTaskDone, "grove-21", nil),
		ev(4, state.EvTaskDone, "grove-20", nil),
		// active: grove-9 created after grove-30 — created time orders.
		ev(5, state.EvTaskCreated, "grove-30", onTrain("thirty")),
		ev(6, state.EvTaskCreated, "grove-9", onTrain("nine")),
		// off-train task: never a car.
		ev(7, state.EvTaskCreated, "grove-40", map[string]string{"title": "off"}),
	}
	issues := []*provider.Task{
		{ID: "grove-50", Title: "fifty", Labels: []string{"trains"}},
		{ID: "grove-8", Title: "eight", Labels: []string{"trains"}, Description: "Depends on #9"},
		{ID: "grove-30", Title: "tracked", Labels: []string{"trains"}}, // active: not queued
		{ID: "grove-20", Title: "landed", Labels: []string{"trains"}},  // landed, issue still open
		{ID: "grove-60", Title: "other", Labels: []string{"keys"}},     // another label
		{ID: "grove-40", Title: "off", Labels: []string{"trains"}},     // tracked off-train: not queued
	}
	st, err := Statuses(StatusInput{
		Features: trainsOnly(),
		Tasks:    foldAll(t, events),
		Events:   events,
		Issues:   func(*state.Feature) ([]*provider.Task, error) { return issues, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st["old"]; ok {
		t.Error("closed feature got a status")
	}
	s := st["trains"]
	want := []string{"grove-21:landed", "grove-20:landed", "grove-30:working", "grove-9:working", "grove-8:queued", "grove-50:queued"}
	if got := tickets(s.Cars); !reflect.DeepEqual(got, want) {
		t.Fatalf("cars = %v\nwant %v", got, want)
	}
	if s.Landed != 2 || s.Total != 6 {
		t.Errorf("landed/total = %d/%d, want 2/6", s.Landed, s.Total)
	}
	if c := s.Cars[0]; c.Number != 21 || c.Title != "twenty-one" || c.LandedAt == nil || !c.LandedAt.Equal(t1(3)) {
		t.Errorf("landed car = %+v", c)
	}
	if got := s.Cars[4].After; !reflect.DeepEqual(got, []int{9}) {
		t.Errorf("after = %v, want [9]", got)
	}
}

func TestStatusesStateMapping(t *testing.T) {
	created := ev(1, state.EvTaskCreated, "grove-1", onTrain("x"))
	status := func(h int, agent, sentinel string) state.Event {
		return ev(h, state.EvAgentStatus, "grove-1", map[string]string{"status": agent, "sentinel": sentinel})
	}
	for _, tc := range []struct {
		name string
		evs  []state.Event
		want string
	}{
		{"just grabbed", nil, CarWorking},
		{"session running", []state.Event{ev(2, state.EvSessionStarted, "grove-1", nil)}, CarWorking},
		{"asked a question", []state.Event{status(2, state.AgentWaiting, "question")}, CarQuestion},
		{"blocked", []state.Event{status(2, state.AgentBlocked, "blocked")}, CarQuestion},
		{"idle with question sentinel", []state.Event{status(2, state.AgentIdle, "question")}, CarQuestion},
		{"answered: re-working", []state.Event{status(2, state.AgentWaiting, "question"), ev(3, state.EvAnswered, "grove-1", nil)}, CarWorking},
		{"reported done", []state.Event{status(2, state.AgentIdle, "done")}, CarReady},
		{"menu up", []state.Event{ev(2, state.EvSessionStarted, "grove-1", nil), ev(3, state.EvWorkerWaiting, "grove-1", nil)}, CarQuestion},
		{"pr ready", []state.Event{ev(2, state.EvSessionStarted, "grove-1", nil), ev(3, state.EvPRReady, "grove-1", map[string]string{"pr": "77"})}, CarReady},
		{"pr merged, not done", []state.Event{ev(3, state.EvPRMerged, "grove-1", map[string]string{"pr": "77"})}, CarReady},
		{"pr opened only", []state.Event{status(2, state.AgentWorking, "none"), ev(3, state.EvPROpened, "grove-1", map[string]string{"pr": "77"})}, CarWorking},
		{"paused", []state.Event{ev(2, state.EvTaskPaused, "grove-1", nil)}, CarWorking},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evs := append([]state.Event{created}, tc.evs...)
			st, _ := Statuses(StatusInput{Features: trainsOnly(), Tasks: foldAll(t, evs), Events: evs})
			cars := st["trains"].Cars
			if len(cars) != 1 || cars[0].State != tc.want {
				t.Fatalf("cars = %v, want one %s", tickets(cars), tc.want)
			}
		})
	}
}

func TestStatusesLandedSurvivesUntrackAndSweep(t *testing.T) {
	events := []state.Event{
		ev(1, state.EvTaskCreated, "grove-5", onTrain("five")),
		ev(2, state.EvPRMerged, "grove-5", map[string]string{"pr": "55"}),
		ev(3, state.EvTaskDone, "grove-5", nil),
		ev(4, state.EvTaskUntracked, "grove-5", nil),
		// untracked without landing: not a car (its open issue re-queues it).
		ev(5, state.EvTaskCreated, "grove-6", onTrain("six")),
		ev(6, state.EvTaskUntracked, "grove-6", nil),
	}
	// A sweep drops the task from the fold entirely; only events remain.
	st, _ := Statuses(StatusInput{Features: trainsOnly(), Tasks: map[string]*state.Task{}, Events: events, SkipQueued: true})
	if got := tickets(st["trains"].Cars); !reflect.DeepEqual(got, []string{"grove-5:landed"}) {
		t.Fatalf("swept: cars = %v", got)
	}
	// Untracked but still folded: still landed, and the PR number rides along.
	st, _ = Statuses(StatusInput{Features: trainsOnly(), Tasks: foldAll(t, events), Events: events})
	cars := st["trains"].Cars
	if got := tickets(cars); !reflect.DeepEqual(got, []string{"grove-5:landed"}) {
		t.Fatalf("untracked: cars = %v", got)
	}
	if cars[0].PR != 55 {
		t.Errorf("pr = %d, want 55", cars[0].PR)
	}
}

func TestStatusesRegrabAfterDoneIsActive(t *testing.T) {
	events := []state.Event{
		ev(1, state.EvTaskCreated, "grove-5", onTrain("five")),
		ev(2, state.EvTaskDone, "grove-5", nil),
		ev(3, state.EvTaskCreated, "grove-5", onTrain("five again")),
	}
	st, _ := Statuses(StatusInput{Features: trainsOnly(), Tasks: foldAll(t, events), Events: events})
	if got := tickets(st["trains"].Cars); !reflect.DeepEqual(got, []string{"grove-5:working"}) {
		t.Fatalf("cars = %v", got)
	}
}

func TestDependsOn(t *testing.T) {
	for body, want := range map[string][]int{
		"depends on #12": {12},
		"DEPENDS ON #12": {12},
		"Depends on #12 and #14\nmore\ndepends on #3": {12, 14, 3},
		"blah\n- depends on #7, #7":                   {7},
		"no deps here #4":                             nil,
		"":                                            nil,
	} {
		if got := DependsOn(body); !reflect.DeepEqual(got, want) {
			t.Errorf("DependsOn(%q) = %v, want %v", body, got, want)
		}
	}
}

func TestStatusesQueuedSkipped(t *testing.T) {
	called := false
	st, err := Statuses(StatusInput{
		Features:   trainsOnly(),
		SkipQueued: true,
		Issues: func(*state.Feature) ([]*provider.Task, error) {
			called = true
			return []*provider.Task{{ID: "grove-1", Labels: []string{"trains"}}}, nil
		},
	})
	if err != nil || called {
		t.Fatalf("err=%v called=%v: the lookup must not run", err, called)
	}
	if s := st["trains"]; len(s.Cars) != 0 || s.Total != 0 || s.Cars == nil {
		t.Errorf("status = %+v, want an empty non-nil cars list", s)
	}
}

func TestStatusesEstAndPRAndErrors(t *testing.T) {
	events := []state.Event{
		ev(1, state.EvTaskCreated, "grove-1", onTrain("one")),
		ev(2, state.EvTaskCreated, "grove-2", onTrain("two")),
	}
	rows := []ledger.Row{
		{Time: t1(1), Ticket: "grove-1", USD: 1.0},
		{Time: t1(2), Ticket: "grove-1", USD: 2.5}, // cumulative: latest wins
		{Time: t1(2), Ticket: "grove-2", USD: 0.5},
		{Time: t1(2), Ticket: "grove-99", USD: 9},
	}
	st, err := Statuses(StatusInput{
		Features: trainsOnly(), Tasks: foldAll(t, events), Events: events, Ledger: rows,
		Issues: func(*state.Feature) ([]*provider.Task, error) { return nil, errors.New("offline") },
		PR: func(f *state.Feature) (*github.PR, error) {
			return &github.PR{Number: 400, URL: "u", State: "OPEN"}, nil
		},
	})
	if err == nil {
		t.Error("queued lookup error not reported")
	}
	s := st["trains"]
	if s.EstUSD != 3.0 || s.Cars[0].EstUSD != 2.5 {
		t.Errorf("est = %v / car %v, want 3.0 / 2.5", s.EstUSD, s.Cars[0].EstUSD)
	}
	if s.PR == nil || s.PR.Number != 400 {
		t.Errorf("pr = %+v", s.PR)
	}
}

// branchFixture: origin/main one commit past the fork point, and a
// feature branch that either conflicts with it (both edit a.txt) or not.
func branchFixture(t *testing.T, conflict bool) string {
	t.Helper()
	root, _, _ := fixture(t)
	write := func(name, body, msg string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, root, "add", name)
		gitIn(t, root, "commit", "-q", "-m", msg)
	}
	write("a.txt", "base\n", "a")
	gitIn(t, root, "push", "-q", "origin", "main")
	gitIn(t, root, "checkout", "-q", "-b", "feature/x")
	if conflict {
		write("a.txt", "feature\n", "feature edits a")
	} else {
		write("b.txt", "feature\n", "feature adds b")
	}
	gitIn(t, root, "push", "-q", "origin", "feature/x")
	gitIn(t, root, "checkout", "-q", "main")
	write("a.txt", "main\n", "main edits a")
	gitIn(t, root, "push", "-q", "origin", "main")
	gitIn(t, root, "fetch", "-q", "origin")
	return root
}

func TestBehindBaseAndMergeable(t *testing.T) {
	for _, conflict := range []bool{true, false} {
		root := branchFixture(t, conflict)
		f := &state.Feature{Slug: "x", Repo: "r", Branch: "feature/x", Base: "main", Label: "x"}
		st, err := Statuses(StatusInput{
			Features: map[string]*state.Feature{"x": f},
			RepoDir:  func(string) string { return root },
			Git:      git.Run,
		})
		if err != nil {
			t.Fatal(err)
		}
		s := st["x"]
		if s.BehindBase == nil || *s.BehindBase != 1 {
			t.Errorf("conflict=%v: behind_base = %v, want 1", conflict, s.BehindBase)
		}
		if s.Mergeable == nil || *s.Mergeable == conflict {
			t.Errorf("conflict=%v: mergeable = %v", conflict, s.Mergeable)
		}
	}
	// Refs never fetched: both absent, not a zero.
	root, _, _ := fixture(t)
	f := &state.Feature{Slug: "y", Branch: "feature/nope", Base: "main"}
	st, _ := Statuses(StatusInput{Features: map[string]*state.Feature{"y": f}, RepoDir: func(string) string { return root }, Git: git.Run})
	if st["y"].BehindBase != nil || st["y"].Mergeable != nil {
		t.Errorf("missing ref: behind=%v mergeable=%v, want both nil", st["y"].BehindBase, st["y"].Mergeable)
	}
}
