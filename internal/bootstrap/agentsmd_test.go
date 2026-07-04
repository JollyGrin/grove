package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentsMDPromptRendersFacts(t *testing.T) {
	p, err := AgentsMDPrompt(Facts{
		RepoName: "demo", Stack: "pnpm", Shape: "single",
		Setup: "pnpm install", Build: "pnpm build", Test: "pnpm test",
	}, "AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`repo "demo"`, "stack: pnpm", "setup: pnpm install",
		"build: pnpm build", "test: pnpm test",
		"## Layout", "## Conventions", "## Gotchas",
		"WROTE AGENTS.md", "~150 lines",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(p, "lint:") {
		t.Error("empty lint fact must be omitted")
	}
}

func TestAgentsMDTargetNeverOverwrites(t *testing.T) {
	dir := t.TempDir()

	target, err := AgentsMDTarget(dir, false)
	if err != nil || filepath.Base(target) != "AGENTS.md" {
		t.Fatalf("fresh repo: %q, %v", target, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("human-written"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AgentsMDTarget(dir, false); err == nil || !strings.Contains(err.Error(), "never overwrites") {
		t.Errorf("existing file without force must refuse, got %v", err)
	}
	target, err = AgentsMDTarget(dir, true)
	if err != nil || filepath.Base(target) != "AGENTS.md.new" {
		t.Errorf("force with existing file must target .new, got %q, %v", target, err)
	}
}

// TestGenerateAgentsMDWithStub drives the full run with a stub worker (the
// dummy-data pattern): a script that ignores its args and writes the file.
func TestGenerateAgentsMDWithStub(t *testing.T) {
	repo := t.TempDir()
	bin := t.TempDir()
	stub := filepath.Join(bin, "claude-stub")
	script := "#!/bin/sh\necho \"$@\" > args.txt\ncat > /dev/null 2>/dev/null || true\necho stub ran\nprintf '# demo\\nstub brain\\n' > AGENTS.md\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/bin/sh")

	var out strings.Builder
	target, err := GenerateAgentsMD(repo, stub, Facts{RepoName: "demo", Stack: "go"}, false, &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if filepath.Base(target) != "AGENTS.md" {
		t.Errorf("target = %s", target)
	}
	raw, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil || !strings.Contains(string(raw), "stub brain") {
		t.Errorf("agent output not written: %v %q", err, raw)
	}
	// The one-shot must have injected skip-permissions and the prompt.
	args, _ := os.ReadFile(filepath.Join(repo, "args.txt"))
	if !strings.Contains(string(args), "--dangerously-skip-permissions") {
		t.Error("one-shot must append skip-permissions for the headless write")
	}
	if !strings.Contains(string(args), "-p") {
		t.Error("must run headless via -p")
	}

	// Failure path: stub that writes nothing.
	lazy := filepath.Join(bin, "lazy-stub")
	if err := os.WriteFile(lazy, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo2 := t.TempDir()
	if _, err := GenerateAgentsMD(repo2, lazy, Facts{RepoName: "x"}, false, &out); err == nil || !strings.Contains(err.Error(), "was not written") {
		t.Errorf("missing output must error, got %v", err)
	}
}
