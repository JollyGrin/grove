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

func TestWithModel(t *testing.T) {
	cases := []struct {
		name, cmd, model, want string
	}{
		{"empty model unchanged", "claude --dangerously-skip-permissions", "", "claude --dangerously-skip-permissions"},
		{"whitespace model unchanged", "claude", "  ", "claude"},
		{"injects after binary before flags", "claude --dangerously-skip-permissions", "claude-sonnet-5", "claude --model 'claude-sonnet-5' --dangerously-skip-permissions"},
		{"bare binary", "claude", "opus", "claude --model 'opus'"},
		{"alias passthrough", "claude --dangerously-skip-permissions", "sonnet", "claude --model 'sonnet' --dangerously-skip-permissions"},
		{"quotes metachars", "claude", "a b", "claude --model 'a b'"},
		{"empty cmd unchanged", "", "opus", ""},
	}
	for _, tc := range cases {
		if got := WithModel(tc.cmd, tc.model); got != tc.want {
			t.Errorf("%s: WithModel(%q, %q) = %q, want %q", tc.name, tc.cmd, tc.model, got, tc.want)
		}
	}
}

func TestWrapProfileNilPassthrough(t *testing.T) {
	cmd := `claude --dangerously-skip-permissions "$(cat /tmp/x.txt)"`
	if got := WrapProfile(cmd, nil, "/secrets/.env"); got != cmd {
		t.Errorf("WrapProfile(nil profile) = %q, want unchanged %q", got, cmd)
	}
}

func TestWrapProfileOrdering(t *testing.T) {
	// WithModel must apply BEFORE the wrap: --model lands on the claude
	// token, not on `.`/`env` from the wrap's own prefix.
	modeled := WithModel("claude --dangerously-skip-permissions", "opus")
	p := &ModelProfile{
		BaseURL: "https://openrouter.ai/api", AuthTokenEnv: "OPENROUTER_API_KEY",
		Opus: "z-ai/glm-5.2", Sonnet: "z-ai/glm-5.2", Haiku: "z-ai/glm-4.5-air",
	}
	got := WrapProfile(modeled, p, "/home/x/.config/grove/.env")
	if !strings.Contains(got, "exec "+modeled) {
		t.Errorf("wrap does not exec the already-modeled command intact:\n%s", got)
	}
}

func TestWrapProfileQuotingAndShape(t *testing.T) {
	p := &ModelProfile{
		BaseURL:      "https://openrouter.ai/api",
		AuthTokenEnv: "OPENROUTER_API_KEY",
		Opus:         "z-ai/glm-5.2",
		Sonnet:       "z-ai/glm-5.2",
		Haiku:        "z-ai/glm-4.5-air",
	}
	cmd := `claude --dangerously-skip-permissions "$(cat /tmp/prompt.txt)"`
	got := WrapProfile(cmd, p, "/home/x/.config/grove/.env")

	if !strings.HasPrefix(got, "( . '/home/x/.config/grove/.env' && export") {
		t.Errorf("does not self-source secrets file first: %s", got)
	}
	if !strings.Contains(got, "ANTHROPIC_BASE_URL='https://openrouter.ai/api'") {
		t.Errorf("base_url not shell-quoted: %s", got)
	}
	if !strings.Contains(got, `ANTHROPIC_AUTH_TOKEN="$OPENROUTER_API_KEY"`) {
		t.Errorf("auth token must stay a bare $NAME, never expanded: %s", got)
	}
	if strings.Contains(got, os.Getenv("OPENROUTER_API_KEY")) && os.Getenv("OPENROUTER_API_KEY") != "" {
		t.Errorf("token value leaked into the command string: %s", got)
	}
	if !strings.Contains(got, "ANTHROPIC_MODEL='z-ai/glm-5.2'") {
		t.Errorf("default (sonnet) slot slug missing/wrong: %s", got)
	}
	if !strings.Contains(got, "&& exec claude --dangerously-skip-permissions") {
		t.Errorf("must use export ... ; exec, never env NAME=$VAR: %s", got)
	}
	if !strings.HasSuffix(got, ")") {
		t.Errorf("wrap must be a single parenthesized subshell: %s", got)
	}
}

func TestWrapProfileMetacharInBaseURL(t *testing.T) {
	p := &ModelProfile{BaseURL: "https://example.com/a'b", AuthTokenEnv: "X", Sonnet: "m"}
	got := WrapProfile("claude", p, "/s/.env")
	if !strings.Contains(got, shellQuote("https://example.com/a'b")) {
		t.Errorf("base_url metachar not safely quoted: %s", got)
	}
}

func TestWrapProfileSlotSelection(t *testing.T) {
	p := &ModelProfile{
		BaseURL: "https://openrouter.ai/api", AuthTokenEnv: "OPENROUTER_API_KEY",
		Opus: "z-ai/glm-5.2", Sonnet: "z-ai/glm-5.2", Haiku: "z-ai/glm-4.5-air",
	}
	cases := []struct {
		name, model, wantSlug string
	}{
		{"no model flag defaults to sonnet", "", "z-ai/glm-5.2"},
		{"opus flag maps to opus slug", "opus", "z-ai/glm-5.2"},
		{"haiku flag maps to haiku slug", "haiku", "z-ai/glm-4.5-air"},
		{"sonnet flag maps to sonnet slug", "sonnet", "z-ai/glm-5.2"},
	}
	for _, tc := range cases {
		modeled := WithModel("claude", tc.model)
		got := WrapProfile(modeled, p, "/s/.env")
		if !strings.Contains(got, "ANTHROPIC_MODEL="+shellQuote(tc.wantSlug)) {
			t.Errorf("%s: got %q, want ANTHROPIC_MODEL=%s", tc.name, got, shellQuote(tc.wantSlug))
		}
	}
}

func TestResolveProfile(t *testing.T) {
	c := &Config{ModelProfiles: map[string]*ModelProfile{
		"openrouter-glm": {BaseURL: "https://openrouter.ai/api"},
	}}
	repo := &Repo{ModelProfile: "openrouter-glm"}

	if name, p, err := c.ResolveProfile("", nil); err != nil || name != "" || p != nil {
		t.Errorf("no explicit, no repo: got (%q, %v, %v), want (\"\", nil, nil)", name, p, err)
	}
	if name, p, err := c.ResolveProfile("", &Repo{}); err != nil || name != "" || p != nil {
		t.Errorf("no explicit, repo without default: got (%q, %v, %v), want (\"\", nil, nil)", name, p, err)
	}
	if name, p, err := c.ResolveProfile("", repo); err != nil || name != "openrouter-glm" || p == nil {
		t.Errorf("repo default should apply: got (%q, %v, %v)", name, p, err)
	}
	if name, p, err := c.ResolveProfile("anthropic", repo); err != nil || name != "" || p != nil {
		t.Errorf("explicit anthropic overrides repo default to no-wrap: got (%q, %v, %v)", name, p, err)
	}
	if name, p, err := c.ResolveProfile("openrouter-glm", nil); err != nil || name != "openrouter-glm" || p == nil {
		t.Errorf("explicit wins with no repo: got (%q, %v, %v)", name, p, err)
	}
	if _, _, err := c.ResolveProfile("nope", nil); err == nil {
		t.Error("unknown profile should error")
	}
}

func TestResolveOrchestratorProfile(t *testing.T) {
	// Rule 2: zero profiles, no default → hint, nothing to spawn or pick.
	if name, cands, act := (&Config{}).ResolveOrchestratorProfile(); act != ProfileHint || name != "" || cands != nil {
		t.Errorf("zero profiles: got (%q, %v, %d), want (\"\", nil, ProfileHint)", name, cands, act)
	}

	// Rule 3: exactly one profile, no default → spawn that lone profile.
	one := &Config{ModelProfiles: map[string]*ModelProfile{
		"openrouter-glm": {BaseURL: "https://openrouter.ai/api"},
	}}
	if name, cands, act := one.ResolveOrchestratorProfile(); act != ProfileSpawn || name != "openrouter-glm" || cands != nil {
		t.Errorf("single profile: got (%q, %v, %d), want (\"openrouter-glm\", nil, ProfileSpawn)", name, cands, act)
	}

	// Rule 4: several profiles, no default → pick over the sorted names.
	several := &Config{ModelProfiles: map[string]*ModelProfile{
		"openrouter-kimi": {}, "openrouter-glm": {},
	}}
	name, cands, act := several.ResolveOrchestratorProfile()
	if act != ProfilePick || name != "" {
		t.Errorf("several profiles, no default: got (%q, _, %d), want (\"\", _, ProfilePick)", name, act)
	}
	if want := []string{"openrouter-glm", "openrouter-kimi"}; !reflect.DeepEqual(cands, want) {
		t.Errorf("several profiles candidates: got %v, want %v (sorted)", cands, want)
	}

	// Rule 1: explicit default wins even when several profiles exist.
	several.Orchestrator.DefaultProfile = "openrouter-kimi"
	if name, cands, act := several.ResolveOrchestratorProfile(); act != ProfileSpawn || name != "openrouter-kimi" || cands != nil {
		t.Errorf("explicit default: got (%q, %v, %d), want (\"openrouter-kimi\", nil, ProfileSpawn)", name, cands, act)
	}

	// Rule 1 precedence: default wins over the lone-profile shortcut too.
	one.Orchestrator.DefaultProfile = "openrouter-glm"
	if name, _, act := one.ResolveOrchestratorProfile(); act != ProfileSpawn || name != "openrouter-glm" {
		t.Errorf("default over single: got (%q, _, %d), want (\"openrouter-glm\", _, ProfileSpawn)", name, act)
	}
}

func TestCockpitLayout(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty defaults to horizontal", "", "horizontal"},
		{"horizontal", "horizontal", "horizontal"},
		{"vertical", "vertical", "vertical"},
		{"tiled", "tiled", "tiled"},
		{"garbage falls back to horizontal", "sideways", "horizontal"},
	}
	for _, tc := range cases {
		c := &Config{}
		c.Cockpit.Layout = tc.in
		if got := c.CockpitLayout(); got != tc.want {
			t.Errorf("%s: CockpitLayout() with %q = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}

	// A workspace-layer cockpit.layout merges over the global one (cockpit
	// merges field-wise: layout wins per-key, sibling effects inherited).
	setHome(t)
	writeGlobal(t, `
cockpit:
  effects: calm
  layout: tiled
`)
	root := newWorkspace(t, `
workspace:
  label: grid
cockpit:
  layout: vertical
`)
	c, err := LoadAt(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.CockpitLayout(); got != "vertical" {
		t.Errorf("workspace cockpit.layout = %q, want vertical (workspace wins)", got)
	}
	if c.Cockpit.Effects != "calm" {
		t.Errorf("cockpit.effects = %q, want calm inherited from global", c.Cockpit.Effects)
	}
}
