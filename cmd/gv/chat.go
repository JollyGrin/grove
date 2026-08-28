package main

// grove-198: `gv orchestrator new --host <host>` — start an orchestrator
// chat ON a remote grove host, inside that host's twin of the workspace
// the caller is standing in.
//
// Two halves, one binary:
//
//   - the LOCAL half (runRemoteOrchestratorNew) mints an op id, fills the
//     workspace label in from the ambient workspace, and relays the spawn
//     over ssh through the same idempotent hop the relay verbs use — a
//     retry after ssh 255 can never double-spawn, because the remote
//     checks its op-id receipt before creating anything.
//   - the RECEIVING half (spawnWorkspaceChat, reached as `gv orchestrator
//     new --workspace <label>`) resolves the label against ITS OWN
//     registry and spawns a detached `grove-chat-<label>-<n>` tmux
//     session in that twin's orchestrator dir, on that host's own
//     orchestrator config, claude binary and auth.
//
// Only names travel: the workspace label and the profile name. Nothing
// falls back to the global layer — a host whose twin is missing errors
// out, because the global layer is a different brain with a different
// orchestrator command (the 2026-07-05 ccwork-inheritance incident).

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/remote"
	"github.com/JollyGrin/grove/internal/resource"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/workspace"
)

// chatHopArgs builds the receiving half's argv (after the `orchestrator`
// verb) for a relayed spawn. Deterministic order so a retry is byte-equal
// to the hop it repeats — that argv equality is half of what makes the
// op-id receipt trustworthy.
func chatHopArgs(opID, host, label, profile string) []string {
	args := []string{"new", "--op-id", opID, "--as", host, "--workspace", label}
	if profile != "" {
		args = append(args, "--profile", profile)
	}
	return args
}

// chatManualRetry renders the by-hand retry command printed when both ssh
// attempts died — safe to paste, because the op id makes a duplicate a
// no-op.
func chatManualRetry(opID, host, label, profile string) string {
	cmd := "gv orchestrator new --host " + remote.Quote(host) + " --op-id " + opID + " --workspace " + remote.Quote(label)
	if profile != "" {
		cmd += " --profile " + remote.Quote(profile)
	}
	return cmd
}

// runRemoteOrchestratorNew is the local half. Returns the remote's exit
// code (the caller exits with it), so a missing twin or an unknown profile
// on the host stays a hard, non-zero failure here.
func runRemoteOrchestratorNew(host string, args []string) (int, error) {
	fs := flag.NewFlagSet("orchestrator new --host", flag.ExitOnError)
	profile := fs.String("profile", "", "open the remote chat on one of the HOST's model profiles")
	label := fs.String("workspace", "", "target this workspace label on the host (default: the ambient workspace's)")
	opID := fs.String("op-id", "", "reuse a client op id (makes a by-hand retry a no-op)")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if *label == "" {
		*label = wsLabel()
	}
	if *label == "" {
		return 0, fmt.Errorf("`gv orchestrator new --host %s` needs a workspace label — run it from inside a workspace, or pass --workspace <label>", host)
	}
	if *opID == "" {
		*opID = remote.NewOpID()
	}
	cfg, err := loadCfg()
	if err != nil {
		return 0, err
	}
	h, err := cfg.Host(host)
	if err != nil {
		return 0, err
	}
	// Tee the remote's stdout: it prints its own attach line (the human
	// form for someone already logged into the host), and the session
	// number is picked over there, so parsing it back is the only way this
	// side can render the ssh form.
	var buf bytes.Buffer
	code, err := runRemoteIdempotent(cfg, host, "orchestrator",
		chatHopArgs(*opID, host, *label, *profile), *opID,
		chatManualRetry(*opID, host, *label, *profile),
		io.MultiWriter(os.Stdout, &buf))
	if err != nil || code != 0 {
		return code, err
	}
	if session := remote.ParseChatSession(buf.String()); session != "" {
		fmt.Printf("  from here: ssh -t %s tmux attach -t =%s\n", h.SSH, session)
	}
	return code, nil
}

// chatPlan is everything the spawn needs, decided before anything is
// created: which session name, which cwd, which command.
type chatPlan struct {
	Session string // grove-chat-<label>-<n>
	OrchDir string // the twin's brain dir (CLAUDE.md lives here)
	Dir     string // the chat pane's cwd: OrchDir, or OrchDir/<profile>
	Cmd     string // the orchestrator launch command
	Profile string // resolved profile name ("" = the host's own Claude)
}

// chatSpawnPlan resolves the profile against the TWIN's config (its claude
// command, its auth, its model profiles) and picks the next free chat
// session name. Pure: it creates nothing, so an unknown profile fails
// before a dir or a session exists. The per-profile cwd convention is
// spawnOrchestratorProfile's (grove-36 T4): Claude Code keys --continue by
// cwd, so each backend gets its own continuity under the brain dir.
func chatSpawnPlan(cfg *config.Config, ws *workspace.Workspace, profile string, sessions []string) (chatPlan, error) {
	name, p, err := cfg.ResolveProfile(profile, nil)
	if err != nil {
		return chatPlan{}, err
	}
	orchDir := orchestratorDirFor(ws, cfg)
	plan := chatPlan{
		Session: tmux.NextChatSession(ws.Label, sessions),
		OrchDir: orchDir,
		Dir:     orchDir,
		Cmd:     orchestratorLaunch(cfg, ws.Root),
		Profile: name,
	}
	if p != nil {
		plan.Dir = filepath.Join(orchDir, name)
		plan.Cmd = orchestratorLaunchProfile(cfg, ws.Root, p)
	}
	return plan, nil
}

// spawnWorkspaceChat is the receiving half: a detached orchestrator chat
// in the named registered workspace, in its own tmux session. Its own
// session rather than a window in the workspace's cockpit, deliberately —
// an ssh client attaching to a chat must not resize the cockpit's shared
// windows for whoever else is watching, and the chat has to outlive the
// ssh connection that started it.
//
// host is the alias the caller knows this machine by (relayed spawns), so
// the hard errors read from the caller's end.
func spawnWorkspaceChat(label, profile, opID, host string) error {
	if err := workspace.ValidateLabel(label); err != nil {
		return err
	}
	list, err := workspace.LoadRegistry()
	if err != nil {
		return err
	}
	ws, err := workspace.ResolveTwin(list, label, host)
	if err != nil {
		return err
	}
	// The twin's own state dir: the receipt is written where the spawn
	// happened, and checked there — never against the global layer this
	// ssh login happens to sit in.
	twinState := config.StateDirAt(ws.Root)
	// Receipt first, before anything is created (grove-186's rule): a
	// retried hop after an ambiguous ssh failure must reprint the first
	// run's answer, not spawn a second chat.
	if opID != "" {
		prior, err := state.EventByOpID(twinState, opID)
		if err != nil {
			return fmt.Errorf("cannot check op %s against the event log: %w", opID, err)
		}
		if prior != nil {
			// The id alone is NOT the receipt. --op-id is operator-facing
			// (the double-255 error prints a retry command carrying one)
			// and every relayed mutation shares this one log, so an id
			// that landed on an `answered` event would otherwise fire this
			// branch with an empty session: a bare attach line, exit 0,
			// and no chat. A same-id event of another kind is refused, not
			// believed — proceeding instead would file a SECOND event
			// under that id, and the next retry would dedup against
			// whichever came first.
			if prior.Type != state.EvOrchestratorSpawned {
				return fmt.Errorf("op %s is already recorded in workspace %s on a %q event — that id belongs to a different operation, so nothing was spawned; re-run without --op-id", opID, label, prior.Type)
			}
			session := prior.Data["session"]
			if session == "" {
				return fmt.Errorf("op %s is recorded in workspace %s but its spawn event names no session — nothing was spawned; re-run without --op-id", opID, label)
			}
			fmt.Printf("✓ already applied (op %s) — orchestrator chat %s in workspace %s\n", opID, session, label)
			fmt.Println(remote.ChatAttachLine(session))
			return nil
		}
	}
	cfg, err := config.LoadAt(ws.Root)
	if err != nil {
		return err
	}
	plan, err := chatSpawnPlan(cfg, ws, profile, tmux.SessionNames())
	if err != nil {
		return err
	}
	if err := seedOrchestratorDir(plan.OrchDir); err != nil {
		return err
	}
	if err := os.MkdirAll(plan.Dir, 0o755); err != nil {
		return err
	}
	// Breadcrumb before the spawn — a chat is a claude process like any
	// other and can trip the same memory cliff as a grab (grove-3).
	if mem, err := resource.Read(); err == nil {
		_ = resource.Log(twinState, resource.Sample{
			Avail: mem.AvailBytes, Total: mem.TotalBytes,
			Workers: resource.LiveWorkers(), Kind: resource.KindOrchestrator,
		})
	}
	if err := tmux.CreateChatSession(plan.Session, plan.Dir, plan.Cmd); err != nil {
		return err
	}
	data := map[string]string{"workspace": label, "session": plan.Session}
	if opID != "" {
		data["op_id"] = opID
	}
	if plan.Profile != "" {
		data["profile"] = plan.Profile
	}
	// The receipt lands after the session exists: a spawn that died mid-way
	// leaves no id, so a retry tries again instead of claiming success.
	if err := state.Append(twinState, state.Event{Type: state.EvOrchestratorSpawned, Data: data}); err != nil {
		return err
	}
	fmt.Printf("✓ orchestrator chat %s — workspace %s%s\n", plan.Session, label, chatProfileSuffix(plan.Profile))
	fmt.Println(remote.ChatAttachLine(plan.Session))
	return nil
}

// chatProfileSuffix names the backend in the success line, or says nothing
// for the host's own Claude.
func chatProfileSuffix(profile string) string {
	if profile == "" {
		return ""
	}
	return ", profile " + profile
}
