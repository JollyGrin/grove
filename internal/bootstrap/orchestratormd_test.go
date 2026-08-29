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
