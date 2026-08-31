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
	// grove-199 nests a REMOTE arm in front of it (@grove_remote, the same
	// OSC-proof carrier): a remote chat pane reads "@<host> · <profile>",
	// falling back to "chat" for the host's own Claude, because an ssh-attach
	// pane's own title belongs to the ssh client, not the agent.
	want := "#{pane_index}: #{?#{@grove_remote},@#{@grove_remote} · #{?#{@grove_profile},#{@grove_profile},chat}," +
		"#{?#{@grove_profile},⚡ #{@grove_profile},#{pane_title}}}"
	if paneBorderFormat != want {
		t.Errorf("paneBorderFormat = %q, want %q", paneBorderFormat, want)
	}
	for _, needle := range []string{"@grove_profile", "@grove_remote", "#{pane_title}", "#{pane_index}"} {
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
		// cockpit is what the injected registry check answers for this
		// session name — the ONLY thing that can tell a cockpit from a
		// chat session, since the two name shapes overlap.
		cockpit bool
		ok      bool
	}{
		{"legacy cockpit orchestrator pane", "grove", 1, 0, true, true},
		{"workspace cockpit orchestrator pane", "grove-unbrewed", 2, 0, true, true},
		{"dashboard pane refused", "grove", 0, 0, true, false},
		{"workspace dashboard pane refused", "grove-unbrewed", 0, 0, true, false},
		// pane-base-index 1 (grove-168): the dashboard sits at index 1 —
		// the old `index == 0` guard silently stopped protecting it.
		{"dashboard at pane-base-index 1 refused", "grove", 1, 1, true, false},
		{"orchestrator above a base-1 dashboard allowed", "grove-unbrewed", 2, 1, true, true},
		{"non-cockpit session refused", "pr-unbrewed-p2p", 1, 0, false, false},
		{"lookalike session refused", "grovething", 1, 0, false, false},
		// grove-199: a detached chat session (grove-198) has no dashboard —
		// its single pane IS the orchestrator, and its seeded brain instructs
		// `gv orchestrator close` for dispatch-and-dismiss. The first-pane
		// rule would strand that claude process alive forever.
		{"chat session's only pane closable", "grove-chat-unbrewed-1", 0, 0, false, true},
		{"chat session under pane-base-index 1 closable", "grove-chat-unbrewed-2", 1, 1, false, true},
		// …but the chat SHAPE is not proof: labels are [a-z0-9][a-z0-9_-]*,
		// so a workspace labelled `chat-app` has cockpit session
		// `grove-chat-app` and one labelled `chat-app-2` has
		// `grove-chat-app-2` — indistinguishable from chat 2 of `chat-app`
		// by name alone. The registry decides, and a registered label means
		// COCKPIT: its dashboard stays protected.
		{"true collision: workspace labelled chat-app", "grove-chat-app", 0, 0, true, false},
		{"true collision under pane-base-index 1", "grove-chat-app", 1, 1, true, false},
		{"true collision: label chat-app-2 vs chat 2 of chat-app", "grove-chat-app-2", 0, 0, true, false},
		{"a second pane in a colliding cockpit is still closable", "grove-chat-app", 2, 0, true, true},
		// The near-miss that is NOT a collision: `grove-chatty` is
		// grove-chat + ty, no separating hyphen, so it never reads as a
		// chat session in the first place.
		{"near-miss cockpit dashboard refused", "grove-chatty", 0, 0, true, false},
	}
	for _, tc := range cases {
		err := closablePane(tc.session, tc.index, tc.first, func(string) bool { return tc.cockpit })
		if tc.ok && err != nil {
			t.Errorf("%s: closablePane(%q, %d, %d) = %v, want nil", tc.name, tc.session, tc.index, tc.first, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: closablePane(%q, %d, %d) = nil, want error", tc.name, tc.session, tc.index, tc.first)
		}
	}
}

// An uninjected guard (nil CockpitCheck) treats EVERY session as a cockpit:
// a caller that forgot to resolve the registry can only be over-protective,
// never the reverse. The chat's own pane is then refused — the operator
// closes it by hand — but no dashboard is ever at risk.
func TestClosablePaneNilCheckFailsSafe(t *testing.T) {
	if err := closablePane("grove-chat-unbrewed-1", 0, 0, nil); err == nil {
		t.Error("nil CockpitCheck: chat pane closed without a registry answer — the guard must fail safe")
	}
	if err := closablePane("grove-unbrewed", 2, 0, nil); err != nil {
		t.Errorf("nil CockpitCheck must not refuse a non-first pane: %v", err)
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

// TestNextChatSession pins the detached-chat numbering (grove-198): the
// lowest free grove-chat-<label>-<n>, per workspace, ignoring every other
// session on the server (cockpits, other workspaces' chats).
func TestNextChatSession(t *testing.T) {
	cases := []struct {
		name     string
		label    string
		existing []string
		want     string
	}{
		{"fresh server", "unbrewed", nil, "grove-chat-unbrewed-1"},
		{"unrelated sessions only", "unbrewed", []string{"grove-unbrewed", "grove", "pr-x"}, "grove-chat-unbrewed-1"},
		{"one taken", "unbrewed", []string{"grove-chat-unbrewed-1"}, "grove-chat-unbrewed-2"},
		{"gap is reused", "unbrewed", []string{"grove-chat-unbrewed-2"}, "grove-chat-unbrewed-1"},
		{"another workspace's chats don't count", "unbrewed", []string{"grove-chat-grid-1", "grove-chat-grid-2"}, "grove-chat-unbrewed-1"},
		{"prefix lookalike label", "unbrewed", []string{"grove-chat-unbrewed-x-1"}, "grove-chat-unbrewed-1"},
	}
	for _, c := range cases {
		if got := NextChatSession(c.label, c.existing); got != c.want {
			t.Errorf("%s: NextChatSession(%q, %v) = %q, want %q", c.name, c.label, c.existing, got, c.want)
		}
	}
}

// ParseChatSessions is what `gv audit` reports and `gv park --chats` kills,
// so its two filters carry weight: the name shape, and the REGISTRY tie-break
// for the `grove-chat-<label>-<n>` / `grove-<label>` collision (grove-203).
func TestParseChatSessions(t *testing.T) {
	// pane_pid, pane_current_command, session_attached, session_created
	row := func(name, pid, cmd, attached, created string) string {
		return strings.Join([]string{name, pid, cmd, attached, created}, "\t")
	}
	out := strings.Join([]string{
		row("grove-unbrewed", "100", "gv", "1", "1700000000"), // the cockpit itself
		row("grove-chat-unbrewed-2", "202", "claude", "0", "1700000200"),
		row("grove-chat-unbrewed-1", "201", "node", "1", "1700000100"),
		row("grove-chat-unbrewed-1", "999", "bash", "1", "1700000100"), // a split pane: one row per session
		row("grove-chat-unbrewed-10", "210", "claude", "0", "1700001000"),
		row("grove-chat-other-1", "301", "claude", "0", "1700000300"), // another workspace's chat
		row("grove-chat-unbrewed-x", "400", "claude", "0", "1700000400"),
		row("grove-chat-unbrewed-", "401", "claude", "0", "1700000400"),
		row("grove-chat-unbrewedextra-1", "402", "claude", "0", "1700000400"),
		row("pr-unbrewed-p2p", "500", "claude", "0", "1700000500"),
		"malformed-without-tabs",
	}, "\n")

	never := CockpitCheck(func(string) bool { return false })
	got := ParseChatSessions(out, "unbrewed", never)
	want := []string{"grove-chat-unbrewed-1", "grove-chat-unbrewed-2", "grove-chat-unbrewed-10"}
	if len(got) != len(want) {
		t.Fatalf("got %d chats %v, want %v", len(got), got, want)
	}
	for i, w := range want {
		if got[i].Session != w {
			t.Errorf("chat %d = %q, want %q (numeric order, not lexical)", i, got[i].Session, w)
		}
	}
	if got[0].PID != 201 || got[0].Command != "node" || !got[0].Attached {
		t.Errorf("first chat = %+v, want pid 201 / node / attached (the FIRST pane row wins)", got[0])
	}
	if got[1].Attached {
		t.Errorf("session_attached 0 must read detached: %+v", got[1])
	}
	if got[0].Created.Unix() != 1700000100 {
		t.Errorf("created = %v, want the session_created epoch", got[0].Created)
	}

	// The collision (grove-199's rule, applied to killing rather than
	// closing): a workspace labelled `unbrewed-2` owns cockpit session
	// `grove-unbrewed-2`, but a label like `chat-unbrewed-2` owns
	// `grove-chat-unbrewed-2` — the exact shape of chat 2 of `unbrewed`.
	// Registered ⇒ cockpit ⇒ never a chat, or park --chats would kill a
	// dashboard and every worker under it.
	registered := CockpitCheck(func(s string) bool { return s == "grove-chat-unbrewed-2" })
	got = ParseChatSessions(out, "unbrewed", registered)
	for _, c := range got {
		if c.Session == "grove-chat-unbrewed-2" {
			t.Fatalf("a REGISTERED cockpit must never be reported as a chat: %v", got)
		}
	}
	if len(got) != 2 {
		t.Errorf("got %d chats, want 2 with the cockpit reading excluded", len(got))
	}

	// A nil check answers "cockpit" to everything: an uninjected caller
	// under-reports rather than over-kills.
	if got := ParseChatSessions(out, "unbrewed", nil); len(got) != 0 {
		t.Errorf("nil CockpitCheck must yield no chats, got %v", got)
	}
	// No label = the legacy global layer, which owns no chats.
	if got := ChatSessions("", never); got != nil {
		t.Errorf("empty label must yield no chats, got %v", got)
	}
}

// ParsePanes is the single parser every chat-shaped report is built from
// (grove-215). The field ORDER is a compatibility contract: grove-203's
// five fields first, the identity fields appended — a short line (an old
// tmux, or the trim eating an EMPTY trailing stamp) must degrade to zero
// values, never drop the pane.
func TestParsePanes(t *testing.T) {
	full := strings.Join([]string{
		// session, pid, cmd, attached, created, pane, index, dir, @grove_chat_session
		strings.Join([]string{"grove-chat-unbrewed-1", "201", "claude", "0", "1700000100", "%7", "1", "/ws/.grove/orchestrator", "eeeb-1111"}, "\t"),
		// unstamped: the trailing empty field is trimmed away by the line trim
		strings.Join([]string{"grove-chat-unbrewed-2", "202", "node", "1", "1700000200", "%8", "1", "/ws/.grove/orchestrator", ""}, "\t"),
		// grove-203-era line (five fields only): still a pane
		strings.Join([]string{"grove-unbrewed", "100", "gv", "1", "1700000000"}, "\t"),
		"",
		"malformed-without-tabs",
	}, "\n")

	panes := ParsePanes(full)
	if len(panes) != 3 {
		t.Fatalf("got %d panes, want 3: %+v", len(panes), panes)
	}
	if panes[0].Pane != "%7" || panes[0].Dir != "/ws/.grove/orchestrator" || panes[0].ChatSession != "eeeb-1111" {
		t.Errorf("stamped pane wrong: %+v", panes[0])
	}
	if panes[0].Index != 1 || panes[0].PID != 201 || panes[0].Command != "claude" || panes[0].Attached {
		t.Errorf("grove-203's first five fields must keep their meaning: %+v", panes[0])
	}
	if panes[0].Created.Unix() != 1700000100 {
		t.Errorf("created = %v, want the session_created epoch", panes[0].Created)
	}
	if panes[1].ChatSession != "" || panes[1].Pane != "%8" {
		t.Errorf("an unstamped pane must still carry its pane id: %+v", panes[1])
	}
	if !panes[1].Attached {
		t.Errorf("session_attached 1 must read attached: %+v", panes[1])
	}
	if panes[2].Pane != "" || panes[2].Dir != "" || panes[2].ChatSession != "" {
		t.Errorf("a short line must degrade to zero values, not drop: %+v", panes[2])
	}
	if ParsePanes("") != nil {
		t.Error("no output must yield no panes")
	}
}

// The identity fields ride out on ChatSession too (grove-215) — `gv chat
// ls` resolves ids per PANE and stamps the pane id it is handed here.
func TestChatSessionsInCarriesIdentity(t *testing.T) {
	out := strings.Join([]string{
		strings.Join([]string{"grove-chat-unbrewed-2", "202", "claude", "0", "1700000200", "%8", "1", "/ws/.grove/orchestrator/glm", "bbbb"}, "\t"),
		strings.Join([]string{"grove-chat-unbrewed-1", "201", "claude", "1", "1700000100", "%7", "1", "/ws/.grove/orchestrator", ""}, "\t"),
	}, "\n")
	got := ChatSessionsIn(ParsePanes(out), "unbrewed", CockpitCheck(func(string) bool { return false }))
	if len(got) != 2 {
		t.Fatalf("got %d chats, want 2: %+v", len(got), got)
	}
	if got[0].N != 1 || got[0].Pane != "%7" || got[0].Dir != "/ws/.grove/orchestrator" || got[0].SessionID != "" {
		t.Errorf("chat 1 = %+v, want n=1 / %%7 / the brain dir / unstamped", got[0])
	}
	if got[1].N != 2 || got[1].Pane != "%8" || got[1].SessionID != "bbbb" {
		t.Errorf("chat 2 = %+v, want n=2 / %%8 / its stamp", got[1])
	}
	if got[1].Dir != "/ws/.grove/orchestrator/glm" {
		t.Errorf("a profiled chat must report its OWN cwd (its project dir): %+v", got[1])
	}
}
