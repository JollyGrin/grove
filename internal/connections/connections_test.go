package connections

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/hooks"
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
	fake := func(events map[string]bool) func([]string) map[string]map[string]bool {
		return func(paths []string) map[string]map[string]bool {
			out := map[string]map[string]bool{}
			for _, p := range paths {
				out[p] = events
			}
			return out
		}
	}
	e := Env{HooksInstalled: fake(map[string]bool{"Stop": true, "SessionEnd": true})}
	st := checkHooksAt("/p/settings.json")(e)
	if st.State != StateMissing || st.Info != "2/4 events wired" {
		t.Errorf("got %v %q, want missing 2/4 events wired", st.State, st.Info)
	}
	e.HooksInstalled = fake(map[string]bool{"SessionStart": true, "Notification": true, "Stop": true, "SessionEnd": true})
	if st := checkHooksAt("/p/settings.json")(e); st.State != StateOK {
		t.Errorf("got %v, want ok", st.State)
	}
	e.HooksInstalled = fake(nil) // missing file reads as nothing installed
	if st := checkHooksAt("/p/settings.json")(e); st.State != StateMissing {
		t.Errorf("missing file: got %v, want missing", st.State)
	}
}

// SettingsPaths maps worker commands to their profiles' settings files —
// the fix for hooks landing only in the Grid's ~/.cc-work while personal
// plain-claude workers went uncaptured.
func TestSettingsPathsDerivation(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := hooks.SettingsPaths([]string{
		"ccwork --dangerously-skip-permissions",
		"claude --dangerously-skip-permissions",
		"claude",
		"CLAUDE_CONFIG_DIR=~/.cc-hobby claude",
	})
	want := []string{
		filepath.Join(home, ".cc-hobby", "settings.json"),
		filepath.Join(home, ".cc-work", "settings.json"),
		filepath.Join(home, ".claude", "settings.json"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path %d: got %s, want %s", i, got[i], want[i])
		}
	}
	if def := hooks.SettingsPaths(nil); len(def) != 1 || def[0] != filepath.Join(home, ".claude", "settings.json") {
		t.Errorf("empty fleet must yield the default profile, got %v", def)
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
	// The grid check is scoped to linear-driven repos (see the scoping
	// test below) — mark both so the shared parent is in scope at all.
	cfg.Repos["demo"].Provider = "linear"
	cfg.Repos["demo2"] = &config.Repo{Path: "/repos/demo2", Base: "main", Claude: "ccwork", Provider: "linear"}
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
	// Machine/profile rows survive without config — hooks fall back to the
	// default profile's settings path.
	for _, id := range []string{"binary:git", "gh-auth", "hooks:/profiles/work/settings.json", "grid:ccwork-plugins"} {
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

// The universal-CLAUDE.md grid convention applies to linear-driven repos
// only — markdown side-repos sharing a parent (e.g. ~/git) must not
// red-flag the doctor.
func TestGridUniversalClaudeMDScopedToLinearRepos(t *testing.T) {
	cfg := &config.Config{}
	cfg.Provider.Kind = "markdown"
	cfg.Repos = map[string]*config.Repo{
		"side": {Path: "/home/x/side"},
		"grid": {Path: "/home/x/acme/mono", Provider: "linear"},
	}
	env := Env{Cfg: cfg, Stat: func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }}
	var ids []string
	for _, c := range GridInterim(env) {
		ids = append(ids, c.ID)
	}
	joined := strings.Join(ids, " ")
	if !strings.Contains(joined, "grid:universal-claude-md:/home/x/acme") {
		t.Errorf("linear repo parent must be checked: %v", ids)
	}
	if strings.Contains(joined, "/home/x/side") || strings.Contains(joined, "universal-claude-md:/home/x ") {
		t.Errorf("markdown repo parent must NOT be checked: %v", ids)
	}
}

// Mixed fleets get provider rows per EFFECTIVE kind (plan review I-4):
// github repos must not get bogus markdown task-dir rows, and the linear
// key row appears once when any repo is linear-driven.
func TestProviderConnectionsMixedFleet(t *testing.T) {
	cfg := testConfig() // global kind markdown; repo "demo" markdown
	cfg.Repos["gh"] = &config.Repo{Path: "/repos/gh", Provider: "github", Claude: "claude"}
	cfg.Repos["lin"] = &config.Repo{Path: "/repos/lin", Provider: "linear", Claude: "claude"}
	results := EvaluateAll(happyEnv(cfg))

	for _, want := range []string{"provider:markdown:demo", "provider:github:gh", "provider:linear-key"} {
		if !hasResult(results, want) {
			t.Errorf("missing row %s", want)
		}
	}
	for _, banned := range []string{"provider:markdown:gh", "provider:markdown:lin", "provider:github:demo"} {
		if hasResult(results, banned) {
			t.Errorf("bogus row %s for the wrong backend", banned)
		}
	}
}

// grove-176: one warn row per configured host; reachable → ok + version,
// unreachable → warn with the ssh error, never an error-severity block.
func TestRemoteHostRows(t *testing.T) {
	cfg := &config.Config{Hosts: map[string]*config.Host{
		"vps":  {SSH: "dean@vps", GV: "/home/dean/go/bin/gv"},
		"down": {SSH: "nobody@down", GV: "gv"},
	}}
	env := Env{Cfg: cfg, Output: func(_ time.Duration, name string, args ...string) (string, error) {
		if name != "ssh" || args[len(args)-1] != "--version" {
			t.Fatalf("probe = %s %v", name, args)
		}
		if args[len(args)-4] == "dean@vps" {
			if args[len(args)-2] != "/home/dean/go/bin/gv" {
				t.Errorf("vps probe uses %q, want configured gv path", args[len(args)-2])
			}
			return "gv v1.2.3\n", nil
		}
		return "", errors.New("exit status 255")
	}}
	conns := remoteHostConnections(env)
	if len(conns) != 2 || conns[0].ID != "host:down" || conns[1].ID != "host:vps" {
		t.Fatalf("rows = %+v", conns)
	}
	for _, c := range conns {
		if c.Severity != SeverityWarn || c.Kind != KindRemoteHost {
			t.Errorf("%s: severity %s kind %s, want warn remote-host", c.ID, c.Severity, c.Kind)
		}
	}
	if st := conns[1].Check(env); st.State != StateOK || st.Info != "gv v1.2.3" {
		t.Errorf("vps = %+v", st)
	}
	if st := conns[0].Check(env); st.State != StateMissing || !strings.Contains(st.Info, "255") {
		t.Errorf("down = %+v", st)
	}
	if st := conns[0].Check(Env{Cfg: cfg}); st.State != StateMissing {
		t.Errorf("nil Output seam = %+v, want missing", st)
	}
}
