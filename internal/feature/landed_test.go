package feature

import (
	"errors"
	"reflect"
	"testing"

	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/provider"
	"github.com/JollyGrin/grove/internal/state"
)

// TestStatusesLandedFromGitHub: grove-397 — on a GitHub provider a car is
// landed when its labelled issue is closed AND a PR from its ticket branch
// merged into the feature branch; events stay the fallback and the union.
func TestStatusesLandedFromGitHub(t *testing.T) {
	closed := func(ids ...string) []*provider.Task {
		var out []*provider.Task
		for _, id := range ids {
			out = append(out, &provider.Task{ID: id, Title: "t " + id, Status: "closed", Labels: []string{"trains"}})
		}
		return out
	}
	merged := func(n int, head string, h int) github.MergedPR {
		return github.MergedPR{Number: n, HeadRefName: head, MergedAt: t1(h)}
	}
	localDone := []state.Event{
		ev(1, state.EvTaskCreated, "grove-5", onTrain("five")),
		ev(2, state.EvTaskDone, "grove-5", nil),
	}
	cases := []struct {
		name    string
		events  []state.Event
		issues  []*provider.Task
		prs     []github.MergedPR
		lookErr error
		want    []string
		wantPR  map[string]int
		wantAt  map[string]int // ticket → hour of landed_at
		wantErr bool
	}{
		{
			name:   "closed, labelled, merged into the branch → landed with mergedAt",
			issues: closed("grove-361"),
			prs:    []github.MergedPR{merged(371, "grove-361-keys-secrets", 7)},
			want:   []string{"grove-361:landed"},
			wantPR: map[string]int{"grove-361": 371}, wantAt: map[string]int{"grove-361": 7},
		},
		{
			name:   "closed but the PR merged elsewhere → not landed",
			issues: closed("grove-361"),
			prs:    []github.MergedPR{merged(371, "grove-36-other", 7)}, // a prefix, not this ticket
			want:   []string{},
		},
		{
			name:   "closed with no PR → neither landed nor queued",
			issues: closed("grove-361"),
			want:   []string{},
		},
		{
			name:   "landed only in local events → still landed (union)",
			events: localDone,
			issues: closed("grove-361"),
			prs:    []github.MergedPR{merged(371, "grove-361-keys", 7)},
			want:   []string{"grove-5:landed", "grove-361:landed"},
			wantAt: map[string]int{"grove-5": 2, "grove-361": 7},
		},
		{
			name:   "both know the car → GitHub wins",
			events: localDone,
			issues: closed("grove-5"),
			prs:    []github.MergedPR{merged(40, "grove-5-five", 3), merged(41, "grove-5-five", 4)},
			want:   []string{"grove-5:landed"},
			wantPR: map[string]int{"grove-5": 41}, wantAt: map[string]int{"grove-5": 4},
		},
		{
			name:    "lookup error → events, error reported",
			events:  localDone,
			issues:  closed("grove-361"),
			prs:     []github.MergedPR{merged(371, "grove-361-keys", 7)},
			lookErr: errors.New("offline"),
			want:    []string{"grove-5:landed"},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, err := Statuses(StatusInput{
				Features: trainsOnly(), Tasks: foldAll(t, c.events), Events: c.events,
				Issues:       func(*state.Feature) ([]*provider.Task, error) { return nil, nil },
				ClosedIssues: func(*state.Feature) ([]*provider.Task, error) { return c.issues, nil },
				MergedPRs: func(f *state.Feature) ([]github.MergedPR, error) {
					if f.Branch != "feature/trains" {
						t.Errorf("merged lookup on %q", f.Branch)
					}
					return c.prs, c.lookErr
				},
			})
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			s := st["trains"]
			if got := tickets(s.Cars); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("cars = %v, want %v", got, c.want)
			}
			if s.Landed != len(c.want) {
				t.Errorf("landed = %d, want %d", s.Landed, len(c.want))
			}
			for _, car := range s.Cars {
				if pr, ok := c.wantPR[car.Ticket]; ok && car.PR != pr {
					t.Errorf("%s pr = %d, want %d", car.Ticket, car.PR, pr)
				}
				if h, ok := c.wantAt[car.Ticket]; ok && !car.LandedAt.Equal(t1(h)) {
					t.Errorf("%s landed_at = %v, want %v", car.Ticket, car.LandedAt, t1(h))
				}
			}
		})
	}
}

// A ticket tracked with no feature — grabbed before the feature was
// adopted, the live grove-361 case — lands from GitHub instead of
// vanishing.
func TestStatusesLandedTrackedOffFeature(t *testing.T) {
	events := []state.Event{ev(1, state.EvTaskCreated, "grove-361", map[string]string{"title": "keys"})}
	st, err := Statuses(StatusInput{
		Features: trainsOnly(), Tasks: foldAll(t, events), Events: events,
		ClosedIssues: func(*state.Feature) ([]*provider.Task, error) {
			return []*provider.Task{{ID: "grove-361", Labels: []string{"trains"}}}, nil
		},
		MergedPRs: func(*state.Feature) ([]github.MergedPR, error) {
			return []github.MergedPR{{Number: 371, HeadRefName: "grove-361-keys", MergedAt: t1(3)}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := tickets(st["trains"].Cars); !reflect.DeepEqual(got, []string{"grove-361:landed"}) {
		t.Errorf("cars = %v, want grove-361 landed", got)
	}
}

// A ticket still tracked on this feature stays an active car even when
// GitHub says it landed — it still owes `gv done`. SkipQueued skips the
// lookup entirely.
func TestStatusesLandedTrackedAndSkipped(t *testing.T) {
	events := []state.Event{ev(1, state.EvTaskCreated, "grove-361", onTrain("keys"))}
	called := false
	in := StatusInput{
		Features: trainsOnly(), Tasks: foldAll(t, events), Events: events,
		ClosedIssues: func(*state.Feature) ([]*provider.Task, error) {
			called = true
			return []*provider.Task{{ID: "grove-361", Labels: []string{"trains"}}}, nil
		},
		MergedPRs: func(*state.Feature) ([]github.MergedPR, error) {
			return []github.MergedPR{{Number: 371, HeadRefName: "grove-361-keys", MergedAt: t1(3)}}, nil
		},
	}
	st, _ := Statuses(in)
	if got := tickets(st["trains"].Cars); !reflect.DeepEqual(got, []string{"grove-361:working"}) {
		t.Errorf("cars = %v, want the tracked car active", got)
	}
	called = false
	in.SkipQueued = true
	Statuses(in)
	if called {
		t.Error("landed lookup ran under SkipQueued")
	}
}
