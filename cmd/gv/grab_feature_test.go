package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/feature"
	"github.com/JollyGrin/grove/internal/provider"
	"github.com/JollyGrin/grove/internal/state"
)

// appendedLine writes one task_created event through state.Append and
// returns the exact events.jsonl line — the bytes plugins read.
func appendedLine(t *testing.T, data map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	ev := state.Event{
		Time: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		Type: state.EvTaskCreated, Ticket: "grove-9", Data: data,
	}
	if err := state.Append(dir, ev); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// TestTaskCreatedGolden (grove-373): a grab without a feature writes the
// same task_created bytes as before feature trains; a feature grab adds
// exactly feature+base.
func TestTaskCreatedGolden(t *testing.T) {
	task := &provider.Task{ID: "grove-9", Title: "car", URL: "https://x/9"}
	args := func(c feature.Choice, profile string) map[string]string {
		return taskCreatedData(task, "grove", "grove-9-car", "/wt/grove-9-car", "grove-ws", "grove · grove-9-car", profile, "", c)
	}
	const head = `{"time":"2026-09-27T12:00:00Z","type":"task_created","ticket":"grove-9","data":{`
	const tail = `"repo":"grove","title":"car","tmux_session":"grove-ws","tmux_window":"grove · grove-9-car","url":"https://x/9","worktree":"/wt/grove-9-car"},"v":`

	off := feature.Choice{Base: "main", Why: "repo base"}
	want := head + `"branch":"grove-9-car",` + tail
	if got := appendedLine(t, args(off, "")); !strings.HasPrefix(got, want) {
		t.Errorf("off-train task_created changed:\n got %s\nwant %s…", got, want)
	}
	// --feature none is off-train too: same bytes.
	none := feature.Choice{Base: "main", Why: "--feature none"}
	if got := appendedLine(t, args(none, "")); !strings.HasPrefix(got, want) {
		t.Errorf("--feature none task_created changed:\n got %s\nwant %s…", got, want)
	}
	// model_profile precedent still holds alongside.
	wantProf := head + `"branch":"grove-9-car","model_profile":"zai-plan-glm",` + tail
	if got := appendedLine(t, args(off, "zai-plan-glm")); !strings.HasPrefix(got, wantProf) {
		t.Errorf("profiled task_created changed:\n got %s\nwant %s…", got, wantProf)
	}

	car := feature.Choice{
		Feature: &state.Feature{Slug: "keys", Repo: "grove", Branch: "feature/keys", Base: "main", Label: "keys"},
		Base:    "feature/keys", Why: "feature keys, from label keys",
	}
	wantCar := head + `"base":"feature/keys","branch":"grove-9-car","feature":"keys",` + tail
	if got := appendedLine(t, args(car, "")); !strings.HasPrefix(got, wantCar) {
		t.Errorf("feature task_created:\n got %s\nwant %s…", got, wantCar)
	}
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestRemoveGuardUsesTaskBase (grove-373): a train car forked from
// origin/feature/keys carries the feature's commits. Checked against its
// own base it is clean; checked against the repo base (main) those
// inherited commits read as unmerged work. Local bare origin, no tmux.
func TestRemoveGuardUsesTaskBase(t *testing.T) {
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	root := filepath.Join(base, "repo")
	gitT(t, base, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, base, "clone", "-q", origin, root)
	gitT(t, root, "config", "user.email", "t@t")
	gitT(t, root, "config", "user.name", "t")
	gitT(t, root, "checkout", "-q", "-b", "main")
	gitT(t, root, "commit", "-q", "--allow-empty", "-m", "one")
	gitT(t, root, "push", "-q", "origin", "main")
	gitT(t, root, "checkout", "-q", "-b", "feature/keys")
	gitT(t, root, "commit", "-q", "--allow-empty", "-m", "feature work")
	gitT(t, root, "push", "-q", "origin", "feature/keys")
	gitT(t, root, "checkout", "-q", "main")
	// The car: forked from origin/feature/keys, never pushed (no upstream),
	// so the guard compares against origin/<base>.
	wt := filepath.Join(base, "wt")
	gitT(t, root, "worktree", "add", "-q", "--no-track", "-b", "grove-9-car", wt, "origin/feature/keys")

	repo := &config.Repo{Path: root, Base: "main"}
	car := &state.Task{Ticket: "grove-9", Branch: "grove-9-car", Worktree: wt, Feature: "keys", Base: "feature/keys"}
	if err := removeGuard(repo, car); err != nil {
		t.Errorf("car with Base=feature/keys refused: %v", err)
	}
	plain := *car
	plain.Feature, plain.Base = "", ""
	err := removeGuard(repo, &plain)
	if err == nil || !strings.Contains(err.Error(), "origin/main") {
		t.Errorf("same branch without a task base should be guarded against origin/main, got %v", err)
	}

	// Worktree gone, branch survives: same resolution on the branch path.
	gitT(t, root, "worktree", "remove", "--force", wt)
	if err := removeGuard(repo, car); err != nil {
		t.Errorf("car branch (no worktree) refused: %v", err)
	}
	if err := removeGuard(repo, &plain); err == nil || !strings.Contains(err.Error(), "origin/main") {
		t.Errorf("plain branch (no worktree) should refuse against origin/main, got %v", err)
	}
}

func TestScanGrabArgs(t *testing.T) {
	cases := []struct {
		args      []string
		feat      string
		set       bool
		repo, ref string
	}{
		{args: []string{"grove-9"}, ref: "grove-9"},
		{args: []string{"--brief", "grove-1 text", "grove-9", "--feature", "keys"}, feat: "keys", set: true, ref: "grove-9"},
		{args: []string{"--feature=none", "--repo=grove", "grove-9"}, feat: "none", set: true, repo: "grove", ref: "grove-9"},
		{args: []string{"-repo", "grove", "--manual", "grove-9"}, repo: "grove", ref: "grove-9"},
	}
	for _, tc := range cases {
		feat, set, repo, ref := scanGrabArgs(tc.args)
		if feat != tc.feat || set != tc.set || repo != tc.repo || ref != tc.ref {
			t.Errorf("scanGrabArgs(%q) = %q %v %q %q, want %q %v %q %q", tc.args, feat, set, repo, ref, tc.feat, tc.set, tc.repo, tc.ref)
		}
	}
}

func TestRefuseHostFeatureGrabExplicit(t *testing.T) {
	err := refuseHostFeatureGrab([]string{"grove-9", "--feature", "keys"})
	if err == nil || !strings.Contains(err.Error(), "--host") {
		t.Errorf("explicit --feature over --host = %v, want a refusal naming --host", err)
	}
	if err := refuseHostFeatureGrab([]string{"grove-9", "--feature", "none"}); err != nil {
		t.Errorf("--feature none over --host refused: %v", err)
	}
}
