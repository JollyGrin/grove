package remote

import (
	"reflect"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
)

func TestExtractHost(t *testing.T) {
	cases := []struct {
		in   []string
		host string
		rest []string
	}{
		{[]string{"grove-1", "--host", "vps", "--repo", "x"}, "vps", []string{"grove-1", "--repo", "x"}},
		{[]string{"--host=vps", "--json"}, "vps", []string{"--json"}},
		{[]string{"--json"}, "", []string{"--json"}},
		{[]string{"--json", "--host"}, "", []string{"--json", "--host"}},
	}
	for _, c := range cases {
		host, rest := ExtractHost(c.in)
		if host != c.host || !reflect.DeepEqual(rest, c.rest) {
			t.Errorf("ExtractHost(%v) = %q %v, want %q %v", c.in, host, rest, c.host, c.rest)
		}
	}
}

func TestExtractHostPrefix(t *testing.T) {
	cases := []struct {
		in   []string
		host string
		rest []string
	}{
		// leading flag position: extracted
		{[]string{"--host", "pc", "grove-7", "compare with gv ls --host vps"}, "pc", []string{"grove-7", "compare with gv ls --host vps"}},
		// after the first positional the rest is relay free text: untouched
		{[]string{"grove-7", "try", "gv", "ls", "--host", "pc"}, "", []string{"grove-7", "try", "gv", "ls", "--host", "pc"}},
		{[]string{"--host=pc", "grove-7"}, "pc", []string{"grove-7"}},
		// trailing bare --host (no value) is left for the verb's parser
		{[]string{"--host"}, "", []string{"--host"}},
		// other flags share the leading region with --host
		{[]string{"--force", "--host", "pc", "grove-7"}, "pc", []string{"--force", "grove-7"}},
	}
	for _, c := range cases {
		host, rest := ExtractHostPrefix(c.in)
		if host != c.host || !reflect.DeepEqual(rest, c.rest) {
			t.Errorf("ExtractHostPrefix(%v) = %q %v, want %q %v", c.in, host, rest, c.host, c.rest)
		}
	}
}

func TestArgv(t *testing.T) {
	h := &config.Host{SSH: "dean@grove-host", GV: "/home/dean/go/bin/gv"}
	cases := []struct {
		verb string
		args []string
		want string // the remote command string (last argv element)
	}{
		{"ls", []string{"--json", "--no-pr"}, "/home/dean/go/bin/gv ls --json --no-pr"},
		{"grab", []string{"grove-1", "--repo", "x"}, "/home/dean/go/bin/gv grab grove-1 --repo x"},
		{"grab", []string{"grove-1", "--brief", "with spaces"}, "/home/dean/go/bin/gv grab grove-1 --brief 'with spaces'"},
		{"grab", []string{"grove-1", "--brief", "it's $HOME; rm -rf"}, `/home/dean/go/bin/gv grab grove-1 --brief 'it'\''s $HOME; rm -rf'`},
		{"grab", []string{"--brief", ""}, "/home/dean/go/bin/gv grab --brief ''"},
		{"answer", []string{"grove-7", "it's alive, ship it"}, `/home/dean/go/bin/gv answer grove-7 'it'\''s alive, ship it'`},
		{"nudge", []string{"grove-7", "when idle, compare with gv ls"}, "/home/dean/go/bin/gv nudge grove-7 'when idle, compare with gv ls'"},
		{"diff", []string{"grove-7", "--stat"}, "/home/dean/go/bin/gv diff grove-7 --stat"},
		{"pause", []string{"grove-7", "--force"}, "/home/dean/go/bin/gv pause grove-7 --force"},
		{"untrack", []string{"grove-7", "--rm"}, "/home/dean/go/bin/gv untrack grove-7 --rm"},
		{"ls", []string{"--json"}, "/home/dean/go/bin/gv ls --json"},
	}
	for _, c := range cases {
		got := Argv(h, c.verb, c.args)
		prefix := []string{"ssh", "-o", "BatchMode=yes", "dean@grove-host", "--"}
		if !reflect.DeepEqual(got[:5], prefix) || len(got) != 6 {
			t.Fatalf("Argv(%s %v) = %v, want prefix %v + one command string", c.verb, c.args, got, prefix)
		}
		if got[5] != c.want {
			t.Errorf("Argv(%s %v) remote cmd = %q, want %q", c.verb, c.args, got[5], c.want)
		}
	}
	// default gv
	if got := Argv(&config.Host{SSH: "h", GV: "gv"}, "ls", nil); got[5] != "gv ls" {
		t.Errorf("bare gv: %q", got[5])
	}
}

func TestRunRejectsUnknownHostAndVerb(t *testing.T) {
	cfg := &config.Config{Hosts: map[string]*config.Host{"a": {SSH: "a", GV: "gv"}, "b": {SSH: "b", GV: "gv"}}}
	_, err := Run(cfg, "nope", "ls", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "a, b") {
		t.Errorf("unknown host err = %v", err)
	}
	_, err = Run(cfg, "a", "done", nil, nil, nil) // done is terminal-state territory — never passes through
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("unsupported verb err = %v", err)
	}
}

// NewOpID (grove-186): 32 lowercase hex chars, unguessable, unique per
// mint — the id a retried relayed hop dedups on.
func TestNewOpID(t *testing.T) {
	id := NewOpID()
	if len(id) != 32 {
		t.Fatalf("NewOpID len = %d (%q), want 32", len(id), id)
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("NewOpID = %q, want lowercase hex only", id)
		}
	}
	if id == NewOpID() {
		t.Fatal("two minted op ids collided")
	}
}

func TestExtractOpIDPrefix(t *testing.T) {
	cases := []struct {
		in   []string
		opID string
		rest []string
	}{
		// leading flag position: extracted
		{[]string{"--op-id", "abc", "grove-7", "keep going"}, "abc", []string{"grove-7", "keep going"}},
		{[]string{"--op-id=abc", "grove-7"}, "abc", []string{"grove-7"}},
		// after the first positional the rest is relay free text: untouched
		{[]string{"grove-7", "try", "--op-id", "x"}, "", []string{"grove-7", "try", "--op-id", "x"}},
		// no flags at all
		{[]string{"grove-7", "hi"}, "", []string{"grove-7", "hi"}},
		// trailing bare --op-id (no value) is left for the verb's parser
		{[]string{"--op-id"}, "", []string{"--op-id"}},
		// other leading flags share the region with --op-id
		{[]string{"--force", "--op-id", "abc", "grove-7"}, "abc", []string{"--force", "grove-7"}},
	}
	for _, c := range cases {
		opID, rest := ExtractOpIDPrefix(c.in)
		if opID != c.opID || !reflect.DeepEqual(rest, c.rest) {
			t.Errorf("ExtractOpIDPrefix(%v) = %q %v, want %q %v", c.in, opID, rest, c.opID, c.rest)
		}
	}
}

// TestChatAttachRoundTrip pins the one line both halves of `gv
// orchestrator new --host` agree on (grove-198): the receiving half prints
// it as a paste-able attach command, the relaying half parses the session
// name back out of it to render the ssh form.
func TestChatAttachRoundTrip(t *testing.T) {
	line := ChatAttachLine("grove-chat-unbrewed-3")
	if line != "attach: tmux attach -t =grove-chat-unbrewed-3" {
		t.Fatalf("ChatAttachLine = %q", line)
	}
	out := "✓ orchestrator chat grove-chat-unbrewed-3 — workspace unbrewed\n" + line + "\n"
	if got := ParseChatSession(out); got != "grove-chat-unbrewed-3" {
		t.Fatalf("ParseChatSession = %q, want the session name", got)
	}
	// Output from a failed spawn (or an older remote gv) carries no line —
	// the relaying half must print no attach hint rather than a wrong one.
	if got := ParseChatSession("gv: no workspace 'x' on @pc\n"); got != "" {
		t.Fatalf("ParseChatSession(no attach line) = %q, want \"\"", got)
	}
}

// TestParseChatSessionSpansBothHops: the relaying half tees ONE buffer
// across both ssh hops, so a first hop that spawned the chat and then had
// ssh die mid-write leaves a truncated attach line in front of the
// retry's complete one. The complete one must win — the parsed name is
// printed as a command for the operator to paste, so a plausible-looking
// wrong session is worse than none.
func TestParseChatSessionSpansBothHops(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{
			"truncated first hop, complete retry",
			"✓ orchestrator chat grove-chat-unbrewed-3 — workspace unbrewed\n" +
				"attach: tmux attach -t =grove-cha\n" +
				"✓ already applied (op abc) — orchestrator chat grove-chat-unbrewed-3 in workspace unbrewed\n" +
				"attach: tmux attach -t =grove-chat-unbrewed-3\n",
			"grove-chat-unbrewed-3",
		},
		{
			// No newline after the truncation: the retry's first line welds
			// onto it, and the whole welded line must be rejected.
			"truncation welded onto the retry's output",
			"attach: tmux attach -t =grove-cha✓ already applied (op abc)\n" +
				"attach: tmux attach -t =grove-chat-unbrewed-12\n",
			"grove-chat-unbrewed-12",
		},
		{
			// Both hops complete (a 255 that fired after the full write):
			// they name the same session, and the last is still right.
			"two complete lines",
			"attach: tmux attach -t =grove-chat-unbrewed-3\nattach: tmux attach -t =grove-chat-unbrewed-3\n",
			"grove-chat-unbrewed-3",
		},
		{
			// Nothing believable at all: no hint beats a wrong one.
			"only a truncated line",
			"attach: tmux attach -t =grove-cha",
			"",
		},
		{
			"session number lost to the truncation",
			"attach: tmux attach -t =grove-chat-unbrewed-\n",
			"",
		},
	}
	for _, c := range cases {
		if got := ParseChatSession(c.out); got != c.want {
			t.Errorf("%s: ParseChatSession = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestOrchestratorIsSupported: the verb relays, and the friendly
// supported-list error names it.
func TestOrchestratorIsSupported(t *testing.T) {
	if !Supported["orchestrator"] {
		t.Fatal("orchestrator must be a --host verb (grove-198)")
	}
	if !strings.Contains(SupportedList, "orchestrator new") {
		t.Fatalf("SupportedList = %q, want it to mention `orchestrator new`", SupportedList)
	}
}
