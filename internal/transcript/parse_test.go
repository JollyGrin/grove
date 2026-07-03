package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractMessageText_String(t *testing.T) {
	raw := []byte(`{"role":"user","content":"Fix the tooltip positioning bug"}`)
	short, full := extractMessageText(raw)
	if short != "Fix the tooltip positioning bug" {
		t.Errorf("short = %q", short)
	}
	if full != "Fix the tooltip positioning bug" {
		t.Errorf("full = %q", full)
	}
}

func TestExtractMessageText_Array(t *testing.T) {
	raw := []byte(`{"role":"user","content":[{"type":"text","text":"Add TGS tooltip component"}]}`)
	short, full := extractMessageText(raw)
	if short != "Add TGS tooltip component" {
		t.Errorf("short = %q", short)
	}
	if full != "Add TGS tooltip component" {
		t.Errorf("full = %q", full)
	}
}

func TestExtractMessageText_Empty(t *testing.T) {
	short, full := extractMessageText(nil)
	if short != "" {
		t.Errorf("short = %q, want empty", short)
	}
	if full != "" {
		t.Errorf("full = %q, want empty", full)
	}
}

func TestTruncate(t *testing.T) {
	short := "hello"
	if got := truncate(short, 80); got != "hello" {
		t.Errorf("got %q", got)
	}

	long := "This is a very long message that definitely exceeds the eighty character limit we set for display purposes here"
	got := truncate(long, 80)
	if len([]rune(got)) != 80 {
		t.Errorf("expected 80 runes, got %d: %q", len([]rune(got)), got)
	}
	if got[len(got)-3:] != "..." {
		t.Errorf("expected trailing ..., got %q", got)
	}
}

func TestTruncate_Newlines(t *testing.T) {
	s := "line one\nline two"
	got := truncate(s, 80)
	if got != "line one line two" {
		t.Errorf("got %q", got)
	}
}

func TestPathsMatch(t *testing.T) {
	if !pathsMatch("/foo/bar", "/foo/bar") {
		t.Error("identical paths should match")
	}
	if !pathsMatch("/foo/bar/", "/foo/bar") {
		t.Error("trailing slash should match")
	}
	if pathsMatch("/foo/bar", "/foo/baz") {
		t.Error("different paths should not match")
	}
}

func TestParseJSONLMeta(t *testing.T) {
	dir := t.TempDir()
	worktreePath := "/Users/grins/git/thegrid/.trees/dev-1301"

	// Real Claude JSONL format: message is {role, content}
	content := `{"type":"system","cwd":"/Users/grins/git/thegrid/.trees/dev-1301","gitBranch":"dev-1301","content":"init"}
{"type":"user","cwd":"/Users/grins/git/thegrid/.trees/dev-1301","message":{"role":"user","content":"Fix the tooltip positioning bug"}}
{"type":"assistant","cwd":"/Users/grins/git/thegrid/.trees/dev-1301","message":{"role":"assistant","content":"I'll help fix that."}}
`
	path := filepath.Join(dir, "abc123.jsonl")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	meta, err := parseJSONLMeta(path, worktreePath)
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if meta.ID != "abc123" {
		t.Errorf("ID = %q, want abc123", meta.ID)
	}
	if meta.CWD != worktreePath {
		t.Errorf("CWD = %q", meta.CWD)
	}
	if meta.FirstPrompt != "Fix the tooltip positioning bug" {
		t.Errorf("FirstPrompt = %q", meta.FirstPrompt)
	}
	if meta.GitBranch != "dev-1301" {
		t.Errorf("GitBranch = %q", meta.GitBranch)
	}
}

func TestParseJSONLMeta_WrongCWD(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"system","cwd":"/Users/grins/git/thegrid","content":"init"}
{"type":"user","cwd":"/Users/grins/git/thegrid","message":{"role":"user","content":"something"}}
`
	path := filepath.Join(dir, "wrong.jsonl")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	meta, err := parseJSONLMeta(path, "/Users/grins/git/thegrid/.trees/dev-1301")
	if err != nil {
		t.Fatal(err)
	}
	if meta != nil {
		t.Error("expected nil for wrong cwd")
	}
}

func TestParseJSONLMeta_ArrayMessage(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"system","cwd":"/tmp/test","content":"init"}
{"type":"user","cwd":"/tmp/test","message":{"role":"user","content":[{"type":"text","text":"Hello world"}]}}
`
	path := filepath.Join(dir, "arr.jsonl")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	meta, err := parseJSONLMeta(path, "/tmp/test")
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if meta.FirstPrompt != "Hello world" {
		t.Errorf("FirstPrompt = %q", meta.FirstPrompt)
	}
}
