package cost

import (
	"testing"

	"github.com/JollyGrin/grove/internal/sub"
	"github.com/JollyGrin/grove/internal/transcript"
)

func TestClassifyTable(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		input map[string]any
		want  GrowthBucket
	}{
		{"read whole", "Read", map[string]any{"file_path": "x.go"}, BReadWhole},
		{"read targeted (offset)", "Read", map[string]any{"file_path": "x.go", "offset": 36.0}, BReadTargeted},
		{"read targeted (limit)", "Read", map[string]any{"file_path": "x.go", "limit": 100.0}, BReadTargeted},
		{"mcp tool", "mcp__linear__list_issues", nil, BMCP},
		{"agent", "Agent", map[string]any{"description": "explore"}, BAgent},
		{"task", "Task", nil, BAgent},
		{"edit", "Edit", map[string]any{"file_path": "x.go"}, BEdit},
		{"write", "Write", map[string]any{"file_path": "x.go"}, BEdit},
		{"multiedit", "MultiEdit", map[string]any{"file_path": "x.go"}, BEdit},
		{"bash build test", "Bash", map[string]any{"command": "go test ./..."}, BBashBuildTest},
		{"bash e2e script", "Bash", map[string]any{"command": "e2e/dummy.sh"}, BBashBuildTest},
		{"bash git readonly", "Bash", map[string]any{"command": "git status"}, BBashGit},
		{"bash git diff", "Bash", map[string]any{"command": "git diff HEAD"}, BBashGit},
		{"bash gh", "Bash", map[string]any{"command": "gh pr view --json state,mergedAt"}, BBashGh},
		{"bash gv json", "Bash", map[string]any{"command": "gv ls --json --no-pr"}, BBashGv},
		{"bash sed read", "Bash", map[string]any{"command": "sed -n 36,100p internal/x.go"}, BBashRead},
		{"bash cat read", "Bash", map[string]any{"command": "cat internal/x.go"}, BBashRead},
		{"bash other", "Bash", map[string]any{"command": "echo hi"}, BOther},
		{"unknown tool", "Glob", map[string]any{"pattern": "*.go"}, BOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.tool, tc.input); got != tc.want {
				t.Errorf("Classify(%q, %v) = %q, want %q", tc.tool, tc.input, got, tc.want)
			}
		})
	}
}

func usageLine(msgID, reqID string, input int) transcript.Line {
	return transcript.Line{
		Type: "assistant", MessageID: msgID, RequestID: reqID, Model: "claude-sonnet-5",
		Usage: &transcript.UsageEntry{MessageID: msgID, RequestID: reqID, Model: "claude-sonnet-5", Input: input},
	}
}

// TestContextAmplification: three calls total, one 1,000-char Read tool
// result lands right after call 1 — two more calls each pay to carry it.
func TestContextAmplification(t *testing.T) {
	lines := []transcript.Line{
		func() transcript.Line {
			l := usageLine("m1", "r1", 1000)
			l.Blocks = []transcript.Block{{Kind: "tool_use", Name: "Read", ToolUseID: "t1", Input: map[string]any{"file_path": "internal/x.go"}}}
			return l
		}(),
		{Type: "user", Blocks: []transcript.Block{{Kind: "tool_result", Name: "Read", ToolUseID: "t1", Chars: 1000}}},
		usageLine("m2", "r2", 500),
		usageLine("m3", "r3", 500),
	}
	rep := Context(lines, nil, 10)
	if rep.APICalls != 3 {
		t.Fatalf("api calls = %d, want 3", rep.APICalls)
	}
	var found *Amplified
	for i := range rep.Top {
		if rep.Top[i].Bucket == BReadWhole {
			found = &rep.Top[i]
		}
	}
	if found == nil {
		t.Fatal("no read_whole entry in Top")
	}
	if found.CallsAfter != 2 {
		t.Errorf("CallsAfter = %d, want 2", found.CallsAfter)
	}
	if found.EstTokens != 1052 {
		t.Errorf("EstTokens = %d, want 1052", found.EstTokens)
	}
}

// TestContextDedupMultiBlock: two JSONL lines sharing one message id are
// one logical API call, not two.
func TestContextDedupMultiBlock(t *testing.T) {
	lines := []transcript.Line{
		func() transcript.Line {
			l := usageLine("m1", "r1", 100)
			l.Blocks = []transcript.Block{{Kind: "text", Chars: 10}}
			return l
		}(),
		func() transcript.Line {
			l := usageLine("m1", "r1", 100)
			l.Blocks = []transcript.Block{{Kind: "tool_use", Name: "Read", ToolUseID: "t1", Input: map[string]any{"file_path": "x.go"}}}
			return l
		}(),
	}
	rep := Context(lines, nil, 10)
	if rep.APICalls != 1 {
		t.Errorf("api calls = %d, want 1", rep.APICalls)
	}
}

// TestContextFloorNotWeighted: content emitted before the first call (the
// floor — system prompt, tool schemas) contributes no growth weight.
func TestContextFloorNotWeighted(t *testing.T) {
	lines := []transcript.Line{
		{Type: "user", Blocks: []transcript.Block{{Kind: "text", Chars: 5000}}},
		func() transcript.Line {
			l := usageLine("m1", "r1", 1234)
			l.Blocks = []transcript.Block{{Kind: "text", Chars: 10}}
			return l
		}(),
	}
	rep := Context(lines, nil, 10)
	if rep.Floor != 1234 {
		t.Errorf("floor = %d, want 1234 (call 1's own ctx tokens)", rep.Floor)
	}
	if len(rep.Top) != 1 || rep.Top[0].Chars != 10 {
		t.Fatalf("top = %+v, want exactly the post-call-1 text block (the 5,000-char floor block must be excluded)", rep.Top)
	}
}

func TestContextCompactions(t *testing.T) {
	lines := []transcript.Line{
		usageLine("m1", "r1", 139163),
		{Type: "system", Subtype: "compact_boundary", Compact: &transcript.Compact{Trigger: "manual", PreTokens: 139163, PostTokens: 6220}},
		usageLine("m2", "r2", 6220),
	}
	rep := Context(lines, nil, 10)
	if len(rep.Compactions) != 1 {
		t.Fatalf("compactions = %+v, want 1 record", rep.Compactions)
	}
	c := rep.Compactions[0]
	if c.Trigger != "manual" || c.PreTokens != 139163 || c.PostTokens != 6220 {
		t.Errorf("compaction = %+v", c)
	}
	if c.AtCall != 1 {
		t.Errorf("compaction AtCall = %d, want 1 (after call 1, before call 2)", c.AtCall)
	}
}

func TestContextDelegationJoin(t *testing.T) {
	subs := []sub.Record{
		{Ticket: "grove-289", Mode: "raw", Exit: 0, InputChars: 10, OutputTokens: 5, Ms: 100},
		{Ticket: "grove-289", Mode: "agentic", Exit: 1, InputChars: 20, OutputTokens: 8, Ms: 200},
	}
	rep := Context(nil, subs, 10)
	want := Delegation{Calls: 2, Raw: 1, Agentic: 1, Failures: 1, InputChars: 30, OutputTokens: 13, Millis: 300}
	if rep.Delegation != want {
		t.Errorf("delegation = %+v, want %+v", rep.Delegation, want)
	}
}

func TestContextHeavyFlags(t *testing.T) {
	if ContextHeavy(199_999) {
		t.Error("199,999 avg ctx flagged heavy")
	}
	if !ContextHeavy(200_000) {
		t.Error("200,000 avg ctx not flagged heavy")
	}
	if NeverCompacted(149, 0) {
		t.Error("149 calls flagged never-compacted (threshold is 150)")
	}
	if !NeverCompacted(150, 0) {
		t.Error("150 calls, 0 compactions not flagged never-compacted")
	}
	if NeverCompacted(150, 1) {
		t.Error("150 calls with 1 compaction flagged never-compacted")
	}
}
