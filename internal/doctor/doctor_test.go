package doctor_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

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
		"11/13 passed",
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
	if len(decoded) != 13 {
		t.Errorf("got %d rows, want 13", len(decoded))
	}
	if decoded[0].ID != "binary:tmux" || decoded[0].State != "ok" {
		t.Errorf("first row: %+v", decoded[0])
	}
}
