package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// applyDir lays out one orchestrator dir for --apply: an edited brain
// (appended.md on base), the first-seed stub beside it and the dry run's
// draft, rendered the way `gv brains --migrate` would. Returns the dir,
// the probe and the seed the apply installs.
func applyDir(t *testing.T, files map[string]string) (string, MigrateProbe) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, MigrateProbe{BrainProbe: ProbeGlobalBrain(dir)}
}

func dirBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// A clean draft applies: the seed becomes CLAUDE.md, the original is
// byte-identical at .bak-pre-overlay, the draft becomes CLAUDE.local.md
// over the stub, and nothing else in the dir moves.
func TestApplySwapsSeedBackupAndOverlay(t *testing.T) {
	base := fixture(t, "base.md")
	seed := "# newer seed\n\nwith new duties\n"
	brain := asBrain(fixture(t, "appended.md"), base)
	draft := RenderDraft("unbrewed", SeedStamp(base), ClassifyBrain(brain, base))
	if hasReviewBlock(draft) {
		t.Fatal("fixture draft must be clean")
	}
	dir, p := applyDir(t, map[string]string{
		BrainFile:          brain,
		OverlayFile:        OverlayStub,
		DraftFile:          draft,
		BrainFile + ".new": "a leftover",
	})
	p.Tracked = true
	d, ok, bak := ReadApply(p)
	plan, err := PlanApply(p, d, ok, bak, seed)
	if err != nil {
		t.Fatalf("plan refused: %v", err)
	}
	if !plan.StubReplaced || !plan.Tracked || len(plan.GitCommands) != 2 {
		t.Errorf("plan %+v", plan)
	}
	if err := Apply(plan); err != nil {
		t.Fatal(err)
	}
	got := dirBytes(t, dir)
	want := map[string]string{
		BrainFile:          StampSeed(seed),
		BackupPreOverlay:   brain,
		OverlayFile:        draft,
		BrainFile + ".new": "a leftover",
	}
	if !sameFiles(got, want) {
		t.Errorf("dir after apply:\n got %v\nwant %v", keys(got), keys(want))
		for k := range want {
			if got[k] != want[k] {
				t.Errorf("%s differs", k)
			}
		}
	}
	if !Pristine(got[BrainFile]) {
		t.Error("the new brain must read as pristine")
	}
	if !HasOverlay(got[OverlayFile]) {
		t.Error("the applied draft must read as a real overlay")
	}
}

// With no overlay file at all the draft simply lands as CLAUDE.local.md.
func TestApplyWithoutStub(t *testing.T) {
	base := fixture(t, "base.md")
	brain := asBrain(fixture(t, "appended.md"), base)
	dir, p := applyDir(t, map[string]string{BrainFile: brain, DraftFile: "# Workspace overlay: x\n\n## Addenda\n\nreal\n"})
	plan, err := PlanApply(p, "# Workspace overlay: x\n\n## Addenda\n\nreal\n", true, false, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if plan.StubReplaced {
		t.Error("no stub to replace")
	}
	if err := Apply(plan); err != nil {
		t.Fatal(err)
	}
	got := dirBytes(t, dir)
	if len(got) != 3 || got[OverlayFile] != "# Workspace overlay: x\n\n## Addenda\n\nreal\n" || got[BackupPreOverlay] != brain || got[BrainFile] != StampSeed("seed") {
		t.Errorf("dir after apply: %v", keys(got))
	}
}

// Every refusal is an *ApplyRefusal and changes no file.
func TestApplyRefusalsChangeNothing(t *testing.T) {
	base := fixture(t, "base.md")
	brain := asBrain(fixture(t, "appended.md"), base)
	clean := RenderDraft("x", SeedStamp(base), ClassifyBrain(brain, base))
	review := RenderDraft("x", SeedStamp(base), ClassifyBrain(asBrain(fixture(t, "rewritten.md"), base), base))
	if !hasReviewBlock(review) {
		t.Fatal("rewritten fixture must need review")
	}
	cases := []struct {
		name  string
		files map[string]string
		probe func(*MigrateProbe)
		want  string
	}{
		{"draft missing", map[string]string{BrainFile: brain, OverlayFile: OverlayStub}, nil, "no "},
		{"review block", map[string]string{BrainFile: brain, OverlayFile: OverlayStub, DraftFile: review}, nil, "Review before applying"},
		{"review block, indented", map[string]string{BrainFile: brain, DraftFile: "# x\n  " + ReviewHeading + "  \n"}, nil, "Review before applying"},
		{"real overlay", map[string]string{BrainFile: brain, OverlayFile: OverlayStub + "- The operator is Dean.\n", DraftFile: clean}, nil, "already exists"},
		{"backup present", map[string]string{BrainFile: brain, OverlayFile: OverlayStub, DraftFile: clean, BackupPreOverlay: "older"}, nil, BackupPreOverlay},
		{"pristine brain", map[string]string{BrainFile: StampSeed(base), OverlayFile: OverlayStub, DraftFile: clean}, nil, "pristine"},
		{"no brain", map[string]string{OverlayFile: OverlayStub, DraftFile: clean}, nil, "nothing to migrate"},
		{"root gone", map[string]string{BrainFile: brain, DraftFile: clean}, func(p *MigrateProbe) { p.RootGone = true }, "is gone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, p := applyDir(t, tc.files)
			if tc.probe != nil {
				tc.probe(&p)
			}
			before := dirBytes(t, dir)
			d, ok, bak := ReadApply(p)
			_, err := PlanApply(p, d, ok, bak, "seed")
			var ref *ApplyRefusal
			if !errors.As(err, &ref) {
				t.Fatalf("want an ApplyRefusal, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal %q lacks %q", err, tc.want)
			}
			if !sameFiles(before, dirBytes(t, dir)) {
				t.Error("a refusal changed a file")
			}
		})
	}
}

// A stub-only overlay is "absent" for --apply (grove-512): it is replaced,
// and nothing about it blocks the swap. Guard the boundary explicitly.
func TestApplyStubCountsAsAbsent(t *testing.T) {
	p := migrateProbe("x", "edited brain, no stamp")
	p.Overlay = OverlayStub
	if _, err := PlanApply(p, "# overlay\n", true, false, "seed"); err != nil {
		t.Fatalf("stub must not refuse: %v", err)
	}
	p.Overlay = OverlayStub + "one real line\n"
	if _, err := PlanApply(p, "# overlay\n", true, false, "seed"); err == nil {
		t.Fatal("a real overlay must refuse")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
