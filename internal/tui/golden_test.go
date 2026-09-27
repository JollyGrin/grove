package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden frames")

// goldenSizes are the pane sizes the no-feature frame is pinned at
// (grove-377): the fixture size, the 59-row cockpit, and small panes.
var goldenSizes = [][2]int{{120, 40}, {120, 59}, {80, 24}, {40, 20}}

// goldenModel is a clock-pinned board with no features: every age is 0m,
// no memory gauge, a fixed tick — the frame is a pure function of fx and
// size.
func goldenModel(t *testing.T, fx fxLevel, w, h int) Model {
	t.Helper()
	pinHour(t, 10)
	now := time.Now()
	m := New(nil, "", "golden")
	m.width, m.height, m.fx = w, h, fx
	m.localTasks = []*state.Task{
		{Ticket: "grove-18", Title: "joy", Repo: "grove", Agent: state.AgentWorking, Created: now},
		{Ticket: "grove-19", Title: "asks", Repo: "grove", Agent: state.AgentWaiting, Question: "which?", Created: now},
	}
	m.live = map[string]string{"grove-18": "working", "grove-19": "waiting"}
	m.assemble()
	m.prs = map[string]*github.PR{"grove-18": {Number: 18, State: "OPEN", CI: "pass"}}
	m.events = []state.Event{{Type: state.EvTaskCreated, Ticket: "grove-18", Time: now}}
	return m
}

// With zero open features the frame is byte-identical to the pre-feature
// cockpit (feature trains Decision 5). The golden files were generated
// before the FEATURES panel existed; -update rewrites them.
func TestNoFeatureFrameGolden(t *testing.T) {
	for _, fx := range []fxLevel{fxOff, fxCalm, fxFull} {
		for _, sz := range goldenSizes {
			m := goldenModel(t, fx, sz[0], sz[1])
			got := m.View()
			path := filepath.Join("testdata", fmt.Sprintf("nofeature-fx%d-%dx%d.golden", fx, sz[0], sz[1]))
			if *updateGolden {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("fx=%d %dx%d: frame drifted from the no-feature golden:\n%s\n--- want ---\n%s", fx, sz[0], sz[1], got, want)
			}
		}
	}
}
