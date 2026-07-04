package connections

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/config"
)

// --- fakes ---

type call struct {
	timeout time.Duration
	name    string
	args    []string
}

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

const allPluginsJSON = `{"plugins":{` +
	`"dev-core@workspace":{},"dev-superpowers@workspace":{},` +
	`"dev-linear@workspace":{},"dev-safety@workspace":{}}}`

// happyEnv is a fake machine where every check passes except AGENTS.md
// (absent — the representative warn).
func happyEnv(cfg *config.Config) Env {
	return Env{
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
		ReadFile: func(string) ([]byte, error) { return []byte(allPluginsJSON), nil },
		Run:      func(time.Duration, string, ...string) error { return nil },
		HooksInstalled: func() (map[string]bool, error) {
			return map[string]bool{"SessionStart": true, "Notification": true, "Stop": true, "SessionEnd": true}, nil
		},
		GOOS: "darwin",
		Home: "/home/u",
	}
}

func findResult(t *testing.T, results []Result, id string) Result {
	t.Helper()
	for _, r := range results {
		if r.Connection.ID == id {
			return r
		}
	}
	t.Fatalf("no result with id %q", id)
	return Result{}
}

func hasResult(results []Result, id string) bool {
	for _, r := range results {
		if r.Connection.ID == id {
			return true
		}
	}
	return false
}

// --- per-kind checkers ---

func TestCheckBinary(t *testing.T) {
	e := Env{LookPath: func(string) (string, error) { return "/usr/bin/git", nil }}
	if st := checkBinary("git")(e); st.State != StateOK {
		t.Errorf("present binary: got %v, want ok", st.State)
	}
	e.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	if st := checkBinary("git")(e); st.State != StateMissing {
		t.Errorf("absent binary: got %v, want missing", st.State)
	}
}

func TestCheckEnvVar(t *testing.T) {
	e := Env{Getenv: func(k string) string {
		if k == "LINEAR_API_KEY" {
			return "lin_api_x"
		}
		return ""
	}}
	if st := checkEnvVar("LINEAR_API_KEY")(e); st.State != StateOK {
		t.Errorf("set var: got %v, want ok", st.State)
	}
	if st := checkEnvVar("OTHER")(e); st.State != StateMissing {
		t.Errorf("unset var: got %v, want missing", st.State)
	}
}

func TestCheckCLIAuth(t *testing.T) {
	var got call
	e := Env{Run: func(timeout time.Duration, name string, args ...string) error {
		got = call{timeout, name, args}
		return nil
	}}
	if st := checkCLIAuth("gh", "auth", "status")(e); st.State != StateOK {
		t.Errorf("authed: got %v, want ok", st.State)
	}
	if got.name != "gh" || strings.Join(got.args, " ") != "auth status" {
		t.Errorf("ran %s %v, want gh [auth status]", got.name, got.args)
	}
	if got.timeout != 3*time.Second {
		t.Errorf("timeout %v, want 3s", got.timeout)
	}
	e.Run = func(time.Duration, string, ...string) error { return errors.New("exit 1") }
	if st := checkCLIAuth("gh", "auth", "status")(e); st.State != StateMissing {
		t.Errorf("unauthed: got %v, want missing", st.State)
	}
}

func TestCheckWorkerCommandZsh(t *testing.T) {
	var got call
	e := Env{
		Getenv: func(k string) string {
			if k == "SHELL" {
				return "/bin/zsh"
			}
			return ""
		},
		Run: func(timeout time.Duration, name string, args ...string) error {
			got = call{timeout, name, args}
			return nil
		},
		LookPath: func(string) (string, error) { return "", errors.New("no") },
	}
	st := checkWorkerCommand("ccwork --dangerously-skip-permissions")(e)
	if st.State != StateOK {
		t.Errorf("got %v, want ok", st.State)
	}
	if got.name != "zsh" || strings.Join(got.args, " ") != "-ic whence -- ccwork" {
		t.Errorf("ran %s %v, want zsh [-ic whence -- ccwork]", got.name, got.args)
	}
	if got.timeout != 3*time.Second {
		t.Errorf("timeout %v, want 3s", got.timeout)
	}
}

func TestCheckWorkerCommandOtherShell(t *testing.T) {
	var got call
	e := Env{
		Getenv: func(k string) string {
			if k == "SHELL" {
				return "/bin/bash"
			}
			return ""
		},
		Run: func(timeout time.Duration, name string, args ...string) error {
			got = call{timeout, name, args}
			return nil
		},
	}
	if st := checkWorkerCommand("ccwork")(e); st.State != StateOK {
		t.Errorf("got %v, want ok", st.State)
	}
	if got.name != "/bin/bash" || strings.Join(got.args, " ") != "-ic command -v -- ccwork" {
		t.Errorf("ran %s %v, want /bin/bash [-ic command -v -- ccwork]", got.name, got.args)
	}
}

func TestCheckWorkerCommandFallsBackToLookPath(t *testing.T) {
	e := Env{
		Getenv: func(k string) string {
			if k == "SHELL" {
				return "/bin/zsh"
			}
			return ""
		},
		Run:      func(time.Duration, string, ...string) error { return errors.New("timeout") },
		LookPath: func(string) (string, error) { return "/usr/local/bin/ccwork", nil },
	}
	st := checkWorkerCommand("ccwork")(e)
	if st.State != StateOK || st.Info != "via PATH" {
		t.Errorf("got %v %q, want ok via PATH", st.State, st.Info)
	}

	e.LookPath = func(string) (string, error) { return "", errors.New("no") }
	if st := checkWorkerCommand("ccwork")(e); st.State != StateMissing {
		t.Errorf("probe+PATH both fail: got %v, want missing", st.State)
	}
}

func TestCheckWorkerCommandNoShell(t *testing.T) {
	ran := false
	e := Env{
		Getenv:   func(string) string { return "" },
		Run:      func(time.Duration, string, ...string) error { ran = true; return nil },
		LookPath: func(string) (string, error) { return "/usr/bin/claude", nil },
	}
	if st := checkWorkerCommand("claude")(e); st.State != StateOK {
		t.Errorf("got %v, want ok", st.State)
	}
	if ran {
		t.Error("no $SHELL must skip the interactive-shell probe")
	}
}

func TestCheckAgentContext(t *testing.T) {
	e := Env{Stat: func(name string) (os.FileInfo, error) {
		if name == "/repos/demo/CLAUDE.md" {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}}
	st := checkAgentContext("/repos/demo")(e)
	if st.State != StateOK || st.Info != "CLAUDE.md" {
		t.Errorf("got %v %q, want ok CLAUDE.md", st.State, st.Info)
	}
	e.Stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	if st := checkAgentContext("/repos/demo")(e); st.State != StateMissing {
		t.Errorf("got %v, want missing", st.State)
	}
}

func TestCheckHooks(t *testing.T) {
	e := Env{HooksInstalled: func() (map[string]bool, error) {
		return map[string]bool{"Stop": true, "SessionEnd": true}, nil
	}}
	st := checkHooks(e)
	if st.State != StateMissing || st.Info != "2/4 events wired" {
		t.Errorf("got %v %q, want missing 2/4 events wired", st.State, st.Info)
	}
	e.HooksInstalled = func() (map[string]bool, error) {
		return map[string]bool{"SessionStart": true, "Notification": true, "Stop": true, "SessionEnd": true}, nil
	}
	if st := checkHooks(e); st.State != StateOK {
		t.Errorf("got %v, want ok", st.State)
	}
	e.HooksInstalled = func() (map[string]bool, error) { return nil, errors.New("no settings.json") }
	if st := checkHooks(e); st.State != StateMissing {
		t.Errorf("read error: got %v, want missing", st.State)
	}
}

func TestCheckCcworkPlugins(t *testing.T) {
	e := Env{Home: "/home/u", ReadFile: func(name string) ([]byte, error) {
		if name != "/home/u/.cc-work/plugins/installed_plugins.json" {
			return nil, fmt.Errorf("unexpected read %s", name)
		}
		return []byte(allPluginsJSON), nil
	}}
	if st := checkCcworkPlugins(e); st.State != StateOK {
		t.Errorf("all plugins: got %v (%s), want ok", st.State, st.Info)
	}

	e.ReadFile = func(string) ([]byte, error) {
		return []byte(`{"plugins":{"dev-core@workspace":{}}}`), nil
	}
	st := checkCcworkPlugins(e)
	if st.State != StateMissing || !strings.Contains(st.Info, "dev-safety@workspace") {
		t.Errorf("got %v %q, want missing naming dev-safety@workspace", st.State, st.Info)
	}

	e.ReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if st := checkCcworkPlugins(e); st.State != StateMissing {
		t.Errorf("no file: got %v, want missing", st.State)
	}
}

// --- instance assembly + EvaluateAll ---

func TestEvaluateAllMarkdownProvider(t *testing.T) {
	results := EvaluateAll(happyEnv(testConfig()))

	if hasResult(results, "provider:linear-key") {
		t.Error("LINEAR_API_KEY row must not exist for the markdown provider")
	}
	r := findResult(t, results, "provider:markdown:demo")
	if r.Status.State != StateOK || r.Connection.Step != "provider" {
		t.Errorf("task dir row: got %v step %q", r.Status.State, r.Connection.Step)
	}
}

func TestEvaluateAllLinearProvider(t *testing.T) {
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
	results := EvaluateAll(env)

	if hasResult(results, "provider:markdown:demo") {
		t.Error("markdown task-dir row must not exist for the linear provider")
	}
	r := findResult(t, results, "provider:linear-key")
	if r.Status.State != StateOK || r.Connection.Kind != KindEnv {
		t.Errorf("linear key row: got %v kind %q", r.Status.State, r.Connection.Kind)
	}
}

func TestTerminalNotifierDarwinOnlyAndWarn(t *testing.T) {
	env := happyEnv(testConfig())
	env.LookPath = func(name string) (string, error) {
		if name == "terminal-notifier" {
			return "", errors.New("not found")
		}
		return "/usr/bin/x", nil
	}
	results := EvaluateAll(env)
	r := findResult(t, results, "binary:terminal-notifier")
	if r.Connection.Severity != SeverityWarn {
		t.Errorf("severity %v, want warn", r.Connection.Severity)
	}
	if r.Status.State != StateWarn {
		t.Errorf("missing warn-severity binary folds to warn, got %v", r.Status.State)
	}

	env.GOOS = "linux"
	if hasResult(EvaluateAll(env), "binary:terminal-notifier") {
		t.Error("terminal-notifier row must not exist off darwin")
	}
}

func TestAgentsMdWarnRow(t *testing.T) {
	results := EvaluateAll(happyEnv(testConfig()))
	r := findResult(t, results, "agents-md:demo")
	if r.Connection.Severity != SeverityWarn || r.Status.State != StateWarn {
		t.Errorf("got severity %v state %v, want warn/warn", r.Connection.Severity, r.Status.State)
	}
	if r.Connection.Fix != "gv init --only agents-md" {
		t.Errorf("fix %q, want gv init --only agents-md", r.Connection.Fix)
	}
}

func TestDevLinearMCPStaticWarn(t *testing.T) {
	results := EvaluateAll(happyEnv(testConfig()))
	r := findResult(t, results, "grid:dev-linear-mcp")
	if r.Status.State != StateWarn || r.Connection.Pack != PackGridInterim {
		t.Errorf("got state %v pack %q, want warn/grid-interim", r.Status.State, r.Connection.Pack)
	}
}

func TestGridUniversalClaudeMdDedupsParents(t *testing.T) {
	cfg := testConfig()
	cfg.Repos["demo2"] = &config.Repo{Path: "/repos/demo2", Base: "main", Claude: "ccwork"}
	results := EvaluateAll(happyEnv(cfg))
	n := 0
	for _, r := range results {
		if strings.HasPrefix(r.Connection.ID, "grid:universal-claude-md:") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("got %d universal CLAUDE.md rows for one shared parent, want 1", n)
	}
	// The two repos share one `ccwork` token too — one worker row.
	if !hasResult(results, "worker:ccwork") {
		t.Fatal("no worker row")
	}
	workers := 0
	for _, r := range results {
		if strings.HasPrefix(r.Connection.ID, "worker:") {
			workers++
		}
	}
	if workers != 1 {
		t.Errorf("got %d worker rows for one shared token, want 1", workers)
	}
}

func TestConfigErrorSkipsRepoDerivedRows(t *testing.T) {
	env := happyEnv(nil)
	env.Cfg = nil
	env.CfgErr = errors.New("read config: no such file")
	results := EvaluateAll(env)

	r := findResult(t, results, "config")
	if r.Status.State != StateMissing || !strings.Contains(r.Status.Info, "no such file") {
		t.Errorf("config row: got %v %q", r.Status.State, r.Status.Info)
	}
	for _, id := range []string{"provider:markdown:demo", "worker:ccwork", "agents-md:demo"} {
		if hasResult(results, id) {
			t.Errorf("row %s must not exist when config failed to load", id)
		}
	}
	// Machine/profile rows survive without config.
	for _, id := range []string{"binary:git", "gh-auth", "hooks", "grid:ccwork-plugins"} {
		if !hasResult(results, id) {
			t.Errorf("row %s must exist even when config failed to load", id)
		}
	}
}

func TestEvaluateAllOrderCoreThenGrid(t *testing.T) {
	results := EvaluateAll(happyEnv(testConfig()))
	sawGrid := false
	for _, r := range results {
		if r.Connection.Pack == PackGridInterim {
			sawGrid = true
		} else if sawGrid {
			t.Fatalf("core row %s after a grid-interim row", r.Connection.ID)
		}
	}
	if !sawGrid {
		t.Fatal("no grid-interim rows")
	}
}
