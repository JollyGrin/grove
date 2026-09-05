// gv — grove: turns Linear tickets into autonomous Claude Code sessions
// in detached tmux and answers "what can I act on right now?"
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/cost"
	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/doctor"
	"github.com/JollyGrin/grove/internal/fleet"
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
                                              (after a real update: the brain sweep below, report-only)
  gv brains [--json]                          every registered workspace's orchestrator brain vs the
                                              seed this binary carries — pure read, names the refresh
                                              command per workspace; grove never overwrites a brain
  gv init [--yes|--only <step>]               wizard: probe · confirm · connections board
                                              (workspace-aware: repo scope in a git repo,
                                              parent scope in a folder of sibling repos)
  gv switch [<label>] [--print]               cross-workspace picker with live rollups
  gv workspaces [--json|add <path>|rm <label>] manage the workspace registry
  gv grab [<task>] [--repo name] [--manual] [--model id] [--profile p]   task → worktree → agent (no arg: list backlog)
  gv ls [--json]                              fleet table
  gv watch [--json] [--ticket X]...           follow this workspace's transition stream, one event
      [--type t,…] [--sentinel s,…]           per line, flushed as it lands (pure read). Default is
      [--since <RFC3339> | --replay]          FROM NOW — never a baseline sampled after the fact.
      [--until <sentinel or event type>]       --until exits 0 exactly when that sentinel or event type lands
                                              (grove-252: e.g. --until pr_merged, --until worker_waiting).
                                              Never derive completion from a pane: the kickoff prompt
                                              contains all three STATUS lines verbatim.
      … --host <name>                         run the verb over ssh on a configured remote host (hosts: in
                                              config.yaml) — grab/ls/adopt/handoff/answer/nudge/diff/
                                              pause/untrack; answer/nudge match --host only before the
                                              ticket (relay free text may legitimately mention it);
                                              relayed answer/nudge hop with a client --op-id so an ssh
                                              retry can never double-steer (exit 255 auto-retries once)
  gv supervise [--interval 30s]                headless loop: the gv watch transitions above, without a
      [--once] [--json]                        cockpit open — the stream for a workspace whose cockpit
                                              isn't up (a VPS running overflow workers). Recommended
                                              interval floor 5s (cost discipline, not enforced). One
                                              writer at a time: a second supervise (or part 4's cockpit
                                              driver) exits 1 naming the pid already emitting. --once
                                              fires one pass then exits 0 — no worker_waiting/
                                              worker_vanished (that hysteresis needs a continuously
                                              running loop).
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
  gv park [--chats]                           kill this workspace's cockpit session (free memory) — resume with gv + gv adopt
  gv                                          cockpit: dashboard left, orchestrator chats right
  gv orchestrator new [--profile p]           add an orchestrator chat pane (O in the TUI; ) for a profiled one);
                                              --profile opens it on a model profile instead of Claude
  gv orchestrator new --resume <id>           revive an archived chat by Claude session id, detached in its
                                              own grove-chat-<label>-<n> (it opens idle, awaiting input)
  gv orchestrator new --host H [--profile p]  spawn that chat on host H instead, detached in its twin of
                                              this workspace — prints the ssh line that attaches to it
  gv orchestrator new --brief T | --brief-file F
                                              seed the new chat's FIRST message with T (or the text of the
                                              local file F) — a standing brief; composes with --workspace/--host
  gv orchestrator close [--ticket X]          dismiss this chat's own pane (fire-and-forget dispatch)
  gv chat ls [--workspace L] [--json]         orchestrator chats in every registered workspace: live
                                              detached chats, the cockpit's own (read-only) panes, and
                                              archived transcripts — the writable field says which take input
  gv chat tail <s> [--follow] [--since N]     that chat's transcript as JSONL ({seq,role,kind,text,tool,ts});
                                              --follow streams appends, --since N resumes after entry N
  gv chat send <s> "<text>"                   relay text into a live chat and verify it SUBMITTED
  gv chat keys <s> <chars>                    raw keystroke, no Enter (option pickers / permission prompts)
  gv chat serve [--port 3000] [--bind ADDR]   phone UI for those chats on http://127.0.0.1:3000 — loopback by
                                              default and no auth of its own, so put it behind
                                              "tailscale serve --bg 3000". Off unless invoked; ^C stops it
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

// ambientLabelExempt lists verbs that never touch the ambient workspace
// label, so a broken label (see requireValidAmbientLabel) doesn't block
// its own fix or the always-usable surfaces. The hook receiver is not
// listed: it returns from main before the check runs (exit 0, always).
var ambientLabelExempt = map[string]bool{
	"version": true, "--version": true, "help": true, "-h": true, "--help": true,
	"init": true, "workspaces": true, "doctor": true, "update": true,
}

// requireValidAmbientLabel applies the registry's ValidateLabel rule to
// the ambient workspace's label (grove-191): a label derived from a
// reserved/invalid directory name — the grove repo itself is dir "grove" —
// used to pass silently here while `workspaces add` refused it, and bare
// `gv` then built a grove-grove cockpit session. The registry and ambient
// paths now agree: error with the pointer instead.
func requireValidAmbientLabel() error {
	if ambient.ws == nil {
		return nil
	}
	if err := workspace.ValidateLabel(ambient.ws.Label); err != nil {
		return fmt.Errorf("workspace at %s: %v — set workspace.label in %s",
			ambient.ws.Root, err, filepath.Join(ambient.ws.Root, ".grove", "config.yaml"))
	}
	return nil
}

// routeIntoWorkspace re-executes this binary with the identical argv
// inside ws (grove-191) — the ssh-passthrough shape one machine down, so
// global-layer verbs reach workspace tasks with zero remote-specific code.
// Prints the routing line first, streams stdio, and exits with the child's
// code (argv is passed as a slice: no shell, no quoting). Only a launch
// failure returns; callers treat that as an ordinary error. No recursion
// is possible: the child's ambient walk-up finds the workspace, and the
// workspace layer never scans the registry.
func routeIntoWorkspace(ws *workspace.Workspace) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	fmt.Printf("→ workspace %s\n", ws.Label)
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Dir = ws.Root
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	os.Exit(0)
	return nil // unreachable; keeps the signature honest for callers
}

// ambiguousError is the honest refusal when a ticket is tracked in more
// than one registered workspace (grove-191): routing would pick a winner
// silently, so name them and let the human cd.
func ambiguousError(owners []workspace.Workspace, idOrURL string) error {
	var labels []string
	for _, ws := range owners {
		labels = append(labels, ws.Label)
	}
	return fmt.Errorf("%s is tracked in several workspaces: %s — run the verb from inside one", idOrURL, strings.Join(labels, ", "))
}

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
		if err := requireValidAmbientLabel(); err != nil {
			fmt.Fprintln(os.Stderr, "gv:", err)
			os.Exit(1)
		}
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

	// grove-191: an ambient workspace label that fails the registry's own
	// ValidateLabel rule (derived from a reserved/invalid directory name —
	// the grove repo itself — or set wrong in config) must fail loudly
	// with the fix pointer, never silently build a grove-<reserved>
	// session. The hook receiver returns above (its exit-0 rule), and the
	// verbs that never touch the ambient label stay usable so the fix
	// paths (init, workspaces) keep working from inside.
	if !ambientLabelExempt[cmd] {
		if err := requireValidAmbientLabel(); err != nil {
			fmt.Fprintln(os.Stderr, "gv:", err)
			os.Exit(1)
		}
	}

	// --host <name> (grove-176): run the verb on a remote grove host over
	// ssh, flags passed through verbatim, output printed unchanged, exit
	// code propagated. Intercepted before dispatch so the local verb never
	// touches local state for a remote task. The relay verbs (answer,
	// nudge) recognize --host only in leading-flag position, before the
	// ticket (ExtractHostPrefix): their free text may legitimately contain
	// "--host pc" (`gv nudge grove-7 try gv ls --host pc`), and
	// string-scanning the whole argv would hijack it. Every other
	// supported verb takes flags only, so whole-argv scanning is safe.
	if remote.Supported[cmd] {
		var host string
		var rest []string
		if cmd == "answer" || cmd == "nudge" {
			host, rest = remote.ExtractHostPrefix(args)
		} else {
			host, rest = remote.ExtractHost(args)
		}
		if host != "" {
			// Relayed answer/nudge hop with a client op id (grove-186):
			// an ambiguous ssh failure retries the SAME argv, and the
			// remote's SeenOpID receipt dedups, so a retry can never
			// double-steer the worker. `orchestrator new` (grove-198)
			// rides the same idempotent hop.
			var code int
			var err error
			switch {
			case cmd == "answer" || cmd == "nudge":
				code, err = runRemoteRelay(host, cmd, rest)
			case cmd == "orchestrator":
				if len(rest) == 0 || rest[0] != "new" {
					fmt.Fprintf(os.Stderr, "gv: --host is only supported for `gv orchestrator new` (supported: %s)\n", remote.SupportedList)
					os.Exit(1)
				}
				code, err = runRemoteOrchestratorNew(host, rest[1:])
			default:
				code, err = runRemote(host, cmd, rest)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "gv:", err)
				os.Exit(1)
			}
			os.Exit(code)
		}
	} else {
		// A real --host on an unsupported verb gets the friendly
		// supported-list error, not a flag-parse death.
		if host, _ := remote.ExtractHost(args); host != "" {
			fmt.Fprintf(os.Stderr, "gv: --host is not supported for `gv %s` yet (supported: %s)\n", cmd, remote.SupportedList)
			os.Exit(1)
		}
	}

	var err error
	switch cmd {
	case "version":
		cmdVersion()
	case "update":
		err = cmdUpdate(args)
	case "brains":
		err = cmdBrains(args)
	case "init":
		err = cmdInit(args)
	case "grab":
		err = cmdGrab(args)
	case "ls":
		err = cmdLs(args)
	case "watch":
		err = cmdWatch(args)
	case "supervise":
		err = cmdSupervise(args)
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
	case "chat":
		err = cmdChat(args)
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

// relayRetryWait is the pause before the automatic same-op-id retry of a
// relayed answer/nudge after ssh dies with 255 (grove-186). Package-level
// so tests can shrink it.
var relayRetryWait = 2 * time.Second

// runRemoteRelay is runRemote for the relay verbs (grove-186): every hop
// carries a client op id — minted here, or reused when the command line
// already carries one (which is what makes the manual retry command in
// the double-failure error safe to paste) — and ssh exit 255 (connection
// failure, outcome unknown: the remote may or may not have acted) re-runs
// the SAME argv once after a beat. The remote's SeenOpID receipt dedups,
// so the retry's worst case is a "✓ already applied", never a second
// paste into the worker. A second 255 gives up with the exact manual
// retry command.
func runRemoteRelay(host, verb string, rest []string) (int, error) {
	cfg, err := loadCfg()
	if err != nil {
		return 0, err
	}
	opID, rest := remote.ExtractOpIDPrefix(rest)
	if opID == "" {
		opID = remote.NewOpID()
	}
	args := append([]string{"--op-id", opID}, rest...)
	manual := "gv " + verb + " --host " + host + " --op-id " + opID
	for _, a := range rest {
		manual += " " + remote.Quote(a)
	}
	return runRemoteIdempotent(cfg, host, verb, args, opID, manual, os.Stdout)
}

// runRemoteIdempotent is the shared hop of every op-id-carrying relayed
// mutation (grove-186 answer/nudge, grove-198 orchestrator new): run the
// argv on the host, and on ssh exit 255 — connection failure, outcome
// unknown — re-run the SAME argv once after a beat. The remote dedups on
// the op id, so the retry's worst case is an "already applied" print. A
// second 255 gives up with `manual`, the exact by-hand retry command.
// stdout is a writer so a caller can tee the remote's output (the chat
// spawn parses the session name out of it).
func runRemoteIdempotent(cfg *config.Config, host, verb string, args []string, opID, manual string, stdout io.Writer) (int, error) {
	return runRemoteIdempotentWith(host, opID, manual, os.Stderr, func() (int, error) {
		return remote.Run(cfg, host, verb, args, stdout, os.Stderr)
	})
}

// runRemoteIdempotentWith is that hop with the ssh call and the notice
// stream injected: the cockpit's `@` spawn (grove-199) relays from inside
// the tea loop, where the hop must neither write to the real stderr (it
// would corrupt the alt-screen) nor share the terminal's stdin with the
// TUI's own key reader — so it passes a buffer and remote.RunDetached.
func runRemoteIdempotentWith(host, opID, manual string, notice io.Writer, run func() (int, error)) (int, error) {
	code, err := run()
	if err != nil {
		return 0, err
	}
	if code != 255 {
		return code, nil
	}
	fmt.Fprintf(notice, "gv: ssh to %s failed (exit 255) — the remote may or may not have acted; retrying once with the same op id %s\n", host, opID)
	time.Sleep(relayRetryWait)
	if code, err = run(); err != nil {
		return 0, err
	}
	if code == 255 {
		return 1, fmt.Errorf("ssh to %s failed twice (exit 255) — nothing further was sent; safe to retry by hand, the op id makes a duplicate a no-op: %s", host, manual)
	}
	return code, nil
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
	applied := false
	opts := update.Options{Current: version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Force: *force, Applied: &applied}
	if !*yes {
		sc := bufio.NewScanner(os.Stdin)
		opts.Confirm = func(_, _ string) bool {
			fmt.Print("replace the installed binary? [y/N] ")
			return sc.Scan() && strings.ToLower(strings.TrimSpace(sc.Text())) == "y"
		}
	}
	if err := update.Run(opts); err != nil {
		return err
	}
	// grove-236: a new binary means a new orchestrator seed, so say which
	// workspaces are now behind. Only after a REAL replace — an
	// already-current box stays quiet — and never fatal: the sweep is a
	// report hung off a finished update, not part of it.
	if applied {
		reportBrainSweepAfterUpdate(opts.Target)
	}
	return nil
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
	tui.SpawnRemoteOrchestrator = spawnRemoteChat
	tui.AttachTask = attachTask
	// The cockpit's X hotkey never reaps chats (grove-203): it kills the
	// session it is drawn in, so a warning printed after the fact would have
	// nowhere to land — the modal names them BEFORE the keypress instead, and
	// the parked event records them either way.
	tui.CloseWorkspace = func(sd, label string) error {
		chats, _ := liveChats(label)
		return closeWorkspace(sd, label, chats, false)
	}
	tui.LiveChats = func(label string) []string {
		chats, _ := liveChats(label)
		names := make([]string, len(chats))
		for i, c := range chats {
			names[i] = c.Session
		}
		return names
	}
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
// record survives — kill-session takes down the cockpit, the orchestrator
// pane, and every worker window in that session, freeing their memory.
// Nothing is deleted and no task terminal state is touched: bare `gv`
// rebuilds the cockpit and `gv adopt <ticket>` revives a worker. Injected
// into the TUI as tui.CloseWorkspace.
//
// What it does NOT reach (grove-203): the workspace's detached orchestrator
// chats. Since grove-198 those are their own `grove-chat-<label>-<n>`
// sessions — deliberately outside grove-<label>, so an attaching ssh client
// cannot resize the cockpit's shared windows and the chat outlives the ssh
// drop — so one kill-session is no longer "one stroke". chats is what
// liveChats found; killChats reaps them too (`gv park --chats`). Either way
// their names go into the parked event, so a park that leaves claude
// processes running on the host is recorded rather than silent — a remote
// host has no cockpit row to notice them from.
func closeWorkspace(stateDir, label string, chats []tmux.ChatSession, killChats bool) error {
	if err := state.Append(stateDir, parkedEvent(chats, killChats)); err != nil {
		return err
	}
	// Chats first: killing grove-<label> can take down this very process
	// (the cockpit X hotkey, or `gv park` run from inside a worker window),
	// so anything sequenced after it may never run.
	if killChats {
		for _, c := range chats {
			if err := tmux.KillSession(c.Session); err != nil {
				return fmt.Errorf("could not kill chat session %s: %w", c.Session, err)
			}
		}
	}
	return tmux.KillSession(cockpitSessionForLabel(label))
}

// parkedEvent builds the durable park record (grove-33). Its chat fields
// (grove-203) are the audit trail for what park did NOT stop: `chats` names
// every detached chat that was live at park time, and `chats_killed` marks
// the `--chats` runs that reaped them. A park with no chats writes no Data
// at all, so the pre-grove-203 record shape is unchanged.
func parkedEvent(chats []tmux.ChatSession, killChats bool) state.Event {
	ev := state.Event{Type: state.EvWorkspaceParked}
	if len(chats) == 0 {
		return ev
	}
	names := make([]string, len(chats))
	for i, c := range chats {
		names[i] = c.Session
	}
	ev.Data = map[string]string{"chats": strings.Join(names, ",")}
	if killChats {
		ev.Data["chats_killed"] = "true"
	}
	return ev
}

// parkChatLines renders what park tells the operator about the chats it
// found — one line each, plus a closing line naming the escape hatch when
// they are being left behind. Empty for a workspace with no chats, so the
// ordinary park keeps its two-line output.
func parkChatLines(chats []tmux.ChatSession, killChats bool) []string {
	if len(chats) == 0 {
		return nil
	}
	lines := make([]string, 0, len(chats)+1)
	for _, c := range chats {
		if killChats {
			lines = append(lines, fmt.Sprintf("  ✗ chat %s (pid %d) — killed", c.Session, c.PID))
			continue
		}
		lines = append(lines, fmt.Sprintf("  ▸ chat %s still running (pid %d, %s) — %s",
			c.Session, c.PID, c.Command, remote.ChatAttachLine(c.Session)))
	}
	if !killChats {
		lines = append(lines, fmt.Sprintf("  %d chat session(s) survive this park — they are their own tmux sessions. `gv park --chats` reaps them; `gv audit` lists them.", len(chats)))
	}
	return lines
}

// liveChats enumerates the workspace's detached orchestrator chats
// (grove-203). The registry read is what separates a chat from a cockpit
// whose label merely looks like one, so its failure is returned rather than
// swallowed — but every caller treats it as a warning: refusing to park, or
// failing an audit, because a registry file is unreadable would be worse
// than the under-report.
func liveChats(label string) ([]tmux.ChatSession, error) {
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return nil, err
	}
	return tmux.ChatSessions(label, isCockpit), nil
}

// cmdPark is the CLI twin of the cockpit X hotkey (grove-33): park the
// ambient workspace. Run from outside the cockpit (or via orchestrator
// dispatch); run from inside the session it kills, the print may not land
// because the pane dies with the session — the parked event is durable
// either way.
//
// Chats survive a park by default (grove-203, "propose then dispose"): they
// are long-lived by design and a chat is the operator's own conversation,
// not a worker to reap on a schedule. So park NAMES each one it is leaving
// behind, with the pid and the attach line, and `--chats` is the explicit
// opt-in that kills them too.
func cmdPark(args []string) error {
	fs := flag.NewFlagSet("park", flag.ExitOnError)
	killChats := fs.Bool("chats", false, "also kill this workspace's detached orchestrator chats (grove-chat-<label>-<n>); by default they survive the park")
	parseAnywhere(fs, args)

	label := wsLabel()
	session := cockpitSessionForLabel(label)
	if !tmux.SessionExists(session) {
		return fmt.Errorf("%s is not running — nothing to park", session)
	}
	chats, err := liveChats(label)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not enumerate this workspace's chat sessions: %v\n", err)
	}
	fmt.Printf("⏸ parking %s — state saved; resume with gv (then gv adopt <ticket>)\n", session)
	for _, line := range parkChatLines(chats, *killChats) {
		fmt.Println(line)
	}
	return closeWorkspace(stateDir(), label, chats, *killChats)
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

// orchestratorDirAt is the brain dir of the workspace rooted at root —
// what a cockpit opened there would use. `gv init` works on the root it
// just configured, which may not be the ambient workspace.
func orchestratorDirAt(root string) string {
	return bootstrap.OrchestratorDirAt(root)
}

// brainStateOf reports the orchestrator brain's drift state for the
// wizard step's "current" column (absent · current · stale · unstamped).
func brainStateOf(root string, force bool) string {
	plan, err := bootstrap.InspectBrain(orchestratorDirAt(root), orchestrator.ClaudeMd, force)
	if err != nil {
		return ""
	}
	return string(plan.State)
}

// refreshOrchestratorBrain is the `orchestrator-md` step: seed an absent
// brain, leave an up-to-date one alone, and drop CLAUDE.md.new beside a
// brain whose seed stamp has moved. It never overwrites an existing
// brain — the human diffs and merges (grove-190).
func refreshOrchestratorBrain(root string, force bool) error {
	plan, wrote, err := bootstrap.RefreshBrain(orchestratorDirAt(root), orchestrator.ClaudeMd, force)
	if err != nil {
		return err
	}
	mark := "•"
	if wrote != "" {
		mark = "✓"
	}
	fmt.Printf("%s %s\n", mark, plan.Note)
	if plan.Action == bootstrap.ActionNew {
		fmt.Printf("   → diff %s %s\n", filepath.Join(orchestratorDirAt(root), bootstrap.BrainFile), wrote)
	}
	return nil
}

// seedOrchestratorDir creates an orchestrator brain dir and installs the
// CLAUDE.md brain on first run. Shared by the cockpit build and the
// detached chat spawn (grove-198), which must seed a twin exactly the way
// a locally opened cockpit would. Never touches a brain that already
// exists — a moved seed is `gv doctor`'s row to raise and
// `gv init --only orchestrator-md`'s job to deliver (grove-190).
func seedOrchestratorDir(orchDir string) error {
	wrote, err := bootstrap.SeedBrain(orchDir, orchestrator.ClaudeMd)
	if err != nil {
		return err
	}
	if wrote != "" {
		fmt.Println("→ installed orchestrator CLAUDE.md at", wrote)
	}
	return nil
}

// buildCockpit lays out the main-vertical cockpit: dashboard TUI as the
// main (left) pane, one orchestrator chat stacked right (O adds more).
// Seeds the orchestrator dir + CLAUDE.md brain on first run.
func buildCockpit(ws *workspace.Workspace, cfg *config.Config) error {
	session := cockpitSessionFor(ws)
	orchDir := orchestratorDirFor(ws, cfg)
	if err := seedOrchestratorDir(orchDir); err != nil {
		return err
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

// wrapOrchestratorLaunch wraps an already-composed orchestrator launch in
// a profile's backend env (a nil profile leaves it untouched). Shared with
// chatSpawnPlan, whose `--resume` variant must compose the flags onto the
// bare launch BEFORE this wrap — WrapProfile ends in `exec <cmd> )`, so a
// flag appended afterwards lands outside the subshell.
func wrapOrchestratorLaunch(launch string, p *config.ModelProfile) string {
	return config.WrapProfile(launch, p, config.SecretsPath())
}

// flagWasSet answers "did the operator type this flag?" — the one thing a
// flag's value cannot say. An explicitly-empty --brief must be refused
// while an absent one is the ordinary no-brief spawn, and those two are
// the same empty string.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// cmdOrchestratorNew spawns a fresh orchestrator chat pane into the
// cockpit's right column (cockpit design §4) — the O keybind's CLI twin.
// Builds the cockpit first if it isn't running. --profile (grove-36) opens
// the new pane on a model profile instead of the operator's own Claude sub.
func cmdOrchestratorNew(args []string) error {
	fs := flag.NewFlagSet("orchestrator new", flag.ExitOnError)
	profileFlag := fs.String("profile", "", "open this orchestrator on a model profile (e.g. openrouter-glm) instead of Claude")
	wsFlag := fs.String("workspace", "", "spawn detached in this REGISTERED workspace's own grove-chat-<label>-<n> session (the receiving half of --host)")
	resumeFlag := fs.String("resume", "", "revive the archived chat with this Claude session id (`gv chat ls` lists them) instead of starting fresh")
	briefFlag := fs.String("brief", "", "seed the chat's FIRST message with this text (the standing brief)")
	briefFileFlag := fs.String("brief-file", "", "read the standing brief from this file (its text is what travels)")
	opFlag := fs.String("op-id", "", "idempotency receipt for a relayed spawn — the same id twice spawns once")
	asFlag := fs.String("as", "", "the host alias the caller knows this machine by (relayed spawns; used in messages)")
	_ = fs.Parse(args)

	if err := chatResumeConflict(*profileFlag, *resumeFlag); err != nil {
		return err
	}
	brief, err := chatBriefText(*briefFlag, flagWasSet(fs, "brief"), *briefFileFlag)
	if err != nil {
		return err
	}
	if err := chatBriefConflict(brief, *resumeFlag); err != nil {
		return err
	}
	// grove-217: a revival is ALWAYS the detached shape (design §5) — it
	// allocates its own grove-chat-<label>-<n>, never a cockpit pane — so
	// an unlabelled --resume takes the ambient workspace, exactly as the
	// --host half does, and refuses when there is none.
	label := *wsFlag
	if label == "" && *resumeFlag != "" {
		if label = wsLabel(); label == "" {
			return fmt.Errorf("`gv orchestrator new --resume %s` needs a workspace — run it from inside one, or pass --workspace <label>", *resumeFlag)
		}
	}
	// grove-198: --workspace is the receiving half — a detached chat in a
	// registered workspace twin, not a pane in this machine's cockpit.
	if label != "" {
		return spawnWorkspaceChat(chatSpawnReq{
			Label: label, Profile: *profileFlag, Resume: *resumeFlag,
			Brief: brief, OpID: *opFlag, Host: *asFlag,
		})
	}

	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	msg, err := spawnOrchestratorProfileBrief(cfg, *profileFlag, brief)
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
	return spawnOrchestratorBrief(cfg, "")
}

// spawnOrchestratorBrief is spawnOrchestrator with grove-271's standing
// brief. The TUI's O keybind has no place to type one, so the injected
// hook keeps its two-argument shape and only the CLI reaches this.
func spawnOrchestratorBrief(cfg *config.Config, brief string) (string, error) {
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
		// The cockpit's own first pane launches `--continue` and adopts
		// whatever conversation it resumes, so a standing brief has no
		// place in it. An unbriefed spawn is done here — it built the
		// pane that was asked for — and a briefed one falls through to a
		// pane of its own, exactly as the profiled twin does.
		if brief == "" {
			return "cockpit built — gv attaches", nil
		}
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

	launch, id, err := mintedOrchestratorLaunch(orchestratorLaunch(cfg, root), dir, brief, nil)
	if err != nil {
		return "", err
	}
	paneID, err := tmux.SpawnPane(session, dir, launch)
	if err != nil {
		return "", err
	}
	stampOrchestratorPane(paneID, id)
	return "✓ new orchestrator chat pane", nil
}

// mintedOrchestratorLaunch is grove-222 applied to a cockpit pane: mint the
// session id, hand it to claude, and return both so the caller can stamp the
// pane it spawns. p (nil for the unprofiled pane) wraps the launch in a
// backend's env — and the flag must go on BEFORE that wrap, because
// WrapProfile ends in `exec <cmd> )` and anything appended afterwards lands
// outside the subshell, in the shell's lap rather than claude's.
//
// No `--continue` limb, ever: a fresh spawn (`)` / `orchestrator new
// --profile`) starts clean (grove-43 — the --continue twin made every
// profiled spawn resume the previous conversation).
//
// The cockpit's FIRST pane is deliberately not routed through here: it
// launches `--continue`, which adopts the id of whatever conversation it
// resumes, so there is nothing to mint. That pane is the one live case
// chat.Resolve still answers — and, as the only unstamped pane in its
// project dir, it is the case Resolve can answer without guessing.
//
// grove-271: a standing brief is written here too, for the same reason —
// the file is named by the id, so it cannot be laid down until the id
// exists. Both additions go on the BARE launch, ahead of the wrap.
func mintedOrchestratorLaunch(launch, orchDir, brief string, p *config.ModelProfile) (string, string, error) {
	id, err := chat.NewSessionID()
	if err != nil {
		return "", "", err
	}
	launch += " --session-id " + id
	if brief != "" {
		path := chatBriefPath(orchDir, id)
		if err := writeChatBrief(path, brief); err != nil {
			return "", "", err
		}
		launch += chatBriefArg(path)
	}
	return wrapOrchestratorLaunch(launch, p), id, nil
}

// stampOrchestratorPane records a spawned chat's identity on its pane.
// Best-effort by design (the grove-215 stamp rule): a tmux too old for pane
// user options must never fail a spawn, and the id stays recoverable from
// the running claude's argv.
func stampOrchestratorPane(pane, id string) {
	if pane == "" || id == "" {
		return
	}
	if err := tmux.SetPaneChatSession(pane, id); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not stamp %s with session id %s: %v\n", pane, id, err)
	}
}

// spawnOrchestratorProfile is spawnOrchestrator's profile-aware twin
// (grove-36 T4): an empty/unknown-as-"anthropic" profileName falls back to
// spawnOrchestrator unchanged (today's exact behavior). Otherwise the new
// pane's fresh launch runs wrapped in the profile's backend
// (orchestratorLaunchProfile), never the operator's own Claude sub.
func spawnOrchestratorProfile(cfg *config.Config, profileName string) (string, error) {
	return spawnOrchestratorProfileBrief(cfg, profileName, "")
}

// spawnOrchestratorProfileBrief is that twin carrying grove-271's standing
// brief — the CLI's entry point; the TUI hook above stays brief-less.
func spawnOrchestratorProfileBrief(cfg *config.Config, profileName, brief string) (string, error) {
	resolvedName, p, err := cfg.ResolveProfile(profileName, nil)
	if err != nil {
		return "", err
	}
	if p == nil {
		return spawnOrchestratorBrief(cfg, brief)
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
	// The brief lives under the BRAIN dir, not the per-profile cwd: one
	// briefs/ per workspace, keyed by session id, whichever backend ran it.
	launch, id, err := mintedOrchestratorLaunch(orchestratorLaunch(cfg, root), baseDir, brief, p)
	if err != nil {
		return "", err
	}
	paneID, err := tmux.SpawnPane(session, paneDir, launch)
	if err != nil {
		return "", err
	}
	stampOrchestratorPane(paneID, id)
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

// cockpitSessionCheck builds the guard's "is this session a cockpit?"
// answer from THIS machine's workspace registry (grove-199): `grove` and
// `grove-mobile` always, plus `grove-<label>` for every registered label.
// Nothing else may derive a workspace from a session name — a chat session
// is `grove-chat-<label>-<n>`, and a workspace labelled `chat-app` produces
// the very same string, so only the registry can tell them apart.
//
// A registry that cannot be read is a hard error rather than a guess: the
// two readings of an ambiguous name differ by a dead dashboard.
func cockpitSessionCheck() (tmux.CockpitCheck, error) {
	list, err := workspace.LoadRegistry()
	if err != nil {
		return nil, fmt.Errorf("cannot read the workspace registry (needed to tell a cockpit from a chat session): %w", err)
	}
	labels := make(map[string]bool, len(list))
	for _, ws := range list {
		labels[ws.Label] = true
	}
	return func(session string) bool {
		if session == "grove" || session == "grove-mobile" {
			return true
		}
		label, ok := strings.CutPrefix(session, "grove-")
		return ok && labels[label]
	}, nil
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
	// Which sessions hold a dashboard to protect is a REGISTRY question,
	// not a name-shape one (grove-199) — resolve it here, where the
	// registry lives, and hand the answer to the guard.
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return err
	}
	// Validate BEFORE logging: a guard rejection must not leave a
	// "dismissed" activity row for a pane that never closed.
	if err := tmux.PaneClosable(pane, isCockpit); err != nil {
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
	return tmux.ClosePane(pane, isCockpit)
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
	// grove-191: at the global layer a repo that belongs to a workspace
	// routes the whole grab there instead of refusing.
	routeWS, err := requireAmbientWorkspace(ambient.ws, workspace.Find(repo.Path), repoName, repo.Path)
	if err != nil {
		return err
	}
	if routeWS != nil {
		if err := routeIntoWorkspace(routeWS); err != nil {
			return fmt.Errorf("route into workspace %s: %w", routeWS.Label, err)
		}
		return fmt.Errorf("workspace route returned") // unreachable: routeIntoWorkspace exits
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
// ambient workspace, un-workspaced repo). Since grove-191 the global-layer
// arm no longer refuses: it returns the owning workspace as a route
// target and the caller re-execs the whole grab inside it
// (routeIntoWorkspace), so `gv grab <t> --repo X` from the login dir —
// including over `gv grab ... --host` — just works.
func requireAmbientWorkspace(ambientWS, repoWS *workspace.Workspace, repoName, repoPath string) (*workspace.Workspace, error) {
	switch {
	case ambientWS == nil && repoWS == nil:
		return nil, nil
	case ambientWS != nil && repoWS != nil && ambientWS.Root == repoWS.Root:
		return nil, nil
	case ambientWS == nil:
		return repoWS, nil
	case repoWS == nil:
		return nil, fmt.Errorf("repo %s (%s) is outside the %s workspace — map it in its own workspace, or in a parent workspace of both",
			repoName, repoPath, ambientWS.Label)
	default:
		return nil, fmt.Errorf("repo %s (%s) belongs to workspace %s, not %s — grab it from there (cd %s)",
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
	fs.BoolVar(&f.OrchestratorMD, "orchestrator-md", false, "refresh the orchestrator brain from the built-in seed")
	fs.BoolVar(&f.NoOrchestratorMD, "no-orchestrator-md", false, "skip the orchestrator brain step")
	fs.BoolVar(&f.ForceOrchestratorMD, "force-orchestrator-md", false, "write CLAUDE.md.new even for an unstamped (hand-managed) brain")
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
		BrainState:     brainStateOf(root, f.ForceOrchestratorMD),
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
	if a.RefreshOrchestratorMD {
		if err := refreshOrchestratorBrain(root, f.ForceOrchestratorMD); err != nil {
			fmt.Fprintf(os.Stderr, "orchestrator-md: %v\n", err)
		}
	}

	// The summary board: what's available, what would improve this repo.
	fmt.Println()
	cfg, cfgErr := loadCfg()
	doctor.Render(os.Stdout, doctor.Run(cfg, cfgErr, orchestratorDirAt(root)))
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

// lsRow is one `gv ls --json` row; the type lives in internal/fleet so the
// remote merge (grove-178) and the cockpit share it.
type lsRow = fleet.Row

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
	withRemote := fs.Bool("remote", false, "fold every configured host's fleet in (ssh, 5s per host, failures warn)")
	parseAnywhere(fs, args)

	cfg, cfgErr := loadCfg()
	tasks, err := state.Load(stateDir())
	if err != nil {
		return err
	}

	// grove-191: at the global layer the fleet is the whole machine —
	// every alive registered workspace's tasks join the view, each row
	// tagged with its workspace label (the additive `workspace` field).
	// Inside a workspace, and on a machine with no alive registered
	// workspaces, the fleet is exactly today's and the output stays
	// byte-identical (Workspace is omitempty, the column collapses).
	// Workspace state is folded read-only via state.Peek — this ls never
	// rewrites another workspace's derived tasks.json.
	type fleetSrc struct {
		label string
		tasks map[string]*state.Task
		cfg   *config.Config // nil: no PR lookups for this fleet
	}
	fleets := []fleetSrc{{tasks: tasks}}
	if cfgErr == nil {
		fleets[0].cfg = cfg
	}
	if ambient.ws == nil {
		list, _ := workspace.LoadRegistry()
		sort.Slice(list, func(i, j int) bool { return list[i].Label < list[j].Label })
		for _, ws := range list {
			if !workspace.Alive(ws) {
				continue
			}
			wt, err := state.Peek(config.StateDirAt(ws.Root))
			if err != nil {
				fmt.Fprintf(os.Stderr, "gv ls: warning: workspace %s: %v\n", ws.Label, err)
				continue
			}
			f := fleetSrc{label: ws.Label, tasks: wt}
			if c, err := config.LoadAt(ws.Root); err == nil {
				f.cfg = c
			}
			fleets = append(fleets, f)
		}
	}
	var handedOff []*state.Task
	prs := map[string]*github.PR{}
	// grove-251: a ticket lands in prAttempted whenever a lookup was
	// actually issued for it, and in prUnknown when that lookup errored or
	// timed out — so "no PR" (not attempted, or attempted and empty) stays
	// distinguishable from "lookup failed" on the row's pr_known field.
	prAttempted := map[string]bool{}
	prUnknown := map[string]bool{}
	if !*noPR {
		for _, f := range fleets {
			if f.cfg == nil {
				continue
			}
			lookups := map[string][2]string{}
			for _, t := range state.Active(f.tasks) {
				if r, ok := f.cfg.Repos[t.Repo]; ok {
					lookups[t.Ticket] = [2]string{r.Path, t.Branch}
					prAttempted[t.Ticket] = true
				}
			}
			got, unknown := github.FetchAll(lookups)
			for k, v := range got {
				prs[k] = v
			}
			for k := range unknown {
				prUnknown[k] = true
			}
		}
	}

	costCache := cost.NewCache()
	rows := make([]lsRow, 0, len(tasks))
	for _, f := range fleets {
		for _, t := range state.Active(f.tasks) {
			// Resolve the current window name (a P3 status glyph is a display
			// suffix on the stable base) so the byte-comparable detect probe's
			// exact name match still finds a live worker.
			live := detect.DetectLive(t.TmuxSession, tmux.ResolveWindowName(t.TmuxSession, t.TmuxWindow))
			liveStr := "gone"
			if live.Exists {
				liveStr = live.Status.String()
			}
			row := lsRow{Task: t, Live: liveStr, PR: prs[t.Ticket], Workspace: f.label}
			if prAttempted[t.Ticket] {
				known := !prUnknown[t.Ticket]
				row.PRKnown = &known
			}
			if !*noCost {
				if tot, err := costCache.ForTask(t.Worktree); err == nil {
					row.Cost = &tot
				}
			}
			rows = append(rows, row)
		}
		handedOff = append(handedOff, state.HandedOff(f.tasks)...)
	}
	// grove-178: one fleet. Remote hosts are asked in parallel (5s each),
	// a failure is one warning line and never a non-zero exit; local rows
	// carry host "local", remote rows their host name, and the forwarding
	// tombstones (grove-177, `live: "handed-off"`) trail the live rows —
	// replaced when a host reports the task live, marked `?` when the
	// named host answered without it. Without --remote, Merge still
	// appends the tombstone rows, so `gv ls --json` keeps carrying them.
	var results []fleet.Result
	if *withRemote {
		switch {
		case cfgErr != nil:
			fmt.Fprintln(os.Stderr, "gv ls: --remote: config:", cfgErr)
		case len(cfg.Hosts) == 0:
			fmt.Fprintln(os.Stderr, "gv ls: --remote: no hosts configured (add a hosts: map to config.yaml)")
		default:
			results = fleet.Fetch(context.Background(), cfg, cfg.HostNames(), nil)
		}
	}
	rows, warnings := fleet.Merge(rows, handedOff, results)
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "gv ls: warning:", w)
	}

	if *asJSON {
		return emitJSON("tasks", rows)
	}

	live := rows[:0] // in-place filter, order kept
	var elsewhere []lsRow
	for _, r := range rows {
		if r.HandedOffTo != "" {
			elsewhere = append(elsewhere, r)
		} else {
			live = append(live, r)
		}
	}
	rows = live

	if len(rows) == 0 && len(elsewhere) == 0 {
		fmt.Println("no active tasks — `gv grab <ticket>` to start one")
		return nil
	}
	anyRemote := false
	for _, r := range rows {
		if r.Host != fleet.LocalHost {
			anyRemote = true
			break
		}
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
	// The WORKSPACE column (grove-191) collapses the same way: it appears
	// only when the aggregate view carries a workspace-tagged row, so a
	// global-only fleet and every in-workspace run stay byte-identical.
	anyWorkspace := false
	for _, r := range rows {
		if r.Workspace != "" {
			anyWorkspace = true
			break
		}
	}
	header := fmt.Sprintf("%-11s %-11s %-10s %-8s %-9s %-5s %-9s %-8s %s",
		"TICKET", "REPO", "STATUS", "LIVE", "PR", "CI", "PREVIEW", "COST", "AGE")
	if anyProfile {
		header += "  PROFILE"
	}
	if anyRemote {
		header += "  HOST"
	}
	if anyWorkspace {
		header += "  WORKSPACE"
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
		} else if r.PRKnown != nil && !*r.PRKnown {
			pr = "?" // lookup failed/timed out — never render as "no PR" (grove-251)
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
		if anyRemote {
			line += "  " + r.Host
		}
		if anyWorkspace {
			ws := "—"
			if r.Workspace != "" {
				ws = "@" + r.Workspace // the cockpit's @host tag, one layer down
			}
			line += "  " + ws
		}
		fmt.Println(line)
		if r.Agent == state.AgentWaiting && r.Question != "" {
			fmt.Printf("  ◆ %s\n", truncateLine(r.Question, 90))
		}
		if r.Agent == state.AgentBlocked && r.Question != "" {
			fmt.Printf("  ⚠ %s\n", truncateLine(r.Question, 90))
		}
	}
	// Tombstones last, dimmed: the take-it-back hint lives in
	// fleet.Elsewhere (the handoff mirror, never a plain adopt).
	for _, r := range elsewhere {
		fmt.Println(dim(fleet.Elsewhere(r, age)))
	}
	return nil
}

// dim wraps a line in the ANSI faint attribute when stdout is a terminal —
// the "elsewhere" bucket reads as background, not as a live row.
func dim(s string) string {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
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
	// grove-203: the workspace's detached chats are not tasks and live in
	// their own tmux sessions, so nothing in Gather's reconciliation can
	// see them — and nothing else on the machine reports them either.
	chats, chatErr := liveChats(wsLabel())
	if chatErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not enumerate chat sessions: %v\n", chatErr)
	}
	rep.ChatSessions = chats

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
	if len(rep.ChatSessions) > 0 {
		fmt.Printf("\nCHAT SESSIONS (detached orchestrator chats — their own tmux sessions, NOT killed by gv park; report only):\n")
		for _, c := range rep.ChatSessions {
			attached := ""
			if c.Attached {
				attached = "  (attached)"
			}
			fmt.Println(strings.TrimRight(fmt.Sprintf("  %-24s pid %-8d %-10s up %-8s%s",
				c.Session, c.PID, c.Command, age(c.Created), attached), " "))
			fmt.Printf("    %s   (or reap them all with gv park --chats)\n", remote.ChatAttachLine(c.Session))
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
	var allTots []cost.Totals
	var doneUSD float64
	var doneCount, doneTurns int
	for _, t := range tasks {
		tot, err := cache.ForTask(t.Worktree)
		if err != nil || tot.Turns == 0 {
			continue
		}
		allTots = append(allTots, tot)
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
	printUnpricedFooter(unpricedModels(allTots))
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
	// UnpricedModels (grove-249, additive) names every model with
	// cost_known:false across rows, with ticket/turn counts — the pricing
	// table gap is loud in the JSON, not just the $0 it silently produced
	// for five weeks. Always present, empty when every row is priced.
	UnpricedModels []unpricedModel `json:"unpriced_models"`
}

// unpricedModel is one row of the unpriced-models footer/JSON field: a
// model with no pricing table entry, and how much fleet activity rode it
// unpriced — the loud-unknowns signal for grove-249.
type unpricedModel struct {
	Model   string `json:"model"`
	Tickets int    `json:"tickets"`
	Turns   int    `json:"turns"`
}

// unpricedModels aggregates cost_known:false model subtotals across every
// ticket's Totals into per-model ticket/turn counts, sorted by model name
// for stable output. Always returns a non-nil (possibly empty) slice so
// the JSON field is `[]`, never `null`, when every row is priced.
func unpricedModels(rows []cost.Totals) []unpricedModel {
	type acc struct{ tickets, turns int }
	byModel := map[string]*acc{}
	var order []string
	for _, tot := range rows {
		for _, m := range tot.Models {
			if m.CostKnown {
				continue
			}
			a, ok := byModel[m.Model]
			if !ok {
				a = &acc{}
				byModel[m.Model] = a
				order = append(order, m.Model)
			}
			a.tickets++
			a.turns += m.Turns
		}
	}
	sort.Strings(order)
	out := make([]unpricedModel, 0, len(order))
	for _, model := range order {
		a := byModel[model]
		out = append(out, unpricedModel{Model: model, Tickets: a.tickets, Turns: a.turns})
	}
	return out
}

// printUnpricedFooter prints one "⚠ unpriced:" line per model with no
// pricing table entry — the loud-unknowns footer for `gv cost` and
// `gv cost --analyze` human output (grove-249). Prints nothing when every
// row is priced.
func printUnpricedFooter(models []unpricedModel) {
	for _, m := range models {
		s := ""
		if m.Tickets != 1 {
			s = "s"
		}
		fmt.Printf("⚠ unpriced: %s — %d ticket%s, %s turns (add cost.pricing.%s in config.yaml)\n",
			m.Model, m.Tickets, s, commaInt(m.Turns), m.Model)
	}
}

// commaInt renders an integer with thousands separators: 9812 -> "9,812".
func commaInt(n int) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
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

	rowTots := make([]cost.Totals, len(rep.Rows))
	for i, r := range rep.Rows {
		rowTots[i] = r.Cost
	}
	rep.UnpricedModels = unpricedModels(rowTots)

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
	printUnpricedFooter(rep.UnpricedModels)
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
	// --op-id <v> (grove-186) rides relayed hops and is recognized in
	// leading-flag position only, like --host: free text may legitimately
	// mention it. The receipt check runs BEFORE anything else a retry
	// would repeat — no tmux send, no event, no text prompt.
	opID, args := remote.ExtractOpIDPrefix(args)
	if len(args) < 1 {
		return fmt.Errorf("usage: gv %s <ticket> [text]", verb)
	}
	t, err := findTask(args[0])
	if err != nil {
		// grove-242: a --host typed after the ticket never reached the
		// dispatcher (ExtractHostPrefix stops at the first non-flag arg —
		// the ticket), so the verb ran locally and the ticket miss reads
		// as breakage. Inspect the payload for the swallowed flag and
		// teach the position rule on the way out; the parse itself must
		// not change, free text may legitimately contain --host.
		if strings.Contains(err.Error(), "no active task") {
			if hint := remote.PostTicketHostHint(args[1:]); hint != "" {
				return fmt.Errorf("%w %s", err, hint)
			}
		}
		return err
	}
	if opID != "" {
		seen, err := state.SeenOpID(stateDir(), opID)
		if err != nil {
			return fmt.Errorf("cannot check op %s against the event log: %w", opID, err)
		}
		if seen {
			fmt.Printf("✓ already applied (op %s)\n", opID)
			return nil
		}
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
	if err := relayText(t, text, opID); err != nil {
		return err
	}
	fmt.Printf("✓ sent to %s\n", t.Ticket)
	return nil
}

// relayText is the shared send path of answer/nudge (and handoff's
// checkpoint nudge): paste into the claude pane, then record EvAnswered
// only once the submit verifiably landed. opID (grove-186) is "" for
// local sends — the event is byte-identical to pre-186 — and the relayed
// hop's id otherwise, stamped as data.op_id for the receipt check.
func relayText(t *state.Task, text, opID string) error {
	// Resolve the claude pane by id — usually .1, but a window that lost
	// its split runs claude in its only pane, and a name-built target can
	// resolve to a prefix-extending sibling's window ("repo · grove-1" vs
	// "repo · grove-10"), steering the wrong agent (grove-116).
	pane, err := tmux.ClaudePaneTarget(t.TmuxSession, t.TmuxWindow)
	if err != nil {
		return fmt.Errorf("%s has no live worker window: %w", t.Ticket, err)
	}
	// Single character → raw key without Enter (option pickers / plan
	// approval), which skips both the compact guard and the consumption
	// scrape: a picker keystroke has no input box to watch and no turn to
	// start. Anything longer → bracketed paste + Enter.
	var warn string
	if len([]rune(text)) == 1 {
		err = tmux.SendRawKey(pane, text)
	} else {
		warn, err = tmux.PasteText(pane, text)
	}
	if err != nil {
		// PasteText verifies the submit landed (grove-144) and refuses to
		// send into a mid-compact pane (grove-186), so a failure here means
		// the agent never got the text — recording EvAnswered anyway is
		// what made this bug silent: gv ls showed `working` on a dead worker.
		return err
	}
	// A verified submit with no sign of uptake still records its event —
	// but the operator hears about it, on stderr so ✓/--json stay clean.
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	var data map[string]string
	if opID != "" {
		data = map[string]string{"op_id": opID}
	}
	return state.Append(stateDir(), state.Event{Type: state.EvAnswered, Ticket: t.Ticket, Data: data})
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
	if id == "" && ambient.ws == nil {
		// grove-191: at the global layer, a ticket any registered
		// workspace has ever tracked routes there — adopt revives done
		// tasks, so the scan matches any state, not just active. Cold
		// adopts (nothing anywhere) keep resolving below.
		list, _ := workspace.LoadRegistry()
		if owners := workspace.FindTicket(list, positionals[0], true); len(owners) > 0 {
			if len(owners) == 1 {
				if err := routeIntoWorkspace(&owners[0]); err != nil {
					return fmt.Errorf("route into workspace %s: %w", owners[0].Label, err)
				}
				return fmt.Errorf("workspace route returned") // unreachable: routeIntoWorkspace exits
			}
			return ambiguousError(owners, positionals[0])
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
		// The dirty guard can't see committed-but-unpushed work; refuse
		// unless HEAD is an ancestor of origin so the reset drops nothing.
		if ahead, aerr := git.AheadCommits(wtPath, "origin/"+branch); aerr != nil {
			return fmt.Errorf("--sync: comparing with origin/%s: %w", branch, aerr)
		} else if len(ahead) > 0 {
			return fmt.Errorf("%s: worktree %s has %d local commit(s) not on origin/%s:\n  %s\n--sync refuses to discard them — push the branch (or reset it by hand), then retry",
				id, wtPath, len(ahead), branch, strings.Join(ahead, "\n  "))
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
	rows := doctor.Run(cfg, cfgErr, orchestratorDirFor(ambient.ws, cfg))
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
	// review I-3) — read-only scan, never acts across workspaces. The
	// workspace layer never routes (that is the global layer's job), so
	// inside a workspace this stays a hint.
	list, _ := workspace.LoadRegistry()
	if ambient.ws != nil {
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
	// grove-191: at the global layer the miss re-routes into the owning
	// workspace (the re-exec prints `→ workspace <label>` and exits with
	// the workspace-layer result); ambiguity is an honest error.
	if owners := workspace.FindTicket(list, idOrURL, false); len(owners) > 0 {
		if len(owners) == 1 {
			if err := routeIntoWorkspace(&owners[0]); err != nil {
				return nil, fmt.Errorf("route into workspace %s: %w", owners[0].Label, err)
			}
			return nil, fmt.Errorf("workspace route returned") // unreachable: routeIntoWorkspace exits
		}
		return nil, ambiguousError(owners, idOrURL)
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
