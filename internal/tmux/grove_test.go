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

func TestClassifyDash(t *testing.T) {
	cases := []struct {
		name  string
		out   string
		id    string
		state DashStatus
	}{
		{"running dash among chats — live",
			"%1\t\tclaude\n%0\t1\tgv\n%2\t\tzsh", "%0", DashLive},
		// The q-quit shape (verified live): pane and tag survive, the dash
		// process is gone — reuse that exact pane.
		{"quit dash left its tagged shell pane — idle",
			"%1\t\tclaude\n%0\t1\tzsh", "%0", DashIdle},
		{"idle tolerates login-shell dash prefix",
			"%0\t1\t-bash\n%1\t\tclaude", "%0", DashIdle},
		{"dash pane gone entirely — missing",
			"%1\t\tclaude", "", DashMissing},
		{"empty output — missing", "", "", DashMissing},
		// Migration: a cockpit built before grove-163 has a live dash but
		// no tag — the running gv command must count, or bare `gv` stacks
		// a second dash onto it.
		{"untagged live dash matched by command",
			"%0\t\tgv\n%1\t\tclaude", "%0", DashLive},
		{"throwaway build basename matched",
			"%0\t\tgv-163\n%1\t\tclaude", "%0", DashLive},
		// A pre-163 cockpit whose dash quit has no tag AND nothing gv-ish:
		// its leftover shell pane is indistinguishable from a chat shell,
		// so the safe repair is a fresh split.
		{"untagged quit dash — missing",
			"%0\t\tzsh\n%1\t\tclaude", "", DashMissing},
		// Operator repurposed the dash pane — never type into or duplicate it.
		{"tagged pane running something else — unknown",
			"%0\t1\thtop\n%1\t\tclaude", "%0", DashUnknown},
	}
	for _, tc := range cases {
		id, state := classifyDash(tc.out)
		if id != tc.id || state != tc.state {
			t.Errorf("%s: classifyDash(%q) = (%q, %v), want (%q, %v)",
				tc.name, tc.out, id, state, tc.id, tc.state)
		}
	}
}

func TestParseFirstPane(t *testing.T) {
	cases := []struct {
		name string
		out  string
		id   string
		ok   bool
	}{
		{"single pane", "%5\t0", "%5", true},
		{"lowest index wins regardless of order", "%9\t2\n%4\t1\n%7\t3", "%4", true},
		// A dash-less cockpit's panes may start above 0 (pane 0 died with
		// the dash) — the survivor with the lowest index is the anchor.
		{"indexes need not start at zero", "%9\t1\n%7\t2", "%9", true},
		{"empty output", "", "", false},
		{"garbage skipped", "not-a-pane\n%3\tx\n%2\t0", "%2", true},
	}
	for _, tc := range cases {
		id, ok := parseFirstPane(tc.out)
		if id != tc.id || ok != tc.ok {
			t.Errorf("%s: parseFirstPane(%q) = (%q, %v), want (%q, %v)",
				tc.name, tc.out, id, ok, tc.id, tc.ok)
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
