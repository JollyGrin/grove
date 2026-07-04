package wizard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/bootstrap"
	"github.com/JollyGrin/grove/internal/probe"
)

func docFrom(t *testing.T, content string) *bootstrap.Doc {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := bootstrap.LoadDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func freshInput(t *testing.T, flags Flags) Input {
	return Input{
		Probe:    &probe.Probe{Stack: "pnpm", Setup: "pnpm install", DefaultBranch: "main"},
		RepoName: "demo", RepoPath: "/tmp/demo",
		Doc:   docFrom(t, ""),
		Flags: flags,
	}
}

func step(t *testing.T, steps []Step, id string) Step {
	t.Helper()
	for _, s := range steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("step %s missing", id)
	return Step{}
}

func TestBuildFreshRepoUsesDetection(t *testing.T) {
	steps, err := Build(freshInput(t, Flags{Yes: true}))
	if err != nil {
		t.Fatal(err)
	}
	if got := step(t, steps, "setup").Value; got != "pnpm install" {
		t.Errorf("setup = %q, want detected", got)
	}
	if got := step(t, steps, "repo").Value; got != "main" {
		t.Errorf("base = %q", got)
	}
	if got := step(t, steps, "worker").Value; got != "" {
		t.Errorf("--yes must not invent a worker command, got %q", got)
	}
	if step(t, steps, "agents-md").On {
		t.Error("--yes must never auto-enable the paid agents-md run")
	}
	if step(t, steps, "hooks").On {
		t.Error("--yes must not newly install hooks (shared settings write)")
	}
}

func TestBuildHandEditedValueBeatsDetection(t *testing.T) {
	in := freshInput(t, Flags{Yes: true})
	in.Doc = docFrom(t, "repos:\n  demo:\n    path: /tmp/demo\n    base: develop\n    setup: pnpm install --frozen\n")
	steps, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := step(t, steps, "setup").Value; got != "pnpm install --frozen" {
		t.Errorf("hand-edited setup must win over detection, got %q", got)
	}
	if got := step(t, steps, "repo").Value; got != "develop" {
		t.Errorf("hand-edited base must win, got %q", got)
	}
}

func TestBuildFlagBeatsEverything(t *testing.T) {
	in := freshInput(t, Flags{Yes: true, Setup: "make deps"})
	in.Doc = docFrom(t, "repos:\n  demo:\n    path: /tmp/demo\n    setup: pnpm install --frozen\n")
	steps, _ := Build(in)
	if got := step(t, steps, "setup").Value; got != "make deps" {
		t.Errorf("explicit flag must win, got %q", got)
	}
}

func TestAgentsMDResolution(t *testing.T) {
	// Interactive + no existing brain → offered ON.
	in := freshInput(t, Flags{})
	steps, _ := Build(in)
	if !step(t, steps, "agents-md").On {
		t.Error("interactive fresh repo should offer agents-md")
	}
	// Existing context → offered OFF.
	in2 := freshInput(t, Flags{})
	in2.Probe.AgentContext = probe.AgentContext{Path: "/tmp/demo/CLAUDE.md", Kind: "CLAUDE.md"}
	steps2, _ := Build(in2)
	if step(t, steps2, "agents-md").On {
		t.Error("existing agent context must default the step off")
	}
	// --yes + explicit flag → ON.
	steps3, _ := Build(freshInput(t, Flags{Yes: true, AgentsMD: true}))
	if !step(t, steps3, "agents-md").On {
		t.Error("explicit --agents-md must enable under --yes")
	}
}

func TestOnlyFiltersAndValidates(t *testing.T) {
	steps, err := Build(freshInput(t, Flags{Only: "hooks", Yes: true}))
	if err != nil || len(steps) != 1 || steps[0].ID != "hooks" {
		t.Fatalf("--only hooks: %v %v", steps, err)
	}
	if !steps[0].On {
		t.Error("--only hooks is the explicit ask — must resolve ON even under --yes")
	}
	amd, err := Build(freshInput(t, Flags{Only: "agents-md", Yes: true}))
	if err != nil || !amd[0].On {
		t.Errorf("--only agents-md must resolve ON: %v %v", amd, err)
	}
	if _, err := Build(freshInput(t, Flags{Only: "nonsense"})); err == nil || !strings.Contains(err.Error(), "agents-md") {
		t.Errorf("unknown --only must list valid steps, got %v", err)
	}
}

func TestApplyOnlyWritesAskedSteps(t *testing.T) {
	in := freshInput(t, Flags{Only: "setup", Setup: "make deps"})
	in.Doc = docFrom(t, "repos:\n  demo:\n    path: /tmp/demo\n    base: develop\n    claude: ccwork --dangerously-skip-permissions\n")
	steps, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	Apply(in, steps)
	if !in.Doc.Dirty() {
		t.Fatal("setup change must dirty the doc")
	}
	if got := in.Doc.Get("repos", "demo", "setup"); got != "make deps" {
		t.Errorf("setup = %q", got)
	}
	if got := in.Doc.Get("repos", "demo", "base"); got != "develop" {
		t.Errorf("--only setup must not touch base, got %q", got)
	}
	if got := in.Doc.Get("repos", "demo", "claude"); !strings.Contains(got, "ccwork") {
		t.Errorf("--only setup must not touch worker, got %q", got)
	}
}

func TestApplyNoChangesLeavesDocClean(t *testing.T) {
	in := freshInput(t, Flags{Yes: true})
	in.Doc = docFrom(t, "provider:\n  kind: markdown\nrepos:\n  demo:\n    path: /tmp/demo\n    base: main\n    setup: pnpm install\n")
	steps, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	Apply(in, steps)
	if in.Doc.Dirty() {
		t.Error("re-running --yes over an already-correct config must be a no-op")
	}
}
