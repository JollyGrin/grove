package state

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

var t0 = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

func featCreated(slug string, at time.Duration) Event {
	return Event{Time: t0.Add(at), Type: EvFeatureCreated, Data: map[string]string{
		"slug": slug, "repo": "grove", "branch": "feature/" + slug, "base": "main", "label": slug,
	}}
}

func featClosed(slug, reason string, at time.Duration) Event {
	return Event{Time: t0.Add(at), Type: EvFeatureClosed, Data: map[string]string{"slug": slug, "reason": reason}}
}

func TestFoldFeatures(t *testing.T) {
	cases := []struct {
		name   string
		events []Event
		want   map[string]Feature // slug → expected (CreatedAt/ClosedAt checked too)
	}{
		{
			name:   "create → open",
			events: []Event{featCreated("keys", 0)},
			want: map[string]Feature{"keys": {Slug: "keys", Repo: "grove", Branch: "feature/keys",
				Base: "main", Label: "keys", CreatedAt: t0}},
		},
		{
			name:   "create + close → closed with reason",
			events: []Event{featCreated("keys", 0), featClosed("keys", FeatureAbandoned, time.Hour)},
			want: map[string]Feature{"keys": {Slug: "keys", Repo: "grove", Branch: "feature/keys",
				Base: "main", Label: "keys", CreatedAt: t0, Closed: true,
				ClosedReason: FeatureAbandoned, ClosedAt: t0.Add(time.Hour)}},
		},
		{
			name: "second create of an open slug is ignored (first wins)",
			events: []Event{featCreated("keys", 0), {Time: t0.Add(time.Minute), Type: EvFeatureCreated,
				Data: map[string]string{"slug": "keys", "repo": "other", "branch": "x", "base": "dev", "label": "x"}}},
			want: map[string]Feature{"keys": {Slug: "keys", Repo: "grove", Branch: "feature/keys",
				Base: "main", Label: "keys", CreatedAt: t0}},
		},
		{
			name:   "re-created after close → open again, newest incarnation",
			events: []Event{featCreated("keys", 0), featClosed("keys", FeatureMerged, time.Hour), featCreated("keys", 2*time.Hour)},
			want: map[string]Feature{"keys": {Slug: "keys", Repo: "grove", Branch: "feature/keys",
				Base: "main", Label: "keys", CreatedAt: t0.Add(2 * time.Hour)}},
		},
		{
			name:   "close of an unknown slug is ignored",
			events: []Event{featClosed("ghost", FeatureMerged, 0)},
			want:   map[string]Feature{},
		},
		{
			name:   "second close keeps the first reason",
			events: []Event{featCreated("keys", 0), featClosed("keys", FeatureMerged, time.Hour), featClosed("keys", FeatureAbandoned, 2*time.Hour)},
			want: map[string]Feature{"keys": {Slug: "keys", Repo: "grove", Branch: "feature/keys",
				Base: "main", Label: "keys", CreatedAt: t0, Closed: true,
				ClosedReason: FeatureMerged, ClosedAt: t0.Add(time.Hour)}},
		},
		{
			name:   "task events do not touch features",
			events: []Event{{Time: t0, Type: EvTaskCreated, Ticket: "grove-1", Data: map[string]string{"slug": "keys"}}},
			want:   map[string]Feature{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, ev := range tc.events {
				if err := Append(dir, ev); err != nil {
					t.Fatal(err)
				}
			}
			cold, err := LoadFeatures(dir)
			if err != nil {
				t.Fatal(err)
			}
			folder := NewFolder(dir, 10)
			if _, _, err := folder.Refresh(); err != nil {
				t.Fatal(err)
			}
			for label, got := range map[string]map[string]*Feature{"LoadFeatures": cold, "Folder": folder.Features()} {
				if len(got) != len(tc.want) {
					t.Fatalf("%s: got %d features, want %d: %+v", label, len(got), len(tc.want), got)
				}
				for slug, w := range tc.want {
					g := got[slug]
					if g == nil {
						t.Fatalf("%s: %s missing", label, slug)
					}
					if !g.CreatedAt.Equal(w.CreatedAt) || !g.ClosedAt.Equal(w.ClosedAt) {
						t.Errorf("%s: %s times = %v/%v, want %v/%v", label, slug, g.CreatedAt, g.ClosedAt, w.CreatedAt, w.ClosedAt)
					}
					g2, w2 := *g, w
					g2.CreatedAt, g2.ClosedAt, w2.CreatedAt, w2.ClosedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
					if g2 != w2 {
						t.Errorf("%s: %s = %+v, want %+v", label, slug, g2, w2)
					}
				}
			}
		})
	}
}

func TestOpenFeature(t *testing.T) {
	fs := map[string]*Feature{"a": {Slug: "a"}, "b": {Slug: "b", Closed: true}}
	if OpenFeature(fs, "a") == nil || OpenFeature(fs, "b") != nil || OpenFeature(fs, "c") != nil {
		t.Fatal("OpenFeature: want a open, b closed, c unknown")
	}
}

// TestFolderFeaturesAreCopies: the cockpit holds the result across ticks
// while later folds mutate the internal structs.
func TestFolderFeaturesAreCopies(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, featCreated("keys", 0)); err != nil {
		t.Fatal(err)
	}
	f := NewFolder(dir, 10)
	f.Refresh()
	held := f.Features()
	if err := Append(dir, featClosed("keys", FeatureMerged, time.Hour)); err != nil {
		t.Fatal(err)
	}
	f.Refresh()
	if held["keys"].Closed {
		t.Error("held copy was mutated by a later fold")
	}
	if !f.Features()["keys"].Closed {
		t.Error("fresh read missed the close")
	}
}

// goldenTaskStream is a fixed task lifecycle without feature events.
func goldenTaskStream() []Event {
	return []Event{
		{Time: t0, Type: EvTaskCreated, Ticket: "grove-1", Data: map[string]string{
			"title": "one", "url": "u1", "repo": "grove", "branch": "grove-1-one",
			"worktree": "/wt/grove-1", "tmux_session": "grove-x", "tmux_window": "grove-1"}},
		{Time: t0.Add(time.Minute), Type: EvSessionStarted, Ticket: "grove-1", Data: map[string]string{"session_id": "s1"}},
		{Time: t0.Add(2 * time.Minute), Type: EvAgentStatus, Ticket: "grove-1", Data: map[string]string{
			"status": "waiting", "sentinel": "question", "question": "tabs?"}},
		{Time: t0.Add(3 * time.Minute), Type: EvTaskCreated, Ticket: "grove-2", Data: map[string]string{
			"title": "two", "repo": "grove", "branch": "grove-2-two", "model_profile": "zai-plan-glm"}},
		{Time: t0.Add(4 * time.Minute), Type: EvPROpened, Ticket: "grove-2", Data: map[string]string{"pr": "7", "url": "p7"}},
	}
}

func loadTasksJSON(t *testing.T, events []Event) []byte {
	t.Helper()
	dir := t.TempDir()
	for _, ev := range events {
		if err := Append(dir, ev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestTasksJSONGoldenWithoutFeatures: feature folding leaves the task view
// byte-identical — both for a stream without feature events (golden, cut
// before this change) and with feature events interleaved.
func TestTasksJSONGoldenWithoutFeatures(t *testing.T) {
	golden := filepath.Join("testdata", "tasks_golden.json")
	got := loadTasksJSON(t, goldenTaskStream())
	if *updateGolden {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("tasks.json drifted from golden:\n%s\nwant:\n%s", got, want)
	}

	var mixed []Event
	for i, ev := range goldenTaskStream() {
		mixed = append(mixed, ev, featCreated("f"+string(rune('a'+i)), time.Duration(i)*time.Second))
	}
	mixed = append(mixed, featClosed("fa", FeatureMerged, time.Hour))
	if got := loadTasksJSON(t, mixed); !bytes.Equal(got, want) {
		t.Fatalf("feature events changed tasks.json:\n%s", got)
	}
}
