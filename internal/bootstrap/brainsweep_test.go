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
		{Label: "thegrid", Root: "/w/thegrid", Exists: true, Content: bootstrap.StampSeed(seedV1) + "\n## my duty\n"},
		{Label: "unbrewed", Root: "/w/unbrewed", Exists: true, Content: "# my own brain\n\nno stamp here\n"},
		{Label: "fresh", Root: "/w/fresh"},
		{Label: "ghost", Root: "/w/ghost", RootGone: true},
		{Label: bootstrap.GlobalBrainLabel, Root: "/cfg/orchestrator", Dir: "/cfg/orchestrator"},
	}
	rows := bootstrap.PlanSweep(probes, seedV2)
	if len(rows) != len(probes) {
		t.Fatalf("want one row per probe, got %d", len(rows))
	}

	cases := []struct {
		label, state, command string
		have                  string
		pristine, attention   bool
	}{
		{"deanlol", "current", "", want, true, false},
		// grove-511: a pristine stale brain is grove's to replace — the
		// command is the fleet-wide refresh, not the .new path.
		{"grove-repo", "stale", "gv brains --refresh", bootstrap.SeedStamp(seedV1), true, true},
		{"thegrid", "stale", "gv init --only orchestrator-md", bootstrap.SeedStamp(seedV1), false, true},
		{"unbrewed", "unstamped", "gv init --only orchestrator-md --force-orchestrator-md", "", false, true},
		{"fresh", "absent", "gv init --only orchestrator-md", "", false, true},
		{"ghost", "missing-root", "", "", false, true},
		// No `gv init` step reaches the global dir, so its command is the
		// refresh sweep.
		{"(global)", "absent", "gv brains --refresh", "", false, true},
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
		if r.Pristine != c.pristine {
			t.Errorf("%s: pristine %v, want %v", c.label, r.Pristine, c.pristine)
		}
		if r.Action != "" {
			t.Errorf("%s: a plain sweep never fills action, got %q", c.label, r.Action)
		}
		if r.Leftovers == nil {
			t.Errorf("%s: leftovers must be an empty list, not null", c.label)
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

// Pristine is the stamp-equals-body-hash test: exactly what StampSeed
// writes, and nothing anyone touched since.
func TestPristine(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"freshly stamped seed", bootstrap.StampSeed(seedV1), true},
		{"stamped twice is still one stamp", bootstrap.StampSeed(bootstrap.StampSeed(seedV1)), true},
		{"prose appended", bootstrap.StampSeed(seedV1) + "\n## mine\n", false},
		{"prose edited", "# Orchestrator\n\nduties: dispatch\n\n<!-- grove-seed: " + bootstrap.SeedStamp(seedV1) + " -->\n", false},
		{"stamp rewritten", "# Orchestrator\n\nduties: dispatch, sweep\n\n<!-- grove-seed: deadbeef1234 -->\n", false},
		{"unstamped", "# my own brain\n", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := bootstrap.Pristine(c.content); got != c.want {
			t.Errorf("%s: Pristine = %v, want %v", c.name, got, c.want)
		}
	}
}

// The --refresh matrix (grove-511), against real directories. Rows keep
// the state they were FOUND in; Action says what happened.
func TestRefreshSweepMatrix(t *testing.T) {
	base := t.TempDir()
	mk := func(label, content string, leftovers ...string) bootstrap.BrainProbe {
		root := filepath.Join(base, label)
		orch := bootstrap.OrchestratorDirAt(root)
		if err := os.MkdirAll(orch, 0o755); err != nil {
			t.Fatal(err)
		}
		if content != "" {
			if err := os.WriteFile(filepath.Join(orch, bootstrap.BrainFile), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, l := range leftovers {
			if err := os.WriteFile(filepath.Join(orch, l), []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return bootstrap.ProbeBrain(label, root)
	}
	edited := bootstrap.StampSeed(seedV1) + "\n## my duty\n"
	handmade := "# my own brain\n"
	globalDir := filepath.Join(base, "cfg", "orchestrator") // does not exist yet
	probes := []bootstrap.BrainProbe{
		mk("current", bootstrap.StampSeed(seedV2)),
		mk("pristine", bootstrap.StampSeed(seedV1), "CLAUDE.md.bak-seed-000000000000"),
		mk("edited", edited),
		mk("handmade", handmade),
		mk("fresh", ""),
		bootstrap.ProbeBrain("ghost", filepath.Join(base, "nope")),
		bootstrap.ProbeGlobalBrain(globalDir),
	}
	rows, err := bootstrap.RefreshSweep(probes, seedV2)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(rows) != len(probes) {
		t.Fatalf("want %d rows, got %d", len(probes), len(rows))
	}
	read := func(dir, name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return ""
		}
		return string(b)
	}
	want := bootstrap.StampSeed(seedV2)
	cases := []struct {
		label, state, action string
		brain, newFile       string // expected bytes afterwards ("" = must not exist)
		attention            bool
	}{
		{"current", "current", "none", want, "", false},
		{"pristine", "stale", "refreshed", want, "", false},
		{"edited", "stale", "new", edited, want, true},
		{"handmade", "unstamped", "report", handmade, "", true},
		{"fresh", "absent", "seed", want, "", false},
		{"ghost", "missing-root", "report", "", "", true},
		{"(global)", "absent", "seed", want, "", false},
	}
	for i, c := range cases {
		r := rows[i]
		if r.Label != c.label || r.State != c.state {
			t.Errorf("row %d: %s/%s, want %s/%s", i, r.Label, r.State, c.label, c.state)
		}
		if r.Action != c.action {
			t.Errorf("%s: action %q, want %q", c.label, r.Action, c.action)
		}
		if r.NeedsAttention() != c.attention {
			t.Errorf("%s: NeedsAttention %v, want %v (action %s)", c.label, r.NeedsAttention(), c.attention, r.Action)
		}
		if r.Acted() && r.Command != "" {
			t.Errorf("%s: a row --refresh acted on has nothing left to run, got %q", c.label, r.Command)
		}
		if got := read(probes[i].Dir, bootstrap.BrainFile); got != c.brain {
			t.Errorf("%s: brain afterwards = %q, want %q", c.label, got, c.brain)
		}
		if got := read(probes[i].Dir, bootstrap.BrainFile+".new"); got != c.newFile {
			t.Errorf("%s: .new afterwards = %q, want %q", c.label, got, c.newFile)
		}
	}
	// The replaced brain is stamped with the NEW seed and pristine again.
	if got := read(probes[1].Dir, bootstrap.BrainFile); bootstrap.BrainStamp(got) != bootstrap.SeedStamp(seedV2) || !bootstrap.Pristine(got) {
		t.Errorf("replaced brain must carry the new stamp and be pristine, got %q", got)
	}
	// Leftovers are listed, never removed; the .new just written joins the list.
	if got := rows[1].Leftovers; len(got) != 1 || got[0] != "CLAUDE.md.bak-seed-000000000000" {
		t.Errorf("pristine row leftovers = %v, want the .bak kept", got)
	}
	if !exists(t, filepath.Join(probes[1].Dir, "CLAUDE.md.bak-seed-000000000000")) {
		t.Error("refresh deleted a .bak leftover — it only ever lists them")
	}
	if got := rows[2].Leftovers; len(got) != 1 || got[0] != "CLAUDE.md.new" {
		t.Errorf("edited row leftovers = %v, want the .new it wrote", got)
	}
	// Idempotent: a second refresh finds everything current or unchanged.
	again := []bootstrap.BrainProbe{
		bootstrap.ProbeBrain("pristine", filepath.Join(base, "pristine")),
		bootstrap.ProbeBrain("edited", filepath.Join(base, "edited")),
		bootstrap.ProbeGlobalBrain(globalDir),
	}
	rows2, err := bootstrap.RefreshSweep(again, seedV2)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows2 {
		switch r.Label {
		case "edited":
			if r.Action != "new" || read(filepath.Join(base, "edited", ".grove", "orchestrator"), bootstrap.BrainFile) != edited {
				t.Errorf("edited: second refresh must rewrite only .new, got %+v", r)
			}
		default:
			if r.State != "current" || r.Action != "none" || !r.Pristine {
				t.Errorf("%s: second refresh must find it current+pristine, got %+v", r.Label, r)
			}
		}
	}
}

// A plain sweep over the same probes writes nothing — including for the
// global brain whose dir does not exist.
func TestPlanSweepWritesNothingForGlobal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orchestrator")
	rows := bootstrap.PlanSweep([]bootstrap.BrainProbe{bootstrap.ProbeGlobalBrain(dir)}, seedV2)
	if rows[0].State != "absent" || rows[0].Label != "(global)" || rows[0].Command != "gv brains --refresh" {
		t.Errorf("global row = %+v", rows[0])
	}
	if exists(t, dir) {
		t.Error("a plain sweep created the global orchestrator dir")
	}
}

// ProbeBrain lists the merge leftovers beside a brain, sorted, and only those.
func TestProbeBrainListsLeftovers(t *testing.T) {
	root := t.TempDir()
	orch := bootstrap.OrchestratorDirAt(root)
	if err := os.MkdirAll(orch, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"CLAUDE.md", "CLAUDE.md.new", "CLAUDE.md.bak-seed-9d015e5f73f8", "CLAUDE.md.bak-pre-seed-9afe65e", "CLAUDE.local.md", "brief.md"} {
		if err := os.WriteFile(filepath.Join(orch, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := bootstrap.ProbeBrain("w", root)
	want := []string{"CLAUDE.md.bak-pre-seed-9afe65e", "CLAUDE.md.bak-seed-9d015e5f73f8", "CLAUDE.md.new"}
	if strings.Join(p.Leftovers, ",") != strings.Join(want, ",") {
		t.Errorf("leftovers = %v, want %v", p.Leftovers, want)
	}
}

// grove-512: overlay fields. A stub-only CLAUDE.local.md is no overlay; one
// real line beside the comments is `overlay: true, overlay_lines: 1`.
func TestPlanSweepReportsOverlay(t *testing.T) {
	seed := "seed"
	rows := bootstrap.PlanSweep([]bootstrap.BrainProbe{
		{Label: "none", Root: "/w/none", Exists: true, Content: bootstrap.StampSeed(seed)},
		{Label: "stub", Root: "/w/stub", Exists: true, Content: bootstrap.StampSeed(seed), Overlay: bootstrap.OverlayStub},
		{Label: "real", Root: "/w/real", Exists: true, Content: bootstrap.StampSeed(seed), Overlay: bootstrap.OverlayStub + "- The operator is Dean.\n"},
		{Label: "many", Root: "/w/many", Exists: true, Content: "# hand\n", Overlay: "# Workspace overlay\n\n## Glossary\n- a\n- b\n"},
		{Label: "ghost", Root: "/w/ghost", RootGone: true, Overlay: "- never read\n"},
	}, seed)
	want := map[string][2]int{"none": {0, 0}, "stub": {0, 0}, "real": {1, 1}, "many": {1, 4}, "ghost": {1, 1}}
	for _, r := range rows {
		w := want[r.Label]
		if r.Overlay != (w[0] == 1) || r.OverlayLines != w[1] {
			t.Errorf("%s: overlay=%v lines=%d, want overlay=%v lines=%d", r.Label, r.Overlay, r.OverlayLines, w[0] == 1, w[1])
		}
	}
}

// grove-512: the probe reads CLAUDE.local.md beside the brain, and
// --refresh seeds the stub only where no overlay file exists.
func TestRefreshSweepSeedsOverlayStubNeverOverExisting(t *testing.T) {
	base := t.TempDir()
	fresh := filepath.Join(base, "fresh")
	kept := filepath.Join(base, "kept")
	keptDir := bootstrap.OrchestratorDirAt(kept)
	for _, d := range []string{fresh, keptDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mine := "- The operator is Dean.\n"
	if err := os.WriteFile(filepath.Join(keptDir, bootstrap.OverlayFile), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	globalDir := filepath.Join(base, "cfg", "orchestrator")
	probes := []bootstrap.BrainProbe{
		bootstrap.ProbeBrain("fresh", fresh),
		bootstrap.ProbeBrain("kept", kept),
		bootstrap.ProbeGlobalBrain(globalDir),
	}
	if probes[1].Overlay != mine {
		t.Fatalf("probe did not read the overlay: %q", probes[1].Overlay)
	}
	rows, err := bootstrap.RefreshSweep(probes, "seed")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	read := func(dir string) string {
		b, _ := os.ReadFile(filepath.Join(dir, bootstrap.OverlayFile))
		return string(b)
	}
	if got := read(bootstrap.OrchestratorDirAt(fresh)); got != bootstrap.OverlayStub {
		t.Errorf("fresh: overlay = %q, want the stub", got)
	}
	if got := read(globalDir); got != bootstrap.OverlayStub {
		t.Errorf("global: overlay = %q, want the stub", got)
	}
	if got := read(keptDir); got != mine {
		t.Errorf("kept: existing overlay rewritten: %q", got)
	}
	for _, r := range rows {
		if r.Action != string(bootstrap.ActionSeed) {
			t.Errorf("%s: action %q, want seed", r.Label, r.Action)
		}
		wantStub := r.Label != "kept"
		if strings.Contains(r.Note, "stub") != wantStub {
			t.Errorf("%s: note %q (stub mention wanted: %v)", r.Label, r.Note, wantStub)
		}
		// Rows report what the probe FOUND: kept's real overlay, nothing
		// for the two that had none before the seed.
		if r.Overlay != !wantStub {
			t.Errorf("%s: overlay=%v", r.Label, r.Overlay)
		}
	}
	// Re-probing after the refresh: the stub is no overlay.
	again := bootstrap.PlanSweep([]bootstrap.BrainProbe{bootstrap.ProbeBrain("fresh", fresh)}, "seed")
	if again[0].Overlay || again[0].OverlayLines != 0 {
		t.Errorf("stub-only overlay must read as none: %+v", again[0])
	}
}

// grove-512: every edited-stale note points at the migrate verb.
func TestPlanSweepEditedNotesNameMigrate(t *testing.T) {
	edited := bootstrap.StampSeed("old") + "\n## mine\n"
	rows := bootstrap.PlanSweep([]bootstrap.BrainProbe{
		{Label: "ws", Root: "/w/ws", Exists: true, Content: edited},
		{Label: bootstrap.GlobalBrainLabel, Root: "/g", Dir: "/g", Exists: true, Content: edited},
		{Label: "pristine", Root: "/w/p", Exists: true, Content: bootstrap.StampSeed("old")},
	}, "new")
	for _, r := range rows {
		has := strings.Contains(r.Note, "gv brains --migrate")
		if want := !r.Pristine; has != want {
			t.Errorf("%s: note %q, migrate hint wanted: %v", r.Label, r.Note, want)
		}
	}
}
