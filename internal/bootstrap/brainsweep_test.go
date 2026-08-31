package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/bootstrap"
)

// The planner is pure, so the whole mixed-registry matrix is one table:
// current · stale · unstamped · absent · missing-root, each with the exact
// command the operator has to type.
func TestPlanSweepClassifiesAMixedRegistry(t *testing.T) {
	want := bootstrap.SeedStamp(seedV2)
	probes := []bootstrap.BrainProbe{
		{Label: "deanlol", Root: "/w/deanlol", Exists: true, Content: bootstrap.StampSeed(seedV2)},
		{Label: "grove-repo", Root: "/w/grove", Exists: true, Content: bootstrap.StampSeed(seedV1)},
		{Label: "unbrewed", Root: "/w/unbrewed", Exists: true, Content: "# my own brain\n\nno stamp here\n"},
		{Label: "fresh", Root: "/w/fresh"},
		{Label: "ghost", Root: "/w/ghost", RootGone: true},
	}
	rows := bootstrap.PlanSweep(probes, seedV2)
	if len(rows) != len(probes) {
		t.Fatalf("want one row per probe, got %d", len(rows))
	}

	cases := []struct {
		label, state, command string
		have                  string
		attention             bool
	}{
		{"deanlol", "current", "", want, false},
		{"grove-repo", "stale", "gv init --only orchestrator-md", bootstrap.SeedStamp(seedV1), true},
		{"unbrewed", "unstamped", "gv init --only orchestrator-md --force-orchestrator-md", "", true},
		{"fresh", "absent", "gv init --only orchestrator-md", "", true},
		{"ghost", "missing-root", "", "", true},
	}
	for i, c := range cases {
		r := rows[i]
		if r.Label != c.label {
			t.Fatalf("row %d: registry order broken, got %q want %q", i, r.Label, c.label)
		}
		if r.State != c.state {
			t.Errorf("%s: state %q, want %q", c.label, r.State, c.state)
		}
		if r.Command != c.command {
			t.Errorf("%s: command %q, want %q", c.label, r.Command, c.command)
		}
		if r.Have != c.have {
			t.Errorf("%s: have %q, want %q", c.label, r.Have, c.have)
		}
		if r.Want != want {
			t.Errorf("%s: want-stamp %q, want %q", c.label, r.Want, want)
		}
		if r.NeedsAttention() != c.attention {
			t.Errorf("%s: NeedsAttention %v, want %v", c.label, r.NeedsAttention(), c.attention)
		}
		if r.Note == "" {
			t.Errorf("%s: every row needs a human note", c.label)
		}
		if r.Root != probes[i].Root {
			t.Errorf("%s: root %q, want %q", c.label, r.Root, probes[i].Root)
		}
	}
}

// The stale note has to name BOTH stamps: the operator's only way to tell
// "the seed moved once" from "this brain is three releases behind".
func TestPlanSweepStaleNoteNamesBothStamps(t *testing.T) {
	rows := bootstrap.PlanSweep([]bootstrap.BrainProbe{
		{Label: "a", Root: "/w/a", Exists: true, Content: bootstrap.StampSeed(seedV1)},
	}, seedV2)
	note := rows[0].Note
	for _, s := range []string{bootstrap.SeedStamp(seedV1), bootstrap.SeedStamp(seedV2)} {
		if !strings.Contains(note, s) {
			t.Errorf("stale note %q must name stamp %s", note, s)
		}
	}
}

// A missing root never suggests a command — there is nowhere to run it.
func TestPlanSweepMissingRootHasNoCommand(t *testing.T) {
	rows := bootstrap.PlanSweep([]bootstrap.BrainProbe{
		{Label: "ghost", Root: "/w/ghost", RootGone: true},
	}, seedV2)
	if rows[0].Command != "" {
		t.Errorf("missing-root must not hand out a command, got %q", rows[0].Command)
	}
	if !strings.Contains(rows[0].Note, "ghost") {
		t.Errorf("missing-root note must name the label to remove, got %q", rows[0].Note)
	}
}

func TestPlanSweepEmptyRegistry(t *testing.T) {
	if rows := bootstrap.PlanSweep(nil, seedV2); len(rows) != 0 {
		t.Errorf("empty registry sweeps to zero rows, got %d", len(rows))
	}
}

// ProbeBrain is the sweep's only I/O — and it must stay read-only.
func TestProbeBrainReadsAndMutatesNothing(t *testing.T) {
	root := t.TempDir()
	orch := bootstrap.OrchestratorDirAt(root)
	if err := os.MkdirAll(orch, 0o755); err != nil {
		t.Fatal(err)
	}
	body := bootstrap.StampSeed(seedV1)
	brainPath := filepath.Join(orch, bootstrap.BrainFile)
	if err := os.WriteFile(brainPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	p := bootstrap.ProbeBrain("w", root)
	if p.RootGone || !p.Exists || p.Content != body {
		t.Fatalf("probe misread the brain: %+v", p)
	}
	after, err := os.ReadFile(brainPath)
	if err != nil || string(after) != body {
		t.Fatalf("probe must not touch the brain (err %v)", err)
	}
	if exists(t, brainPath+".new") {
		t.Error("probe wrote a .new file — the sweep is pure read")
	}
}

func TestProbeBrainMissingRootAndAbsentBrain(t *testing.T) {
	gone := bootstrap.ProbeBrain("ghost", filepath.Join(t.TempDir(), "nope"))
	if !gone.RootGone {
		t.Error("a vanished root must probe as RootGone, not as an absent brain")
	}
	empty := bootstrap.ProbeBrain("fresh", t.TempDir())
	if empty.RootGone || empty.Exists {
		t.Errorf("a live root with no brain probes as absent, got %+v", empty)
	}
}
