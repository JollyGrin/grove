package probe

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo creates a git repo with one commit on branch main.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		runGit(t, dir, args...)
	}
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRun(t *testing.T, root string) *Probe {
	t.Helper()
	p, err := Run(root, "")
	if err != nil {
		t.Fatalf("Run(%s): %v", root, err)
	}
	return p
}

func TestStackFixtures(t *testing.T) {
	tests := []struct {
		fixture                         string
		stack, setup, build, test, lint string
	}{
		{"pnpm", "pnpm", "pnpm install", "pnpm build", "pnpm test", "pnpm lint"},
		{"yarnrepo", "yarn", "yarn", "", "yarn test", ""},
		{"npmrepo", "npm", "npm install", "npm run build", "", "npm run lint"},
		{"bunrepo", "bun", "bun install", "", "bun run test", ""},
		{"gorepo", "go", "", "go build ./...", "go test ./...", ""},
		{"rustrepo", "rust", "", "cargo build", "cargo test", ""},
		{"pyrepo", "python", "", "", "pytest", ""},
		{"rubyrepo", "ruby", "", "", "rake", ""},
		{"unknown", "unknown", "", "", "", ""},
		// pnpm-lock.yaml and yarn.lock both present: pnpm wins the chain.
		{"priority", "pnpm", "pnpm install", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			p := mustRun(t, filepath.Join("testdata", tt.fixture))
			if p.Stack != tt.stack {
				t.Errorf("Stack = %q, want %q", p.Stack, tt.stack)
			}
			if p.Setup != tt.setup {
				t.Errorf("Setup = %q, want %q", p.Setup, tt.setup)
			}
			if p.Build != tt.build {
				t.Errorf("Build = %q, want %q", p.Build, tt.build)
			}
			if p.Test != tt.test {
				t.Errorf("Test = %q, want %q", p.Test, tt.test)
			}
			if p.Lint != tt.lint {
				t.Errorf("Lint = %q, want %q", p.Lint, tt.lint)
			}
			if p.Shape != ShapeSingle {
				t.Errorf("Shape = %q, want %q", p.Shape, ShapeSingle)
			}
		})
	}
}

func TestShapeMonorepoMarkers(t *testing.T) {
	for _, marker := range []string{"pnpm-workspace.yaml", "turbo.json", "nx.json", "go.work"} {
		t.Run(marker, func(t *testing.T) {
			dir := t.TempDir()
			touch(t, filepath.Join(dir, marker))
			if p := mustRun(t, dir); p.Shape != ShapeMonorepo {
				t.Errorf("Shape = %q, want %q", p.Shape, ShapeMonorepo)
			}
		})
	}
}

func TestShapeMonorepoFixture(t *testing.T) {
	p := mustRun(t, filepath.Join("testdata", "monorepo"))
	if p.Shape != ShapeMonorepo {
		t.Errorf("Shape = %q, want %q", p.Shape, ShapeMonorepo)
	}
	if p.Stack != "pnpm" {
		t.Errorf("Stack = %q, want pnpm", p.Stack)
	}
}

func TestShapeParent(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "repo-a", ".git", "HEAD"))
	touch(t, filepath.Join(dir, "repo-b", ".git", "HEAD"))
	if p := mustRun(t, dir); p.Shape != ShapeParent {
		t.Errorf("Shape = %q, want %q", p.Shape, ShapeParent)
	}
}

func TestShapeParentNeedsTwoChildren(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "repo-a", ".git", "HEAD"))
	if p := mustRun(t, dir); p.Shape != ShapeSingle {
		t.Errorf("one child repo: Shape = %q, want %q", p.Shape, ShapeSingle)
	}
}

func TestShapeGitRootIsSingle(t *testing.T) {
	// A root that is itself a git repo is single even with repo children.
	dir := initRepo(t)
	touch(t, filepath.Join(dir, "repo-a", ".git", "HEAD"))
	touch(t, filepath.Join(dir, "repo-b", ".git", "HEAD"))
	if p := mustRun(t, dir); p.Shape != ShapeSingle {
		t.Errorf("Shape = %q, want %q", p.Shape, ShapeSingle)
	}
}

func TestAgentContextUpTree(t *testing.T) {
	root := filepath.Join("testdata", "uptree", "child")
	p := mustRun(t, root)
	if p.AgentContext.Kind != ".cursorrules" {
		t.Fatalf("Kind = %q, want .cursorrules", p.AgentContext.Kind)
	}
	want, err := filepath.Abs(filepath.Join("testdata", "uptree", ".cursorrules"))
	if err != nil {
		t.Fatal(err)
	}
	if p.AgentContext.Path != want {
		t.Errorf("Path = %q, want %q", p.AgentContext.Path, want)
	}
}

func TestAgentContextPriority(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, ".cursorrules"))
	touch(t, filepath.Join(dir, "CLAUDE.md"))
	if p := mustRun(t, dir); p.AgentContext.Kind != "CLAUDE.md" {
		t.Errorf("Kind = %q, want CLAUDE.md over .cursorrules", p.AgentContext.Kind)
	}
	touch(t, filepath.Join(dir, "AGENTS.md"))
	if p := mustRun(t, dir); p.AgentContext.Kind != "AGENTS.md" {
		t.Errorf("Kind = %q, want AGENTS.md over the rest", p.AgentContext.Kind)
	}
}

func TestAgentContextNearestWins(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "AGENTS.md"))
	child := filepath.Join(dir, "sub")
	touch(t, filepath.Join(child, "CLAUDE.md"))
	p := mustRun(t, child)
	if p.AgentContext.Kind != "CLAUDE.md" {
		t.Errorf("Kind = %q, want nearest CLAUDE.md over up-tree AGENTS.md", p.AgentContext.Kind)
	}
}

func TestAgentContextNone(t *testing.T) {
	p := mustRun(t, t.TempDir())
	if p.AgentContext != (AgentContext{}) {
		t.Errorf("AgentContext = %+v, want zero", p.AgentContext)
	}
}

func TestHasTaskDir(t *testing.T) {
	p := mustRun(t, filepath.Join("testdata", "taskdir"))
	if !p.HasTaskDir {
		t.Error("HasTaskDir = false, want true")
	}
	if p := mustRun(t, filepath.Join("testdata", "gorepo")); p.HasTaskDir {
		t.Error("HasTaskDir = true, want false")
	}
}

func TestLinearKeySet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GROVE_TEST_LINEAR_KEY", "lin_api_xxx")
	p, err := Run(dir, "GROVE_TEST_LINEAR_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if !p.LinearKeySet {
		t.Error("LinearKeySet = false, want true")
	}
	t.Setenv("GROVE_TEST_LINEAR_KEY", "")
	if p, _ = Run(dir, "GROVE_TEST_LINEAR_KEY"); p.LinearKeySet {
		t.Error("empty env: LinearKeySet = true, want false")
	}
	if p, _ = Run(dir, ""); p.LinearKeySet {
		t.Error("no env name: LinearKeySet = true, want false")
	}
}

func TestGitFields(t *testing.T) {
	dir := initRepo(t)
	p := mustRun(t, dir)
	if p.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want main", p.DefaultBranch)
	}
	if p.RemoteHost != RemoteNone {
		t.Errorf("no remote: RemoteHost = %q, want %q", p.RemoteHost, RemoteNone)
	}

	runGit(t, dir, "remote", "add", "origin", "git@github.com:acme/widgets.git")
	if p = mustRun(t, dir); p.RemoteHost != RemoteGitHub {
		t.Errorf("RemoteHost = %q, want %q", p.RemoteHost, RemoteGitHub)
	}

	runGit(t, dir, "remote", "set-url", "origin", "https://gitlab.example.com/acme/widgets.git")
	if p = mustRun(t, dir); p.RemoteHost != RemoteGitLab {
		t.Errorf("RemoteHost = %q, want %q", p.RemoteHost, RemoteGitLab)
	}

	runGit(t, dir, "remote", "set-url", "origin", "https://bitbucket.org/acme/widgets.git")
	if p = mustRun(t, dir); p.RemoteHost != RemoteOther {
		t.Errorf("RemoteHost = %q, want %q", p.RemoteHost, RemoteOther)
	}
}

func TestNoGitDir(t *testing.T) {
	p := mustRun(t, t.TempDir())
	if p.DefaultBranch != "" {
		t.Errorf("DefaultBranch = %q, want empty", p.DefaultBranch)
	}
	if p.RemoteHost != RemoteNone {
		t.Errorf("RemoteHost = %q, want %q", p.RemoteHost, RemoteNone)
	}
}

func TestRunMissingRoot(t *testing.T) {
	if _, err := Run(filepath.Join(t.TempDir(), "nope"), ""); err == nil {
		t.Error("Run on missing root: want error, got nil")
	}
}
