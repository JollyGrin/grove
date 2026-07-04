// Package probe implements the deterministic drop-in probe (DESIGN §6.1):
// cheap file-presence detection of a repo's stack, suggested commands,
// shape, existing agent context, git facts, and task-backend signals.
// No LLM, no network — filesystem reads plus at most cheap local git
// subprocess calls.
package probe

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JollyGrin/grove/internal/git"
)

// Shape values for Probe.Shape.
const (
	ShapeSingle   = "single"   // one repo, no workspace markers
	ShapeMonorepo = "monorepo" // workspace markers at the root
	ShapeParent   = "parent"   // not a repo itself; holds ≥2 sibling repos
)

// RemoteHost values for Probe.RemoteHost.
const (
	RemoteGitHub = "github"
	RemoteGitLab = "gitlab"
	RemoteOther  = "other"
	RemoteNone   = "none"
)

// AgentContext is the nearest existing agent-context file, found by
// walking up from the probed root. Honor it; never overwrite (DESIGN §6.1).
type AgentContext struct {
	Path string // absolute path; empty when none found
	Kind string // "AGENTS.md", "CLAUDE.md" or ".cursorrules"
}

// Probe is the result of one deterministic drop-in scan.
type Probe struct {
	Stack string // pnpm|yarn|npm|bun|go|rust|python|ruby|unknown

	// Suggested commands; empty when nothing could be inferred.
	Setup string
	Build string
	Test  string
	Lint  string

	Shape        string // single|monorepo|parent
	AgentContext AgentContext

	DefaultBranch string // empty when root is not a git repo
	RemoteHost    string // github|gitlab|other|none
	HasTaskDir    bool   // .grove/tasks exists under root
	LinearKeySet  bool   // the given linear key env var is non-empty
}

// Run probes root. linearKeyEnv names the env var holding the Linear API
// key (empty means "don't check"). Git-dependent fields are best-effort:
// a non-git root or a failing git call leaves them at their zero values
// rather than failing the probe.
func Run(root, linearKeyEnv string) (*Probe, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("probe: %s is not a directory", root)
	}

	p := &Probe{Stack: "unknown", RemoteHost: RemoteNone}
	detectStack(root, p)
	p.Shape = detectShape(root)
	p.AgentContext = findAgentContext(root)
	if exists(filepath.Join(root, ".git")) {
		if branch, err := git.DefaultBranch(root); err == nil {
			p.DefaultBranch = branch
		}
		p.RemoteHost = remoteHost(root)
	}
	p.HasTaskDir = isDir(filepath.Join(root, ".grove", "tasks"))
	p.LinearKeySet = linearKeyEnv != "" && os.Getenv(linearKeyEnv) != ""
	return p, nil
}

// lockfiles is the stack-detection priority chain (Turborepo-style):
// first hit at the root wins.
var lockfiles = []struct{ file, stack string }{
	{"pnpm-lock.yaml", "pnpm"},
	{"yarn.lock", "yarn"},
	{"package-lock.json", "npm"},
	{"bun.lockb", "bun"},
	{"go.mod", "go"},
	{"Cargo.toml", "rust"},
	{"uv.lock", "python"},
	{"pyproject.toml", "python"},
	{"Gemfile.lock", "ruby"},
}

func detectStack(root string, p *Probe) {
	for _, lf := range lockfiles {
		if !exists(filepath.Join(root, lf.file)) {
			continue
		}
		p.Stack = lf.stack
		suggestCommands(root, p)
		return
	}
}

// suggestCommands fills Setup/Build/Test/Lint from package.json scripts
// for JS stacks, language conventions otherwise. Anything not cheaply
// inferable stays empty.
func suggestCommands(root string, p *Probe) {
	switch p.Stack {
	case "pnpm", "yarn", "npm", "bun":
		suggestJS(root, p)
	case "go":
		p.Build = "go build ./..."
		p.Test = "go test ./..."
	case "rust":
		p.Build = "cargo build"
		p.Test = "cargo test"
	case "python":
		data, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
		if err == nil && strings.Contains(string(data), "pytest") {
			p.Test = "pytest"
		}
	case "ruby":
		if exists(filepath.Join(root, "Rakefile")) {
			p.Test = "rake"
		}
	}
}

func suggestJS(root string, p *Probe) {
	// pnpm/yarn run scripts bare; npm/bun need the "run" verb.
	setup := map[string]string{"pnpm": "pnpm install", "yarn": "yarn", "npm": "npm install", "bun": "bun install"}
	runner := map[string]string{"pnpm": "pnpm ", "yarn": "yarn ", "npm": "npm run ", "bun": "bun run "}
	p.Setup = setup[p.Stack]

	scripts := readScripts(root)
	if _, ok := scripts["build"]; ok {
		p.Build = runner[p.Stack] + "build"
	}
	if _, ok := scripts["test"]; ok {
		p.Test = runner[p.Stack] + "test"
	}
	if _, ok := scripts["lint"]; ok {
		p.Lint = runner[p.Stack] + "lint"
	}
}

// readScripts returns package.json "scripts", or nil when the file is
// missing or unparseable (best-effort — a broken manifest never fails
// the probe).
func readScripts(root string) map[string]string {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	return pkg.Scripts
}

// monorepoMarkers at the root force ShapeMonorepo.
var monorepoMarkers = []string{"pnpm-workspace.yaml", "turbo.json", "nx.json", "go.work"}

func detectShape(root string) string {
	for _, marker := range monorepoMarkers {
		if exists(filepath.Join(root, marker)) {
			return ShapeMonorepo
		}
	}
	// Parent-of-repos: the root itself is not a git repo but holds ≥2
	// direct children that are.
	if exists(filepath.Join(root, ".git")) {
		return ShapeSingle
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ShapeSingle
	}
	repos := 0
	for _, e := range entries {
		if e.IsDir() && exists(filepath.Join(root, e.Name(), ".git")) {
			repos++
		}
	}
	if repos >= 2 {
		return ShapeParent
	}
	return ShapeSingle
}

// contextFiles in priority order within one directory; the nearest
// directory (walking up from root) wins overall.
var contextFiles = []string{"AGENTS.md", "CLAUDE.md", ".cursorrules"}

func findAgentContext(root string) AgentContext {
	for dir := root; ; {
		for _, name := range contextFiles {
			path := filepath.Join(dir, name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return AgentContext{Path: path, Kind: name}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return AgentContext{}
		}
		dir = parent
	}
}

// remoteHost classifies `git remote get-url origin`. Any failure (no
// remote, no git) is RemoteNone.
func remoteHost(root string) string {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return RemoteNone
	}
	url := strings.ToLower(strings.TrimSpace(string(out)))
	switch {
	case strings.Contains(url, "github."):
		return RemoteGitHub
	case strings.Contains(url, "gitlab."):
		return RemoteGitLab
	default:
		return RemoteOther
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
