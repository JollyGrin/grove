package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/bootstrap"
)

const seedV1 = "# Orchestrator\n\nduties: dispatch, sweep\n"
const seedV2 = "# Orchestrator\n\nduties: dispatch, sweep, handoff\n"

func brain(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read brain: %v", err)
	}
	return string(b)
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

// Case 1: absent → the seed is written, stamped.
func TestRefreshBrainAbsentSeeds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orchestrator")
	plan, wrote, err := bootstrap.RefreshBrain(dir, seedV1, false)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if plan.State != bootstrap.BrainAbsent || plan.Action != bootstrap.ActionSeed {
		t.Fatalf("plan = %+v, want absent/seed", plan)
	}
	if wrote != filepath.Join(dir, "CLAUDE.md") {
		t.Errorf("wrote %q, want the brain itself", wrote)
	}
	got := brain(t, dir)
	if !strings.Contains(got, "duties: dispatch, sweep") {
		t.Errorf("seed body missing:\n%s", got)
	}
	if bootstrap.BrainStamp(got) != bootstrap.SeedStamp(seedV1) {
		t.Errorf("stamp %q, want %q", bootstrap.BrainStamp(got), bootstrap.SeedStamp(seedV1))
	}
}

// Case 2: present with a matching stamp → no-op.
func TestRefreshBrainCurrentIsNoop(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orchestrator")
	if _, _, err := bootstrap.RefreshBrain(dir, seedV1, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := brain(t, dir)

	plan, wrote, err := bootstrap.RefreshBrain(dir, seedV1, false)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if plan.State != bootstrap.BrainCurrent || plan.Action != bootstrap.ActionNone {
		t.Fatalf("plan = %+v, want current/none", plan)
	}
	if wrote != "" {
		t.Errorf("wrote %q, want nothing", wrote)
	}
	if brain(t, dir) != before {
		t.Error("a no-op run rewrote the brain")
	}
	if exists(t, filepath.Join(dir, "CLAUDE.md.new")) {
		t.Error("a no-op run left a .new file")
	}
	if n := strings.Count(before, "<!-- grove-seed:"); n != 1 {
		t.Errorf("%d stamp lines, want exactly 1", n)
	}
}

// Case 3: present with a stale stamp → .new written, original untouched.
func TestRefreshBrainStaleWritesNew(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orchestrator")
	if _, _, err := bootstrap.RefreshBrain(dir, seedV1, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// The operator edits the prose around the stamp — drift is decided
	// from the stamp alone, so this must still read as stale.
	original := brain(t, dir) + "\nmy own notes\n"
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, wrote, err := bootstrap.RefreshBrain(dir, seedV2, false)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if plan.State != bootstrap.BrainStale || plan.Action != bootstrap.ActionNew {
		t.Fatalf("plan = %+v, want stale/new", plan)
	}
	if plan.Have != bootstrap.SeedStamp(seedV1) || plan.Want != bootstrap.SeedStamp(seedV2) {
		t.Errorf("plan stamps %s → %s, want %s → %s", plan.Have, plan.Want, bootstrap.SeedStamp(seedV1), bootstrap.SeedStamp(seedV2))
	}
	if wrote != filepath.Join(dir, "CLAUDE.md.new") {
		t.Errorf("wrote %q, want CLAUDE.md.new", wrote)
	}
	if brain(t, dir) != original {
		t.Error("grove overwrote an existing brain")
	}
	newBody, err := os.ReadFile(wrote)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.BrainStamp(string(newBody)) != bootstrap.SeedStamp(seedV2) {
		t.Error(".new is not stamped with the current seed")
	}

	// Twice is idempotent: same .new, one stamp line, brain still untouched.
	if _, wrote2, err := bootstrap.RefreshBrain(dir, seedV2, false); err != nil || wrote2 != wrote {
		t.Fatalf("second run: %q %v", wrote2, err)
	}
	again, _ := os.ReadFile(wrote)
	if string(again) != string(newBody) {
		t.Error("second run changed .new")
	}
	if n := strings.Count(string(again), "<!-- grove-seed:"); n != 1 {
		t.Errorf("%d stamp lines in .new, want exactly 1", n)
	}
	if brain(t, dir) != original {
		t.Error("second run overwrote the brain")
	}
}

// Case 4: present with no stamp → reported once, nothing written.
func TestRefreshBrainUnstampedIsReportedNotRewritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orchestrator")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	handMade := "# my own brain\n"
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(handMade), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, wrote, err := bootstrap.RefreshBrain(dir, seedV2, false)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if plan.State != bootstrap.BrainUnstamped || plan.Action != bootstrap.ActionReport {
		t.Fatalf("plan = %+v, want unstamped/report", plan)
	}
	if wrote != "" {
		t.Errorf("wrote %q, want nothing", wrote)
	}
	if exists(t, filepath.Join(dir, "CLAUDE.md.new")) {
		t.Error("a hand-managed brain was nagged with a .new file")
	}
	if brain(t, dir) != handMade {
		t.Error("a hand-managed brain was rewritten")
	}
	if !strings.Contains(plan.Note, "hand-managed") {
		t.Errorf("note %q should say the brain is hand-managed", plan.Note)
	}

	// --force is the human's explicit ask: now the .new appears, and the
	// brain is STILL untouched.
	plan, wrote, err = bootstrap.RefreshBrain(dir, seedV2, true)
	if err != nil {
		t.Fatalf("forced refresh: %v", err)
	}
	if plan.Action != bootstrap.ActionNew || wrote != filepath.Join(dir, "CLAUDE.md.new") {
		t.Fatalf("forced: plan %+v wrote %q", plan, wrote)
	}
	if brain(t, dir) != handMade {
		t.Error("forced run overwrote the brain")
	}
}

// SeedBrain (the cockpit build) seeds when absent and never litters .new.
func TestSeedBrainOnlySeeds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orchestrator")
	wrote, err := bootstrap.SeedBrain(dir, seedV1)
	if err != nil || wrote == "" {
		t.Fatalf("seed: %q %v", wrote, err)
	}
	if bootstrap.BrainStamp(brain(t, dir)) != bootstrap.SeedStamp(seedV1) {
		t.Error("cockpit seed write is unstamped")
	}
	// Seed moved: the cockpit leaves the brain alone, silently.
	wrote, err = bootstrap.SeedBrain(dir, seedV2)
	if err != nil || wrote != "" {
		t.Fatalf("second seed: %q %v", wrote, err)
	}
	if exists(t, filepath.Join(dir, "CLAUDE.md.new")) {
		t.Error("cockpit build wrote a .new file")
	}
}

// Stamping is idempotent and stamp-insensitive to surrounding edits.
func TestStampSeedIdempotent(t *testing.T) {
	once := bootstrap.StampSeed(seedV1)
	if got := bootstrap.StampSeed(once); got != once {
		t.Errorf("re-stamping changed the bytes:\n%q\n%q", once, got)
	}
	if n := strings.Count(once, "<!-- grove-seed:"); n != 1 {
		t.Errorf("%d stamp lines, want 1", n)
	}
	if bootstrap.SeedStamp(once) != bootstrap.SeedStamp(seedV1) {
		t.Error("the stamp must be computed over the seed body, not the stamped file")
	}
	if bootstrap.SeedStamp(seedV1) == bootstrap.SeedStamp(seedV2) {
		t.Error("different seeds must stamp differently")
	}
	if bootstrap.BrainStamp("no marker here") != "" {
		t.Error("unstamped content must report no stamp")
	}
}

// grove-512: the first seed drops the overlay stub beside the brain —
// and never over a CLAUDE.local.md that is already there.
func TestSeedWritesOverlayStubOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(dir string) error
	}{
		{"SeedBrain", func(dir string) error { _, err := bootstrap.SeedBrain(dir, seedV1); return err }},
		{"RefreshBrain", func(dir string) error { _, _, err := bootstrap.RefreshBrain(dir, seedV1, false); return err }},
	} {
		t.Run(tc.name+"/absent", func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "orchestrator")
			if err := tc.seed(dir); err != nil {
				t.Fatalf("seed: %v", err)
			}
			b, err := os.ReadFile(filepath.Join(dir, bootstrap.OverlayFile))
			if err != nil {
				t.Fatalf("stub not written: %v", err)
			}
			if string(b) != bootstrap.OverlayStub {
				t.Errorf("stub bytes = %q, want OverlayStub", b)
			}
		})
		t.Run(tc.name+"/existing", func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "orchestrator")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			mine := "- The operator is Dean.\n"
			if err := os.WriteFile(filepath.Join(dir, bootstrap.OverlayFile), []byte(mine), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := tc.seed(dir); err != nil {
				t.Fatalf("seed: %v", err)
			}
			b, _ := os.ReadFile(filepath.Join(dir, bootstrap.OverlayFile))
			if string(b) != mine {
				t.Errorf("an existing overlay was rewritten: %q", b)
			}
			if !exists(t, filepath.Join(dir, "CLAUDE.md")) {
				t.Error("the brain itself must still be seeded")
			}
		})
	}
	// A current brain with no overlay: no stub appears after the fact —
	// the stub is a first-seed courtesy, not a nag.
	dir := filepath.Join(t.TempDir(), "orchestrator")
	if _, _, err := bootstrap.RefreshBrain(dir, seedV1, false); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, bootstrap.OverlayFile))
	if _, _, err := bootstrap.RefreshBrain(dir, seedV1, false); err != nil {
		t.Fatal(err)
	}
	if exists(t, filepath.Join(dir, bootstrap.OverlayFile)) {
		t.Error("a no-op refresh must not write the stub")
	}
}

// grove-512: Claude Code strips block HTML comments, so the stub must be
// nothing but comments — zero context cost, and `overlay: false`.
func TestOverlayStubIsOnlyComments(t *testing.T) {
	if rest := strings.TrimSpace(bootstrap.StripHTMLComments(bootstrap.OverlayStub)); rest != "" {
		t.Errorf("stub leaks past comment stripping: %q", rest)
	}
	if bootstrap.HasOverlay(bootstrap.OverlayStub) || bootstrap.OverlayLines(bootstrap.OverlayStub) != 0 {
		t.Error("the stub must count as no overlay")
	}
	for _, h := range []string{"Glossary", "Overrides", "Addenda", "Workspace duties", bootstrap.OverlayDocURL} {
		if !strings.Contains(bootstrap.OverlayStub, h) {
			t.Errorf("stub missing %q", h)
		}
	}
	// One comment per line: a shell `grep -v '^<!--'` finds nothing.
	for _, ln := range strings.Split(strings.TrimRight(bootstrap.OverlayStub, "\n"), "\n") {
		if !strings.HasPrefix(ln, "<!--") || !strings.HasSuffix(ln, "-->") {
			t.Errorf("stub line is not a self-contained comment: %q", ln)
		}
	}
}

func TestOverlayLines(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"\n\n  \n", 0},
		{bootstrap.OverlayStub, 0},
		{"<!-- a\nmulti-line\ncomment -->\n", 0},
		{bootstrap.OverlayStub + "- The operator is Dean.\n", 1},
		{"- one\n<!-- hidden -->\n- two <!-- trailing --> still counts\n\n", 2},
		{"<!-- unterminated\n- not a comment line", 2}, // no closing marker: nothing is stripped
	} {
		if got := bootstrap.OverlayLines(tc.in); got != tc.want {
			t.Errorf("OverlayLines(%q) = %d, want %d", tc.in, got, tc.want)
		}
		if got := bootstrap.HasOverlay(tc.in); got != (tc.want > 0) {
			t.Errorf("HasOverlay(%q) = %v", tc.in, got)
		}
	}
}

// grove-512: the edited-stale note names the migrate verb; the pristine
// one does not (nothing of the operator's is in that file).
func TestPlanBrainEditedStaleNamesMigrate(t *testing.T) {
	edited := bootstrap.PlanBrain(bootstrap.StampSeed(seedV1)+"\n## mine\n", true, seedV2, false)
	if !strings.Contains(edited.Note, "gv brains --migrate") || !strings.Contains(edited.Note, bootstrap.OverlayFile) {
		t.Errorf("edited stale note must point at the overlay + migrate: %q", edited.Note)
	}
	pristine := bootstrap.PlanBrain(bootstrap.StampSeed(seedV1), true, seedV2, false)
	if strings.Contains(pristine.Note, "--migrate") {
		t.Errorf("pristine stale note has nothing to migrate: %q", pristine.Note)
	}
}
