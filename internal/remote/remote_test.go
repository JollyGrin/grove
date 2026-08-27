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
