package tmux

import (
	"strings"
	"testing"
)

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
		ok      bool
	}{
		{"legacy cockpit orchestrator pane", "grove", 1, true},
		{"workspace cockpit orchestrator pane", "grove-unbrewed", 2, true},
		{"dashboard pane refused", "grove", 0, false},
		{"workspace dashboard pane refused", "grove-unbrewed", 0, false},
		{"non-cockpit session refused", "pr-unbrewed-p2p", 1, false},
		{"lookalike session refused", "grovething", 1, false},
	}
	for _, tc := range cases {
		err := closablePane(tc.session, tc.index)
		if tc.ok && err != nil {
			t.Errorf("%s: closablePane(%q, %d) = %v, want nil", tc.name, tc.session, tc.index, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: closablePane(%q, %d) = nil, want error", tc.name, tc.session, tc.index)
		}
	}
}
