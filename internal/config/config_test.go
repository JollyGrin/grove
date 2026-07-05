package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// setHome points HOME (and the config Dir) at a scratch dir and clears
// GROVE_STATE_DIR so tests never see the live environment.
func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROVE_STATE_DIR", "")
	return home
}

func writeGlobal(t *testing.T, yaml string) string {
	t.Helper()
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// newWorkspace creates a workspace root with a .grove/config.yaml.
func newWorkspace(t *testing.T, yaml string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".grove"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".grove", "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadAtLegacyPassthrough(t *testing.T) {
	setHome(t)
	repo := t.TempDir()
	writeGlobal(t, `
repos:
  demo:
    path: `+repo+`
audit:
  stale_days: 5
notify:
  ntfy: https://ntfy.sh/x
`)
	want, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadAt("")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadAt(\"\") = %+v, want Load() = %+v", got, want)
	}
}

func TestLoadAtMerge(t *testing.T) {
	setHome(t)
	globalRepo := t.TempDir()
	wsRepo := t.TempDir()
	writeGlobal(t, `
provider:
  kind: linear
  markdown:
    dir: custom/tasks
linear:
  api_key_env: MY_LINEAR_KEY
repos:
  global-repo:
    path: `+globalRepo+`
    base: develop
audit:
  stale_days: 7
notify:
  ntfy: https://ntfy.sh/global-topic
cost:
  stuck_turns: 30
`)
	root := newWorkspace(t, `
workspace:
  label: grid
  scope: parent
provider:
  kind: markdown
linear:
  team: DEV
repos:
  ws-repo:
    path: `+wsRepo+`
audit:
  stale_days: 3
cost:
  stuck_turns: 45
`)
	c, err := LoadAt(root)
	if err != nil {
		t.Fatal(err)
	}

	// Workspace block parsed.
	if c.Workspace.Label != "grid" || c.Workspace.Scope != "parent" {
		t.Errorf("workspace block = %+v, want {grid parent}", c.Workspace)
	}
	// Scalar from the workspace wins over global (zero-value survival:
	// defaulting runs after the merge, so 3 is not re-defaulted to 7).
	if c.Audit.StaleDays != 3 {
		t.Errorf("audit.stale_days = %d, want 3 (workspace wins)", c.Audit.StaleDays)
	}
	// Explicit workspace cost.stuck_turns wins over global.
	if c.Cost.StuckTurns != 45 {
		t.Errorf("cost.stuck_turns = %d, want 45", c.Cost.StuckTurns)
	}
	// Global-only field inherited.
	if c.Notify.Ntfy != "https://ntfy.sh/global-topic" {
		t.Errorf("notify.ntfy = %q, want inherited global topic", c.Notify.Ntfy)
	}
	// repos: wholesale replacement — global repos invisible.
	if len(c.Repos) != 1 || c.Repos["ws-repo"] == nil {
		t.Errorf("repos = %v, want exactly {ws-repo}", c.Repos)
	}
	if c.Repos["global-repo"] != nil {
		t.Error("global repo leaked through a wholesale-replaced repos map")
	}
	// provider: wholesale replacement — kind flips AND the global
	// markdown.dir is gone, so the default reapplies post-merge.
	if c.Provider.Kind != "markdown" {
		t.Errorf("provider.kind = %q, want markdown", c.Provider.Kind)
	}
	if c.Provider.Markdown.Dir != filepath.Join(".grove", "tasks") {
		t.Errorf("provider.markdown.dir = %q, want default (wholesale replace drops global sub-fields)", c.Provider.Markdown.Dir)
	}
	// linear: field-wise merge — workspace team + inherited api_key_env.
	if c.Linear.Team != "DEV" || c.Linear.APIKeyEnv != "MY_LINEAR_KEY" {
		t.Errorf("linear = %+v, want team DEV with inherited MY_LINEAR_KEY", c.Linear)
	}
}

// The orchestrator section is workspace-scoped: a global claude command
// (e.g. a work-only wrapper like ccwork) must never leak into a
// workspace's cockpit. DESIGN §6.5 / "no personal defaults in core".
func TestOrchestratorIsWorkspaceScoped(t *testing.T) {
	setHome(t)
	globalRepo := t.TempDir()
	writeGlobal(t, `
repos:
  g:
    path: `+globalRepo+`
orchestrator:
  claude: ccwork --dangerously-skip-permissions
`)

	// Legacy global load keeps the configured command.
	c, err := LoadAt("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Orchestrator.Claude != "ccwork --dangerously-skip-permissions" {
		t.Errorf("global orchestrator.claude = %q, want configured ccwork", c.Orchestrator.Claude)
	}

	// Workspace without its own orchestrator block: global is NOT
	// inherited — the safe default applies.
	wsRepo := t.TempDir()
	root := newWorkspace(t, `
workspace:
  label: personal
  scope: parent
repos:
  r:
    path: `+wsRepo+`
`)
	c, err = LoadAt(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Orchestrator.Claude != "claude --dangerously-skip-permissions" {
		t.Errorf("workspace orchestrator.claude = %q, want default claude (global must not leak)", c.Orchestrator.Claude)
	}

	// Workspace with its own orchestrator block wins.
	root2 := newWorkspace(t, `
workspace:
  label: other
  scope: parent
repos:
  r:
    path: `+wsRepo+`
orchestrator:
  claude: claude --model opus
`)
	c, err = LoadAt(root2)
	if err != nil {
		t.Fatal(err)
	}
	if c.Orchestrator.Claude != "claude --model opus" {
		t.Errorf("workspace orchestrator.claude = %q, want the workspace's own", c.Orchestrator.Claude)
	}
}

// The per-repo worker default is plain claude — ccwork is a personal
// work wrapper and must not be a baked-in default (DESIGN "no personal
// defaults in core").
func TestRepoClaudeDefault(t *testing.T) {
	setHome(t)
	repo := t.TempDir()
	writeGlobal(t, `
repos:
  demo:
    path: `+repo+`
`)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Repos["demo"].Claude != "claude --dangerously-skip-permissions" {
		t.Errorf("repo claude default = %q, want claude --dangerously-skip-permissions", c.Repos["demo"].Claude)
	}
}

func TestLoadAtWorkspaceOnlyAndGlobalOnly(t *testing.T) {
	setHome(t)
	wsRepo := t.TempDir()

	// Global missing entirely: workspace layer alone must load.
	root := newWorkspace(t, `
workspace:
  label: solo
  scope: repo
repos:
  r:
    path: `+wsRepo+`
`)
	c, err := LoadAt(root)
	if err != nil {
		t.Fatalf("workspace-only load: %v", err)
	}
	if c.Workspace.Label != "solo" || c.Repos["r"] == nil {
		t.Errorf("workspace-only config = %+v", c)
	}
	if c.Provider.Kind != "markdown" || c.Audit.StaleDays != 7 || c.Cost.StuckTurns != 30 {
		t.Errorf("defaults must still apply post-merge: %+v", c)
	}

	// Workspace file missing: global layer alone, workspace block zero.
	globalRepo := t.TempDir()
	writeGlobal(t, `
repos:
  g:
    path: `+globalRepo+`
`)
	bare := t.TempDir() // root with no .grove/config.yaml
	c, err = LoadAt(bare)
	if err != nil {
		t.Fatalf("global-only load: %v", err)
	}
	if c.Repos["g"] == nil || c.Workspace.Label != "" {
		t.Errorf("global-only config = %+v", c)
	}
}

func TestLoadAtBothMissing(t *testing.T) {
	setHome(t)
	root := t.TempDir()
	_, err := LoadAt(root)
	if err == nil {
		t.Fatal("want Load's missing-config error when both layers are missing")
	}
	if !strings.Contains(err.Error(), "read config:") ||
		!strings.Contains(err.Error(), "config.example.yaml") {
		t.Errorf("error must match Load's missing-file shape, got: %v", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(Dir(), "config.yaml")) {
		t.Errorf("error must name the global path, got: %v", err)
	}
}

func TestStateDirAt(t *testing.T) {
	home := setHome(t)

	// Legacy fallback: root == "" is today's StateDir.
	if got, want := StateDirAt(""), filepath.Join(home, ".local", "state", "grove"); got != want {
		t.Errorf("StateDirAt(\"\") = %q, want %q", got, want)
	}

	// Workspace path.
	root := t.TempDir()
	if got, want := StateDirAt(root), filepath.Join(root, ".grove", "state"); got != want {
		t.Errorf("StateDirAt(root) = %q, want %q", got, want)
	} else if st, err := os.Stat(got); err != nil || !st.IsDir() {
		t.Errorf("workspace state dir not created: %v", err)
	}

	// Env override wins even with a root (test-harness contract).
	override := filepath.Join(t.TempDir(), "scratch-state")
	t.Setenv("GROVE_STATE_DIR", override)
	if got := StateDirAt(root); got != override {
		t.Errorf("StateDirAt(root) with GROVE_STATE_DIR = %q, want %q", got, override)
	}
	if got := StateDirAt(""); got != override {
		t.Errorf("StateDirAt(\"\") with GROVE_STATE_DIR = %q, want %q", got, override)
	}
}
