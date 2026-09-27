package main

import (
	"context"
	"io"
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
		return taskCreatedData(task, "grove", "grove-9-car", "/wt/grove-9-car", "grove-ws", "grove · grove-9-car", profile, c)
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

// hostFeatures is one open train, the ticket's label set, and a resolver
// that knows grove-9 on repo grove.
func hostFeatures() (map[string]*state.Feature, func(string, string) (string, string, []string, bool)) {
	features := map[string]*state.Feature{
		"keys": {Slug: "keys", Repo: "grove", Branch: "feature/gv-keys-secrets", Base: "main", Label: "keys"},
		"old":  {Slug: "old", Repo: "grove", Branch: "feature/old", Base: "main", Label: "old", Closed: true},
	}
	resolve := func(repoFlag, ref string) (string, string, []string, bool) {
		switch ref {
		case "grove-9":
			return "grove", "main", []string{"keys"}, true
		case "grove-10":
			return "grove", "main", []string{"bug"}, true
		}
		return "", "", nil, false
	}
	return features, resolve
}

// TestForwardGrabFeature (grove-398): the registering host resolves the
// feature and forwards its branch; the argv the remote runner sees is
// the assertion.
func TestForwardGrabFeature(t *testing.T) {
	features, resolve := hostFeatures()
	var got [][]string
	orig := remoteRun
	remoteRun = func(_ *config.Config, host, verb string, args []string, _, _ io.Writer) (int, error) {
		if host != "pc" || verb != "grab" {
			t.Errorf("remote %s %s, want pc grab", host, verb)
		}
		got = append(got, append([]string(nil), args...))
		return 0, nil
	}
	defer func() { remoteRun = orig }()

	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"explicit", []string{"grove-9", "--feature", "keys"},
			[]string{"grove-9", "--feature", "keys", "--feature-branch", "feature/gv-keys-secrets"}},
		{"explicit=", []string{"--feature=keys", "--brief", "--feature x", "grove-77"},
			[]string{"--brief", "--feature x", "grove-77", "--feature", "keys", "--feature-branch", "feature/gv-keys-secrets"}},
		{"label inference", []string{"grove-9", "--profile", "p"},
			[]string{"grove-9", "--profile", "p", "--feature", "keys", "--feature-branch", "feature/gv-keys-secrets"}},
		{"opt out", []string{"grove-9", "--feature", "none"}, []string{"grove-9", "--feature", "none"}},
		{"off train", []string{"grove-10"}, []string{"grove-10"}},
		{"unresolvable here", []string{"grove-99"}, []string{"grove-99"}},
	}
	for _, tc := range cases {
		got = nil
		if _, err := forwardGrab(&config.Config{}, "pc", tc.args, features, resolve); err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if len(got) != 1 || strings.Join(got[0], "\x00") != strings.Join(tc.want, "\x00") {
			t.Errorf("%s: remote argv %q, want %q", tc.name, got, tc.want)
		}
	}

	for _, bad := range [][]string{
		{"grove-9", "--feature", "old"},                 // closed
		{"grove-9", "--feature", "keys", "--repo", "x"}, // wrong repo
		{"grove-9", "--feature-branch", "feature/x"},    // internal flag from a human
	} {
		got = nil
		if _, err := forwardGrab(&config.Config{}, "pc", bad, features, resolve); err == nil || len(got) != 0 {
			t.Errorf("%q: err=%v calls=%d, want a refusal and no remote call", bad, err, len(got))
		}
	}
}

func TestCheckForwardedFeature(t *testing.T) {
	if err := checkForwardedFeature("", "feature/x"); err == nil || !strings.Contains(err.Error(), "--feature") {
		t.Errorf("--feature-branch without --feature = %v, want a refusal", err)
	}
	if err := checkForwardedFeature("none", "feature/x"); err == nil {
		t.Error("--feature none with --feature-branch accepted")
	}
	if err := checkForwardedFeature("keys", "feature/x"); err != nil {
		t.Errorf("keys + branch refused: %v", err)
	}
}

func TestTakeFlag(t *testing.T) {
	v, set, rest := takeFlag([]string{"grove-9", "--brief", "--feature-branch y", "--feature-branch=feature/x", "--manual"}, "feature-branch")
	if !set || v != "feature/x" || strings.Join(rest, ",") != "grove-9,--brief,--feature-branch y,--manual" {
		t.Errorf("takeFlag = %q %v %q", v, set, rest)
	}
}

// TestForwardedChoiceForks (grove-398): the receiving host forks from
// origin/<branch> with no feature registry, records feature+base, and
// refuses a branch origin does not have. Local bare origin, no tmux.
func TestForwardedChoiceForks(t *testing.T) {
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	seed := filepath.Join(base, "seed")
	root := filepath.Join(base, "repo")
	gitT(t, base, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, base, "clone", "-q", origin, seed)
	gitT(t, seed, "config", "user.email", "t@t")
	gitT(t, seed, "config", "user.name", "t")
	gitT(t, seed, "checkout", "-q", "-b", "main")
	gitT(t, seed, "commit", "-q", "--allow-empty", "-m", "one")
	gitT(t, seed, "push", "-q", "origin", "main")
	// The host's clone predates the feature branch: only the fetch can
	// bring it in.
	gitT(t, base, "clone", "-q", origin, root)
	gitT(t, seed, "checkout", "-q", "-b", "feature/gv-keys-secrets")
	gitT(t, seed, "commit", "-q", "--allow-empty", "-m", "feature work")
	gitT(t, seed, "push", "-q", "origin", "feature/gv-keys-secrets")
	tip := strings.TrimSpace(gitT(t, seed, "rev-parse", "HEAD"))

	c, err := forwardedChoice(root, "keys", "feature/gv-keys-secrets")
	if err != nil {
		t.Fatalf("forwardedChoice: %v", err)
	}
	ref, err := feature.ForkRef(root, c)
	if err != nil || ref != "origin/feature/gv-keys-secrets" {
		t.Fatalf("ForkRef = %q %v, want origin/feature/gv-keys-secrets", ref, err)
	}
	if got := strings.TrimSpace(gitT(t, root, "rev-parse", ref)); got != tip {
		t.Errorf("fork ref at %s, want the feature tip %s", got, tip)
	}
	task := &provider.Task{ID: "grove-9", Title: "car"}
	d := taskCreatedData(task, "grove", "grove-9-car", "/wt", "s", "w", "", c)
	if d["feature"] != "keys" || d["base"] != "feature/gv-keys-secrets" {
		t.Errorf("task_created feature=%q base=%q", d["feature"], d["base"])
	}

	if _, err := forwardedChoice(root, "keys", "feature/missing"); err == nil {
		t.Error("a branch missing on origin was accepted")
	}
}

// TestFeatureStatusesNoRemote (grove-398): --no-remote never runs the
// host runner; without it, one run per configured host.
func TestFeatureStatusesNoRemote(t *testing.T) {
	oldDir := ambient.stateDir
	ambient.stateDir = t.TempDir()
	t.Cleanup(func() { ambient.stateDir = oldDir })
	features, _ := hostFeatures()
	cfg := &config.Config{Hosts: map[string]*config.Host{"pc": {SSH: "pc", GV: "gv"}}}
	calls := 0
	run := func(context.Context, *config.Host) ([]byte, error) { calls++; return []byte(`{"tasks":[]}`), nil }

	if _, _ = featureStatuses(cfg, features, false, true, false, run); calls != 0 {
		t.Errorf("--no-remote ran the host runner %d times", calls)
	}
	if _, _ = featureStatuses(cfg, features, false, true, true, run); calls != 1 {
		t.Errorf("remote lookup ran %d times, want 1", calls)
	}
}
