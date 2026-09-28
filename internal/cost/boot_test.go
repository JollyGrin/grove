package cost

import (
	"os"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/transcript"
)

func parseBootFixture(t *testing.T) (*transcript.UsageEntry, []transcript.BootLine) {
	t.Helper()
	f, err := os.Open("../transcript/testdata/boot.jsonl")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	return transcript.ParseBoot(f)
}

func TestBootParts(t *testing.T) {
	first, lines := parseBootFixture(t)
	boot, ok := BootOf(first, lines)
	if !ok {
		t.Fatal("BootOf returned ok = false for a fixture with a billable first call")
	}

	if boot.Tokens != 8000 {
		t.Fatalf("Tokens = %d, want 8000", boot.Tokens)
	}
	if boot.Input != 5000 || boot.CacheWrite != 1000 || boot.CacheRead != 2000 {
		t.Fatalf("Input/CacheWrite/CacheRead = %d/%d/%d, want 5000/1000/2000", boot.Input, boot.CacheWrite, boot.CacheRead)
	}
	if !boot.HasImage {
		t.Error("HasImage = false, want true")
	}

	want := []BootPart{
		{Name: "tool_schemas_est", Chars: 0, EstTokens: 7968},
		{Name: "other", Chars: 39, EstTokens: 10},
		{Name: "first_prompt", Chars: 15, EstTokens: 4},
		{Name: "orchestrator_brain", Path: ".grove/orchestrator/CLAUDE.md", Chars: 15, EstTokens: 4},
		{Name: "agent_listing", Chars: 13, EstTokens: 3},
		{Name: "deferred_tools", Chars: 11, EstTokens: 3, Count: 3},
		{Name: "repo_instructions", Path: "CLAUDE.md", Chars: 10, EstTokens: 2},
		{Name: "skill_listing", Chars: 9, EstTokens: 2, Count: 3},
		{Name: "system_prompt", Chars: 10, EstTokens: 2},
		{Name: "mcp_instructions", Chars: 6, EstTokens: 1},
		{Name: "memory_index", Path: "MEMORY.md", Chars: 5, EstTokens: 1},
	}

	if len(boot.Parts) != len(want) {
		t.Fatalf("len(Parts) = %d, want %d\ngot: %+v", len(boot.Parts), len(want), boot.Parts)
	}
	for i, w := range want {
		g := boot.Parts[i]
		if g.Name != w.Name || g.Path != w.Path || g.Chars != w.Chars || g.EstTokens != w.EstTokens || g.Count != w.Count {
			t.Errorf("Parts[%d] = %+v, want %+v", i, g, w)
		}
	}
}

func TestBootResidualClamped(t *testing.T) {
	first := &transcript.UsageEntry{Input: 10, Model: "claude-sonnet-5"}
	lines := []transcript.BootLine{
		{Type: "attachment", Attachment: &transcript.BootAttachment{
			Type:         "prompt_snapshot",
			SystemPrompt: []string{"this system prompt is far bigger than the whole boot request"},
		}},
	}

	boot, ok := BootOf(first, lines)
	if !ok {
		t.Fatal("BootOf returned ok = false")
	}
	if boot.Tokens != 10 {
		t.Fatalf("Tokens = %d, want 10", boot.Tokens)
	}

	var residual *BootPart
	for i := range boot.Parts {
		if boot.Parts[i].Name == "tool_schemas_est" {
			residual = &boot.Parts[i]
		}
	}
	if residual == nil {
		t.Fatal("no tool_schemas_est part found")
	}
	if residual.EstTokens != 0 {
		t.Errorf("tool_schemas_est.EstTokens = %d, want 0 (clamped, never negative)", residual.EstTokens)
	}
}

func TestBootNoCall(t *testing.T) {
	body := `{"type":"attachment","attachment":{"type":"skill_listing","content":"ABC","skillCount":1}}
{"type":"user","message":{"content":"hello"}}
`
	first, lines := transcript.ParseBoot(strings.NewReader(body))
	boot, ok := BootOf(first, lines)
	if ok {
		t.Errorf("BootOf returned ok = true, want false for a transcript with no billable assistant line; boot = %+v", boot)
	}
}
