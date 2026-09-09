package sub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeClaude writes a fake `claude` script to dir that records its
// argv (one per line) and $ANTHROPIC_BASE_URL to outFile, then prints
// stdoutBody.
func writeFakeClaude(t *testing.T, dir, outFile, stdoutBody string) string {
	t.Helper()
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"{\n" +
		"  echo \"BASE_URL=$ANTHROPIC_BASE_URL\"\n" +
		"  for a in \"$@\"; do printf 'ARG:%s\\n' \"$a\"; done\n" +
		"} > " + outFile + "\n" +
		"cat <<'CLAUDE_EOF'\n" + stdoutBody + "\nCLAUDE_EOF\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAgenticArgs(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	claudeBin := writeFakeClaude(t, dir, out,
		`{"result":"- found: a.go:1","num_turns":2,"duration_api_ms":5,"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0}}`)

	l := Lane{Name: "x", BaseURL: "https://example.test", Model: "m"}
	res, err := Agentic(context.Background(), l, "key", claudeBin, dir, "where", "", 12, false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "- found: a.go:1" || res.Turns != 2 {
		t.Errorf("res = %+v", res)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	dump := string(raw)
	for _, want := range []string{"ARG:--bare", "ARG:--max-turns\nARG:12", "ARG:--allowedTools"} {
		if !strings.Contains(dump, want) {
			t.Errorf("argv dump missing %q; dump=\n%s", want, dump)
		}
	}
	if strings.Contains(dump, "Edit") {
		t.Errorf("argv dump contains Edit without --allow-write; dump=\n%s", dump)
	}
}

func TestAgenticAllowWriteAddsTools(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	claudeBin := writeFakeClaude(t, dir, out,
		`{"result":"ok","num_turns":1,"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0}}`)

	l := Lane{BaseURL: "https://example.test", Model: "m"}
	_, err := Agentic(context.Background(), l, "key", claudeBin, dir, "u", "", 12, true, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	if !strings.Contains(string(raw), ",Edit,Write") {
		t.Errorf("expected Edit,Write in allowedTools with --allow-write; dump=\n%s", raw)
	}
}

func TestAgenticEnvChildOnly(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	os.Unsetenv("ANTHROPIC_BASE_URL")

	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	claudeBin := writeFakeClaude(t, dir, out,
		`{"result":"ok","num_turns":1,"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0}}`)

	l := Lane{BaseURL: "https://child-only.test", Model: "m"}
	_, err := Agentic(context.Background(), l, "key", claudeBin, dir, "u", "", 12, false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	if !strings.Contains(string(raw), "BASE_URL=https://child-only.test") {
		t.Errorf("child did not see ANTHROPIC_BASE_URL; dump=\n%s", raw)
	}
	if v := os.Getenv("ANTHROPIC_BASE_URL"); v != "" {
		t.Errorf("parent ANTHROPIC_BASE_URL leaked: %q", v)
	}
}

func TestAgenticWarningPrefixStripped(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	body := "[claude-code:unrecognized_model] warning line\n" +
		`{"result":"- ok","num_turns":1,"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0}}`
	claudeBin := writeFakeClaude(t, dir, out, body)

	l := Lane{BaseURL: "https://example.test", Model: "m"}
	res, err := Agentic(context.Background(), l, "key", claudeBin, dir, "u", "", 12, false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "- ok" {
		t.Errorf("Text = %q, want - ok", res.Text)
	}
}

func TestAgenticNullResult(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	claudeBin := writeFakeClaude(t, dir, out,
		`{"result":null,"num_turns":12,"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0}}`)

	l := Lane{BaseURL: "https://example.test", Model: "m"}
	_, err := Agentic(context.Background(), l, "key", claudeBin, dir, "u", "", 12, false, 5*time.Second)
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("err = %v, want ErrNoAnswer", err)
	}
}

func TestAgenticStartHint(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	claudeBin := writeFakeClaude(t, dir, out,
		`{"result":"ok","num_turns":1,"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0}}`)

	l := Lane{BaseURL: "https://example.test", Model: "m"}
	_, err := Agentic(context.Background(), l, "key", claudeBin, dir, "where does X happen", "grep -rn X .", 12, false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	dump := string(raw)
	if !strings.Contains(dump, "Start by running: grep -rn X .") {
		t.Errorf("prompt missing start hint; dump=\n%s", dump)
	}
	// The prompt is the arg right after -p, NOT last — a trailing bare
	// string gets swallowed into --allowedTools's variadic list (verified
	// live against claude --help; see the comment in agentic.go).
	promptIdx := strings.Index(dump, "ARG:-p\nARG:")
	if promptIdx < 0 {
		t.Fatalf("could not find -p followed by the prompt arg; dump=\n%s", dump)
	}
	promptArg := dump[promptIdx+len("ARG:-p\nARG:"):]
	if nextArg := strings.Index(promptArg, "\nARG:--bare"); nextArg >= 0 {
		promptArg = promptArg[:nextArg]
	}
	if !strings.HasSuffix(promptArg, "Start by running: grep -rn X .") {
		t.Errorf("prompt (arg after -p) does not end with the start hint; got=%q", promptArg)
	}
}
