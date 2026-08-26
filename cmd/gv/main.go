// gv — grove: turns Linear tickets into autonomous Claude Code sessions
// in detached tmux and answers "what can I act on right now?"
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/JollyGrin/grove/internal/audit"
	"github.com/JollyGrin/grove/internal/bootstrap"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/cost"
	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/doctor"
	"github.com/JollyGrin/grove/internal/git"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/hooks"
	"github.com/JollyGrin/grove/internal/kickoff"
	"github.com/JollyGrin/grove/internal/ledger"
	"github.com/JollyGrin/grove/internal/linear"
	"github.com/JollyGrin/grove/internal/probe"
	"github.com/JollyGrin/grove/internal/provider"
	"github.com/JollyGrin/grove/internal/remote"
	"github.com/JollyGrin/grove/internal/resource"
	"github.com/JollyGrin/grove/internal/schema"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/tui"
	"github.com/JollyGrin/grove/internal/update"
	"github.com/JollyGrin/grove/internal/wizard"
	"github.com/JollyGrin/grove/internal/workspace"
	"github.com/JollyGrin/grove/internal/worktree"
	"github.com/JollyGrin/grove/orchestrator"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"
)

// version is stamped at build time via -ldflags "-X main.version=<tag>"
// (see .github/workflows/release.yml); a plain `go build` leaves it "dev".
var version = "dev"

const usage = `gv — grove

  gv version | gv --version                    print the build version
  gv update [--yes] [--force]                 self-update from the latest GitHub release
  gv init [--yes|--only <step>]               wizard: probe · confirm · connections board
                                              (workspace-aware: repo scope in a git repo,
                                              parent scope in a folder of sibling repos)
  gv switch [<label>] [--print]               cross-workspace picker with live rollups
  gv workspaces [--json|add <path>|rm <label>] manage the workspace registry
  gv grab [<task>] [--repo name] [--manual] [--model id] [--profile p]   task → worktree → agent (no arg: list backlog)
  gv ls [--json]                              fleet table
      … --host <name>                         grab/ls/adopt on a configured remote host (hosts: in config.yaml)
  gv handoff <ticket> --to <host> [--rm] [--yes] [--no-checkpoint] [--timeout 10m]   move a running task to a remote host
  gv handoff <ticket> --from <host>            the mirror: release it there, cold-adopt it here
  gv audit [--json]                           cross-check tasks vs reality (pure read)
  gv cost [--json] [--analyze]                per-ticket token/cost estimates (pure read)
  gv cost --ledger | --record on|off          recorded spend history · persistence toggle
  gv answer <ticket> [text]                   reply to a waiting agent
  gv nudge <ticket> [text]                    follow-up prompt to a session
  gv attach <ticket>                          jump into the tmux window
  gv diff <ticket> [--stat]                   branch diff vs base — review without attach
  gv adopt <ticket> [--branch b] [--manual] [--model id]   revive a disconnected task / adopt a branch
  gv pause <ticket> [--force]                 park a worker: kill its window to free CPU — worktree,
                                              branch, and uncommitted changes survive; resume: gv adopt
  gv done <ticket> [--force]                  verify merged → clean up everything
  gv untrack <ticket> [--rm] [--rm-remote]    stop tracking (git untouched unless --rm)
  gv sweep [--dry-run|--json]                 per-row-confirmed cleanup offers: merged→done,
                                              abandoned→untrack, idle→pause, orphan process→kill
  gv park                                     kill this workspace's cockpit session (free memory) — resume with gv + gv adopt
  gv                                          cockpit: dashboard left, orchestrator chats right
  gv orchestrator new [--profile p]            add an orchestrator chat pane (O in the TUI; ) for a profiled one);
                                              --profile opens it on a model profile instead of Claude
  gv orchestrator close [--ticket X]          dismiss this chat's own pane (fire-and-forget dispatch)
  gv dash                                     dashboard TUI only (the cockpit's left pane)
  gv mobile                                   phone-sized dashboard session (for SSH/Termius)
  gv doctor                                   preflight checks
  gv hooks install|status                     wire settings.json per worker profile (default ~/.claude)
  gv hook <event>                             (internal) hook receiver
  gv run-setup <repo>                         (internal) serialized worktree setup
`

// ambient is the workspace context resolved once at startup from cwd
// (nearest .grove/ walk-up). Legacy — no workspace — keeps today's
// behavior: global config + global state dir.
var ambient struct {
	ws       *workspace.Workspace
	stateDir string
}

func resolveAmbient() {
	if cwd, err := os.Getwd(); err == nil {
		ambient.ws = workspace.Find(cwd)
	}
	root := ""
	if ambient.ws != nil {
		root = ambient.ws.Root
	}
	ambient.stateDir = config.StateDirAt(root)
}

// loadCfg loads the ambient workspace's merged config, or the global one
// on the legacy path.
func loadCfg() (*config.Config, error) {
	if ambient.ws != nil {
		return config.LoadAt(ambient.ws.Root)
	}
	return config.Load()
}

func stateDir() string { return ambient.stateDir }

func wsLabel() string {
	if ambient.ws != nil {
		return ambient.ws.Label
	}
	return ""
}

// echoWorkspace prints the resolved fleet before a mutating verb acts —
// the DESIGN §14 guard against nested-workspace surprises.
func echoWorkspace() {
	if ambient.ws != nil {
		fmt.Printf("→ workspace: %s\n", ambient.ws.Label)
	}
}

// shellQuoteRoot is the ambient workspace root as a single-quoted shell
// argv token for the run-setup relay (” on the legacy path).
func shellQuoteRoot() string {
	root := ""
	if ambient.ws != nil {
		root = ambient.ws.Root
	}
	return "'" + strings.ReplaceAll(root, "'", `'\''`) + "'"
}

// allWorkerCommands unions worker commands across the legacy global
// config AND every registered workspace — hooks coverage is machine-wide,
// never ambient-scoped (plan review round-2: installing from unbrewed
// must not drop the Grid profile).
func allWorkerCommands() []string {
	var out []string
	if cfg, err := config.Load(); err == nil {
		out = append(out, hooks.WorkerCommands(cfg)...)
	}
	list, _ := workspace.LoadRegistry()
	for _, ws := range list {
		if !workspace.Alive(ws) {
			continue
		}
		if cfg, err := config.LoadAt(ws.Root); err == nil {
			out = append(out, hooks.WorkerCommands(cfg)...)
		}
	}
	return out
}

// hookCandidates is the receiver's ownership scan order: registered
// workspaces sorted by label, legacy global LAST (DESIGN §12 —
// ownership by task membership, never directory guessing).
func hookCandidates() []hooks.Candidate {
	var out []hooks.Candidate
	list, _ := workspace.LoadRegistry()
	sort.Slice(list, func(i, j int) bool { return list[i].Label < list[j].Label })
	for _, ws := range list {
		if !workspace.Alive(ws) {
			continue
		}
		out = append(out, hooks.Candidate{Label: ws.Label, StateDir: config.StateDirAt(ws.Root)})
	}
	return append(out, hooks.Candidate{Label: "", StateDir: config.StateDir()})
}

func main() {
	resolveAmbient()
	if len(os.Args) < 2 {
		// Bare gv: inside a workspace -> its cockpit; outside with a
		// registry -> the switcher (DESIGN 6.5.3); outside with none ->
		// the legacy global cockpit.
		var err error
		if ambient.ws == nil {
			if list, _ := workspace.LoadRegistry(); len(list) > 0 {
				err = cmdSwitch(nil)
			} else {
				err = cmdUI()
			}
		} else {
			err = cmdUI()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "gv:", err)
			os.Exit(1)
		}
		return
	}
	cmd, args := os.Args[1], os.Args[2:]
	if cmd == "--version" {
		cmd = "version"
	}

	// Hook receiver: always exit 0, never break a session.
	if cmd == "hook" {
		if len(args) == 1 {
			if err := hooks.Receive(hookCandidates(), args[0], os.Stdin); err != nil {
				fmt.Fprintln(os.Stderr, "gv hook:", err)
			}
		}
		return
	}

	// --host <name> (grove-176): run the verb on a remote grove host over
	// ssh, flags passed through verbatim, output printed unchanged, exit
	// code propagated. Intercepted before dispatch so the local verb never
	// touches local state for a remote task. Only verbs that support
	// --host are scanned at all: a relay's free text may legitimately
	// contain "--host pc" (`gv nudge grove-7 try gv ls --host pc`), and
	// string-scanning every argv would hijack it.
	if remote.Supported[cmd] {
		if host, rest := remote.ExtractHost(args); host != "" {
			code, err := runRemote(host, cmd, rest)
			if err != nil {
				fmt.Fprintln(os.Stderr, "gv:", err)
				os.Exit(1)
			}
			os.Exit(code)
		}
	}

	var err error
	switch cmd {
	case "version":
		cmdVersion()
	case "update":
		err = cmdUpdate(args)
	case "init":
		err = cmdInit(args)
	case "grab":
		err = cmdGrab(args)
	case "ls":
		err = cmdLs(args)
	case "audit":
		err = cmdAudit(args)
	case "cost":
		err = cmdCost(args)
	case "answer":
		err = cmdRelay(args, true)
	case "nudge":
		err = cmdRelay(args, false)
	case "attach":
		err = cmdAttach(args)
	case "diff":
		err = cmdDiff(args)
	case "adopt":
		err = cmdAdopt(args)
	case "pause":
		err = cmdPause(args)
	case "done":
		err = cmdDone(args)
	case "untrack":
		err = cmdUntrack(args)
	case "handoff":
		err = cmdHandoff(args)
	case "sweep":
		err = cmdSweep(args)
	case "park", "close":
		err = cmdPark(args)
	case "ui":
		err = cmdUI()
	case "switch":
		err = cmdSwitch(args)
	case "workspaces":
		err = cmdWorkspaces(args)
	case "dash":
		err = cmdDashboard()
	case "orchestrator":
		switch {
		case len(args) > 0 && args[0] == "new":
			err = cmdOrchestratorNew(args[1:])
		case len(args) > 0 && args[0] == "close":
			err = cmdOrchestratorClose(args[1:])
		default:
			err = cmdUI()
		}
	case "mobile":
		err = cmdMobile()
	case "doctor":
		err = cmdDoctor(args)
	case "hooks":
		err = cmdHooks(args)
	case "run-setup":
		err = cmdRunSetup(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gv:", err)
		os.Exit(1)
	}
}

// runRemote resolves the host from config and passes the verb through.
// Config load errors surface here even for verbs that tolerate a broken
// config locally — without config there is no host to reach.
func runRemote(host, verb string, args []string) (int, error) {
	if !remote.Supported[verb] {
		return 0, fmt.Errorf("--host is not supported for `gv %s` yet (supported: %s)", verb, remote.SupportedList)
	}
	cfg, err := loadCfg()
	if err != nil {
		return 0, err
	}
	return remote.Run(cfg, host, verb, args, os.Stdout, os.Stderr)
}

// cmdVersion prints the stamped build version (`gv version` / `gv --version`).
func cmdVersion() {
	fmt.Printf("gv %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
}

// cmdUpdate replaces the running binary with the latest GitHub release
// (grove-160). Propose-then-dispose: prints old → new and asks before
// touching anything; --yes skips the prompt, --force allows replacing a
// dev (source) build. All decision logic lives in internal/update.
func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	force := fs.Bool("force", false, "replace even a dev (source) build")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts := update.Options{Current: version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Force: *force}
	if !*yes {
		sc := bufio.NewScanner(os.Stdin)
		opts.Confirm = func(_, _ string) bool {
			fmt.Print("replace the installed binary? [y/N] ")
			return sc.Scan() && strings.ToLower(strings.TrimSpace(sc.Text())) == "y"
		}
	}
	return update.Run(opts)
}

// cmdDashboard runs the TUI. Attach is handled after the tea loop exits
// because tmux attach replaces the process (syscall.Exec).
func cmdDashboard() error {
	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	tui.FinishTask = finishTask
	tui.SpawnOrchestrator = spawnOrchestrator
	tui.SpawnOrchestratorProfile = spawnOrchestratorProfile
	tui.AttachTask = attachTask
	tui.CloseWorkspace = closeWorkspace
	tui.SaveHotkeyBinding = func(digit, profile string) error {
		// Workspace-scoped like the orchestrator block it lives in (LoadAt
		// drops the global orchestrator section inside a workspace).
		root := ""
		if ambient.ws != nil {
			root = ambient.ws.Root
		}
		return config.SaveHotkey(config.PathAt(root), digit, profile)
	}
	attachTo, farewell, err := tui.Run(cfg, stateDir(), wsLabel())
	if err != nil {
		return err
	}
	if attachTo != nil {
		// Outside-tmux path only: attach replaces the process, so the
		// TUI had to quit first. Inside tmux the TUI attaches in place.
		return attachTask(attachTo)
	}
	// A5 quit farewell (grove-56): one dim line after the alt-screen closes.
	// Empty at fx off — a quiet quit stays quiet.
	if farewell != "" {
		fmt.Println(farewell)
	}
	return nil
}

// cmdUI builds the cockpit (jayminwest-style): tmux session `gv`, pane 0 =
// dashboard TUI, pane 1 = orchestrator Claude chat in its own directory.
// The orchestrator cwd is untracked, so worker hooks ignore it; --continue
// resumes the same conversation across cockpit launches.
func cmdUI() error {
	return openCockpit(ambient.ws)
}

// openCockpit builds (if needed) and attaches the cockpit for a
// workspace; nil = the legacy global cockpit.
func openCockpit(ws *workspace.Workspace) error {
	var cfg *config.Config
	var err error
	if ws != nil {
		cfg, err = config.LoadAt(ws.Root)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return err
	}
	session := cockpitSessionFor(ws)
	// Not just "does the session exist" — grab may have created it as a
	// reserved placeholder (window 0 = cockpit hint) before the cockpit was
	// ever opened. Build (upgrade the placeholder in place) until it's ready.
	if !tmux.CockpitReady(session) {
		if err := buildCockpit(ws, cfg); err != nil {
			return err
		}
	}
	return tmux.AttachSession(session)
}

// cockpitSessionFor: grove-<label> per workspace; legacy = grove.
func cockpitSessionFor(ws *workspace.Workspace) string {
	if ws != nil {
		return cockpitSessionForLabel(ws.Label)
	}
	return "grove"
}

// cockpitSessionForLabel is the label-string form of cockpitSessionFor, for
// callers that carry only the label (the TUI, `gv park`).
func cockpitSessionForLabel(label string) string {
	if label == "" {
		return "grove"
	}
	return "grove-" + label
}

// closeWorkspace parks a workspace (grove-33): it logs the durable
// EvWorkspaceParked BEFORE killing the shared grove-<label> session, so the
// record survives — kill-session takes down the cockpit, the orchestrator,
// and every worker in one stroke, freeing their memory. Nothing is deleted
// and no task terminal state is touched: bare `gv` rebuilds the cockpit and
// `gv adopt <ticket>` revives a worker. Injected into the TUI as
// tui.CloseWorkspace.
func closeWorkspace(stateDir, label string) error {
	if err := state.Append(stateDir, state.Event{Type: state.EvWorkspaceParked}); err != nil {
		return err
	}
	return tmux.KillSession(cockpitSessionForLabel(label))
}

// cmdPark is the CLI twin of the cockpit X hotkey (grove-33): park the
// ambient workspace. Run from outside the cockpit (or via orchestrator
// dispatch); run from inside the session it kills, the print may not land
// because the pane dies with the session — the parked event is durable
// either way.
func cmdPark(args []string) error {
	session := cockpitSessionForLabel(wsLabel())
	if !tmux.SessionExists(session) {
		return fmt.Errorf("%s is not running — nothing to park", session)
	}
	fmt.Printf("⏸ parking %s — state saved; resume with gv (then gv adopt <ticket>)\n", session)
	return closeWorkspace(stateDir(), wsLabel())
}

// cockpitTitleFor: the outer terminal-tab title for a cockpit — the
// workspace label (e.g. "unbrewed"), or "grove" for the legacy global
// cockpit. Mirrors cockpitSessionFor's ws==nil fallback.
func cockpitTitleFor(ws *workspace.Workspace) string {
	if ws != nil {
		return ws.Label
	}
	return "grove"
}

// orchestratorDirFor: the workspace's own brain dir — its cwd walk-ups to
// the workspace, so the orchestrator's gv calls hit THIS fleet.
func orchestratorDirFor(ws *workspace.Workspace, cfg *config.Config) string {
	if ws != nil {
		return filepath.Join(ws.Root, ".grove", "orchestrator")
	}
	return cfg.Orchestrator.Dir
}

// buildCockpit lays out the main-vertical cockpit: dashboard TUI as the
// main (left) pane, one orchestrator chat stacked right (O adds more).
// Seeds the orchestrator dir + CLAUDE.md brain on first run.
func buildCockpit(ws *workspace.Workspace, cfg *config.Config) error {
	session := cockpitSessionFor(ws)
	orchDir := orchestratorDirFor(ws, cfg)
	if err := os.MkdirAll(orchDir, 0o755); err != nil {
		return err
	}
	claudeMd := filepath.Join(orchDir, "CLAUDE.md")
	if _, err := os.Stat(claudeMd); os.IsNotExist(err) {
		if err := os.WriteFile(claudeMd, []byte(orchestrator.ClaudeMd), 0o644); err != nil {
			return err
		}
		fmt.Println("→ installed orchestrator CLAUDE.md at", claudeMd)
	}
	root := ""
	if ws != nil {
		root = ws.Root
	}
	// Ensure the session + reserved cockpit window exist — grab may have
	// created them as a placeholder before the cockpit was ever opened. This
	// then upgrades window 0 (the placeholder hint) into the real dash +
	// orchestrator layout, in place, so the cockpit is always `Ctrl-b w`
	// window 0. Root at the workspace root (orchDir lives under it and is
	// created just above).
	if err := tmux.EnsureWorkspaceSession(session, workspaceRoot(ws, orchDir)); err != nil {
		return err
	}
	cockpitWin := tmux.Exact(session) + ":cockpit"
	// Worker windows may have been created after the placeholder — focus the
	// cockpit window so the pane ops below and main-vertical target it, not a
	// worker.
	if err := tmux.SelectWindow(cockpitWin); err != nil {
		return err
	}
	// Seed the session's layout option from config before the first spawn,
	// so SpawnPane's re-tile already honors it (grove-52).
	if err := tmux.SetCockpitLayout(session, cfg.CockpitLayout()); err != nil {
		return err
	}
	// Name the outer terminal tab after the workspace so several cockpits
	// in separate tabs are tellable apart. Session-scoped: won't leak into
	// worker windows or unrelated tmux sessions.
	if err := tmux.SetTitle(session, cockpitTitleFor(ws)); err != nil {
		return err
	}
	// Absolute path, not "gv": the pane must run THIS binary even when a
	// stale one is first on PATH (same rule as the hook installer).
	dash := "gv dash"
	if exe, err := os.Executable(); err == nil {
		dash = exe + " dash"
	}
	// The placeholder pane's index depends on the user's pane-base-index —
	// a literal ".0" target broke the cockpit build on fresh installs with
	// `pane-base-index 1` (grove-168). Resolve the pane id instead.
	dashPane, err := tmux.FirstPaneID(cockpitWin)
	if err != nil {
		return err
	}
	if err := tmux.SendKeys(dashPane, dash); err != nil {
		return err
	}
	if _, err := tmux.SpawnPane(session, orchDir, orchestratorCmd(cfg, root)); err != nil {
		return err
	}
	if err := tmux.SelectLayout(session, cfg.CockpitLayout()); err != nil {
		return err
	}
	// Mark the cockpit built so a later `gv` (openCockpit) attaches instead
	// of rebuilding over a live layout.
	return tmux.MarkCockpitReady(session)
}

// orchestratorLaunch is the orchestrator claude invocation: the configured
// command plus --add-dir for every fleet surface. The chat's cwd stays the
// orchestrator dir (its CLAUDE.md brain must reload on every /clear, and
// that cwd is hook-invisible) — --add-dir is what lets @-references and
// file tools reach the actual repos and their task worktrees from there.
// root is the workspace root (empty for the legacy global cockpit): every
// repo and its .worktrees live under a single root, so one --add-dir there
// covers the whole fleet — a parent-scope workspace with a dozen child
// repos would otherwise produce an unreadable, ever-growing flag list.
// Without a root (legacy multi-repo fleet, repos scattered anywhere) fall
// back to one --add-dir per repo + worktrees dir.
func orchestratorLaunch(cfg *config.Config, root string) string {
	cmd := cfg.Orchestrator.Claude
	if root != "" {
		return cmd + fmt.Sprintf(" --add-dir '%s'", root)
	}
	var names []string
	for name := range cfg.Repos {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := map[string]bool{}
	for _, name := range names {
		r := cfg.Repos[name]
		for _, dir := range []string{r.Path, filepath.Join(filepath.Dir(r.Path), ".worktrees", name)} {
			if seen[dir] {
				continue
			}
			seen[dir] = true
			// claude refuses to start on a missing --add-dir; the
			// worktrees dir only exists once something was grabbed.
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				cmd += fmt.Sprintf(" --add-dir '%s'", dir)
			}
		}
	}
	return cmd
}

// orchestratorCmd resumes the last orchestrator chat when one exists;
// fresh spawns (O / orchestrator new) always start clean, so this is only
// for the cockpit's first pane.
func orchestratorCmd(cfg *config.Config, root string) string {
	launch := orchestratorLaunch(cfg, root)
	return fmt.Sprintf("%s --continue 2>/dev/null || %s", launch, launch)
}

// orchestratorLaunchProfile is orchestratorLaunch wrapped in a profile's
// backend env — the FRESH profiled spawn command. No `--continue` limb:
// fresh spawns () / orchestrator new --profile) always start clean, exactly
// like the unprofiled spawnOrchestrator (grove-43 — the --continue twin made
// every profiled spawn resume the previous conversation). Resume-on-spawn
// exists only for the cockpit's first pane (orchestratorCmd), which is
// always the default Anthropic orchestrator and never profiled.
func orchestratorLaunchProfile(cfg *config.Config, root string, p *config.ModelProfile) string {
	return config.WrapProfile(orchestratorLaunch(cfg, root), p, config.SecretsPath())
}

// cmdOrchestratorNew spawns a fresh orchestrator chat pane into the
// cockpit's right column (cockpit design §4) — the O keybind's CLI twin.
// Builds the cockpit first if it isn't running. --profile (grove-36) opens
// the new pane on a model profile instead of the operator's own Claude sub.
func cmdOrchestratorNew(args []string) error {
	fs := flag.NewFlagSet("orchestrator new", flag.ExitOnError)
	profileFlag := fs.String("profile", "", "open this orchestrator on a model profile (e.g. openrouter-glm) instead of Claude")
	_ = fs.Parse(args)

	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	msg, err := spawnOrchestratorProfile(cfg, *profileFlag)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	if !tmux.IsInsideTmux() {
		return tmux.AttachSession(cockpitSessionFor(ambient.ws))
	}
	return nil
}

// spawnOrchestrator is also injected into the TUI as the O keybind.
// Ambient-scoped (cockpit design §4.6 happy path): the pane joins the
// invoking workspace's cockpit and its gv calls hit that fleet.
func spawnOrchestrator(cfg *config.Config) (string, error) {
	ws := ambient.ws
	session := cockpitSessionFor(ws)
	dir := orchestratorDirFor(ws, cfg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if !tmux.SessionExists(session) {
		if err := buildCockpit(ws, cfg); err != nil {
			return "", err
		}
		return "cockpit built — gv attaches", nil
	}
	root := ""
	if ws != nil {
		root = ws.Root
	}
	// Breadcrumb before the spawn — a new orchestrator chat is a spawn that
	// can trip the same memory cliff as a grab (grove-3). Best-effort.
	if mem, err := resource.Read(); err == nil {
		_ = resource.Log(stateDir(), resource.Sample{
			Avail: mem.AvailBytes, Total: mem.TotalBytes,
			Workers: resource.LiveWorkers(), Kind: resource.KindOrchestrator,
		})
	}

	if _, err := tmux.SpawnPane(session, dir, orchestratorLaunch(cfg, root)); err != nil {
		return "", err
	}
	return "✓ new orchestrator chat pane", nil
}

// spawnOrchestratorProfile is spawnOrchestrator's profile-aware twin
// (grove-36 T4): an empty/unknown-as-"anthropic" profileName falls back to
// spawnOrchestrator unchanged (today's exact behavior). Otherwise the new
// pane's fresh launch runs wrapped in the profile's backend
// (orchestratorLaunchProfile), never the operator's own Claude sub.
func spawnOrchestratorProfile(cfg *config.Config, profileName string) (string, error) {
	resolvedName, p, err := cfg.ResolveProfile(profileName, nil)
	if err != nil {
		return "", err
	}
	if p == nil {
		return spawnOrchestrator(cfg)
	}
	ws := ambient.ws
	session := cockpitSessionFor(ws)
	baseDir := orchestratorDirFor(ws, cfg)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}
	if !tmux.SessionExists(session) {
		// Build the (unprofiled) cockpit first — 0/the default pane stays
		// Anthropic — then fall through to add the profiled pane below.
		if err := buildCockpit(ws, cfg); err != nil {
			return "", err
		}
	}
	root := ""
	if ws != nil {
		root = ws.Root
	}
	if mem, err := resource.Read(); err == nil {
		_ = resource.Log(stateDir(), resource.Sample{
			Avail: mem.AvailBytes, Total: mem.TotalBytes,
			Workers: resource.LiveWorkers(), Kind: resource.KindOrchestrator,
		})
	}

	// Per-profile cwd (grove-36 T4): Claude Code keys `--continue` by cwd, so
	// running the profiled pane in <orchDir>/<profile>/ gives each backend its
	// own continuity — a fresh GLM orchestrator can no longer resume the
	// Anthropic default pane's conversation (the confirmed bug). The
	// orchestrator CLAUDE.md brain still loads: Claude Code reads memory
	// recursively up the tree, and this dir's parent is the orchestrator dir
	// that holds CLAUDE.md. The unprofiled path (spawnOrchestrator) is
	// untouched — its pane keeps cwd = orchDir exactly as before.
	paneDir := filepath.Join(baseDir, resolvedName)
	if err := os.MkdirAll(paneDir, 0o755); err != nil {
		return "", err
	}
	paneID, err := tmux.SpawnPane(session, paneDir, orchestratorLaunchProfile(cfg, root, p))
	if err != nil {
		return "", err
	}
	// Best-effort visual tag: label the new pane with its profile and turn on
	// the cockpit window's pane borders so a profiled orchestrator is
	// distinguishable from the default Anthropic pane it shares the window
	// with. Cosmetic — never fail the spawn over it, and never rename the
	// cockpit window itself (cockpit-detection keys off its literal name).
	if err := tmux.SetPaneProfile(paneID, resolvedName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not tag orchestrator pane with profile %q: %v\n", resolvedName, err)
	}
	if err := tmux.ShowPaneBorders(session + ":cockpit"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not show cockpit pane borders: %v\n", err)
	}
	return fmt.Sprintf("✓ new orchestrator chat pane (%s)", resolvedName), nil
}

// cmdOrchestratorClose dismisses the calling orchestrator's own cockpit
// pane — the CLI half of the fire-and-forget dispatch flow (orchestrator
// CLAUDE.md). It logs an activity event FIRST (durable before the pane, and
// its process, are killed), then kills $TMUX_PANE. Guarded in tmux.ClosePane
// so it can only ever close a grove cockpit pane, never the dashboard.
func cmdOrchestratorClose(args []string) error {
	fs := flag.NewFlagSet("orchestrator close", flag.ExitOnError)
	ticket := fs.String("ticket", "", "ticket this dispatch handled (for the activity feed)")
	reason := fs.String("reason", "dispatched", "why the chat closed (activity feed label)")
	_ = fs.Parse(args)

	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return fmt.Errorf("no $TMUX_PANE — `gv orchestrator close` runs from inside a cockpit pane")
	}
	// Validate BEFORE logging: a guard rejection must not leave a
	// "dismissed" activity row for a pane that never closed.
	if err := tmux.PaneClosable(pane); err != nil {
		return err
	}
	// Log before the kill: kill-pane takes down this very process, so a
	// post-kill append would never land. Ticket rides in Data (not the
	// Event.Ticket field) so fold leaves the derived task view untouched.
	data := map[string]string{"reason": *reason}
	if *ticket != "" {
		data["ticket"] = *ticket
	}
	if err := state.Append(stateDir(), state.Event{
		Type: state.EvOrchestratorClosed,
		Data: data,
	}); err != nil {
		return err
	}
	return tmux.ClosePane(pane)
}

// cmdMobile is the phone cockpit. tmux sizes a session to its SMALLEST
// attached client, so attaching a phone to the desktop `gv` session would
// shrink every desk pane — mobile gets its own single-pane session running
// the dashboard, sized independently. Termius flow: `ssh <mac> -t 'gv
// mobile'`.
func cmdMobile() error {
	const session = "grove-mobile"
	if !tmux.SessionExists(session) {
		home, _ := os.UserHomeDir()
		if err := tmux.CreateSession(session, home); err != nil {
			return err
		}
		// Resolve the fresh session's single pane by id — under
		// `pane-base-index 1` a ".0" target doesn't exist (grove-168).
		pane, err := tmux.FirstPaneID(tmux.ExactActive(session))
		if err != nil {
			return err
		}
		if err := tmux.SendKeys(pane, "gv"); err != nil {
			return err
		}
	}
	return tmux.AttachSession(session)
}

// parseAnywhere lets flags appear after positionals (`gv grab <url> --repo
// monorepo`) — stdlib flag stops at the first non-flag arg otherwise.
func parseAnywhere(fs *flag.FlagSet, args []string) []string {
	var positionals []string
	for {
		_ = fs.Parse(args) // ExitOnError: bad flags abort with usage
		args = fs.Args()
		if len(args) == 0 {
			return positionals
		}
		positionals = append(positionals, args[0])
		args = args[1:]
	}
}

// --- grab ---

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(title string) string {
	s := slugStrip.ReplaceAllString(strings.ToLower(title), "-")
	s = strings.Trim(s, "-")
	if len(s) > 32 {
		s = s[:32]
		if i := strings.LastIndexByte(s, '-'); i > 12 {
			s = s[:i]
		}
	}
	return s
}

func cmdGrab(args []string) error {
	echoWorkspace()
	fs := flag.NewFlagSet("grab", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo name from config (overrides label inference)")
	manual := fs.Bool("manual", false, "hand-driven session: task context only, no autonomous kickoff")
	modelFlag := fs.String("model", "", "pin this worker to a model (e.g. claude-sonnet-5, opus) — one-off, no config edit")
	profileFlag := fs.String("profile", "", "run this worker on a model profile (e.g. openrouter-glm) instead of the repo's default Claude sub")
	briefFlag := fs.String("brief", "", "ad-hoc operator instructions appended to the kickoff prompt as a final \"## Operator brief\" section")
	positionals := parseAnywhere(fs, args)
	if len(positionals) > 1 {
		return fmt.Errorf("usage: gv grab [<task-id-or-url>] [--repo name] [--manual] [--model id] [--profile name] [--brief text]")
	}

	cfg, err := loadCfg()
	if err != nil {
		return err
	}

	tasks, err := state.Load(stateDir())
	if err != nil {
		return err
	}

	// Provider/repo resolution order differs per kind: linear infers the
	// repo from ticket labels (fetch first), markdown roots task files in
	// the repo (resolve repo first). An explicit --repo resolves first
	// either way so its per-repo provider override wins.
	var (
		repoName string
		repo     *config.Repo
		prov     provider.Provider
		task     *provider.Task
	)
	kind := cfg.Provider.Kind
	if *repoFlag != "" {
		if repoName, repo, err = cfg.ResolveRepo(*repoFlag, nil); err != nil {
			return err
		}
		kind = cfg.ProviderKindFor(repo)
	}
	if kind == "linear" {
		if len(positionals) != 1 {
			return fmt.Errorf("usage: gv grab <ticket-id-or-url> [--repo name] [--manual] [--model id] [--profile name] [--brief text]")
		}
		if prov, err = provider.FromConfigKind(cfg, "linear", "", ""); err != nil {
			return err
		}
		fmt.Println("→ fetching ticket from Linear…")
		id, err := prov.ParseID(positionals[0])
		if err != nil {
			return err
		}
		if task, err = prov.Get(id); err != nil {
			return err
		}
		if repo == nil {
			if repoName, repo, err = cfg.ResolveRepo("", task.Labels); err != nil {
				return err
			}
		}
	} else {
		if repo == nil {
			if repoName, repo, err = cfg.ResolveRepo("", nil); err != nil {
				return err
			}
		}
		// Any repo-rooted kind (markdown, github) — the repo's effective
		// kind decides, never a hardcoded default (plan review C-1).
		if repo != nil {
			kind = cfg.ProviderKindFor(repo)
		}
		if kind == "" {
			kind = "markdown"
		}
		if prov, err = provider.FromConfigKind(cfg, kind, repoName, repo.Path); err != nil {
			return err
		}
		if len(positionals) == 0 {
			return printBacklog(prov, repoName, tasks)
		}
		id, err := prov.ParseID(positionals[0])
		if err != nil {
			return err
		}
		if task, err = prov.Get(id); err != nil {
			return err
		}
	}

	if t, ok := tasks[task.ID]; ok && !t.Done {
		return fmt.Errorf("%s is already tracked (worktree %s) — `gv attach %s`, `gv done %s`, or `gv adopt %s` if its window died",
			task.ID, t.Worktree, task.ID, task.ID, task.ID)
	}

	// grove-78: fail closed BEFORE any side effect — a worker never routes
	// outside the workspace gv is running in. The session is derived from
	// the workspace below; a repo whose path resolves elsewhere would
	// silently escape to the legacy global session or a sibling workspace's.
	if err := requireAmbientWorkspace(ambient.ws, workspace.Find(repo.Path), repoName, repo.Path); err != nil {
		return err
	}

	profileName, profile, err := cfg.ResolveProfile(*profileFlag, repo)
	if err != nil {
		return err
	}

	name := task.ID + "-" + slugify(task.Title)
	fmt.Printf("→ %s on %s (branch %s)\n", task.ID, repoName, name)
	if *modelFlag != "" {
		fmt.Printf("→ model pinned to %s (this worker only)\n", *modelFlag)
	}
	if profileName != "" {
		fmt.Printf("→ model profile %s (this worker only)\n", profileName)
	}
	if *briefFlag != "" {
		fmt.Println("→ operator brief attached")
	}

	// Collapse into the workspace's single session (grove-<label>, or the
	// global grove for a true legacy run); window 0 stays the reserved
	// cockpit, worker windows land alongside it — one legible `Ctrl-b w`
	// node per workspace. requireAmbientWorkspace above guarantees the
	// repo's own workspace IS the ambient one, so the ambient session is
	// the only one a worker can land in. The window is tagged with the
	// active profile so a profiled worker reads at a glance in
	// `Ctrl-b w` / `gv ls`; the no-profile name stays byte-identical.
	ws := ambient.ws
	sessionName := cockpitSessionFor(ws)
	windowName := tmux.WorkerWindowProfile(repoShort(repoName, ws), name, profileName)

	if git.HasRemote(repo.Path, "origin") {
		if err := git.Fetch(repo.Path, "origin", repo.Base); err != nil {
			fmt.Fprintf(os.Stderr, "warning: git fetch failed (%v) — branching from local %s\n", err, repo.Base)
		}
	}
	baseRef, err := git.BaseRef(repo.Path, repo.Base)
	if err != nil {
		return err
	}
	wt, err := worktree.Add(repo.Path, name, baseRef)
	if err != nil {
		return err
	}
	fmt.Printf("→ worktree %s\n", wt.Path)

	// grove-78: a failed grab must not strand artifacts that block the
	// retry (live: an orphan worktree+branch made the re-grab die on
	// "branch already exists"). From here until the task-created event is
	// durably appended, any error rolls back the LOCAL side effects —
	// worktree, branch, prompt file, fresh window. The remote branch
	// (worktree.Add's best-effort push) is deliberately kept: gv may not be
	// the only pusher, and an existing remote branch never blocks a retry.
	grabbed := false
	promptPath := ""
	windowCreated := false
	defer func() {
		if grabbed {
			return
		}
		if windowCreated {
			_ = tmux.KillWindow(sessionName, windowName)
		}
		cleanupFailedGrab(repo.Path, wt.Path, name, promptPath)
	}()

	for _, envFile := range []string{".env", ".envrc", ".env.local"} {
		src := filepath.Join(repo.Path, envFile)
		if data, err := os.ReadFile(src); err == nil {
			_ = os.WriteFile(filepath.Join(wt.Path, envFile), data, 0o600)
			fmt.Printf("→ copied %s\n", envFile)
		}
	}

	promptMode := kickoff.ModeDefault
	if *manual {
		promptMode = kickoff.ModeManual
	}
	prompt, err := kickoff.Render(task, prov.Verbs(), prov.Kind(), repo.Prompt, promptMode, *briefFlag)
	if err != nil {
		return err
	}
	promptDir := filepath.Join(stateDir(), "prompts")
	_ = os.MkdirAll(promptDir, 0o755)
	promptPath = filepath.Join(promptDir, task.ID+".txt")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return err
	}

	// Resource breadcrumb at the tipping moment: capture pressure just before
	// this spawn so a jetsam kill (which takes the whole tmux server, not this
	// log file) stays recoverable after the fact (grove-3). Best-effort.
	if mem, err := resource.Read(); err == nil {
		_ = resource.Log(stateDir(), resource.Sample{
			Avail: mem.AvailBytes, Total: mem.TotalBytes,
			Workers: resource.LiveWorkers(),
			Kind:    resource.KindGrab, Ticket: task.ID,
		})
	}

	if err := tmux.EnsureWorkspaceSession(sessionName, workspaceRoot(ws, repo.Path)); err != nil {
		return err
	}
	if err := tmux.CreateWindow(sessionName, windowName, wt.Path); err != nil {
		return err
	}
	windowCreated = true
	// Target the fresh window by id (grove-116): a name-built target
	// prefix-matches, so it could resolve to a sibling window whose name
	// extends this one's ("repo · grove-1" vs "repo · grove-10").
	windowTarget, ok := tmux.WindowID(sessionName, windowName)
	if !ok {
		return fmt.Errorf("window %q vanished right after creation in session %q", windowName, sessionName)
	}
	// Pin the ticket name: a worker window's name is the ticket, never
	// whatever the claude pane's foreground process reports.
	if err := tmux.DisableAutoRename(windowTarget); err != nil {
		return err
	}
	// The split's "%N" id is captured at creation (grove-168): its numeric
	// index depends on the user's pane-base-index, so a literal ".1" target
	// could land the claude command in the worktree SHELL pane instead.
	claudePane, err := tmux.SplitVerticalWindow(windowTarget, wt.Path)
	if err != nil {
		return err
	}

	// Claude pane: (serialized setup) && claude with the prompt as argv via
	// command substitution — single line, no send-keys mangling, and the
	// pane returns to a shell if claude exits.
	claudeBin := config.WithModel(repo.Claude, *modelFlag)
	claudeCmd := fmt.Sprintf(`%s "$(cat %q)"`, claudeBin, promptPath)
	// Profile wrap applies to the composed claude+prompt command only —
	// never to repo.Claude itself (hooks resolve the worker's config dir
	// from the stored r.Claude, hooks.go:246-274) and never to the setup
	// prefix added just below.
	claudeCmd = config.WrapProfile(claudeCmd, profile, config.SecretsPath())
	if repo.Setup != "" {
		exe, _ := os.Executable()
		claudeCmd = fmt.Sprintf("%s run-setup %s %s && %s", exe, repoName, shellQuoteRoot(), claudeCmd)
	}
	if err := tmux.SendKeys(claudePane, claudeCmd); err != nil {
		return err
	}

	grabData := map[string]string{
		"title": task.Title, "url": task.URL, "repo": repoName,
		"branch": name, "worktree": wt.Path,
		"tmux_session": sessionName, "tmux_window": windowName,
	}
	// Persist the profile only when set so an unprofiled grab's event stays
	// byte-identical to today's (grove-36 T2).
	if profileName != "" {
		grabData["model_profile"] = profileName
	}
	if err := state.Append(stateDir(), state.Event{
		Type: state.EvTaskCreated, Ticket: task.ID, Data: grabData,
	}); err != nil {
		return err
	}
	// The task is durably tracked from here — cleanup is `gv untrack`'s
	// job now, so the failure rollback stands down.
	grabbed = true

	mode := "autonomous"
	if *manual {
		mode = "manual — attach to drive it"
	}
	fmt.Printf("✓ %s grabbed (%s)\n  watch:  gv ls\n  attach: gv attach %s\n", task.ID, mode, task.ID)
	return nil
}

// repoShort strips a redundant workspace-label prefix from a repo name so a
// worker window reads "p2p · …" inside the grove-unbrewed session rather than
// "unbrewed-p2p · …". Falls back to the bare repo name when no workspace is
// resolvable or the prefix doesn't apply.
func repoShort(repoName string, ws *workspace.Workspace) string {
	if ws != nil {
		if p := ws.Label + "-"; strings.HasPrefix(repoName, p) && len(repoName) > len(p) {
			return repoName[len(p):]
		}
	}
	return repoName
}

// workspaceRoot is the cwd to root a workspace session at: the workspace
// root when resolvable, else the repo path (un-workspaced/global grove).
// Both always exist, so tmux new-session -c never fails.
func workspaceRoot(ws *workspace.Workspace, repoPath string) string {
	if ws != nil {
		return ws.Root
	}
	return repoPath
}

// requireAmbientWorkspace is grab's containment gate (grove-78): the tmux
// session a worker lands in is derived from the workspace, so a repo whose
// path resolves under no `.grove/` marker would silently escape to the
// legacy global session, and one under a DIFFERENT workspace's marker
// would escape to that sibling's session — both live surprises. Workers
// must never leave the workspace gv runs in; fail closed with guidance.
// The legacy session stays reachable only from true legacy runs (no
// ambient workspace, un-workspaced repo).
func requireAmbientWorkspace(ambientWS, repoWS *workspace.Workspace, repoName, repoPath string) error {
	switch {
	case ambientWS == nil && repoWS == nil:
		return nil
	case ambientWS != nil && repoWS != nil && ambientWS.Root == repoWS.Root:
		return nil
	case ambientWS == nil:
		return fmt.Errorf("repo %s (%s) belongs to workspace %s — grab it from inside that workspace (cd %s)",
			repoName, repoPath, repoWS.Label, repoWS.Root)
	case repoWS == nil:
		return fmt.Errorf("repo %s (%s) is outside the %s workspace — map it in its own workspace, or in a parent workspace of both",
			repoName, repoPath, ambientWS.Label)
	default:
		return fmt.Errorf("repo %s (%s) belongs to workspace %s, not %s — grab it from there (cd %s)",
			repoName, repoPath, repoWS.Label, ambientWS.Label, repoWS.Root)
	}
}

// cleanupFailedGrab rolls back the LOCAL artifacts of a grab that failed
// after worktree.Add: the worktree, its branch, and the kickoff prompt.
// The remote branch is never touched — worktree.Add's best-effort push may
// have landed, gv can't know it was the only pusher (hard-rule territory),
// and a remote branch never blocks a retry. Best-effort: rollback failures
// are warnings; the original grab error is what surfaces to the operator.
func cleanupFailedGrab(repoPath, wtPath, branch, promptPath string) {
	fmt.Fprintln(os.Stderr, "→ grab failed — rolling back worktree, local branch, prompt (remote branch, if pushed, untouched)")
	if err := worktree.RemoveSafe(repoPath, wtPath); err != nil {
		// The worktree is seconds old and created by this very call, so
		// force is safe — copied .env files must not strand the rollback.
		if err := git.RemoveWorktreeForce(repoPath, wtPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: rollback worktree remove: %v\n", err)
			return // the branch is still checked out there; deleting it would fail too
		}
	}
	if err := git.ForceDeleteBranch(repoPath, branch); err != nil {
		fmt.Fprintf(os.Stderr, "warning: rollback local branch delete: %v\n", err)
	}
	if promptPath != "" {
		_ = os.Remove(promptPath)
	}
}

// printBacklog renders the provider's grabbable backlog (gv grab with no
// args), excluding tasks grove already has in flight — the event state is
// authoritative for those (DESIGN.md §5.2).
func printBacklog(prov provider.Provider, repoName string, tracked map[string]*state.Task) error {
	if !prov.Capabilities().CanList {
		return fmt.Errorf("usage: gv grab <task-id> [--repo name] [--manual] (the %s provider cannot list)", prov.Kind())
	}
	backlog, err := prov.List()
	if err != nil {
		return err
	}
	var rows []*provider.Task
	for _, task := range backlog {
		if t, ok := tracked[task.ID]; ok && !t.Done {
			continue // in flight — grove's live state wins over frontmatter
		}
		rows = append(rows, task)
	}
	if len(rows) == 0 {
		fmt.Printf("no grabbable tasks for %s — add a file under the task dir (see `gv init`)\n", repoName)
		return nil
	}
	fmt.Printf("grabbable tasks (%s):\n", repoName)
	for _, task := range rows {
		status := task.Status
		if status == "" {
			status = "todo"
		}
		fmt.Printf("  %-12s %-8s %s\n", task.ID, status, task.Title)
	}
	if c, ok := prov.(interface{ ListCapped() bool }); ok && c.ListCapped() {
		fmt.Println("  … list capped at 200 open issues — narrow on GitHub if you miss one")
	}
	fmt.Println("\ngv grab <task-id> to start one")
	return nil
}

// cmdInit is the P0 deterministic scaffold: register the cwd repo in
// ~/.config/grove/config.yaml and create .grove/tasks/ with a sample task.
// The probe/wizard/AGENTS.md bootstrap is Phase 1.
// cmdInit is the wizard (plan 2026-07-04-phase-1a): probe → detect-then-
// confirm steps → apply confirmed diffs → summary board. Re-running is the
// reconfigure path (pre-populated with current values). Non-TTY behaves as
// --yes; --yes never invents values, never installs hooks it didn't have,
// and never spawns the paid agents-md run.
func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	var f wizard.Flags
	fs.BoolVar(&f.Yes, "yes", false, "accept all detections; fill unset fields only")
	fs.StringVar(&f.Only, "only", "", "run a single step: "+strings.Join(wizard.StepIDs, "|"))
	fs.StringVar(&f.Base, "base", "", "base branch")
	fs.StringVar(&f.Setup, "setup", "", "worktree setup command")
	fs.StringVar(&f.Worker, "worker", "", "worker command")
	fs.StringVar(&f.Provider, "provider", "", "task backend: markdown|linear")
	fs.StringVar(&f.Ntfy, "ntfy", "", "ntfy topic URL")
	fs.BoolVar(&f.Hooks, "hooks", false, "install session hooks")
	fs.BoolVar(&f.NoHooks, "no-hooks", false, "skip the hooks step")
	fs.BoolVar(&f.AgentsMD, "agents-md", false, "generate AGENTS.md via a one-shot agent")
	fs.BoolVar(&f.NoAgentsMD, "no-agents-md", false, "skip the AGENTS.md step")
	fs.BoolVar(&f.ForceAgentsMD, "force-agents-md", false, "write AGENTS.md.new when AGENTS.md exists")
	fs.StringVar(&f.Label, "label", "", "workspace label (cockpit session grove-<label>)")
	_ = parseAnywhere(fs, args)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		f.Yes = true // non-TTY must never hang (/dev/null IS a char device, so stat-mode checks lie)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	// Workspace root + scope: a git repo is a repo-scope workspace
	// (monorepo included); a dir DIRECTLY holding >=2 child repos is a
	// parent-scope workspace (the thegrid/unbrewed shape). Parent
	// detection is checked FIRST against the cwd itself — an enclosing
	// repo further up (a git-inited $HOME holding dotfiles is common)
	// must never claim a parent folder nested inside it.
	scope := wizard.ScopeRepo
	abs, _ := filepath.Abs(cwd)
	abs, _ = filepath.EvalSymlinks(abs)
	root, rootErr := git.RepoRoot(cwd)
	switch {
	case len(childRepos(abs)) >= 2 && (rootErr != nil || root != abs):
		root, scope = abs, wizard.ScopeParent
	case rootErr != nil:
		return fmt.Errorf("gv init needs a git repo (repo scope) or a folder of sibling repos (parent scope): %w", rootErr)
	}
	name := filepath.Base(root)
	cfgPath := filepath.Join(root, ".grove", "config.yaml")
	doc, err := bootstrap.LoadDoc(cfgPath)
	if err != nil {
		return err
	}
	keyEnv := doc.Get("linear", "api_key_env")
	if keyEnv == "" {
		keyEnv = "LINEAR_API_KEY"
	}
	p, err := probe.Run(root, keyEnv)
	if err != nil {
		return err
	}
	hookPaths := hooks.SettingsPaths(initHookWorkerCommands(doc, root, name, scope))
	in := wizard.Input{
		Probe: p, RepoName: name, RepoPath: root, Doc: doc,
		HooksInstalled: hooks.AllInstalled(hookPaths),
		HooksPaths:     hookPaths,
		Scope:          scope,
		DetectedLabel:  filepath.Base(root),
		Flags:          f,
	}
	steps, err := wizard.Build(in)
	if err != nil {
		return err
	}
	fmt.Printf("🌱 grove init — %s (%s%s)\n", name, p.Stack, shapeNote(p.Shape))
	if !f.Yes {
		if err := runWizardForms(steps); err != nil {
			fmt.Println("aborted — nothing written")
			return nil
		}
	}
	a := wizard.Collect(steps)
	if a.Label != "" {
		if err := workspace.ValidateLabel(a.Label); err != nil {
			return err
		}
	}
	wizard.Apply(in, steps)
	// Parent scope: each detected child repo becomes an entry (path+base;
	// per-repo tuning is a per-child `gv init` or a hand edit).
	if scope == wizard.ScopeParent {
		for _, child := range childRepos(root) {
			cname := filepath.Base(child)
			doc.SetRepoField(cname, "path", child)
			if doc.Get("repos", cname, "base") == "" {
				base := "main"
				if b, err := git.DefaultBranch(child); err == nil {
					base = b
				}
				doc.SetRepoField(cname, "base", base)
			}
		}
	}
	if err := doc.Save(); err != nil {
		return err
	}
	if err := seedWorkspaceScaffold(root); err != nil {
		return err
	}
	if a.Label != "" {
		ws := workspace.Workspace{Root: root, Label: a.Label, Scope: scope}
		if err := workspace.AddToRegistry(ws); err != nil {
			fmt.Fprintf(os.Stderr, "registry: %v\n", err)
		}
	}
	resolveAmbient() // the marker now exists — the board below reflects it
	if doc.Dirty() {
		fmt.Printf("✓ config updated: %s\n", cfgPath)
	} else {
		fmt.Printf("• config already up to date: %s\n", cfgPath)
	}

	if scope == wizard.ScopeRepo && (a.Provider == "" || a.Provider == "markdown") {
		if dir, wrote, err := bootstrap.ScaffoldTasks(root, time.Now().Format("2006-01-02")); err == nil && wrote {
			fmt.Printf("✓ scaffolded %s with a sample task\n", dir)
		}
	}
	if a.InstallHooks && !in.HooksInstalled {
		// Re-derive after Save so a worker chosen THIS run gets its
		// profile's settings.json included.
		done, err := hooks.Install(hooks.SettingsPaths(initHookWorkerCommands(doc, root, name, scope)))
		for _, p := range done {
			fmt.Printf("✓ session hooks wired into %s\n", p)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "hooks install failed: %v\n", err)
		}
	}
	if a.RunAgentsMD && scope == wizard.ScopeRepo {
		worker := doc.Get("repos", name, "claude")
		if worker == "" {
			worker = "claude"
		}
		facts := bootstrap.Facts{
			RepoName: name, Stack: p.Stack, Shape: p.Shape,
			Setup: a.Setup, Build: p.Build, Test: p.Test, Lint: p.Lint,
		}
		fmt.Println("→ bootstrap agent writing the repo brain (one-shot, review before committing)…")
		target, err := bootstrap.GenerateAgentsMD(root, worker, facts, f.ForceAgentsMD, os.Stdout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agents-md: %v\n", err)
		} else {
			fmt.Printf("✓ wrote %s — review it, then commit\n", target)
		}
	}

	// The summary board: what's available, what would improve this repo.
	fmt.Println()
	cfg, cfgErr := loadCfg()
	doctor.Render(os.Stdout, doctor.Run(cfg, cfgErr))
	fmt.Printf("\nnext: gv grab   (list backlog) · gv grab task-001 · gv   (cockpit)\n")
	return nil
}

func shapeNote(shape string) string {
	if shape == "" || shape == "single" {
		return ""
	}
	return " · " + shape
}

// runWizardForms is the thin huh loop — every decision was made in
// internal/wizard; this only collects human edits into the steps.
func runWizardForms(steps []wizard.Step) error {
	for i := range steps {
		s := &steps[i]
		var field huh.Field
		switch s.Kind {
		case wizard.KindConfirm:
			field = huh.NewConfirm().Title(s.Title).Value(&s.On)
		case wizard.KindSelect:
			opts := make([]huh.Option[string], 0, len(s.Options)+2)
			if s.Value == "" {
				opts = append(opts, huh.NewOption("(keep config default)", ""))
			}
			seen := map[string]bool{"": true}
			for _, o := range append([]string{s.Value}, s.Options...) {
				if o == "" || seen[o] {
					continue
				}
				seen[o] = true
				opts = append(opts, huh.NewOption(o, o))
			}
			field = huh.NewSelect[string]().Title(s.Title).Options(opts...).Value(&s.Value)
		default:
			if s.Detected != "" && s.Value == s.Detected && s.Current == "" {
				s.Title += " (detected)"
			}
			field = huh.NewInput().Title(s.Title).Value(&s.Value)
		}
		if err := huh.NewForm(huh.NewGroup(field)).Run(); err != nil {
			return err
		}
	}
	return nil
}

// run-setup serializes per-repo setup commands behind a lockfile so three
// simultaneous grabs don't run three pnpm installs at once.
func cmdRunSetup(args []string) error {
	// argv: <repo> [workspace-root]. The root travels explicitly because a
	// worktree cwd may live outside the workspace and not walk up (plan
	// review S-3); "" = legacy globals.
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: gv run-setup <repo> [workspace-root]")
	}
	root := ""
	if len(args) == 2 {
		root = args[1]
	}
	cfg, err := config.LoadAt(root)
	if err != nil {
		return err
	}
	repo, ok := cfg.Repos[args[0]]
	if !ok {
		return fmt.Errorf("unknown repo %q", args[0])
	}
	if repo.Setup == "" {
		return nil
	}

	lockPath := filepath.Join(config.StateDirAt(root), "setup-"+args[0]+".lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	fmt.Printf("gv: waiting for setup lock (%s)…\n", args[0])
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	fmt.Printf("gv: running setup: %s\n", repo.Setup)
	cmd := exec.Command("sh", "-c", repo.Setup)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

// --- ls ---

type lsRow struct {
	*state.Task
	Live string       `json:"live"`
	PR   *github.PR   `json:"pr,omitempty"`
	Cost *cost.Totals `json:"cost,omitempty"`
}

// emitJSON prints a --json payload in the plugin-contract envelope
// (docs/plugins.md): a top-level object with schema_version plus the
// command's payload under one named key. Additive-only from here —
// removing or renaming any emitted field is a major contract break.
func emitJSON(key string, payload any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(schema.Envelope(key, payload))
}

func cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	noPR := fs.Bool("no-pr", false, "skip gh PR lookups (faster)")
	noCost := fs.Bool("no-cost", false, "skip transcript scanning for the COST column (faster)")
	parseAnywhere(fs, args)

	cfg, cfgErr := loadCfg()
	tasks, err := state.Load(stateDir())
	if err != nil {
		return err
	}
	active := state.Active(tasks)

	prs := map[string]*github.PR{}
	if !*noPR && cfgErr == nil {
		lookups := map[string][2]string{}
		for _, t := range active {
			if r, ok := cfg.Repos[t.Repo]; ok {
				lookups[t.Ticket] = [2]string{r.Path, t.Branch}
			}
		}
		prs = github.FetchAll(lookups)
	}

	costCache := cost.NewCache()
	rows := make([]lsRow, 0, len(active))
	for _, t := range active {
		// Resolve the current window name (a P3 status glyph is a display
		// suffix on the stable base) so the byte-comparable detect probe's
		// exact name match still finds a live worker.
		live := detect.DetectLive(t.TmuxSession, tmux.ResolveWindowName(t.TmuxSession, t.TmuxWindow))
		liveStr := "gone"
		if live.Exists {
			liveStr = live.Status.String()
		}
		row := lsRow{Task: t, Live: liveStr, PR: prs[t.Ticket]}
		if !*noCost {
			if tot, err := costCache.ForTask(t.Worktree); err == nil {
				row.Cost = &tot
			}
		}
		rows = append(rows, row)
	}
	// Forwarding tombstones (grove-177): a handed-off task is untracked
	// here but `gv ls --json` still carries it with handed_off_to set, so
	// a merged fleet view (#178) can follow the pointer. Additive only:
	// the row shape is unchanged, these rows just have done=true.
	tombstones := state.HandedOff(tasks)
	for _, t := range tombstones {
		rows = append(rows, lsRow{Task: t, Live: "handed-off"})
	}

	if *asJSON {
		return emitJSON("tasks", rows)
	}
	rows = rows[:len(rows)-len(tombstones)]

	if len(rows) == 0 && len(tombstones) == 0 {
		fmt.Println("no active tasks — `gv grab <ticket>` to start one")
		return nil
	}
	// The PROFILE column collapses entirely when no active task is profiled,
	// so the default (all-Anthropic) fleet's table is byte-identical to today
	// (grove-36 T2 invariant).
	anyProfile := false
	for _, r := range rows {
		if r.ModelProfile != "" {
			anyProfile = true
			break
		}
	}
	header := fmt.Sprintf("%-11s %-11s %-10s %-8s %-9s %-5s %-9s %-8s %s",
		"TICKET", "REPO", "STATUS", "LIVE", "PR", "CI", "PREVIEW", "COST", "AGE")
	if anyProfile {
		header += "  PROFILE"
	}
	fmt.Println(header)
	for _, r := range rows {
		pr, ci, preview := "—", "—", "—"
		if r.PR != nil {
			pr = fmt.Sprintf("#%d", r.PR.Number)
			if r.PR.State == "MERGED" {
				pr += " ⬢"
			}
			switch r.PR.CI {
			case "pass":
				ci = "✓"
			case "fail":
				ci = "✗"
			case "pending":
				ci = "◌"
			}
			if r.PR.PreviewURL != "" {
				preview = "⬡ up"
			}
		}
		status := r.Label()
		if r.Paused {
			status = "⏸ " + status // paused stays on the plate, visibly resumable (grove-90)
		}
		line := fmt.Sprintf("%-11s %-11s %-10s %-8s %-9s %-5s %-9s %-8s %s",
			r.Ticket, r.Repo, status, r.Live, pr, ci, preview, fmtUSD(r.Cost), age(r.Created))
		if anyProfile {
			prof := "—"
			if r.ModelProfile != "" {
				prof = "⚡ " + r.ModelProfile
			}
			line += "  " + prof
		}
		fmt.Println(line)
		if r.Agent == state.AgentWaiting && r.Question != "" {
			fmt.Printf("  ◆ %s\n", truncateLine(r.Question, 90))
		}
		if r.Agent == state.AgentBlocked && r.Question != "" {
			fmt.Printf("  ⚠ %s\n", truncateLine(r.Question, 90))
		}
	}
	// The take-it-back hint must be the handoff mirror, not a plain adopt:
	// `gv adopt` here would start a second live worker on the branch while
	// the remote still runs its own (the split-brain handoff exists to
	// avoid). `gv untrack` is the pointer's terminal path.
	for _, t := range tombstones {
		fmt.Printf("%-11s %-11s → %s (handed off %s; take back: gv handoff %s --from %s · drop row: gv untrack %s)\n",
			t.Ticket, t.Repo, t.HandedOffTo, age(t.Updated), t.Ticket, t.HandedOffTo, t.Ticket)
	}
	return nil
}

// fmtUSD renders an estimate compactly: "$4.20", "$123" — "~" prefix when
// some entries had no pricing (partial estimate), "—" when nothing billed.
func fmtUSD(t *cost.Totals) string {
	if t == nil || t.Turns == 0 {
		return "—"
	}
	prefix := "$"
	if !t.CostKnown {
		prefix = "~$"
	}
	if t.USD >= 100 {
		return fmt.Sprintf("%s%.0f", prefix, t.USD)
	}
	return fmt.Sprintf("%s%.2f", prefix, t.USD)
}

func age(t time.Time) string {
	d := time.Since(t).Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func truncateLine(s string, n int) string {
	s = strings.SplitN(s, "\n", 2)[0]
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// --- audit ---

func cmdAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	parseAnywhere(fs, args)

	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	tasks, err := state.Load(stateDir())
	if err != nil {
		return err
	}
	rep := audit.Gather(cfg, tasks, stateDir())

	if *asJSON {
		return emitJSON("report", rep)
	}

	if len(rep.Tasks) == 0 {
		fmt.Println("no active tasks")
	} else {
		fmt.Printf("%-11s %-11s %-13s %-4s %-4s %-7s %-7s %s\n",
			"TICKET", "REPO", "CLASS", "WT", "WIN", "PR", "AGE", "SUGGESTED")
		for _, r := range rep.Tasks {
			pr := "—"
			if !r.Facts.PRKnown {
				pr = "?"
			} else if r.Facts.PRState != "" {
				pr = strings.ToLower(r.Facts.PRState)
			}
			fmt.Printf("%-11s %-11s %-13s %-4s %-4s %-7s %-7s %s\n",
				r.Ticket, r.Repo, r.Class, mark(r.Facts.WorktreeExists), mark(r.Facts.WindowAlive),
				pr, age(r.Updated), r.Suggestion)
		}
	}

	if len(rep.Orphans) > 0 {
		fmt.Printf("\nORPHAN WORKTREES (not tracked by gv — report only, never deleted by gv):\n")
		for _, o := range rep.Orphans {
			dirty := ""
			if o.Dirty {
				dirty = "  (dirty)"
			}
			fmt.Printf("  %-11s %s%s\n", o.Repo, o.Path, dirty)
		}
	}
	if len(rep.OrphanProcesses) > 0 {
		fmt.Printf("\nORPHAN PROCESSES (claude/mcp descendants reparented to launchd — report only, never killed by gv):\n")
		for _, p := range rep.OrphanProcesses {
			fmt.Printf("  pid %-8d cpu %5.1f%%  elapsed %-10s %s\n", p.PID, p.CPUPct, p.Elapsed, truncateLine(p.Args, 80))
			fmt.Printf("    kill %d  (or confirm it via gv sweep)\n", p.PID)
		}
	}
	if len(rep.WorktreeProcesses) > 0 {
		fmt.Printf("\nWORKTREE PROCESSES (referencing a grove worktree whose task is done or dir is gone — report only, gv sweep offers the kill):\n")
		for _, p := range rep.WorktreeProcesses {
			fmt.Printf("  pid %-8d %-11s cpu %5.1f%%  rss %6.1f MB  elapsed %-10s %s\n",
				p.PID, p.Ticket, p.CPUPct, float64(p.RSSKB)/1024, p.Elapsed, truncateLine(p.Args, 70))
		}
	}
	if len(rep.StalePrompts) > 0 {
		fmt.Printf("\n%d stale prompt file(s) for done tasks (gv sweep prunes them)\n", len(rep.StalePrompts))
	}
	fmt.Printf("\nevents.jsonl: %.1f KB\n", float64(rep.EventsSizeBytes)/1024)
	return nil
}

func mark(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

// --- cost ---

type costRow struct {
	Ticket string      `json:"ticket"`
	Repo   string      `json:"repo"`
	Done   bool        `json:"done"`
	Cost   cost.Totals `json:"cost"`
}

// cmdCost reports per-ticket token/cost estimates (active table + done
// rollup). Pure read; numbers are estimates — on a Max plan, $ is a
// relative-effort signal, not a bill.
func cmdCost(args []string) error {
	fs := flag.NewFlagSet("cost", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	analyze := fs.Bool("analyze", false, "outcome-priced ledger with analysis flags")
	record := fs.String("record", "", "turn the persistent spend ledger on|off (also toggleable from the cockpit costs page)")
	showLedger := fs.Bool("ledger", false, "print the recorded spend history (latest snapshot per ticket)")
	parseAnywhere(fs, args)

	if *record != "" {
		if *record != "on" && *record != "off" {
			return fmt.Errorf("--record wants on or off, got %q", *record)
		}
		if err := ledger.SetRecording(stateDir(), *record == "on"); err != nil {
			return err
		}
		fmt.Printf("✓ spend recording %s (ledger: %s)\n", *record, ledger.Path(stateDir()))
		return nil
	}
	if *showLedger {
		return costLedger(*asJSON)
	}

	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	tasks, err := state.Load(stateDir())
	if err != nil {
		return err
	}
	if *analyze {
		return costAnalyze(cfg, tasks, *asJSON)
	}

	cache := cost.NewCache()
	var rows []costRow
	var doneUSD float64
	var doneCount, doneTurns int
	for _, t := range tasks {
		tot, err := cache.ForTask(t.Worktree)
		if err != nil || tot.Turns == 0 {
			continue
		}
		if t.Done {
			doneCount++
			doneUSD += tot.USD
			doneTurns += tot.Turns
			if *asJSON {
				rows = append(rows, costRow{Ticket: t.Ticket, Repo: t.Repo, Done: true, Cost: tot})
			}
			continue
		}
		rows = append(rows, costRow{Ticket: t.Ticket, Repo: t.Repo, Cost: tot})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Cost.USD > rows[j].Cost.USD })

	if *asJSON {
		return emitJSON("rows", rows)
	}

	if len(rows) == 0 {
		fmt.Println("no active tasks with transcripts")
	} else {
		fmt.Printf("%-11s %-11s %-8s %-6s %-8s %-8s %-7s %s\n",
			"TICKET", "REPO", "EST $", "TURNS", "IN", "OUT", "CACHE%", "MODELS")
		for _, r := range rows {
			fmt.Printf("%-11s %-11s %-8s %-6d %-8s %-8s %-7s %s\n",
				r.Ticket, r.Repo, fmtUSD(&r.Cost), r.Cost.Turns,
				fmtTok(r.Cost.Input), fmtTok(r.Cost.Output),
				fmt.Sprintf("%.0f%%", 100*r.Cost.CacheReadShare()), r.Cost.Mix())
		}
	}
	fmt.Printf("\ndone tasks: %d · est $%.2f total · %d turns  (estimates, not billing)\n",
		doneCount, doneUSD, doneTurns)
	return nil
}

// costLedger prints the recorded history — reads the ledger alone, so it
// works after transcripts are pruned and worktrees removed (the e2e proof
// of the durability contract).
func costLedger(asJSON bool) error {
	rows, err := ledger.Read(stateDir())
	if err != nil {
		return err
	}
	latest := ledger.Latest(rows)
	var out []ledger.Row
	for _, r := range latest {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })

	if asJSON {
		return emitJSON("rows", out)
	}
	if len(out) == 0 {
		fmt.Printf("ledger empty — enable with `gv cost --record on` (file: %s)\n", ledger.Path(stateDir()))
		return nil
	}
	fmt.Printf("%-11s %-11s %-8s %-6s %-9s %-24s %s\n",
		"TICKET", "REPO", "EST $", "TURNS", "OUTCOME", "WHEN", "TITLE")
	for _, r := range out {
		fmt.Printf("%-11s %-11s $%-7.2f %-6d %-9s %-24s %s\n",
			r.Ticket, r.Repo, r.USD, r.Turns, r.Outcome,
			r.Time.Local().Format("2006-01-02 15:04"), r.Title)
	}
	fmt.Println("\n(estimates, not billing — snapshots recorded locally by grove)")
	return nil
}

type analyzeRow struct {
	Ticket  string      `json:"ticket"`
	Repo    string      `json:"repo"`
	Done    bool        `json:"done"`
	Outcome string      `json:"outcome"` // merged | closed | open | none | unknown
	Steers  int         `json:"steers"`
	Flags   []string    `json:"flags,omitempty"`
	Cost    cost.Totals `json:"cost"`
}

type analyzeReport struct {
	Rows           []analyzeRow       `json:"rows"`
	TotalUSD       float64            `json:"total_est_usd"`
	MergedCount    int                `json:"merged_count"`
	USDPerMergedPR float64            `json:"est_usd_per_merged_pr"`
	AbandonedUSD   float64            `json:"est_usd_on_abandoned"` // closed-PR tickets: pure waste
	ByRepoUSD      map[string]float64 `json:"by_repo_est_usd"`
}

// costAnalyze assembles the outcome-priced ledger: per ticket → cost,
// tokens, steering, PR outcome, deterministic flags — the judgment layer
// (which ticket shapes burn tokens, what to change) belongs to the
// orchestrator reading the --json form. Pure read.
func costAnalyze(cfg *config.Config, tasks map[string]*state.Task, asJSON bool) error {
	cache := cost.NewCache()
	steers, err := state.EventCounts(stateDir(), state.EvAnswered)
	if err != nil {
		return err
	}

	// PR outcomes concurrently — every ticket ever tracked (done included).
	type prRes struct {
		ticket  string
		outcome string
	}
	outcomes := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range tasks {
		repo, ok := cfg.Repos[t.Repo]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(ticket, path, branch string) {
			defer wg.Done()
			out := "unknown"
			if pr, err := github.PRForBranch(path, branch); err == nil {
				switch {
				case pr == nil:
					out = "none"
				case pr.State == "MERGED":
					out = "merged"
				case pr.State == "CLOSED":
					out = "closed"
				default:
					out = "open"
				}
			}
			mu.Lock()
			outcomes[ticket] = out
			mu.Unlock()
		}(t.Ticket, repo.Path, t.Branch)
	}
	wg.Wait()

	rep := analyzeReport{ByRepoUSD: map[string]float64{}}
	var mergedUSDs []float64
	for _, t := range tasks {
		tot, err := cache.ForTask(t.Worktree)
		if err != nil || tot.Turns == 0 {
			continue
		}
		row := analyzeRow{
			Ticket: t.Ticket, Repo: t.Repo, Done: t.Done,
			Outcome: outcomes[t.Ticket], Steers: steers[t.Ticket], Cost: tot,
		}
		rep.Rows = append(rep.Rows, row)
		rep.TotalUSD += tot.USD
		rep.ByRepoUSD[t.Repo] += tot.USD
		switch row.Outcome {
		case "merged":
			rep.MergedCount++
			mergedUSDs = append(mergedUSDs, tot.USD)
		case "closed":
			rep.AbandonedUSD += tot.USD
		}
	}
	medianMerged := median(mergedUSDs)
	if rep.MergedCount > 0 {
		var mergedTotal float64
		for _, v := range mergedUSDs {
			mergedTotal += v
		}
		rep.USDPerMergedPR = mergedTotal / float64(rep.MergedCount)
	}
	for i := range rep.Rows {
		r := &rep.Rows[i]
		if cost.StuckFlag(r.Cost.Turns, cfg.Cost.StuckTurns, r.Outcome != "none" && r.Outcome != "unknown") {
			r.Flags = append(r.Flags, "stuck: many turns, no PR")
		}
		if cost.SteeringAnomaly(r.Steers, r.Cost.Turns) {
			r.Flags = append(r.Flags, "steering: >25% of turns needed a human answer")
		}
		if cost.CostOutlier(r.Cost.USD, medianMerged) {
			r.Flags = append(r.Flags, "cost: ≥2× median of merged tickets")
		}
	}
	sort.Slice(rep.Rows, func(i, j int) bool { return rep.Rows[i].Cost.USD > rep.Rows[j].Cost.USD })

	if asJSON {
		return emitJSON("report", rep)
	}

	fmt.Printf("%-11s %-11s %-9s %-8s %-6s %-6s %-7s %s\n",
		"TICKET", "REPO", "OUTCOME", "EST $", "TURNS", "STEER", "CACHE%", "FLAGS")
	for _, r := range rep.Rows {
		fmt.Printf("%-11s %-11s %-9s %-8s %-6d %-6d %-7s %s\n",
			r.Ticket, r.Repo, r.Outcome, fmtUSD(&r.Cost), r.Cost.Turns, r.Steers,
			fmt.Sprintf("%.0f%%", 100*r.Cost.CacheReadShare()), strings.Join(r.Flags, "; "))
	}
	fmt.Printf("\ntotal est $%.2f · %d merged (est $%.2f per merged PR) · est $%.2f on abandoned tickets\n",
		rep.TotalUSD, rep.MergedCount, rep.USDPerMergedPR, rep.AbandonedUSD)
	for repo, usd := range rep.ByRepoUSD {
		fmt.Printf("  %-11s est $%.2f\n", repo, usd)
	}
	fmt.Println("(estimates from transcript token counts — a relative-effort signal, not billing)")
	return nil
}

func median(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vs...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

// fmtTok renders token counts compactly: 950, 9.9k, 1.2M.
func fmtTok(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// --- answer / nudge ---

func cmdRelay(args []string, isAnswer bool) error {
	verb := "nudge"
	if isAnswer {
		verb = "answer"
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: gv %s <ticket> [text]", verb)
	}
	t, err := findTask(args[0])
	if err != nil {
		return err
	}

	if isAnswer && t.Question != "" {
		fmt.Printf("◆ %s asked:\n  %s\n\n", t.Ticket, t.Question)
	}
	text := strings.TrimSpace(strings.Join(args[1:], " "))
	if text == "" {
		fmt.Printf("%s> ", verb)
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		if sc.Scan() {
			text = strings.TrimSpace(sc.Text())
		}
	}
	if text == "" {
		return fmt.Errorf("empty %s — nothing sent", verb)
	}
	if err := relayText(t, text); err != nil {
		return err
	}
	fmt.Printf("✓ sent to %s\n", t.Ticket)
	return nil
}

// relayText is the shared send path of answer/nudge (and handoff's
// checkpoint nudge): paste into the claude pane, then record EvAnswered
// only once the submit verifiably landed.
func relayText(t *state.Task, text string) error {
	// Resolve the claude pane by id — usually .1, but a window that lost
	// its split runs claude in its only pane, and a name-built target can
	// resolve to a prefix-extending sibling's window ("repo · grove-1" vs
	// "repo · grove-10"), steering the wrong agent (grove-116).
	pane, err := tmux.ClaudePaneTarget(t.TmuxSession, t.TmuxWindow)
	if err != nil {
		return fmt.Errorf("%s has no live worker window: %w", t.Ticket, err)
	}
	// Single character → raw key without Enter (option pickers / plan
	// approval). Anything longer → bracketed paste + Enter.
	if len([]rune(text)) == 1 {
		err = tmux.SendRawKey(pane, text)
	} else {
		err = tmux.PasteText(pane, text)
	}
	if err != nil {
		// PasteText verifies the submit landed (grove-144), so a failure here
		// means the agent never got the text — recording EvAnswered anyway is
		// what made this bug silent: gv ls showed `working` on a dead worker.
		return err
	}
	return state.Append(stateDir(), state.Event{Type: state.EvAnswered, Ticket: t.Ticket})
}

// --- attach ---

func cmdAttach(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gv attach <ticket>")
	}
	t, err := findTask(args[0])
	if err != nil {
		return err
	}
	return attachTask(t)
}

// attachTask jumps to a task's tmux window, with first-attach bookkeeping
// (lazy editor inject + attached event). Also injected into the TUI as the
// a keybind's inside-tmux path, where switch-client leaves the dashboard
// running in its cockpit pane.
func attachTask(t *state.Task) error {
	if !t.Attached {
		maybeInjectEditor(t.TmuxSession, t.TmuxWindow)
		_ = state.Append(stateDir(), state.Event{Type: state.EvAttached, Ticket: t.Ticket})
	}
	return tmux.AttachWindow(t.TmuxSession, t.TmuxWindow)
}

// maybeInjectEditor lazily starts nvim in the window's first (shell) pane
// on first attach (10 headless worktrees × tsserver is real RAM) — but only
// when that pane is not where claude lives: a window that lost its split
// would otherwise get "nvim ." typed INTO the agent session. Both panes are
// resolved to "%N" ids (grove-168): the first pane's numeric index depends
// on the user's pane-base-index, so neither a ".0" target nor an `== 0`
// lost-split check survives `pane-base-index 1`.
func maybeInjectEditor(session, window string) {
	// Window resolved by id (grove-116) so the inject can never type into
	// a prefix-extending sibling's shell pane.
	id, ok := tmux.WindowID(session, window)
	if !ok {
		return
	}
	shellPane, err := tmux.FirstPaneID(id)
	if err != nil {
		return
	}
	claudePane, err := tmux.ClaudePaneTarget(session, window)
	if err != nil || claudePane == shellPane {
		return // lost split: claude is the first pane — don't type into it
	}
	_ = tmux.SendKeys(shellPane, "nvim .")
}

// --- diff ---

// cmdDiff shows the branch's work-since-fork without attaching: git's own
// color and pager apply (stdio inherited).
func cmdDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	stat := fs.Bool("stat", false, "summary form (files + line counts)")
	positionals := parseAnywhere(fs, args)
	if len(positionals) != 1 {
		return fmt.Errorf("usage: gv diff <ticket> [--stat]")
	}
	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	t, err := findTask(positionals[0])
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(t.Worktree); statErr != nil {
		return fmt.Errorf("%s: worktree %s is gone — `gv adopt %s` to re-create it", t.Ticket, t.Worktree, t.Ticket)
	}
	base := "main"
	if repo, ok := cfg.Repos[t.Repo]; ok {
		base = repo.Base
	}
	gitArgs := []string{"diff", git.DiffBase(t.Worktree, base) + "...HEAD"}
	if *stat {
		gitArgs = append(gitArgs, "--stat")
	}
	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = t.Worktree
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

// --- adopt ---

// cmdAdopt revives a task from whatever survives a disconnect: an intact
// worktree, a local or remote-only branch, a stored session id — or, for
// tickets gv never tracked, just a branch on origin. Fallback chain:
// missing worktree → AddExisting; stored session id → resume with a
// pickup-prompt fallback; no id → pickup prompt.
func cmdAdopt(args []string) error {
	echoWorkspace()
	fs := flag.NewFlagSet("adopt", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo name from config (cold adopt: overrides label inference)")
	branchFlag := fs.String("branch", "", "branch to adopt (default: from state, or origin/<ticket>-* inference)")
	manual := fs.Bool("manual", false, "hand-driven session: ticket context only, no autonomous pickup")
	modelFlag := fs.String("model", "", "pin this worker to a model (e.g. claude-sonnet-5, opus) — one-off, no config edit")
	profileFlag := fs.String("profile", "", "run this worker on a model profile (default: the profile it was grabbed with; 'anthropic' strips it)")
	syncFlag := fs.Bool("sync", false, "fetch and hard-reset the worktree to origin/<branch> first (handoff pickup: another host worked the branch, so any surviving local checkout/branch ref is stale)")
	positionals := parseAnywhere(fs, args)
	if len(positionals) != 1 {
		return fmt.Errorf("usage: gv adopt <ticket> [--repo name] [--branch b] [--manual] [--model id] [--profile name] [--sync]")
	}
	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	tasks, err := state.Load(stateDir())
	if err != nil {
		return err
	}

	// Resolve the id against tracked state first (both id shapes are live
	// with per-repo providers); a cold adopt normalizes by the effective
	// provider kind (--repo's override when given, else global).
	id := ""
	for _, cand := range provider.IDCandidates(positionals[0]) {
		if _, ok := tasks[cand]; ok {
			id = cand
			break
		}
	}
	if id == "" {
		kind := cfg.Provider.Kind
		var coldRepo *config.Repo
		coldName := ""
		if *repoFlag != "" {
			if n, r, rErr := cfg.ResolveRepo(*repoFlag, nil); rErr == nil {
				kind, coldRepo, coldName = cfg.ProviderKindFor(r), r, n
			}
		}
		if kind == "github" {
			if coldRepo == nil {
				return fmt.Errorf("github ids are repo-scoped — pass --repo to adopt %s", positionals[0])
			}
			p, perr := provider.FromConfigKind(cfg, "github", coldName, coldRepo.Path)
			if perr != nil {
				return perr
			}
			if id, err = p.ParseID(positionals[0]); err != nil {
				return err
			}
		} else if id, err = parseAnyID(kind, positionals[0]); err != nil {
			return err
		}
	}

	// Resolve repo, branch, and prior session — from state if gv has ever
	// seen this task (active, done, or untracked), else cold via provider.
	var repoName, branch, sessionID, storedProfile string
	var task *provider.Task
	if t, ok := tasks[id]; ok {
		repoName, branch, sessionID, storedProfile = t.Repo, t.Branch, t.SessionID, t.ModelProfile
		task = &provider.Task{ID: t.Ticket, Title: t.Title, URL: t.URL}
		if !t.Done && tmux.WindowLive(t.TmuxSession, t.TmuxWindow) {
			return fmt.Errorf("%s already has a live window — `gv attach %s`", id, id)
		}
	}
	if *branchFlag != "" {
		branch = *branchFlag
	}

	// A repo is needed before the provider exists (markdown roots task
	// files in the repo). From state, else --repo/label inference — cold
	// markdown adopts can't infer from labels, so --repo or sole-repo.
	if repoName == "" {
		var labels []string
		if task != nil {
			labels = task.Labels
		}
		repoName, _, err = cfg.ResolveRepo(*repoFlag, labels)
		if err != nil {
			return err
		}
	}
	repo, ok := cfg.Repos[repoName]
	if !ok {
		return fmt.Errorf("repo %q no longer in config", repoName)
	}

	// Effective profile: an explicit --profile wins; otherwise default to the
	// profile the worker was grabbed with (grove-36 T3 — adopt must not
	// silently resurrect a GLM worker on the operator's own Anthropic sub).
	// --profile anthropic (or an empty stored profile) strips it. Falls back
	// to the repo's model_profile default via ResolveProfile, exactly like grab.
	effProfile := *profileFlag
	if effProfile == "" {
		effProfile = storedProfile
	}
	profileName, profile, err := cfg.ResolveProfile(effProfile, repo)
	if err != nil {
		return err
	}

	// Fresh task fetch enriches the pickup prompt (description + new
	// comments). Non-fatal for tracked tasks — offline adopt still works
	// with the fields state carries.
	repoKind := cfg.ProviderKindFor(repo)
	prov, provErr := provider.FromConfigKind(cfg, repoKind, repoName, repo.Path)
	if provErr == nil {
		if fetched, fetchErr := prov.Get(id); fetchErr == nil {
			task = fetched
		} else if task == nil {
			return fmt.Errorf("%s is not tracked and the %s fetch failed: %w", id, repoKind, fetchErr)
		} else {
			fmt.Fprintf(os.Stderr, "warning: %s fetch failed (%v) — pickup prompt uses stored task fields\n", repoKind, fetchErr)
		}
	} else if task == nil {
		return fmt.Errorf("%s is not tracked and %v", id, provErr)
	}

	if branch == "" {
		candidates, err := git.RemoteBranches(repo.Path, id+"-*")
		if err != nil {
			return fmt.Errorf("branch inference (origin/%s-*): %w", id, err)
		}
		switch len(candidates) {
		case 0:
			return fmt.Errorf("no branch matching origin/%s-* in %s — pass --branch", id, repoName)
		case 1:
			branch = candidates[0]
		default:
			return fmt.Errorf("multiple branches match origin/%s-*: %s — pass --branch", id, strings.Join(candidates, ", "))
		}
	}

	fmt.Printf("→ adopting %s on %s (branch %s)\n", id, repoName, branch)
	if profileName != "" {
		fmt.Printf("→ model profile %s\n", profileName)
	}

	// --sync (grove-177, handoff pickup): make origin the source of truth
	// BEFORE the worktree exists — AddExisting would otherwise re-create
	// it from a stale surviving local branch ref, and a kept checkout is
	// frozen at the sha it was handed off with.
	if *syncFlag {
		if err := git.Fetch(repo.Path, "origin", branch); err != nil {
			return fmt.Errorf("--sync: fetch origin %s: %w", branch, err)
		}
	}

	// Worktree: reuse as-is when present (never touch dirty files), else
	// re-create from the existing branch.
	wtPath := worktree.DefaultPath(repo.Path, branch)
	freshWorktree := false
	if _, statErr := os.Stat(wtPath); statErr != nil {
		wt, err := worktree.AddExisting(repo.Path, branch)
		if err != nil {
			return err
		}
		wtPath = wt.Path
		freshWorktree = true
		for _, envFile := range []string{".env", ".envrc", ".env.local"} {
			src := filepath.Join(repo.Path, envFile)
			if data, err := os.ReadFile(src); err == nil {
				_ = os.WriteFile(filepath.Join(wtPath, envFile), data, 0o600)
				fmt.Printf("→ copied %s\n", envFile)
			}
		}
		fmt.Printf("→ worktree %s\n", wtPath)
	} else {
		fmt.Printf("→ reusing worktree %s\n", wtPath)
	}

	// The reset applies to fresh worktrees too: AddExisting creates from
	// the LOCAL branch ref when one survives, and that ref is exactly
	// what went stale while the other host worked the branch.
	if *syncFlag {
		if dirty, derr := git.IsDirty(wtPath); derr != nil {
			return derr
		} else if dirty {
			return fmt.Errorf("%s: worktree %s has uncommitted changes — --sync would discard them; commit or stash first, or adopt without --sync", id, wtPath)
		}
		if err := git.ResetHard(wtPath, "origin/"+branch); err != nil {
			return fmt.Errorf("--sync: reset to origin/%s: %w", branch, err)
		}
		fmt.Printf("→ synced to origin/%s\n", branch)
	}

	// Pickup (or manual) prompt — also the fallback when resume fails.
	promptMode := kickoff.ModePickup
	if *manual {
		promptMode = kickoff.ModeManual
	}
	var verbs provider.Verbs
	if provErr == nil {
		verbs = prov.Verbs()
	}
	prompt, err := kickoff.Render(task, verbs, repoKind, "", promptMode, "")
	if err != nil {
		return err
	}
	promptDir := filepath.Join(stateDir(), "prompts")
	_ = os.MkdirAll(promptDir, 0o755)
	promptPath := filepath.Join(promptDir, id+".txt")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return err
	}

	ws := workspace.Find(repo.Path)
	sessionName := cockpitSessionFor(ws)
	if err := tmux.EnsureWorkspaceSession(sessionName, workspaceRoot(ws, repo.Path)); err != nil {
		return err
	}
	windowName := tmux.WorkerWindowProfile(repoShort(repoName, ws), branch, profileName)
	if tmux.WindowExists(sessionName, windowName) {
		return fmt.Errorf("window %s:%s already exists — `gv attach %s`", sessionName, windowName, id)
	}
	if err := tmux.CreateWindow(sessionName, windowName, wtPath); err != nil {
		return err
	}
	// Target the fresh window by id (grove-116): a name-built target
	// prefix-matches, so it could resolve to a sibling window whose name
	// extends this one's ("repo · grove-1" vs "repo · grove-10").
	windowTarget, ok := tmux.WindowID(sessionName, windowName)
	if !ok {
		return fmt.Errorf("window %q vanished right after creation in session %q", windowName, sessionName)
	}
	if err := tmux.DisableAutoRename(windowTarget); err != nil {
		return err
	}
	// Capture the split's "%N" id at creation (grove-168): a literal ".1"
	// index only names this pane under the default pane-base-index.
	claudePane, err := tmux.SplitVerticalWindow(windowTarget, wtPath)
	if err != nil {
		return err
	}

	// Event BEFORE the pane command: FindByCwd skips Done tasks, so the
	// revived session's SessionStart hook only matches (and captures the
	// new session id) once the fold has flipped Done=false.
	adoptData := map[string]string{
		"title": task.Title, "url": task.URL, "repo": repoName,
		"branch": branch, "worktree": wtPath,
		"tmux_session": sessionName, "tmux_window": windowName,
	}
	// Persist the effective profile so state stays true — including the
	// intentional strip: `--profile anthropic` writes "" and clears the field
	// on fold (grove-36 T3). Only omit the key when there was never a profile
	// to change, keeping unprofiled adopt events byte-identical.
	if profileName != "" || storedProfile != "" {
		adoptData["model_profile"] = profileName
	}
	if err := state.Append(stateDir(), state.Event{
		Type: state.EvTaskAdopted, Ticket: id, Data: adoptData,
	}); err != nil {
		return err
	}

	claudeBin := config.WithModel(repo.Claude, *modelFlag)
	secrets := config.SecretsPath()
	// Wrap each claude limb separately: WrapProfile ends in `exec`, which
	// replaces the shell, so a single wrap around `resume || fresh` would make
	// the `|| fresh` fallback unreachable and, on a resume failure, silently
	// drop to the operator's own Claude sub — the exact provider switch this
	// ticket kills (grove-36 T3). Wrap the
	// composed claude+prompt command only, never repo.Claude itself (hooks
	// resolve the worker's config dir from the stored r.Claude) nor the setup
	// prefix added below. profile == nil leaves every limb byte-identical.
	freshCmd := config.WrapProfile(fmt.Sprintf(`%s "$(cat %q)"`, claudeBin, promptPath), profile, secrets)
	claudeCmd := freshCmd
	if sessionID != "" && !*manual {
		resumeCmd := config.WrapProfile(fmt.Sprintf("%s --resume %s", claudeBin, sessionID), profile, secrets)
		claudeCmd = resumeCmd + " || " + freshCmd
	}
	if freshWorktree && repo.Setup != "" {
		exe, _ := os.Executable()
		claudeCmd = fmt.Sprintf("%s run-setup %s %s && %s", exe, repoName, shellQuoteRoot(), claudeCmd)
	}
	if err := tmux.SendKeys(claudePane, claudeCmd); err != nil {
		return err
	}

	how := "pickup prompt"
	if sessionID != "" && !*manual {
		how = "resume " + sessionID + " (pickup prompt fallback)"
	}
	if *manual {
		how = "manual — attach to drive it"
	}
	if *modelFlag != "" {
		how += ", model " + *modelFlag
	}
	fmt.Printf("✓ %s adopted (%s)\n  watch:  gv ls\n  attach: gv attach %s\n", id, how, id)
	return nil
}

// --- pause ---

// cmdPause parks one worker (grove-90): kill its tmux window to free the
// CPU its node process + MCP children burn, leaving worktree, branch, and
// session transcript untouched — `gv adopt <ticket>` resumes the stored
// session losslessly. Paused is a bookmark, not trash: the task stays in
// `gv ls` (⏸), audit classifies it `paused` (never abandoned), and sweep
// never offers cleanup for it.
func cmdPause(args []string) error {
	echoWorkspace()
	fs := flag.NewFlagSet("pause", flag.ExitOnError)
	force := fs.Bool("force", false, "pause even mid-turn (the in-flight turn is lost; everything committed to the transcript survives)")
	positionals := parseAnywhere(fs, args)
	if len(positionals) != 1 {
		return fmt.Errorf("usage: gv pause <ticket> [--force]  (kills the window only — worktree, branch, and uncommitted changes survive; resume: gv adopt <ticket>)")
	}
	t, err := findTask(positionals[0])
	if err != nil {
		return err
	}
	windowLive := tmux.WindowLive(t.TmuxSession, t.TmuxWindow)
	if t.Paused && !windowLive {
		return fmt.Errorf("%s is already paused — `gv adopt %s` resumes it", t.Ticket, t.Ticket)
	}
	// Mid-turn guard: agent state `working` means a turn is in flight
	// (hooks are truth — Stop hasn't fired). Killing now loses only that
	// turn, but the operator should choose that knowingly.
	if t.Agent == state.AgentWorking && !*force {
		return fmt.Errorf("%s appears mid-turn (agent working) — pausing now loses the in-flight turn; `gv pause %s --force` to pause anyway", t.Ticket, t.Ticket)
	}
	return pauseTask(t)
}

// pauseTask is the shared pause action (gv pause and sweep's idle offer):
// event BEFORE the kill (the grove-33 park pattern) — the bookmark must
// be durable even if this process dies with the window.
func pauseTask(t *state.Task) error {
	if err := state.Append(stateDir(), state.Event{Type: state.EvTaskPaused, Ticket: t.Ticket}); err != nil {
		return err
	}
	if tmux.WindowLive(t.TmuxSession, t.TmuxWindow) {
		if err := tmux.KillWindow(t.TmuxSession, t.TmuxWindow); err != nil {
			return err
		}
	}
	fmt.Printf("⏸ %s paused — window killed; worktree, branch, and uncommitted changes untouched\n  resume: gv adopt %s\n", t.Ticket, t.Ticket)
	return nil
}

// --- done / sweep ---

func cmdDone(args []string) error {
	echoWorkspace()
	fs := flag.NewFlagSet("done", flag.ExitOnError)
	force := fs.Bool("force", false, "clean up even if the PR is not merged (or none exists)")
	positionals := parseAnywhere(fs, args)
	if len(positionals) != 1 {
		return fmt.Errorf("usage: gv done <ticket> [--force]")
	}
	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	t, err := findTask(positionals[0])
	if err != nil {
		return err
	}
	return finishTask(cfg, t, *force)
}

func finishTask(cfg *config.Config, t *state.Task, force bool) error {
	repo, ok := cfg.Repos[t.Repo]
	if !ok {
		return fmt.Errorf("repo %q no longer in config", t.Repo)
	}

	// Degraded no-remote path (DESIGN.md §5.2): with no remote there is no
	// PR to verify, so --force IS the human confirmation.
	hasRemote := git.HasRemote(repo.Path, "origin")
	outcome := "none"
	if !hasRemote {
		if !force {
			return fmt.Errorf("%s: repo %s has no remote — grove cannot verify the work merged; confirm cleanup with --force (the local branch is deleted)", t.Ticket, t.Repo)
		}
		fmt.Println("→ no remote: skipping merge check (--force is the confirmation)")
	} else {
		merged, pr, err := github.Merged(repo.Path, t.Branch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: merge check failed: %v\n", err)
		}
		if pr != nil {
			outcome = ledger.Outcome(pr.State)
		}
		if !merged && !force {
			prState := "no PR found"
			if pr != nil {
				prState = fmt.Sprintf("PR #%d is %s", pr.Number, pr.State)
			}
			return fmt.Errorf("%s: %s — not cleaning up (use --force to override)", t.Ticket, prState)
		}
	}

	// Final ledger row BEFORE teardown: this snapshot is what keeps the
	// ticket's cost history alive after Claude Code prunes the transcripts
	// and the worktree is gone. Best-effort — a ledger hiccup must never
	// block cleanup.
	if ledger.Enabled(stateDir(), cfg.Cost.Record) {
		tot, _ := cost.NewCache().ForTask(t.Worktree)
		row := ledger.Row{
			Time: time.Now(), Ticket: t.Ticket, Title: t.Title,
			Desc: ledger.Snip(provider.BestEffortDescription(cfg, t.Repo, t.Ticket), 200),
			Repo: t.Repo, Branch: t.Branch, Outcome: outcome,
			Input: tot.Input, Output: tot.Output,
			CacheCreate: tot.CacheCreate5m + tot.CacheCreate1h,
			CacheRead:   tot.CacheRead, Turns: tot.Turns, USD: tot.USD,
			Models: tot.Mix(),
		}
		if err := ledger.Append(stateDir(), row); err != nil {
			fmt.Fprintf(os.Stderr, "warning: spend ledger append: %v\n", err)
		} else {
			fmt.Printf("→ ledger: final snapshot recorded (est $%.2f, %s)\n", tot.USD, outcome)
		}
	}

	if tmux.WindowLive(t.TmuxSession, t.TmuxWindow) {
		_ = tmux.KillWindow(t.TmuxSession, t.TmuxWindow)
		fmt.Println("→ killed tmux window")
	}
	killWorktreeProcesses(t)
	if err := worktree.RemoveSafe(repo.Path, t.Worktree); err != nil {
		if !force {
			return fmt.Errorf("worktree remove: %w (dirty? retry with --force)", err)
		}
		if err := git.RemoveWorktreeForce(repo.Path, t.Worktree); err != nil {
			return fmt.Errorf("worktree remove --force: %w", err)
		}
	}
	fmt.Println("→ removed worktree")
	// -D, not -d: squash-merged branches are never ancestry-merged.
	if err := git.ForceDeleteBranch(repo.Path, t.Branch); err != nil {
		fmt.Fprintf(os.Stderr, "warning: local branch delete: %v\n", err)
	}
	if hasRemote {
		if err := git.DeleteRemoteBranch(repo.Path, t.Branch); err != nil {
			fmt.Fprintf(os.Stderr, "warning: remote branch delete (may already be gone): %v\n", err)
		}
		fmt.Println("→ deleted branch (local + remote)")
	} else {
		fmt.Println("→ deleted local branch")
	}

	if err := state.Append(stateDir(), state.Event{Type: state.EvTaskDone, Ticket: t.Ticket}); err != nil {
		return err
	}
	fmt.Printf("✓ %s cleaned up (the task's terminal status in your tracker is yours to move)\n", t.Ticket)
	return nil
}

// cmdUntrack drops a task from tracking. Without --rm nothing but state
// changes — worktree, branches, and window all survive (for "I'm taking
// this over by hand"). --rm is the routine abandon path: window, worktree,
// and local branch go; the remote branch survives unless --rm-remote,
// because an abandoned branch may hold the only copy of unmerged work.
func cmdUntrack(args []string) error {
	echoWorkspace()
	fs := flag.NewFlagSet("untrack", flag.ExitOnError)
	rm := fs.Bool("rm", false, "also remove window, worktree, and local branch")
	rmRemote := fs.Bool("rm-remote", false, "with --rm: delete the remote branch too")
	force := fs.Bool("force", false, "with --rm: remove even dirty/unpushed worktrees")
	positionals := parseAnywhere(fs, args)
	if len(positionals) != 1 {
		return fmt.Errorf("usage: gv untrack <ticket> [--rm] [--rm-remote] [--force]")
	}
	t, err := findTask(positionals[0])
	if err != nil {
		// Handed-off tombstones (grove-177) are already untracked;
		// untrack is their terminal path — the fold clears the pointer
		// without resurrecting a worker onto the branch. --rm still
		// removes the kept hand-edit checkout.
		if ts, terr := findTombstone(positionals[0]); terr == nil {
			if *rm {
				cfg, err := loadCfg()
				if err != nil {
					return err
				}
				if err := removeTaskArtifacts(cfg, ts, *rmRemote, *force); err != nil {
					return err
				}
			}
			if err := state.Append(stateDir(), state.Event{Type: state.EvTaskUntracked, Ticket: ts.Ticket}); err != nil {
				return err
			}
			fmt.Printf("✓ %s handoff pointer dropped (the task itself stays on %s)\n", ts.Ticket, ts.HandedOffTo)
			return nil
		}
		return err
	}

	if *rm {
		cfg, err := loadCfg()
		if err != nil {
			return err
		}
		if err := removeTaskArtifacts(cfg, t, *rmRemote, *force); err != nil {
			return err
		}
	}

	if err := state.Append(stateDir(), state.Event{Type: state.EvTaskUntracked, Ticket: t.Ticket}); err != nil {
		return err
	}
	if *rm {
		fmt.Printf("✓ %s untracked and cleaned up\n", t.Ticket)
	} else {
		fmt.Printf("✓ %s untracked — worktree, branches, and window untouched\n", t.Ticket)
	}
	return nil
}

// removeTaskArtifacts is the shared --rm teardown (untrack --rm and
// sweep's abandoned path): kill window, remove worktree, delete the
// local branch. The remote branch survives unless rmRemote — an
// abandoned branch may hold the only copy of unmerged work. Guarded by
// SafeToRemove unless force; a missing worktree falls back to checking
// the surviving branch against origin/<base>.
func removeTaskArtifacts(cfg *config.Config, t *state.Task, rmRemote, force bool) error {
	repo, ok := cfg.Repos[t.Repo]
	if !ok {
		return fmt.Errorf("repo %q no longer in config", t.Repo)
	}
	baseRef := "origin/" + repo.Base

	if !force {
		if _, statErr := os.Stat(t.Worktree); statErr == nil {
			ok, reason, err := git.SafeToRemove(t.Worktree, baseRef)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%s: %s — not removing (use --force to override)", t.Ticket, reason)
			}
		} else if git.LocalBranchExists(repo.Path, t.Branch) {
			// Worktree dir is gone but the branch survives — make sure
			// deleting it can't lose the only copy of unmerged commits.
			n, err := git.CommitsNotOn(repo.Path, baseRef, t.Branch)
			if err != nil || n > 0 {
				return fmt.Errorf("%s: branch %s has %d commit(s) not on %s — not deleting (use --force to override)",
					t.Ticket, t.Branch, n, baseRef)
			}
		}
	}

	if tmux.WindowLive(t.TmuxSession, t.TmuxWindow) {
		_ = tmux.KillWindow(t.TmuxSession, t.TmuxWindow)
		fmt.Println("→ killed tmux window")
	}
	killWorktreeProcesses(t)
	if err := worktree.RemoveSafe(repo.Path, t.Worktree); err != nil {
		if !force {
			return fmt.Errorf("worktree remove: %w (retry with --force)", err)
		}
		if err := git.RemoveWorktreeForce(repo.Path, t.Worktree); err != nil {
			return fmt.Errorf("worktree remove --force: %w", err)
		}
	}
	fmt.Println("→ removed worktree")
	if git.LocalBranchExists(repo.Path, t.Branch) {
		if err := git.ForceDeleteBranch(repo.Path, t.Branch); err != nil {
			fmt.Fprintf(os.Stderr, "warning: local branch delete: %v\n", err)
		} else {
			fmt.Println("→ deleted local branch")
		}
	}
	if rmRemote {
		if err := git.DeleteRemoteBranch(repo.Path, t.Branch); err != nil {
			fmt.Fprintf(os.Stderr, "warning: remote branch delete (may already be gone): %v\n", err)
		} else {
			fmt.Println("→ deleted remote branch")
		}
	} else {
		fmt.Println("→ remote branch kept (delete with --rm-remote)")
	}
	return nil
}

// cmdSweep consumes the audit classification: merged tasks get the full
// done cleanup, abandoned tasks (closed PR / stale with no PR) get
// untrack --rm, idle tasks (done/waiting, quiet past idle_after) get
// pause, and orphaned claude/mcp processes get a plain SIGTERM — each
// per-item confirmed, never forced. Offer-building is the pure
// audit.SweepOffers (paused tasks yield zero offers there). Stale prompt
// files of done tasks are pruned automatically at the end.
func cmdSweep(args []string) error {
	echoWorkspace()
	fs := flag.NewFlagSet("sweep", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "print what would be offered, mutate nothing")
	asJSON := fs.Bool("json", false, "machine-readable dry-run output (implies --dry-run)")
	parseAnywhere(fs, args)

	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	tasks, err := state.Load(stateDir())
	if err != nil {
		return err
	}
	rep := audit.Gather(cfg, tasks, stateDir())

	items := audit.SweepOffers(rep.Tasks)
	// Decorate abandoned offers with the worktree-guard preview — impure
	// (stat + git), so it stays out of the pure offer builder.
	byTicket := map[string]audit.TaskResult{}
	for _, r := range rep.Tasks {
		byTicket[r.Ticket] = r
	}
	for i := range items {
		if items[i].Class != audit.Abandoned {
			continue
		}
		r := byTicket[items[i].Ticket]
		if _, statErr := os.Stat(r.Worktree); statErr == nil {
			if repo, ok := cfg.Repos[r.Repo]; ok {
				if ok, reason, err := git.SafeToRemove(r.Worktree, "origin/"+repo.Base); err == nil && !ok {
					items[i].Detail = "guard would refuse: " + reason
				}
			}
		}
	}

	if *asJSON {
		return emitJSON("report", struct {
			Items             []audit.SweepOffer      `json:"items"`
			OrphanProcesses   []audit.OrphanProcess   `json:"orphan_processes"`
			WorktreeProcesses []audit.WorktreeProcess `json:"worktree_processes"`
			StalePrompts      []string                `json:"stale_prompts"`
		}{items, rep.OrphanProcesses, rep.WorktreeProcesses, rep.StalePrompts})
	}

	if len(items) == 0 && len(rep.OrphanProcesses) == 0 && len(rep.WorktreeProcesses) == 0 && len(rep.StalePrompts) == 0 {
		fmt.Println("nothing to sweep")
		return nil
	}

	if *dryRun {
		for _, it := range items {
			fmt.Printf("%s [%s] → %s", it.Ticket, it.Class, it.Action)
			if it.Detail != "" {
				fmt.Printf("  (%s)", it.Detail)
			}
			fmt.Println()
		}
		for _, p := range rep.OrphanProcesses {
			fmt.Printf("pid %d [orphan process] → kill (SIGTERM)  (cpu %.1f%%, up %s: %s)\n",
				p.PID, p.CPUPct, p.Elapsed, truncateLine(p.Args, 60))
		}
		for _, p := range rep.WorktreeProcesses {
			fmt.Printf("pid %d [worktree process %s] → kill (SIGTERM)  (cpu %.1f%%, up %s: %s)\n",
				p.PID, p.Ticket, p.CPUPct, p.Elapsed, truncateLine(p.Args, 60))
		}
		fmt.Printf("%d stale prompt file(s) would be pruned\n", len(rep.StalePrompts))
		return nil
	}

	sc := bufio.NewScanner(os.Stdin)
	confirm := func(prompt string) bool {
		fmt.Printf("%s [y/N] ", prompt)
		return sc.Scan() && strings.ToLower(strings.TrimSpace(sc.Text())) == "y"
	}

	swept := 0
	for _, it := range items {
		t := tasks[it.Ticket]
		if t == nil {
			continue
		}
		prompt := fmt.Sprintf("%s [%s]: %s", it.Ticket, it.Class, it.Action)
		if it.Detail != "" {
			prompt += " (" + it.Detail + ")"
		}
		if !confirm(prompt + " — proceed?") {
			continue
		}
		var actErr error
		switch it.Class {
		case audit.Merged:
			actErr = finishTask(cfg, t, false)
		case audit.Abandoned:
			if actErr = removeTaskArtifacts(cfg, t, false, false); actErr == nil {
				actErr = state.Append(stateDir(), state.Event{Type: state.EvTaskUntracked, Ticket: t.Ticket})
			}
		case audit.Idle:
			// No mid-turn guard needed: Idle requires a done/waiting agent
			// by construction — a working agent never classifies idle.
			actErr = pauseTask(t)
		}
		if actErr != nil {
			fmt.Fprintf(os.Stderr, "  %v\n", actErr)
			continue
		}
		swept++
	}

	killed := 0
	offered := map[int]bool{}
	for _, p := range rep.OrphanProcesses {
		offered[p.PID] = true
		prompt := fmt.Sprintf("pid %d [orphan process] (cpu %.1f%%, up %s): %s — kill (SIGTERM)?",
			p.PID, p.CPUPct, p.Elapsed, truncateLine(p.Args, 60))
		if !confirm(prompt) {
			continue
		}
		if err := killOrphan(p.PID); err != nil {
			fmt.Fprintf(os.Stderr, "  %v\n", err)
			continue
		}
		killed++
	}
	for _, p := range rep.WorktreeProcesses {
		// A pid can classify both ways (claude-shaped AND in a reapable
		// worktree) — one offer is enough.
		if offered[p.PID] {
			continue
		}
		prompt := fmt.Sprintf("pid %d [worktree process %s] (cpu %.1f%%, up %s): %s — kill (SIGTERM)?",
			p.PID, p.Ticket, p.CPUPct, p.Elapsed, truncateLine(p.Args, 60))
		if !confirm(prompt) {
			continue
		}
		if err := killOrphan(p.PID); err != nil {
			fmt.Fprintf(os.Stderr, "  %v\n", err)
			continue
		}
		killed++
	}

	pruned := 0
	for _, name := range rep.StalePrompts {
		if err := os.Remove(filepath.Join(stateDir(), "prompts", name)); err == nil {
			pruned++
		}
	}
	fmt.Printf("swept %d task(s), killed %d orphan process(es), pruned %d stale prompt file(s)\n", swept, killed, pruned)
	return nil
}

// killWorktreeProcesses SIGTERMs every process whose argv references the
// task's worktree path — build/test children daemonize out of the pane
// and survive tmux kill-window (grove-156: jest-worker at 100% CPU for
// days). Runs immediately before worktree removal in both teardown paths;
// gv done / untrack --rm is itself the human's confirmation, so no
// per-item prompt. Safe by construction: the path is grove-created and
// unique to this task. Best-effort — a ps failure or a survivor never
// blocks teardown (survivors are reported, never SIGKILLed).
func killWorktreeProcesses(t *state.Task) {
	if t.Worktree == "" {
		return
	}
	psOut, err := exec.Command("ps", "-Ao", audit.PSFormat).Output()
	if err != nil {
		return
	}
	self := os.Getpid()
	for _, p := range audit.DetectWorktreeProcesses(string(psOut), map[string]string{t.Worktree: t.Ticket}) {
		if p.PID == self {
			continue
		}
		fmt.Printf("→ killing pid %d still referencing the worktree: %s\n", p.PID, truncateLine(p.Args, 60))
		if err := killOrphan(p.PID); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
	}
}

// killOrphan SIGTERMs an orphaned claude/mcp process and waits briefly
// for it to exit. A process that refuses to die is reported, never
// SIGKILLed — escalation stays the human's call (propose, then dispose).
func killOrphan(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("kill %d: %w", pid, err)
	}
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			fmt.Printf("→ pid %d terminated\n", pid)
			return nil
		}
	}
	return fmt.Errorf("pid %d still alive 2s after SIGTERM — not escalating; `kill -9 %d` is yours if you mean it", pid, pid)
}

// --- doctor / hooks ---

// cmdDoctor renders the connections manifest. Exits 1 only when *errors*
// remain — a warnings-only board exits 0 (deliberate change from the P0
// doctor, which failed on any red row).
func cmdDoctor(args []string) error {
	jsonOut := false
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
			continue
		}
		return fmt.Errorf("usage: gv doctor [--json]")
	}
	cfg, cfgErr := loadCfg()
	rows := doctor.Run(cfg, cfgErr)
	if jsonOut {
		if err := doctor.RenderJSON(os.Stdout, rows); err != nil {
			return err
		}
	} else {
		fmt.Println("GROVE DOCTOR")
		doctor.Render(os.Stdout, rows)
	}
	if doctor.Errors(rows) > 0 {
		os.Exit(1)
	}
	return nil
}

// hookSettingsPaths derives every worker profile's settings.json from the
// configured worker commands; a broken config still yields the default
// profile so hooks stay installable.
func hookSettingsPaths() []string {
	if workers := allWorkerCommands(); len(workers) > 0 {
		return hooks.SettingsPaths(workers)
	}
	return hooks.SettingsPaths(nil)
}

// initHookWorkerCommands returns only the worker commands gv init is about
// to touch — this repo (or, in parent scope, its child repos) — read from
// the workspace's own doc. Never the global machine config or other
// workspaces: a brand-new personal repo must default to plain claude and
// never surface an unrelated profile's settings.json (e.g. ~/.cc-work)
// just because some OTHER repo on the machine is configured to use it.
func initHookWorkerCommands(doc *bootstrap.Doc, root, name, scope string) []string {
	if scope == wizard.ScopeParent {
		var out []string
		for _, child := range childRepos(root) {
			out = append(out, doc.Get("repos", filepath.Base(child), "claude"))
		}
		return out
	}
	return []string{doc.Get("repos", name, "claude")}
}

func cmdHooks(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gv hooks install|status")
	}
	paths := hookSettingsPaths()
	switch args[0] {
	case "install":
		done, err := hooks.Install(paths)
		for _, p := range done {
			fmt.Printf("✓ hooks wired into %s\n", p)
		}
		return err
	case "status":
		byPath := hooks.Installed(paths)
		for _, path := range paths {
			fmt.Println(path)
			for _, ev := range []string{"SessionStart", "Notification", "Stop", "SessionEnd"} {
				mark := "✗"
				if byPath[path][ev] {
					mark = "✓"
				}
				fmt.Printf("  %s %s\n", mark, ev)
			}
		}
		return nil
	}
	return fmt.Errorf("usage: gv hooks install|status")
}

// --- helpers ---

// findTask resolves a raw reference against tracked tasks by trying every
// id-shape normalization — per-repo providers mean DEV-1234 and task-001
// coexist in one fleet, so the tracked state (not the global provider
// kind) is the arbiter.
// findTombstone resolves an id to a handed-off forwarding row (grove-177)
// — a task findTask deliberately skips because it is Done here.
func findTombstone(idOrURL string) (*state.Task, error) {
	tasks, err := state.Load(stateDir())
	if err != nil {
		return nil, err
	}
	for _, id := range provider.IDCandidates(idOrURL) {
		if t, ok := tasks[id]; ok && t.Done && t.HandedOffTo != "" {
			return t, nil
		}
	}
	return nil, fmt.Errorf("no handed-off task %s", idOrURL)
}

func findTask(idOrURL string) (*state.Task, error) {
	tasks, err := state.Load(stateDir())
	if err != nil {
		return nil, err
	}
	cands := provider.IDCandidates(idOrURL)
	for _, id := range cands {
		if t, ok := tasks[id]; ok && !t.Done {
			return t, nil
		}
	}
	// Short github refs: `gv done 7` matches a unique tracked id ending
	// in -7; several matches is an honest error (plan review I-2).
	if m := shortRefRe.FindStringSubmatch(strings.TrimSpace(idOrURL)); m != nil {
		var hits []*state.Task
		for id, t := range tasks {
			if !t.Done && strings.HasSuffix(id, "-"+m[1]) {
				hits = append(hits, t)
			}
		}
		if len(hits) == 1 {
			return hits[0], nil
		}
		if len(hits) > 1 {
			var ids []string
			for _, t := range hits {
				ids = append(ids, t.Ticket)
			}
			sort.Strings(ids)
			return nil, fmt.Errorf("#%s is tracked in several repos: %s — use the full id", m[1], strings.Join(ids, ", "))
		}
	}
	// Mid-migration hint: the id may be tracked by another fleet (plan
	// review I-3) — read-only scan, never acts across workspaces.
	list, _ := workspace.LoadRegistry()
	for _, ws := range list {
		if !workspace.Alive(ws) {
			continue
		}
		owned := state.ReadTasks(config.StateDirAt(ws.Root))
		for _, id := range cands {
			if t, ok := owned[id]; ok && !t.Done && ws.Label != wsLabel() {
				return nil, fmt.Errorf("no active task %s here — it is tracked in workspace %q (cd there or `gv switch %s`)", idOrURL, ws.Label, ws.Label)
			}
		}
	}
	return nil, fmt.Errorf("no active task %s — see `gv ls`", idOrURL)
}

// parseAnyID normalizes an id for a provider kind without a constructed
// provider (markdown ids are repo-independent, linear ids DEV-1234-shaped).
func parseAnyID(kind, raw string) (string, error) {
	if kind == "linear" {
		return linear.ParseIdentifier(strings.ToUpper(raw))
	}
	return provider.NewMarkdown("").ParseID(raw)
}

// childRepos lists direct subdirectories that are git repos — the
// parent-scope detection/registration input.
func childRepos(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		child := filepath.Join(root, e.Name())
		if st, err := os.Stat(filepath.Join(child, ".git")); err == nil && st.IsDir() {
			out = append(out, child)
		}
	}
	return out
}

// seedWorkspaceScaffold writes <root>/.grove/.gitignore so state and the
// orchestrator brain never end up committed; the config stays committable.
func seedWorkspaceScaffold(root string) error {
	dir := filepath.Join(root, ".grove")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	gi := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gi); err == nil {
		return nil
	}
	return os.WriteFile(gi, []byte("state/\norchestrator/\nconfig.local.yaml\n"), 0o644)
}

// cmdSwitch is the cross-workspace jump (DESIGN §6.5.3): a picker with
// live rollups, sorted by actionability; selecting attaches that
// workspace's cockpit. Non-TTY never renders a picker — with a label it
// acts, without one it prints the rollup list and exits 0.
func cmdSwitch(args []string) error {
	fs := flag.NewFlagSet("switch", flag.ExitOnError)
	printOnly := fs.Bool("print", false, "print the workspace root instead of jumping")
	positionals := parseAnywhere(fs, args)

	list, err := workspace.LoadRegistry()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return fmt.Errorf("no workspaces registered — run `gv init` in a repo (or parent folder) first")
	}
	rollups := map[string]workspace.Rollup{}
	var alive []workspace.Workspace
	for _, ws := range list {
		if !workspace.Alive(ws) {
			fmt.Fprintf(os.Stderr, "! %s: root %s has no .grove/ anymore — `gv workspaces rm %s`\n", ws.Label, ws.Root, ws.Label)
			continue
		}
		rollups[ws.Label] = workspace.ReadRollup(ws)
		alive = append(alive, ws)
	}
	sorted := workspace.SortByActionability(alive, rollups)

	pick := func(label string) *workspace.Workspace {
		for i := range sorted {
			if sorted[i].Label == label {
				return &sorted[i]
			}
		}
		return nil
	}

	if len(positionals) == 1 {
		ws := pick(positionals[0])
		if ws == nil {
			return fmt.Errorf("unknown workspace %q — see `gv workspaces`", positionals[0])
		}
		if *printOnly {
			fmt.Println(ws.Root)
			return nil
		}
		return openCockpit(ws)
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		for _, ws := range sorted {
			r := rollups[ws.Label]
			fmt.Printf("%-16s %d working · %d waiting · %d review   %s\n", ws.Label, r.Working, r.Waiting, r.Review, ws.Root)
		}
		return nil
	}

	opts := make([]huh.Option[string], 0, len(sorted))
	for _, ws := range sorted {
		r := rollups[ws.Label]
		opts = append(opts, huh.NewOption(
			fmt.Sprintf("%-16s %d working · %d waiting · %d review", ws.Label, r.Working, r.Waiting, r.Review), ws.Label))
	}
	var chosen string
	sel := huh.NewSelect[string]().Title("workspaces (most actionable first)").Options(opts...).Value(&chosen)
	if err := huh.NewForm(huh.NewGroup(sel)).Run(); err != nil {
		return nil // esc = no jump
	}
	if *printOnly {
		fmt.Println(pick(chosen).Root)
		return nil
	}
	return openCockpit(pick(chosen))
}

// cmdWorkspaces manages the registry directly.
func cmdWorkspaces(args []string) error {
	fs := flag.NewFlagSet("workspaces", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable list")
	positionals := parseAnywhere(fs, args)

	if len(positionals) >= 1 {
		switch positionals[0] {
		case "add":
			if len(positionals) != 2 {
				return fmt.Errorf("usage: gv workspaces add <path>")
			}
			abs, err := filepath.Abs(positionals[1])
			if err != nil {
				return err
			}
			ws := workspace.Find(abs)
			if ws == nil {
				return fmt.Errorf("%s has no .grove/ — run `gv init` there first", abs)
			}
			if err := workspace.AddToRegistry(*ws); err != nil {
				return err
			}
			fmt.Printf("✓ registered %s (%s)\n", ws.Label, ws.Root)
			return nil
		case "rm":
			if len(positionals) != 2 {
				return fmt.Errorf("usage: gv workspaces rm <label>")
			}
			if err := workspace.RemoveFromRegistry(positionals[1]); err != nil {
				return err
			}
			fmt.Printf("✓ removed %s from the registry (files untouched)\n", positionals[1])
			return nil
		default:
			return fmt.Errorf("usage: gv workspaces [--json | add <path> | rm <label>]")
		}
	}

	list, err := workspace.LoadRegistry()
	if err != nil {
		return err
	}
	if *asJSON {
		return emitJSON("workspaces", list)
	}
	for _, ws := range list {
		mark := " "
		if !workspace.Alive(ws) {
			mark = "✗"
		}
		fmt.Printf("%s %-16s %-7s %s\n", mark, ws.Label, ws.Scope, ws.Root)
	}
	return nil
}

// shortRefRe matches bare github issue refs (7, #7) for the findTask
// numeric-suffix fallback.
var shortRefRe = regexp.MustCompile(`^#?(\d+)$`)
