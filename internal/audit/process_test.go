package audit

import (
	"reflect"
	"testing"
)

const psHeader = "  PID  PPID  %CPU ELAPSED ARGS\n"

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
				"12345     1   4.5   01:23:45 /usr/local/bin/claude mcp serve\n",
			panes: "",
			want: []OrphanProcess{
				{PID: 12345, CPUPct: 4.5, Elapsed: "01:23:45", Args: "/usr/local/bin/claude mcp serve"},
			},
		},
		{
			name: "ppid=1 node process referencing .claude/ is orphaned",
			ps: psHeader +
				"22222     1   0.3   00:05:00 node /Users/dean/.claude/mcp-helper.js\n",
			panes: "",
			want: []OrphanProcess{
				{PID: 22222, CPUPct: 0.3, Elapsed: "00:05:00", Args: "node /Users/dean/.claude/mcp-helper.js"},
			},
		},
		{
			name: "live-pane descendant is NOT classified even if it looks orphan-shaped",
			ps: psHeader +
				"500     1   1.0   00:10:00 claude mcp serve\n",
			// 500 is (unusually) itself a live tracked pane pid — the
			// ancestry check must still exclude it.
			panes: "500\n",
			want:  nil,
		},
		{
			name: "non-claude ppid=1 node process is ignored (no path match)",
			ps: psHeader +
				"777     1   0.0   00:00:10 node /usr/local/bin/some-other-daemon.js\n",
			panes: "",
			want:  nil,
		},
		{
			name: "ppid != 1 claude process is ignored regardless of args",
			ps: psHeader +
				"888   100   2.0   00:01:00 claude mcp serve\n" +
				"100     1   0.0   00:20:00 -bash\n",
			panes: "",
			want:  nil,
		},
		{
			name: "node ppid=1 process matching known mcp config path is orphaned",
			ps: psHeader +
				"999     1   0.1   00:02:00 node /Users/dean/Library/Application Support/mcp-config.json --serve\n",
			panes: "",
			want: []OrphanProcess{
				{PID: 999, CPUPct: 0.1, Elapsed: "00:02:00", Args: "node /Users/dean/Library/Application Support/mcp-config.json --serve"},
			},
		},
		{
			name: "Claude desktop app bundle process is not an orphan",
			ps: psHeader +
				"33333     1   1.2   02:00:00 /Applications/Claude.app/Contents/MacOS/Claude --type=renderer\n",
			panes: "",
			want:  nil,
		},
		{
			name: "args merely containing the substring mcp (no word boundary) are not an orphan",
			ps: psHeader +
				"44444     1   0.0   00:01:00 tmcpipe --daemon\n" +
				"44445     1   0.0   00:01:00 curl https://example.com/mcprotocol/spec\n",
			panes: "",
			want:  nil,
		},
		{
			name: "a real orphaned grove worker (claude with a tracked-worktree arg) is still detected",
			ps: psHeader +
				"55555     1   3.3   00:45:00 /usr/local/bin/claude --resume /Users/dean/git/.worktrees/grove/grove-42-some-task\n",
			panes: "",
			want: []OrphanProcess{
				{PID: 55555, CPUPct: 3.3, Elapsed: "00:45:00", Args: "/usr/local/bin/claude --resume /Users/dean/git/.worktrees/grove/grove-42-some-task"},
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
		"100     1   0.0   00:20:00 -bash\n" + // pane shell, itself reparented in this fixture
		"200   100   0.5   00:01:00 claude mcp serve\n"
	panes := "100\n"

	got := DetectOrphanProcesses(ps, panes)
	if got != nil {
		t.Errorf("expected no orphans (200 is a descendant of live pane 100), got %+v", got)
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
