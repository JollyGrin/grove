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
	_, err = Run(cfg, "a", "adopt", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("unsupported verb err = %v", err)
	}
}
