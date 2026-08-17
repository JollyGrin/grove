package audit

import (
	"reflect"
	"testing"
)

const psHeader = "  PID  PPID  %CPU    RSS ELAPSED ARGS\n"

func TestDetectOrphanProcesses(t *testing.T) {
	cases := []struct {
		name  string
		ps    string
		panes string
		want  []OrphanProcess
	}{
		{
			name: "ppid=1 claude process not under any live pane is orphaned",
			ps: psHeader +
				"12345     1   4.5  20480   01:23:45 /usr/local/bin/claude mcp serve\n",
			panes: "",
			want: []OrphanProcess{
				{PID: 12345, CPUPct: 4.5, RSSKB: 20480, Elapsed: "01:23:45", Args: "/usr/local/bin/claude mcp serve"},
			},
		},
		{
			name: "ppid=1 node process referencing .claude/ is orphaned",
			ps: psHeader +
				"22222     1   0.3   1024   00:05:00 node /Users/dean/.claude/mcp-helper.js\n",
			panes: "",
			want: []OrphanProcess{
				{PID: 22222, CPUPct: 0.3, RSSKB: 1024, Elapsed: "00:05:00", Args: "node /Users/dean/.claude/mcp-helper.js"},
			},
		},
		{
			name: "live-pane descendant is NOT classified even if it looks orphan-shaped",
			ps: psHeader +
				"500     1   1.0   512   00:10:00 claude mcp serve\n",
			// 500 is (unusually) itself a live tracked pane pid — the
			// ancestry check must still exclude it.
			panes: "500\n",
			want:  nil,
		},
		{
			name: "non-claude ppid=1 node process is ignored (no path match)",
			ps: psHeader +
				"777     1   0.0   256   00:00:10 node /usr/local/bin/some-other-daemon.js\n",
			panes: "",
			want:  nil,
		},
		{
			name: "ppid != 1 claude process is ignored regardless of args",
			ps: psHeader +
				"888   100   2.0   512   00:01:00 claude mcp serve\n" +
				"100     1   0.0   128   00:20:00 -bash\n",
			panes: "",
			want:  nil,
		},
		{
			name: "node ppid=1 process matching known mcp config path is orphaned",
			ps: psHeader +
				"999     1   0.1   2048   00:02:00 node /Users/dean/Library/Application Support/mcp-config.json --serve\n",
			panes: "",
			want: []OrphanProcess{
				{PID: 999, CPUPct: 0.1, RSSKB: 2048, Elapsed: "00:02:00", Args: "node /Users/dean/Library/Application Support/mcp-config.json --serve"},
			},
		},
		{
			name: "Claude desktop app bundle process is not an orphan",
			ps: psHeader +
				"33333     1   1.2  99999   02:00:00 /Applications/Claude.app/Contents/MacOS/Claude --type=renderer\n",
			panes: "",
			want:  nil,
		},
		{
			name: "args merely containing the substring mcp (no word boundary) are not an orphan",
			ps: psHeader +
				"44444     1   0.0   128   00:01:00 tmcpipe --daemon\n" +
				"44445     1   0.0   128   00:01:00 curl https://example.com/mcprotocol/spec\n",
			panes: "",
			want:  nil,
		},
		{
			name: "a real orphaned grove worker (claude with a tracked-worktree arg) is still detected",
			ps: psHeader +
				"55555     1   3.3  40960   00:45:00 /usr/local/bin/claude --resume /Users/dean/git/.worktrees/grove/grove-42-some-task\n",
			panes: "",
			want: []OrphanProcess{
				{PID: 55555, CPUPct: 3.3, RSSKB: 40960, Elapsed: "00:45:00", Args: "/usr/local/bin/claude --resume /Users/dean/git/.worktrees/grove/grove-42-some-task"},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectOrphanProcesses(c.ps, c.panes)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("DetectOrphanProcesses() = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestDetectOrphanProcessesDescendantOfLivePane(t *testing.T) {
	// A ppid==1 suspect whose pid is a descendant (child) of a live pane's
	// pid must not be classified, even though structurally a ppid==1 row
	// has no real parent chain back to the pane — the ancestry check is
	// evaluated against the full process table regardless.
	ps := psHeader +
		"100     1   0.0   128   00:20:00 -bash\n" + // pane shell, itself reparented in this fixture
		"200   100   0.5   256   00:01:00 claude mcp serve\n"
	panes := "100\n"

	got := DetectOrphanProcesses(ps, panes)
	if got != nil {
		t.Errorf("expected no orphans (200 is a descendant of live pane 100), got %+v", got)
	}
}

func TestDetectWorktreeProcesses(t *testing.T) {
	const wt = "/Users/dean/git/.worktrees/grove/grove-15-short"
	cases := []struct {
		name      string
		ps        string
		worktrees map[string]string
		want      []WorktreeProcess
	}{
		{
			name: "any process whose argv references the worktree path matches, regardless of ppid or signature",
			ps: psHeader +
				"9001   800  99.8 819200   4-01:00:00 node /Users/dean/git/.worktrees/grove/grove-15-short/node_modules/jest-worker/build/workers/processChild.js\n",
			worktrees: map[string]string{wt: "grove-15"},
			want: []WorktreeProcess{
				{PID: 9001, CPUPct: 99.8, RSSKB: 819200, Elapsed: "4-01:00:00",
					Args:   "node /Users/dean/git/.worktrees/grove/grove-15-short/node_modules/jest-worker/build/workers/processChild.js",
					Ticket: "grove-15", Worktree: wt},
			},
		},
		{
			name: "path at end of argv matches (bare worktree path as an argument)",
			ps: psHeader +
				"9002     1   0.0   1024   00:01:00 sh -c cd /Users/dean/git/.worktrees/grove/grove-15-short\n",
			worktrees: map[string]string{wt: "grove-15"},
			want: []WorktreeProcess{
				{PID: 9002, RSSKB: 1024, Elapsed: "00:01:00",
					Args:   "sh -c cd /Users/dean/git/.worktrees/grove/grove-15-short",
					Ticket: "grove-15", Worktree: wt},
			},
		},
		{
			name: "a sibling worktree sharing the path as a string prefix does NOT match",
			ps: psHeader +
				"9003     1   0.0   1024   00:01:00 node /Users/dean/git/.worktrees/grove/grove-15-shorter-name/build.js\n" +
				"9004     1   0.0   1024   00:01:00 node /Users/dean/git/.worktrees/grove/grove-156-other/build.js\n",
			worktrees: map[string]string{wt: "grove-15"},
			want:      nil,
		},
		{
			name: "processes outside any given worktree path are never matched",
			ps: psHeader +
				"9005     1  50.0  20480   3-00:00:00 node /Users/dean/git/.worktrees/ovs/other-tool-task/processChild.js\n" +
				"9006     1   0.0   1024   00:01:00 vim /Users/dean/git/somewhere/else.txt\n",
			worktrees: map[string]string{wt: "grove-15"},
			want:      nil,
		},
		{
			name:      "empty worktree map matches nothing",
			ps:        psHeader + "9007     1   0.0   1024   00:01:00 node " + wt + "/x.js\n",
			worktrees: nil,
			want:      nil,
		},
		{
			name:      "empty path in the map is ignored (never a match-everything wildcard)",
			ps:        psHeader + "9008     1   0.0   1024   00:01:00 node /anything/at/all.js\n",
			worktrees: map[string]string{"": "grove-0"},
			want:      nil,
		},
		{
			name: "multiple worktrees: each process attributed to its ticket, output sorted by pid",
			ps: psHeader +
				"9010     1   0.0   1024   00:01:00 node /wt/b-task/serve.js\n" +
				"9009     1   0.0   2048   00:02:00 node /wt/a-task/serve.js\n",
			worktrees: map[string]string{"/wt/a-task": "grove-1", "/wt/b-task": "grove-2"},
			want: []WorktreeProcess{
				{PID: 9009, RSSKB: 2048, Elapsed: "00:02:00", Args: "node /wt/a-task/serve.js", Ticket: "grove-1", Worktree: "/wt/a-task"},
				{PID: 9010, RSSKB: 1024, Elapsed: "00:01:00", Args: "node /wt/b-task/serve.js", Ticket: "grove-2", Worktree: "/wt/b-task"},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectWorktreeProcesses(c.ps, c.worktrees)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("DetectWorktreeProcesses() = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestArgsReferencePath(t *testing.T) {
	const wt = "/wt/grove-15-short"
	cases := []struct {
		args string
		want bool
	}{
		{"node /wt/grove-15-short/x.js", true},                    // subpath
		{"sh -c cd /wt/grove-15-short && npm test", true},         // followed by space
		{"tail -f /wt/grove-15-short", true},                      // end of string
		{"node /wt/grove-15-short2/x.js", false},                  // digit extends the name
		{"node /wt/grove-15-shorter/x.js", false},                 // letters extend the name
		{"node /wt/grove-15-short.bak/x.js", false},               // dot extends the name
		{"node /wt/grove-15-short_old/x.js", false},               // underscore extends the name
		{"node /wt/grove-15-short-v2/x.js", false},                // dash extends the name
		{"cp /wt/grove-15-short-v2/a /wt/grove-15-short/b", true}, // second occurrence is a real match
		{"node /elsewhere/x.js", false},
	}
	for _, c := range cases {
		if got := argsReferencePath(c.args, wt); got != c.want {
			t.Errorf("argsReferencePath(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestParsePanePIDs(t *testing.T) {
	got := parsePanePIDs("1678\n1701\n\n74721\n")
	want := []int{1678, 1701, 74721}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsePanePIDs = %v, want %v", got, want)
	}
}

func TestIsSuspectArgs(t *testing.T) {
	cases := map[string]bool{
		"/usr/local/bin/claude mcp serve":                                true,
		"claude --version":                                               true,
		"something-mcp-server --port 1234":                               true,
		"node /Users/dean/.claude/mcp-helper.js":                         true,
		"node /usr/local/bin/some-other-daemon.js":                       false,
		"/usr/libexec/logd":                                              false,
		"my-claude-helper --run":                                         true,  // boundary before trailing '-'
		"notaclaudexprocess --run":                                       false, // no boundary right after "claude"
		"/Applications/Claude.app/Contents/MacOS/Claude --type=renderer": false, // app bundle, excluded outright
		"tmcpipe --daemon":                                               false, // "mcp" substring, no word boundary
		"curl https://example.com/mcprotocol/spec":                       false, // "mcp" substring, no word boundary
	}
	for args, want := range cases {
		if got := isSuspectArgs(args); got != want {
			t.Errorf("isSuspectArgs(%q) = %v, want %v", args, got, want)
		}
	}
}
