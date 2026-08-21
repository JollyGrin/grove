// Package connections is grove's declarative manifest of everything the
// tool depends on to function: binaries, auth, config, worker commands,
// hooks. Each dependency is declared once as a Connection; consumers
// (doctor rendering, the wizard's summary board, later reconnect prompts)
// derive from EvaluateAll instead of hand-maintaining their own checks.
// Design: docs/grove-connections-design.md §3 (manifest) + §5.1 (doctor).
package connections

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/hooks"
)

// Severity says what a failing connection does: error blocks the verbs in
// RequiredFor (and doctor exit code); warn only degrades (banner, not stop).
type Severity string

const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
)

// State is the flutter-style check outcome, rendered ✓ / ! / ✗.
type State string

const (
	StateOK      State = "ok"
	StateWarn    State = "warn"
	StateMissing State = "missing"
)

// Connection kinds — the check machinery is generic per kind; instances
// are data (design doc §3 taxonomy).
const (
	KindBinary        = "binary"
	KindEnv           = "env"
	KindFile          = "file"
	KindCLIAuth       = "cli-auth"
	KindWorkerCommand = "worker-command"
	KindAgentPlugins  = "agent-plugins"
	KindHooks         = "hooks"
	KindMCPAuth       = "mcp-auth"
	KindRemoteHost    = "remote-host"
)

// Status is one evaluated check result.
type Status struct {
	State State
	Info  string
}

// Connection is one declared dependency.
type Connection struct {
	ID          string
	Step        string // wizard step id (`gv init --only <step>`); "" when no step owns it
	Kind        string
	Severity    Severity
	RequiredFor []string // verbs that need this connection
	Title       string
	Fix         string // cheapest copy-pastable remedy
	Pack        string // "" = core; PackGridInterim = compiled-in grid pack
	Check       func(Env) Status
}

// Result pairs a declared connection with its evaluated status.
type Result struct {
	Connection Connection
	Status     Status
}

// Env carries the loaded config plus injectable seams for every side
// effect a checker performs, so kind checkers are table-testable without
// touching the real machine.
type Env struct {
	Cfg    *config.Config
	CfgErr error

	LookPath          func(file string) (string, error)
	Getenv            func(key string) string
	Stat              func(name string) (os.FileInfo, error)
	ReadFile          func(name string) ([]byte, error)
	Run               func(timeout time.Duration, name string, args ...string) error
	Output            func(timeout time.Duration, name string, args ...string) (string, error) // stdout-capturing Run (remote-host probes)
	HooksInstalled    func(paths []string) map[string]map[string]bool
	HookSettingsPaths func(workers []string) []string
	GOOS              string
	Home              string
}

// NewEnv builds the real-machine Env.
func NewEnv(cfg *config.Config, cfgErr error) Env {
	home, _ := os.UserHomeDir()
	return Env{
		Cfg:               cfg,
		CfgErr:            cfgErr,
		LookPath:          exec.LookPath,
		Getenv:            os.Getenv,
		Stat:              os.Stat,
		ReadFile:          os.ReadFile,
		Run:               run,
		Output:            output,
		HooksInstalled:    hooks.Installed,
		HookSettingsPaths: hooks.SettingsPaths,
		GOOS:              runtime.GOOS,
		Home:              home,
	}
}

// probeTimeout bounds every subprocess probe (cli-auth, worker-command) so
// a wedged interactive shell can never hang doctor/init.
const probeTimeout = 3 * time.Second

func run(timeout time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Run()
}

// remoteProbeTimeout bounds the per-host ssh probe (grove-176): a
// Tailscale hop plus a remote `gv --version` comfortably fits, and a
// host that is down costs doctor at most this long — never a hang.
const remoteProbeTimeout = 8 * time.Second

func output(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

// EvaluateAll evaluates every declared connection in render order: core
// first, then the grid-interim pack. Warn-severity failures are folded to
// StateWarn — they degrade, never block.
func EvaluateAll(env Env) []Result {
	conns := append(Core(env), GridInterim(env)...)
	results := make([]Result, 0, len(conns))
	for _, c := range conns {
		st := c.Check(env)
		if st.State == StateMissing && c.Severity == SeverityWarn {
			st.State = StateWarn
		}
		results = append(results, Result{Connection: c, Status: st})
	}
	return results
}

// --- per-kind checkers (generic machinery; instances live in core.go and
// grid_interim.go as data) ---

func checkBinary(name string) func(Env) Status {
	return func(e Env) Status {
		if _, err := e.LookPath(name); err != nil {
			return Status{State: StateMissing}
		}
		return Status{State: StateOK}
	}
}

func checkEnvVar(name string) func(Env) Status {
	return func(e Env) Status {
		if e.Getenv(name) == "" {
			return Status{State: StateMissing}
		}
		return Status{State: StateOK}
	}
}

func checkFile(path string) func(Env) Status {
	return func(e Env) Status {
		if _, err := e.Stat(path); err != nil {
			return Status{State: StateMissing}
		}
		return Status{State: StateOK}
	}
}

func checkCLIAuth(name string, args ...string) func(Env) Status {
	return func(e Env) Status {
		if err := e.Run(probeTimeout, name, args...); err != nil {
			return Status{State: StateMissing}
		}
		return Status{State: StateOK}
	}
}

// checkRemoteHost probes a configured remote grove host (grove-176):
// ssh reachability in BatchMode (no password prompts) plus the remote
// `<gv> --version`, whose first line lands in Info. Best-effort: the
// row is warn-severity, so an unreachable overflow box never blocks
// local work.
func checkRemoteHost(h *config.Host) func(Env) Status {
	return func(e Env) Status {
		if e.Output == nil {
			return Status{State: StateMissing, Info: "no probe"}
		}
		out, err := e.Output(remoteProbeTimeout, "ssh",
			"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", h.SSH, "--", h.GV, "--version")
		if err != nil {
			return Status{State: StateMissing, Info: "ssh " + h.SSH + ": " + err.Error()}
		}
		ver := strings.TrimSpace(out)
		if i := strings.IndexByte(ver, '\n'); i >= 0 {
			ver = ver[:i]
		}
		return Status{State: StateOK, Info: ver}
	}
}

// checkWorkerCommand resolves the first token of the worker command in the
// user's *interactive* shell — worker panes run interactive shells, where
// aliases (the usual `ccwork` shape) resolve but LookPath can't see them.
// Shell-aware: zsh gets `whence` (aliases + functions + PATH), anything
// else gets POSIX `command -v`. On probe timeout/error (or no $SHELL) it
// falls back to a plain PATH lookup of the token.
func checkWorkerCommand(command string) func(Env) Status {
	return func(e Env) Status {
		fields := strings.Fields(command)
		if len(fields) == 0 {
			return Status{State: StateMissing, Info: "empty worker command"}
		}
		tok := fields[0]
		shell := e.Getenv("SHELL")
		if shell != "" {
			var err error
			if filepath.Base(shell) == "zsh" {
				err = e.Run(probeTimeout, "zsh", "-ic", "whence -- "+tok)
			} else {
				err = e.Run(probeTimeout, shell, "-ic", "command -v -- "+tok)
			}
			if err == nil {
				return Status{State: StateOK}
			}
		}
		if _, err := e.LookPath(tok); err == nil {
			return Status{State: StateOK, Info: "via PATH"}
		}
		return Status{State: StateMissing}
	}
}

// checkAgentContext reports whether a repo carries agent-readable context.
func checkAgentContext(repoPath string) func(Env) Status {
	return func(e Env) Status {
		for _, f := range []string{"AGENTS.md", "CLAUDE.md"} {
			if _, err := e.Stat(filepath.Join(repoPath, f)); err == nil {
				return Status{State: StateOK, Info: f}
			}
		}
		return Status{State: StateMissing}
	}
}

func checkHooksAt(path string) func(Env) Status {
	return func(e Env) Status {
		installed := e.HooksInstalled([]string{path})[path]
		wired := 0
		for _, ok := range installed {
			if ok {
				wired++
			}
		}
		if wired == 4 {
			return Status{State: StateOK}
		}
		return Status{State: StateMissing, Info: fmt.Sprintf("%d/4 events wired", wired)}
	}
}
