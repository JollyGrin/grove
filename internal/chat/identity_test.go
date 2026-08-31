package chat

// grove-222: the two sources of truth that replaced mtime guessing — a
// minted id, and the id the running agent was actually launched on.

import (
	"strings"
	"testing"
)

// A minted id must satisfy claude's own gate ("must be a valid UUID") and
// grove's shell-safety gate, and two of them must never collide.
func TestNewSessionID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id, err := NewSessionID()
		if err != nil {
			t.Fatalf("NewSessionID: %v", err)
		}
		if len(id) != 36 || strings.Count(id, "-") != 4 {
			t.Fatalf("not UUID-shaped: %q", id)
		}
		if id[14] != '4' {
			t.Errorf("want a v4 UUID, got version %q in %q", id[14], id)
		}
		if v := id[19]; v != '8' && v != '9' && v != 'a' && v != 'b' {
			t.Errorf("want the 10 variant, got %q in %q", v, id)
		}
		// The id is interpolated into the command tmux types into the pane,
		// so it must pass the same gate --resume does.
		if !ValidSessionID(id) {
			t.Errorf("a minted id must be safe to put in a launch command: %q", id)
		}
		if seen[id] {
			t.Fatalf("NewSessionID repeated itself: %q", id)
		}
		seen[id] = true
	}
}

func TestLaunchedSessionID(t *testing.T) {
	const uuid = "d07752df-bca6-4343-9da9-001696742d46"
	cases := []struct {
		name string
		args string
		want string
	}{
		{"a minted fresh chat", "claude --add-dir '/home/d/git' --session-id " + uuid, uuid},
		{"the = form", "claude --session-id=" + uuid, uuid},
		{"a revived chat", "claude --resume " + uuid, uuid},
		{"a short e2e-style id", "fakeclaude --session-id f0f0aaaa", "f0f0aaaa"},
		{"no id in the argv", "claude --dangerously-skip-permissions --add-dir /x", ""},
		{"a flag with nothing after it", "claude --session-id", ""},
		{"an empty argv", "", ""},
		// A WORKER's argv carries its whole ticket text. A ticket that quotes
		// the flag must never be read as an identity — this exact string is
		// in grove-222's own kickoff prompt.
		{"prose quoting the flag", "claude --dangerously-skip-permissions You are working on grove-222: `claude --session-id <uuid>` exists, and --resume <session-id> is #217's", ""},
		// Documented limit, not a bug to hide: a BARE id in prose does read as
		// an identity. It costs nothing here — a chat pane's claude has no
		// prompt in its argv (only workers do, and no worker is a chat row),
		// and only the pane's OWN process tree is ever walked.
		{"prose quoting a bare id", "claude -p the stamped id was --session-id " + uuid, uuid},
		{"a quoted id is not a token", "claude -p 'was --session-id " + uuid + "'", ""},
		{"two different ids is a refusal", "claude --session-id " + uuid + " --resume 4309dd45-0000-4000-8000-000000000000", ""},
		{"the same id twice is not ambiguous", "claude --session-id " + uuid + " --resume " + uuid, uuid},
	}
	for _, c := range cases {
		if got := LaunchedSessionID(c.args); got != c.want {
			t.Errorf("%s: LaunchedSessionID(%q) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

func TestParseProcs(t *testing.T) {
	out := `    PID   PPID ARGS
      1      0 /sbin/init
  40100      1 -bash
  40101  40100 claude --session-id abcd1234
garbage line
  40102  40101 node /usr/lib/node_modules/thing
`
	procs := ParseProcs(out)
	if len(procs) != 4 {
		t.Fatalf("got %d procs, want 4 (header and garbage skipped): %+v", len(procs), procs)
	}
	if procs[2].PID != 40101 || procs[2].PPID != 40100 || procs[2].Args != "claude --session-id abcd1234" {
		t.Errorf("row parsed wrong: %+v", procs[2])
	}
	if len(ParseProcs("")) != 0 {
		t.Error("no output is no procs, not a panic")
	}
}

// The pane pid is a SHELL — the launch is typed into it — so the id lives in
// a descendant, sometimes two levels down.
func TestPaneSessionID(t *testing.T) {
	const idA = "d07752df-bca6-4343-9da9-001696742d46"
	const idB = "4309dd45-1111-4111-8111-111111111111"
	procs := []Proc{
		{PID: 1, PPID: 0, Args: "/sbin/init"},
		{PID: 100, PPID: 1, Args: "-bash"},                               // pane %713's shell
		{PID: 101, PPID: 100, Args: "claude --session-id " + idA},        // its agent
		{PID: 102, PPID: 101, Args: "rg --json needle"},                  // a tool it ran
		{PID: 200, PPID: 1, Args: "-bash"},                               // another pane's shell
		{PID: 201, PPID: 200, Args: "sh -c exec claude --resume " + idB}, // wrapped in a profile
		{PID: 300, PPID: 1, Args: "-bash"},                               // a pane with no agent
	}
	if got := PaneSessionID(procs, 100); got != idA {
		t.Errorf("PaneSessionID(pane shell 100) = %q, want %q", got, idA)
	}
	if got := PaneSessionID(procs, 200); got != idB {
		t.Errorf("a revived chat's id must be readable too: %q, want %q", got, idB)
	}
	if got := PaneSessionID(procs, 300); got != "" {
		t.Errorf("a pane with no agent must answer nothing, got %q", got)
	}
	// Never look outside the pane's own tree: pid 101 belongs to pane 100.
	if got := PaneSessionID(procs, 999); got != "" {
		t.Errorf("an unknown pane pid must answer nothing, got %q", got)
	}
	if got := PaneSessionID(nil, 100); got != "" {
		t.Errorf("no process table = no answer, got %q", got)
	}
	if got := PaneSessionID(procs, 0); got != "" {
		t.Errorf("a pane with no pid must answer nothing, got %q", got)
	}
	// Two agents under one pane is not a question this can answer.
	two := append(procs, Proc{PID: 103, PPID: 100, Args: "claude --session-id " + idB})
	if got := PaneSessionID(two, 100); got != "" {
		t.Errorf("two different ids under one pane must refuse, got %q", got)
	}
	// A ps snapshot can hold a cycle (a reused pid between rows); the walk
	// must terminate rather than spin.
	cyclic := []Proc{{PID: 100, PPID: 101, Args: "-bash"}, {PID: 101, PPID: 100, Args: "claude --resume " + idA}}
	if got := PaneSessionID(cyclic, 100); got != idA {
		t.Errorf("a cyclic table must still resolve, got %q", got)
	}
}
