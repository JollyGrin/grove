package config

import (
	"strings"
	"testing"
)

func TestWithAutocompact(t *testing.T) {
	cases := []struct {
		name, cmd, v, want string
	}{
		{"unset unchanged", "claude --dangerously-skip-permissions", "", "claude --dangerously-skip-permissions"},
		{"whitespace unchanged", "claude", "  ", "claude"},
		{"injects after binary before flags", "claude --dangerously-skip-permissions", "150000", "claude --autocompact 150000 --dangerously-skip-permissions"},
		{"bare binary", "claude", "150000", "claude --autocompact 150000"},
		{"auto", "claude --dangerously-skip-permissions", "auto", "claude --autocompact auto --dangerously-skip-permissions"},
		{"empty cmd unchanged", "", "150000", ""},
		// grove-142 lesson: the repo's own flag must not survive the key.
		{"strip then inject, space form", "claude --dangerously-skip-permissions --autocompact 120000", "150000", "claude --autocompact 150000 --dangerously-skip-permissions"},
		{"strip then inject, equals form", "claude --dangerously-skip-permissions --autocompact=120000", "150000", "claude --autocompact 150000 --dangerously-skip-permissions"},
		{"strip then inject, quoted form", "claude --autocompact '120000' --dangerously-skip-permissions", "auto", "claude --autocompact auto --dangerously-skip-permissions"},
		{"strip then inject, double-quoted equals form", `claude --autocompact="auto" --dangerously-skip-permissions`, "150000", "claude --autocompact 150000 --dangerously-skip-permissions"},
		{"stacks with model and effort pins", WithEffort(WithModel("claude --dangerously-skip-permissions", "opus"), "low"), "150000", "claude --autocompact 150000 --effort low --model 'opus' --dangerously-skip-permissions"},
	}
	for _, tc := range cases {
		got := WithAutocompact(tc.cmd, tc.v)
		if got != tc.want {
			t.Errorf("%s: WithAutocompact(%q, %q) = %q, want %q", tc.name, tc.cmd, tc.v, got, tc.want)
		}
		if tc.v = strings.TrimSpace(tc.v); tc.v != "" && got != "" {
			if n := strings.Count(got, "--autocompact"); n != 1 {
				t.Errorf("%s: %q carries %d --autocompact flags, want exactly one", tc.name, got, n)
			}
			if AutocompactFlag(got) != tc.v {
				t.Errorf("%s: %q reads back %q, want %q", tc.name, got, AutocompactFlag(got), tc.v)
			}
		}
	}
}

func TestAutocompactFlag(t *testing.T) {
	cases := map[string]string{
		"claude":                                           "",
		"claude --autocompact 150000":                      "150000",
		"claude --autocompact=auto --foo":                  "auto",
		"claude --autocompact '150000'":                    "150000",
		`claude --autocompact="200000"`:                    "200000",
		"claude --autocompact 100000 --autocompact 150000": "150000", // last wins, like claude's parser
		"claude --autocompaction 1":                        "",
		"claude --dangerously-skip-permissions -a":         "",
	}
	for cmd, want := range cases {
		if got := AutocompactFlag(cmd); got != want {
			t.Errorf("AutocompactFlag(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestCheckAutocompact(t *testing.T) {
	for _, ok := range []string{"", "auto", "100000", "150000", "1000000"} {
		if err := CheckAutocompact(ok); err != nil {
			t.Errorf("CheckAutocompact(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"Auto", "150k", "99999", "1000001", "-1", "0", "1.5e5", " 150000", "on"} {
		if err := CheckAutocompact(bad); err == nil {
			t.Errorf("CheckAutocompact(%q) = nil, want error", bad)
		}
	}
}

// A repo `autocompact:` key is the per-repo window every grab/adopt passes
// through; a value claude would refuse is a config load error naming the
// repo, never a launch that dies after the worktree exists.
func TestRepoAutocompactKey(t *testing.T) {
	setHome(t)
	repo := t.TempDir()
	writeGlobal(t, `
repos:
  r:
    path: `+repo+`
    autocompact: 150000
`)
	c, err := LoadAt("")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Repos["r"].Autocompact; got != "150000" {
		t.Errorf("repos.r.autocompact = %q, want 150000", got)
	}

	writeGlobal(t, `
repos:
  r:
    path: `+repo+`
    autocompact: 150k
`)
	_, err = LoadAt("")
	if err == nil || !strings.Contains(err.Error(), `repo "r": autocompact`) || !strings.Contains(err.Error(), "150k") {
		t.Errorf("autocompact: 150k loaded with err %v, want a load error naming repo r and the bad value", err)
	}
}
