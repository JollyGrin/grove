package doctor_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/bootstrap"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/connections"
	"github.com/JollyGrin/grove/internal/doctor"
	"github.com/JollyGrin/grove/internal/schema"
)

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Provider.Kind = "markdown"
	cfg.Provider.Markdown.Dir = ".grove/tasks"
	cfg.Linear.APIKeyEnv = "LINEAR_API_KEY"
	cfg.Repos = map[string]*config.Repo{
		"demo": {Path: "/repos/demo", Base: "main", Claude: "ccwork --dangerously-skip-permissions"},
	}
	return cfg
}

// happyEnv fakes a healthy darwin machine: every check passes except
// AGENTS.md (absent) — plus the always-warn dev-linear MCP reminder.
func happyEnv(cfg *config.Config) connections.Env {
	return connections.Env{
		Cfg:      cfg,
		LookPath: func(string) (string, error) { return "/usr/bin/x", nil },
		Getenv: func(k string) string {
			if k == "SHELL" {
				return "/bin/zsh"
			}
			return ""
		},
		Stat: func(name string) (os.FileInfo, error) {
			switch name {
			case "/repos/demo/.grove/tasks", "/repos/CLAUDE.md":
				return nil, nil
			}
			return nil, os.ErrNotExist
		},
		ReadFile: func(string) ([]byte, error) {
			return []byte(`{"plugins":{` +
				`"dev-core@workspace":{},"dev-superpowers@workspace":{},` +
				`"dev-linear@workspace":{},"dev-safety@workspace":{}}}`), nil
		},
		Run: func(time.Duration, string, ...string) error { return nil },
		HooksInstalled: func(paths []string) map[string]map[string]bool {
			out := map[string]map[string]bool{}
			for _, p := range paths {
				out[p] = map[string]bool{"SessionStart": true, "Notification": true, "Stop": true, "SessionEnd": true}
			}
			return out
		},
		HookSettingsPaths: func([]string) []string { return []string{"/profiles/work/settings.json"} },
		GOOS:              "darwin",
		Home:              "/home/u",
	}
}

func rows(env connections.Env) []doctor.Row {
	return doctor.FromResults(connections.EvaluateAll(env))
}

// TestFullRowSet pins the before→after row map from the phase-1a plan:
// every P0 doctor check maps to a manifest row, plus the three deliberate
// changes (terminal-notifier warn+darwin-only, provider-conditional linear
// key, dev-linear MCP static warn) and the new AGENTS.md warn row.
func TestFullRowSet(t *testing.T) {
	got := rows(happyEnv(testConfig()))

	want := []struct {
		id       string
		severity string
		state    string
		pack     string
	}{
		{"binary:tmux", "error", "ok", ""},
		{"binary:gh", "error", "ok", ""},
		{"binary:git", "error", "ok", ""},
		{"binary:terminal-notifier", "warn", "ok", ""},
		{"binary:claude", "error", "ok", ""},
		{"gh-auth", "error", "ok", ""},
		{"config", "error", "ok", ""},
		{"provider:markdown:demo", "error", "ok", ""},
		{"worker:ccwork", "error", "ok", ""},
		{"agents-md:demo", "warn", "warn", ""},
		{"memory:demo", "warn", "ok", ""},
		{"sub:lane", "warn", "ok", ""},
		{"effort-override", "warn", "ok", ""},
		{"hooks:/profiles/work/settings.json", "error", "ok", ""},
		{"grid:ccwork-plugins", "error", "ok", "grid-interim"},
		{"grid:dev-linear-mcp", "warn", "warn", "grid-interim"},
	}

	if len(got) != len(want) {
		var ids []string
		for _, r := range got {
			ids = append(ids, r.ID)
		}
		t.Fatalf("got %d rows, want %d:\n%s", len(got), len(want), strings.Join(ids, "\n"))
	}
	for i, w := range want {
		g := got[i]
		if g.ID != w.id || g.Severity != w.severity || g.State != w.state || g.Pack != w.pack {
			t.Errorf("row %d: got {%s %s %s pack=%q}, want {%s %s %s pack=%q}",
				i, g.ID, g.Severity, g.State, g.Pack, w.id, w.severity, w.state, w.pack)
		}
	}
}

// The orchestrator brain row (grove-190) is the delivery tripwire for a
// moved seed: the cockpit only ever seeds an ABSENT brain, so drift can
// only surface here. It stays green on a freshly seeded workspace and on
// a hand-managed (unstamped) brain; a stale stamp flags with the refresh
// command as the remedy.
func TestOrchestratorBrainRow(t *testing.T) {
	const seed = "# seed v2\n"
	brainEnv := func(content string, readErr error) connections.Env {
		env := happyEnv(testConfig())
		env.OrchestratorDir = "/ws/.grove/orchestrator"
		env.OrchestratorSeed = seed
		env.ReadFile = func(name string) ([]byte, error) {
			if name == "/ws/.grove/orchestrator/CLAUDE.md" {
				if readErr != nil {
					return nil, readErr
				}
				return []byte(content), nil
			}
			return happyEnv(testConfig()).ReadFile(name)
		}
		return env
	}
	find := func(t *testing.T, env connections.Env) doctor.Row {
		t.Helper()
		for _, r := range rows(env) {
			if r.ID == "orchestrator-md" {
				return r
			}
		}
		t.Fatal("no orchestrator-md row")
		return doctor.Row{}
	}

	fresh := find(t, brainEnv(bootstrap.StampSeed(seed), nil))
	if fresh.State != "ok" || fresh.Severity != "warn" {
		t.Errorf("freshly seeded: %+v, want ok/warn", fresh)
	}
	if fresh.Title != "orchestrator brain up to date" {
		t.Errorf("title = %q", fresh.Title)
	}

	stale := find(t, brainEnv(bootstrap.StampSeed("# seed v1\n")+"\nmy own notes\n", nil))
	if stale.State != "warn" {
		t.Errorf("stale stamp: state %q, want warn", stale.State)
	}
	if stale.Fix != "gv init --only orchestrator-md" {
		t.Errorf("fix = %q, want the refresh command", stale.Fix)
	}
	if !strings.Contains(stale.Info, "seed moved") {
		t.Errorf("info = %q, want the stamp move", stale.Info)
	}

	// Once the refresh has dropped a current .new beside the brain, the
	// row says it is waiting to be merged rather than reading as untouched.
	waiting := brainEnv(bootstrap.StampSeed("# seed v1\n"), nil)
	inner := waiting.ReadFile
	waiting.ReadFile = func(name string) ([]byte, error) {
		if name == "/ws/.grove/orchestrator/CLAUDE.md.new" {
			return []byte(bootstrap.StampSeed(seed)), nil
		}
		return inner(name)
	}
	if r := find(t, waiting); r.State != "warn" || !strings.Contains(r.Info, "waiting to be diffed in") {
		t.Errorf("pending .new: %+v", r)
	}

	handMade := find(t, brainEnv("# my own brain\n", nil))
	if handMade.State != "ok" || !strings.Contains(handMade.Info, "hand-managed") {
		t.Errorf("unstamped brain must be reported, not nagged: %+v", handMade)
	}

	unseeded := find(t, brainEnv("", os.ErrNotExist))
	if unseeded.State != "ok" || !strings.Contains(unseeded.Info, "not seeded") {
		t.Errorf("unseeded workspace: %+v, want a green 'not seeded yet' row", unseeded)
	}

	// No dir known (legacy global path, no config): the row drops out
	// rather than checking a guessed path.
	for _, r := range rows(happyEnv(testConfig())) {
		if r.ID == "orchestrator-md" {
			t.Error("row must not exist without an orchestrator dir")
		}
	}
}

func TestLinearProviderSwapsProviderRow(t *testing.T) {
	cfg := testConfig()
	cfg.Provider.Kind = "linear"
	env := happyEnv(cfg)
	env.Getenv = func(k string) string {
		switch k {
		case "SHELL":
			return "/bin/zsh"
		case "LINEAR_API_KEY":
			return "lin_api_x"
		}
		return ""
	}
	var ids []string
	for _, r := range rows(env) {
		ids = append(ids, r.ID)
	}
	joined := strings.Join(ids, " ")
	if !strings.Contains(joined, "provider:linear-key") {
		t.Error("linear provider must add the key-env row")
	}
	if strings.Contains(joined, "provider:markdown:") {
		t.Error("linear provider must not add markdown task-dir rows")
	}
}

// TestExitCodeSemantics pins the deliberate change: warnings alone exit 0
// (Errors == 0); only error-severity failures make doctor exit 1.
func TestExitCodeSemantics(t *testing.T) {
	// Happy board has two warn rows (AGENTS.md, dev-linear MCP): no errors.
	if n := doctor.Errors(rows(happyEnv(testConfig()))); n != 0 {
		t.Errorf("warnings-only board: Errors = %d, want 0", n)
	}

	// Break gh auth (error severity) → one error.
	env := happyEnv(testConfig())
	env.Run = func(_ time.Duration, name string, args ...string) error {
		if name == "gh" {
			return errors.New("not logged in")
		}
		return nil
	}
	if n := doctor.Errors(rows(env)); n != 1 {
		t.Errorf("broken gh auth: Errors = %d, want 1", n)
	}

	// Break terminal-notifier too (warn severity) → still one error.
	env.LookPath = func(name string) (string, error) {
		if name == "terminal-notifier" {
			return "", errors.New("not found")
		}
		return "/usr/bin/x", nil
	}
	if n := doctor.Errors(rows(env)); n != 1 {
		t.Errorf("extra warn failure must not count as error: Errors = %d, want 1", n)
	}
}

func TestRenderHappy(t *testing.T) {
	var buf bytes.Buffer
	doctor.Render(&buf, rows(happyEnv(testConfig())))
	out := buf.String()

	for _, want := range []string{
		"── grid pack (interim) ──",
		"\033[33m!\033[0m", // yellow warn mark present
		"AGENTS.md in demo",
		"→ gv init --only agents-md",
		"14/16 passed",
		"🌳 ready to grow",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderErrorSuppressesTree(t *testing.T) {
	env := happyEnv(testConfig())
	env.Run = func(_ time.Duration, name string, _ ...string) error {
		if name == "gh" {
			return errors.New("not logged in")
		}
		return nil
	}
	var buf bytes.Buffer
	doctor.Render(&buf, rows(env))
	out := buf.String()
	if strings.Contains(out, "🌳") {
		t.Error("tree must not print when errors remain")
	}
	if !strings.Contains(out, "→ gh auth login") {
		t.Errorf("missing fix line for gh auth:\n%s", out)
	}
}

func TestRenderJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := doctor.RenderJSON(&buf, rows(happyEnv(testConfig()))); err != nil {
		t.Fatal(err)
	}
	// grove-75: --json payloads ship in the plugin-contract envelope.
	var envelope struct {
		SchemaVersion int          `json:"schema_version"`
		Rows          []doctor.Row `json:"rows"`
	}
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if envelope.SchemaVersion != schema.Version {
		t.Errorf("schema_version = %d, want %d", envelope.SchemaVersion, schema.Version)
	}
	decoded := envelope.Rows
	if len(decoded) != 16 {
		t.Errorf("got %d rows, want 16", len(decoded))
	}
	if decoded[0].ID != "binary:tmux" || decoded[0].State != "ok" {
		t.Errorf("first row: %+v", decoded[0])
	}
}

// The effort-override row (grove-435): CLAUDE_CODE_EFFORT_LEVEL in the
// launching env beats every `gv grab --effort` silently, and a settings
// maxEffortLevel caps it the same way — doctor is the only place either
// surfaces. Green when neither is set; warn naming the override otherwise.
func TestEffortOverrideRow(t *testing.T) {
	find := func(t *testing.T, env connections.Env) doctor.Row {
		t.Helper()
		for _, r := range rows(env) {
			if r.ID == "effort-override" {
				return r
			}
		}
		t.Fatal("no effort-override row")
		return doctor.Row{}
	}

	clean := find(t, happyEnv(testConfig()))
	if clean.State != "ok" || clean.Severity != "warn" {
		t.Errorf("clean env: %+v, want ok/warn", clean)
	}

	env := happyEnv(testConfig())
	env.Getenv = func(k string) string {
		switch k {
		case "SHELL":
			return "/bin/zsh"
		case "CLAUDE_CODE_EFFORT_LEVEL":
			return "low"
		}
		return ""
	}
	fromEnv := find(t, env)
	if fromEnv.State != "warn" {
		t.Errorf("env var set: state %q, want warn", fromEnv.State)
	}
	if !strings.Contains(fromEnv.Info, "CLAUDE_CODE_EFFORT_LEVEL=low") || !strings.Contains(fromEnv.Info, "beats every --effort") {
		t.Errorf("info = %q, want the var, its value, and the consequence", fromEnv.Info)
	}
	if !strings.Contains(fromEnv.Fix, "unset CLAUDE_CODE_EFFORT_LEVEL") {
		t.Errorf("fix = %q, want the unset remedy", fromEnv.Fix)
	}

	env = happyEnv(testConfig())
	base := happyEnv(testConfig()).ReadFile
	env.ReadFile = func(name string) ([]byte, error) {
		if name == "/profiles/work/settings.json" {
			return []byte(`{"maxEffortLevel":"medium","hooks":{}}`), nil
		}
		return base(name)
	}
	fromSettings := find(t, env)
	if fromSettings.State != "warn" {
		t.Errorf("maxEffortLevel set: state %q, want warn", fromSettings.State)
	}
	if !strings.Contains(fromSettings.Info, "maxEffortLevel=medium in /profiles/work/settings.json") {
		t.Errorf("info = %q, want the cap and its file", fromSettings.Info)
	}
}

// fakeEntry is a minimal os.DirEntry for the memory row's ReadDir seam.
type fakeEntry struct {
	name  string
	dir   bool
	mtime time.Time
}

func (f fakeEntry) Name() string               { return f.name }
func (f fakeEntry) IsDir() bool                { return f.dir }
func (f fakeEntry) Type() fs.FileMode          { return 0 }
func (f fakeEntry) Info() (fs.FileInfo, error) { return fakeInfo{f}, nil }

type fakeInfo struct{ fakeEntry }

func (f fakeInfo) Size() int64        { return 1 }
func (f fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (f fakeInfo) ModTime() time.Time { return f.mtime }
func (f fakeInfo) Sys() any           { return nil }

// The auto-memory row (grove-437): per repo, where that repo's workers'
// auto memory resolves to, how many notes it holds and the newest
// `modified`. The directory follows Claude Code's derivation (the
// profile's projects dir keyed on the ENCODED repo path, shared by every
// worktree), is relocated by an autoMemoryDirectory in any readable
// scope (local > project > user), and the row warns when the surface is
// switched off — by settings or by CLAUDE_CODE_DISABLE_AUTO_MEMORY.
func TestMemoryRow(t *testing.T) {
	find := func(t *testing.T, env connections.Env) doctor.Row {
		t.Helper()
		for _, r := range rows(env) {
			if r.ID == "memory:demo" {
				return r
			}
		}
		t.Fatal("no memory:demo row")
		return doctor.Row{}
	}
	const defaultDir = "/profiles/work/projects/-repos-demo/memory"

	// Nothing written yet: green, naming the default directory.
	empty := find(t, happyEnv(testConfig()))
	if empty.State != "ok" || empty.Severity != "warn" {
		t.Errorf("fresh profile: %+v, want ok/warn", empty)
	}
	if !strings.Contains(empty.Info, defaultDir+" — no notes yet") {
		t.Errorf("info = %q, want the derived default dir and 'no notes yet'", empty.Info)
	}

	// A populated directory: the index is not a note; the newest
	// `modified` frontmatter wins over mtimes, and a note without
	// frontmatter falls back to its mtime.
	older := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	env := happyEnv(testConfig())
	base := env.ReadFile
	env.ReadDir = func(name string) ([]os.DirEntry, error) {
		if name != defaultDir {
			return nil, os.ErrNotExist
		}
		return []os.DirEntry{
			fakeEntry{name: "MEMORY.md", mtime: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)},
			fakeEntry{name: "a-lesson.md", mtime: older},
			fakeEntry{name: "b-lesson.md", mtime: older},
			fakeEntry{name: "subdir", dir: true},
		}, nil
	}
	env.ReadFile = func(name string) ([]byte, error) {
		switch name {
		case defaultDir + "/a-lesson.md":
			return []byte("---\nname: a\ntype: feedback\nmodified: 2026-10-07T11:41:00Z\n---\n\nbody\n"), nil
		case defaultDir + "/b-lesson.md":
			return []byte("no frontmatter\n"), nil
		}
		return base(name)
	}
	populated := find(t, env)
	if populated.State != "ok" {
		t.Errorf("populated: state %q, want ok", populated.State)
	}
	if !strings.Contains(populated.Info, "2 note(s), newest 2026-10-07 11:41Z") {
		t.Errorf("info = %q, want 2 notes (index excluded) and the frontmatter modified", populated.Info)
	}

	// autoMemoryDirectory in the repo's local scope beats the user scope,
	// and ~/ expands against Home.
	env = happyEnv(testConfig())
	env.ReadFile = func(name string) ([]byte, error) {
		switch name {
		case "/repos/demo/.claude/settings.local.json":
			return []byte(`{"autoMemoryDirectory":"~/mem/demo"}`), nil
		case "/profiles/work/settings.json":
			return []byte(`{"autoMemoryDirectory":"/elsewhere","hooks":{}}`), nil
		}
		return base(name)
	}
	relocated := find(t, env)
	if relocated.State != "ok" || !strings.Contains(relocated.Info, "/home/u/mem/demo (autoMemoryDirectory in /repos/demo/.claude/settings.local.json)") {
		t.Errorf("relocated: %+v, want the local-scope dir with its source", relocated)
	}

	// Disabled in the profile's settings: warn, naming the file.
	env = happyEnv(testConfig())
	env.ReadFile = func(name string) ([]byte, error) {
		if name == "/profiles/work/settings.json" {
			return []byte(`{"autoMemoryEnabled":false,"hooks":{}}`), nil
		}
		return base(name)
	}
	off := find(t, env)
	if off.State != "warn" || !strings.Contains(off.Info, "autoMemoryEnabled: false in /profiles/work/settings.json") {
		t.Errorf("disabled by settings: %+v, want warn naming the file", off)
	}
	if !strings.Contains(off.Fix, "autoMemoryEnabled") {
		t.Errorf("fix = %q, want the settings remedy", off.Fix)
	}

	// Disabled by the environment: warn, naming the variable.
	env = happyEnv(testConfig())
	env.Getenv = func(k string) string {
		switch k {
		case "SHELL":
			return "/bin/zsh"
		case "CLAUDE_CODE_DISABLE_AUTO_MEMORY":
			return "1"
		}
		return ""
	}
	offEnv := find(t, env)
	if offEnv.State != "warn" || !strings.Contains(offEnv.Info, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1") {
		t.Errorf("disabled by env: %+v, want warn naming the var", offEnv)
	}
}
