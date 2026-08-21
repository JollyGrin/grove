package tmux

import (
	"strings"
	"testing"
)

func TestTmuxLayout(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"horizontal", "horizontal", "even-horizontal"},
		{"vertical", "vertical", "main-vertical"},
		{"tiled", "tiled", "tiled"},
		{"empty falls back to default", "", "even-horizontal"},
		{"garbage falls back to default", "garbage", "even-horizontal"},
	}
	for _, tc := range cases {
		if got := tmuxLayout(tc.in); got != tc.want {
			t.Errorf("%s: tmuxLayout(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
	// The package default must itself map to the side-by-side shape.
	if got := tmuxLayout(defaultCockpitLayout); got != "even-horizontal" {
		t.Errorf("tmuxLayout(defaultCockpitLayout) = %q, want even-horizontal", got)
	}
}

func TestParseActiveWindow(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"active is first", "1\tgrove · 63\n0\tgrove · 58", "grove · 63"},
		{"active is second", "0\tgrove · 63\n1\tgrove · 58", "grove · 58"},
		{"single window", "1\tgrove", "grove"},
		{"no active window", "0\tgrove · 63\n0\tgrove · 58", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		if got := parseActiveWindow(tc.out); got != tc.want {
			t.Errorf("%s: parseActiveWindow(%q) = %q, want %q", tc.name, tc.out, got, tc.want)
		}
	}
}

func TestWorkerWindow(t *testing.T) {
	cases := []struct {
		name      string
		repoShort string
		ticket    string
		want      string
	}{
		{"plain", "p2p", "154-undo-button", "p2p · 154-undo-button"},
		{"sanitizes dots", "pro.server", "39-x", "pro-server · 39-x"},
		{"sanitizes colons", "a:b", "1:2", "a-b · 1-2"},
	}
	for _, tc := range cases {
		if got := WorkerWindow(tc.repoShort, tc.ticket); got != tc.want {
			t.Errorf("%s: WorkerWindow(%q, %q) = %q, want %q", tc.name, tc.repoShort, tc.ticket, got, tc.want)
		}
	}
}

func TestWorkerWindowProfile(t *testing.T) {
	cases := []struct {
		name      string
		repoShort string
		ticket    string
		profile   string
		want      string
	}{
		// No profile: byte-identical to WorkerWindow (load-bearing invariant).
		{"no profile", "p2p", "154-undo-button", "", "p2p · 154-undo-button"},
		{"with profile", "p2p", "154-undo-button", "openrouter-glm", "p2p · 154-undo-button · openrouter-glm"},
		{"sanitizes profile", "p2p", "39-x", "open.router:glm", "p2p · 39-x · open-router-glm"},
	}
	for _, tc := range cases {
		got := WorkerWindowProfile(tc.repoShort, tc.ticket, tc.profile)
		if got != tc.want {
			t.Errorf("%s: WorkerWindowProfile(%q, %q, %q) = %q, want %q", tc.name, tc.repoShort, tc.ticket, tc.profile, got, tc.want)
		}
		// The no-profile path must equal WorkerWindow exactly.
		if tc.profile == "" {
			if base := WorkerWindow(tc.repoShort, tc.ticket); got != base {
				t.Errorf("%s: no-profile name %q diverged from WorkerWindow %q", tc.name, got, base)
			}
		}
	}
}

func TestPaneBorderFormat(t *testing.T) {
	// The profiled pane's tag must live in the @grove_profile user option —
	// the OSC-title-proof carrier (grove-36 T1) — and fall back to the pane
	// title when the option is unset so unprofiled panes are visually
	// unchanged. Guard the exact expansion so a refactor can't silently drop
	// the fallback (which would blank every unprofiled pane's border).
	want := "#{pane_index}: #{?#{@grove_profile},⚡ #{@grove_profile},#{pane_title}}"
	if paneBorderFormat != want {
		t.Errorf("paneBorderFormat = %q, want %q", paneBorderFormat, want)
	}
	for _, needle := range []string{"@grove_profile", "#{pane_title}", "#{pane_index}"} {
		if !strings.Contains(paneBorderFormat, needle) {
			t.Errorf("paneBorderFormat %q missing %q", paneBorderFormat, needle)
		}
	}
}

func TestClosablePane(t *testing.T) {
	cases := []struct {
		name    string
		session string
		index   int
		first   int
		ok      bool
	}{
		{"legacy cockpit orchestrator pane", "grove", 1, 0, true},
		{"workspace cockpit orchestrator pane", "grove-unbrewed", 2, 0, true},
		{"dashboard pane refused", "grove", 0, 0, false},
		{"workspace dashboard pane refused", "grove-unbrewed", 0, 0, false},
		// pane-base-index 1 (grove-168): the dashboard sits at index 1 —
		// the old `index == 0` guard silently stopped protecting it.
		{"dashboard at pane-base-index 1 refused", "grove", 1, 1, false},
		{"orchestrator above a base-1 dashboard allowed", "grove-unbrewed", 2, 1, true},
		{"non-cockpit session refused", "pr-unbrewed-p2p", 1, 0, false},
		{"lookalike session refused", "grovething", 1, 0, false},
	}
	for _, tc := range cases {
		err := closablePane(tc.session, tc.index, tc.first)
		if tc.ok && err != nil {
			t.Errorf("%s: closablePane(%q, %d, %d) = %v, want nil", tc.name, tc.session, tc.index, tc.first, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: closablePane(%q, %d, %d) = nil, want error", tc.name, tc.session, tc.index, tc.first)
		}
	}
}

func TestLowestPaneIndex(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want int
		ok   bool
	}{
		{"default base", "0\n1\n2\n", 0, true},
		{"pane-base-index 1", "1\n2\n", 1, true},
		{"unordered output", "3\n1\n2\n", 1, true},
		{"garbage skipped", "x\n2\n", 2, true},
		{"empty", "\n", 0, false},
	}
	for _, tc := range cases {
		got, ok := lowestPaneIndex(tc.out)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: lowestPaneIndex(%q) = (%d, %v), want (%d, %v)", tc.name, tc.out, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFirstPaneID(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
		ok   bool
	}{
		{"default base", "%4 0\n%7 1\n", "%4", true},
		// pane-base-index 1: the first pane is index 1 — the literal-".0"
		// assumption this helper replaces (grove-168).
		{"pane-base-index 1", "%4 1\n%7 2\n", "%4", true},
		{"unordered output", "%7 2\n%4 1\n", "%4", true},
		{"malformed lines skipped", "garbage\n%9 3\n", "%9", true},
		{"no panes", "\n", "", false},
	}
	for _, tc := range cases {
		got, ok := firstPaneID(tc.out)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: firstPaneID(%q) = (%q, %v), want (%q, %v)", tc.name, tc.out, got, ok, tc.want, tc.ok)
		}
	}
}
