package config

import (
	"strings"
	"testing"
)

func TestWithEffort(t *testing.T) {
	cases := []struct {
		name, cmd, effort, want string
	}{
		{"empty effort unchanged", "claude --dangerously-skip-permissions", "", "claude --dangerously-skip-permissions"},
		{"whitespace effort unchanged", "claude", "  ", "claude"},
		{"injects after binary before flags", "claude --dangerously-skip-permissions", "low", "claude --effort low --dangerously-skip-permissions"},
		{"bare binary", "claude", "max", "claude --effort max"},
		{"empty cmd unchanged", "", "high", ""},
		// grove-142 lesson: the repo's own flag must not survive the pin.
		{"strip then inject, space form", "claude --dangerously-skip-permissions --effort high", "low", "claude --effort low --dangerously-skip-permissions"},
		{"strip then inject, equals form", "claude --dangerously-skip-permissions --effort=high", "low", "claude --effort low --dangerously-skip-permissions"},
		{"strip then inject, quoted form", "claude --effort 'high' --dangerously-skip-permissions", "medium", "claude --effort medium --dangerously-skip-permissions"},
		{"strip then inject, double-quoted equals form", `claude --effort="high" --dangerously-skip-permissions`, "medium", "claude --effort medium --dangerously-skip-permissions"},
		{"stacks with a model pin", WithModel("claude --dangerously-skip-permissions", "opus"), "xhigh", "claude --effort xhigh --model 'opus' --dangerously-skip-permissions"},
	}
	for _, tc := range cases {
		got := WithEffort(tc.cmd, tc.effort)
		if got != tc.want {
			t.Errorf("%s: WithEffort(%q, %q) = %q, want %q", tc.name, tc.cmd, tc.effort, got, tc.want)
		}
		if tc.effort = strings.TrimSpace(tc.effort); tc.effort != "" && got != "" {
			if n := strings.Count(got, "--effort"); n != 1 {
				t.Errorf("%s: %q carries %d --effort flags, want exactly one", tc.name, got, n)
			}
			if EffortFlag(got) != tc.effort {
				t.Errorf("%s: %q reads back effort %q, want %q", tc.name, got, EffortFlag(got), tc.effort)
			}
		}
	}
}

func TestEffortFlag(t *testing.T) {
	cases := map[string]string{
		"claude":                                   "",
		"claude --effort low":                      "low",
		"claude --effort=high --foo":               "high",
		"claude --effort 'xhigh'":                  "xhigh",
		`claude --effort="max"`:                    "max",
		"claude --effort low --effort high":        "high", // last wins, like claude's parser
		"claude --efforts low":                     "",
		"claude --dangerously-skip-permissions -e": "",
	}
	for cmd, want := range cases {
		if got := EffortFlag(cmd); got != want {
			t.Errorf("EffortFlag(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestCheckEffort(t *testing.T) {
	for _, ok := range append([]string{""}, EffortLevels...) {
		if err := CheckEffort(ok); err != nil {
			t.Errorf("CheckEffort(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"High", "ultra", "1", "lo", " low"} {
		err := CheckEffort(bad)
		if err == nil {
			t.Errorf("CheckEffort(%q) = nil, want error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "low|medium|high|xhigh|max") {
			t.Errorf("CheckEffort(%q) error %q does not name the documented set", bad, err)
		}
	}
}

// A repo `effort:` key is the per-repo default a grab inherits when no
// --effort flag is given; a value outside EffortLevels is a config load
// error naming the repo, never a silent model-default launch.
func TestRepoEffortKey(t *testing.T) {
	setHome(t)
	repo := t.TempDir()
	writeGlobal(t, `
repos:
  r:
    path: `+repo+`
    effort: medium
`)
	c, err := LoadAt("")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Repos["r"].Effort; got != "medium" {
		t.Errorf("repos.r.effort = %q, want medium", got)
	}

	writeGlobal(t, `
repos:
  r:
    path: `+repo+`
    effort: ultra
`)
	_, err = LoadAt("")
	if err == nil || !strings.Contains(err.Error(), `repo "r": effort:`) || !strings.Contains(err.Error(), "ultra") {
		t.Errorf("effort: ultra loaded with err %v, want a load error naming repo r and the bad value", err)
	}
}
