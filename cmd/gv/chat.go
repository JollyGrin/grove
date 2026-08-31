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
// grove-217 adds the third name that travels: `--resume <session-id>`,
// which revives an ARCHIVED chat (a transcript with no live pane) instead
// of starting a fresh one — `gv adopt`'s pattern applied to chats. The
// receiving half resolves the id against ITS OWN transcript dirs, so an
// unknown id is a hard error before anything is created.
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
	"strings"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/remote"
	"github.com/JollyGrin/grove/internal/resource"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/transcript"
	"github.com/JollyGrin/grove/internal/workspace"
)

// chatSpawnReq is one orchestrator-chat spawn in the terms that travel:
// names only, never paths, ids of the host's own making, or anything
// resolved on this side. The same struct describes a local spawn, the
// relayed hop, and the by-hand retry, so the three can never drift.
type chatSpawnReq struct {
	Label   string // the REGISTERED workspace label, resolved on the far side
	Profile string // model profile name, "" = the host's own Claude
	Resume  string // grove-217: revive this Claude session id instead of starting fresh
	OpID    string // idempotency receipt for a relayed spawn
	Host    string // the alias the caller knows the SPAWNING machine by
}

// chatHopArgs builds the receiving half's argv (after the `orchestrator`
// verb) for a relayed spawn. Deterministic order so a retry is byte-equal
// to the hop it repeats — that argv equality is half of what makes the
// op-id receipt trustworthy.
func chatHopArgs(r chatSpawnReq) []string {
	args := []string{"new", "--op-id", r.OpID, "--as", r.Host, "--workspace", r.Label}
	if r.Profile != "" {
		args = append(args, "--profile", r.Profile)
	}
	if r.Resume != "" {
		args = append(args, "--resume", r.Resume)
	}
	return args
}

// chatManualRetry renders the by-hand retry command printed when both ssh
// attempts died — safe to paste, because the op id makes a duplicate a
// no-op.
func chatManualRetry(r chatSpawnReq) string {
	cmd := "gv orchestrator new --host " + remote.Quote(r.Host) + " --op-id " + r.OpID + " --workspace " + remote.Quote(r.Label)
	if r.Profile != "" {
		cmd += " --profile " + remote.Quote(r.Profile)
	}
	if r.Resume != "" {
		cmd += " --resume " + remote.Quote(r.Resume)
	}
	return cmd
}

// chatResumeConflict refuses `--resume` together with `--profile`. A
// resumed conversation CARRIES its own backend: it lives in the project
// dir of the cwd it ran in (the grove-36 T4 one-cwd-per-profile
// convention), and that dir is what `claude --resume` searches. Honouring
// a contradicting --profile would either look for the id in a dir that
// does not hold it, or resume a GLM conversation on the operator's Claude
// sub. So the flag pair is a hard error rather than a precedence rule
// nobody would remember.
func chatResumeConflict(profile, resume string) error {
	if profile == "" || resume == "" {
		return nil
	}
	return fmt.Errorf("--resume and --profile are mutually exclusive: a resumed conversation already carries its backend (the cwd it ran in decides it) — drop --profile")
}

// runRemoteOrchestratorNew is the local half. Returns the remote's exit
// code (the caller exits with it), so a missing twin or an unknown profile
// on the host stays a hard, non-zero failure here.
func runRemoteOrchestratorNew(host string, args []string) (int, error) {
	fs := flag.NewFlagSet("orchestrator new --host", flag.ExitOnError)
	profile := fs.String("profile", "", "open the remote chat on one of the HOST's model profiles")
	label := fs.String("workspace", "", "target this workspace label on the host (default: the ambient workspace's)")
	resume := fs.String("resume", "", "revive one of the HOST's archived chats by Claude session id")
	opID := fs.String("op-id", "", "reuse a client op id (makes a by-hand retry a no-op)")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if err := chatResumeConflict(*profile, *resume); err != nil {
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
	// The id is the HOST's to resolve — its transcripts live there — but a
	// malformed one is refused here, before an ssh round trip.
	if *resume != "" && !chat.ValidSessionID(*resume) {
		return 0, fmt.Errorf("--resume %q is not a Claude session id — run `gv chat ls --workspace %s` on %s to list them", *resume, *label, host)
	}
	req := chatSpawnReq{Label: *label, Profile: *profile, Resume: *resume, OpID: *opID, Host: host}
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
		chatHopArgs(req), *opID, chatManualRetry(req),
		io.MultiWriter(os.Stdout, &buf))
	if err != nil || code != 0 {
		return code, err
	}
	if session := remote.ParseChatSession(buf.String()); session != "" {
		fmt.Printf("  from here: %s\n", remoteChatAttachCmd(h.SSH, session))
	}
	return code, nil
}

// chatPlan is everything the spawn needs, decided before anything is
// created: which session name, which cwd, which command.
type chatPlan struct {
	Session   string // grove-chat-<label>-<n>
	OrchDir   string // the twin's brain dir (CLAUDE.md lives here)
	Dir       string // the chat pane's cwd: OrchDir, or OrchDir/<profile>
	Cmd       string // the orchestrator launch command
	Profile   string // resolved profile name ("" = the host's own Claude)
	Resume    string // the Claude session id being revived ("" = a fresh chat)
	SessionID string // grove-222: the id this chat WILL run on — minted here for a
	// fresh chat, the revived id for a --resume — so the pane can be stamped
	// at creation instead of guessed at later.
}

// chatSpawnPlan resolves the profile against the TWIN's config (its claude
// command, its auth, its model profiles) and picks the next free chat
// session name. Pure: it creates nothing, so an unknown profile fails
// before a dir or a session exists. The per-profile cwd convention is
// spawnOrchestratorProfile's (grove-36 T4): Claude Code keys --continue by
// cwd, so each backend gets its own continuity under the brain dir.
//
// resume (grove-217) is a shape-validated Claude session id whose caller
// has already resolved WHICH dir it lives in — the profile argument, for a
// revival, IS that answer. The flag is appended to the bare launch and the
// profile wrapper goes on LAST, because WrapProfile ends in `exec <cmd> )`:
// a flag appended after the wrap lands outside the subshell and is handed
// to the shell instead of to claude.
func chatSpawnPlan(cfg *config.Config, ws *workspace.Workspace, profile, resume string, sessions []string) (chatPlan, error) {
	name, p, err := cfg.ResolveProfile(profile, nil)
	if err != nil {
		return chatPlan{}, err
	}
	if resume != "" && !chat.ValidSessionID(resume) {
		return chatPlan{}, fmt.Errorf("--resume %q is not a Claude session id", resume)
	}
	orchDir := orchestratorDirFor(ws, cfg)
	launch := orchestratorLaunch(cfg, ws.Root)
	// grove-222: a FRESH chat gets its id minted here and handed to claude
	// (`--session-id <uuid>`), so the pane's identity is known before the
	// agent boots. A revival already has one — its own — and the two flags
	// are mutually exclusive by construction: --resume names the id.
	id := resume
	if resume == "" {
		minted, err := chat.NewSessionID()
		if err != nil {
			return chatPlan{}, err
		}
		id = minted
		launch += " --session-id " + minted
	} else {
		launch += " --resume " + resume
	}
	plan := chatPlan{
		Session:   tmux.NextChatSession(ws.Label, sessions),
		OrchDir:   orchDir,
		Dir:       orchDir,
		Cmd:       launch,
		Profile:   name,
		Resume:    resume,
		SessionID: id,
	}
	if p != nil {
		plan.Dir = filepath.Join(orchDir, name)
		plan.Cmd = wrapOrchestratorLaunch(launch, p)
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
// r.Host is the alias the caller knows this machine by (relayed spawns), so
// the hard errors read from the caller's end.
func spawnWorkspaceChat(r chatSpawnReq) error {
	label := r.Label
	if err := chatResumeConflict(r.Profile, r.Resume); err != nil {
		return err
	}
	if err := workspace.ValidateLabel(label); err != nil {
		return err
	}
	list, err := workspace.LoadRegistry()
	if err != nil {
		return err
	}
	ws, err := workspace.ResolveTwin(list, label, r.Host)
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
	if r.OpID != "" {
		prior, err := state.EventByOpID(twinState, r.OpID)
		if err != nil {
			return fmt.Errorf("cannot check op %s against the event log: %w", r.OpID, err)
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
				return fmt.Errorf("op %s is already recorded in workspace %s on a %q event — that id belongs to a different operation, so nothing was spawned; re-run without --op-id", r.OpID, label, prior.Type)
			}
			session := prior.Data["session"]
			if session == "" {
				return fmt.Errorf("op %s is recorded in workspace %s but its spawn event names no session — nothing was spawned; re-run without --op-id", r.OpID, label)
			}
			fmt.Printf("✓ already applied (op %s) — orchestrator chat %s in workspace %s\n", r.OpID, session, label)
			fmt.Println(remote.ChatAttachLine(session))
			return nil
		}
	}
	cfg, err := config.LoadAt(ws.Root)
	if err != nil {
		return err
	}
	// grove-217: a revival resolves its conversation FIRST — an unknown id,
	// a malformed one, or one a live pane is already holding is a hard
	// error here, before a dir, a session or an event exists.
	profile, revived := r.Profile, ""
	if r.Resume != "" {
		name, s, err := resumeTarget(ws, r.Resume, tmux.Panes())
		if err != nil {
			return err
		}
		profile, revived = name, s.FirstPrompt
	}
	plan, err := chatSpawnPlan(cfg, ws, profile, r.Resume, tmux.SessionNames())
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
	// The pane wears its identity from second zero (grove-222): the id was
	// either minted for this launch or carried by --resume, so nothing is
	// ever inferred from transcript mtime for a chat grove spawned.
	// Best-effort — a tmux too old for pane user options must not fail the
	// spawn, and the id is still recoverable from the running claude's argv.
	if plan.SessionID != "" {
		if err := tmux.StampChatSession(plan.Session, plan.SessionID); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not stamp %s with session id %s: %v\n", plan.Session, plan.SessionID, err)
		}
	}
	data := map[string]string{"workspace": label, "session": plan.Session}
	if r.OpID != "" {
		data["op_id"] = r.OpID
	}
	if plan.Profile != "" {
		data["profile"] = plan.Profile
	}
	if plan.Resume != "" {
		data["resume"] = plan.Resume
	}
	// The receipt lands after the session exists: a spawn that died mid-way
	// leaves no id, so a retry tries again instead of claiming success.
	if err := state.Append(twinState, state.Event{Type: state.EvOrchestratorSpawned, Data: data}); err != nil {
		return err
	}
	fmt.Printf("✓ orchestrator chat %s — workspace %s%s%s\n", plan.Session, label, chatProfileSuffix(plan.Profile), chatResumeSuffix(plan.Resume, revived))
	fmt.Println(remote.ChatAttachLine(plan.Session))
	if plan.Resume != "" {
		// Verified 2026-08-31: `claude --resume` re-fires SessionStart with
		// the SAME session id and opens IDLE — it does not replay or
		// auto-continue. Said out loud because a client that waits for
		// output before enabling its input box would wait forever.
		fmt.Println("  (resumed idle — it answers when you send something)")
	}
	return nil
}

// chatResumeSuffix names the revived conversation in the success line: the
// id (the thing a client joins on) and its first prompt (the thing a human
// recognises it by).
func chatResumeSuffix(id, label string) string {
	if id == "" {
		return ""
	}
	suffix := ", resumed " + id
	if label != "" {
		suffix += " (" + label + ")"
	}
	return suffix
}

// resumeTarget resolves a --resume id against THIS machine's copy of the
// workspace's chat history and answers the one question the spawn needs:
// which profile's cwd the conversation ran in. Transcripts key on the
// encoded cwd, so `claude --resume` launched from the wrong dir is looking
// in a project dir that does not hold the id — and the pane is detached,
// so whatever it says about that, nobody reads it. Hence: resolve the dir,
// or refuse; never launch hopefully.
// panes is injected (tmux.Panes() in production) so the whole resolution
// is testable without a tmux server.
func resumeTarget(ws *workspace.Workspace, id string, panes []tmux.LivePane) (profile string, s transcript.Session, err error) {
	if !chat.ValidSessionID(id) {
		return "", s, fmt.Errorf("--resume %q is not a Claude session id — `gv chat ls --workspace %s` lists them", id, ws.Label)
	}
	orchDir := orchestratorDirAt(ws.Root)
	var dirs []chat.ProjectDir
	for _, dir := range orchestratorProjectDirs(*ws) {
		sessions, err := transcript.ListSessions(dir)
		if err != nil {
			continue
		}
		dirs = append(dirs, chat.ProjectDir{Dir: dir, Sessions: sessions})
	}
	d, found, ok := chat.FindSession(dirs, id)
	if !ok {
		return "", s, fmt.Errorf("no chat %s in workspace %s — nothing under %s has that transcript, so there is nothing to resume (`gv chat ls --workspace %s` lists the ids this machine has)", id, ws.Label, orchDir, ws.Label)
	}
	if holder := chat.LiveHolder(panes, id); holder != "" {
		return "", s, fmt.Errorf("chat %s is already live in %s — attach to it (tmux attach -t %s) rather than reviving a second process on one transcript", id, holder, tmux.Exact(holder))
	}
	name, ok := chat.ProfileForDir(orchDir, d.Dir)
	if !ok {
		return "", s, fmt.Errorf("chat %s ran in %s, which is not %s nor one of its profile dirs — resume it by hand from that directory", id, d.Dir, orchDir)
	}
	return name, found, nil
}

// chatProfileSuffix names the backend in the success line, or says nothing
// for the host's own Claude.
func chatProfileSuffix(profile string) string {
	if profile == "" {
		return ""
	}
	return ", profile " + profile
}

// --- grove-199: the cockpit's `@`-armed remote spawn ---

// remoteChatAttachCmd is the local pane's command — attach, over ssh, to
// the chat session the host just spawned — and the same line the CLI
// prints for the operator to paste. The session name is exact-anchored
// (`=`, the grove-99 target rule) so a host-side session whose name merely
// extends this one can never be attached instead; the anchored target and
// the dial name are both quoted, because the shell running this line is
// zsh on the Mac, where a bare leading `=` is equals-expanded and the
// command dies before ssh runs (grove-207).
func remoteChatAttachCmd(sshTarget, session string) string {
	return "ssh -t " + remote.Quote(sshTarget) + " tmux attach -t " + remote.Quote(tmux.Exact(session))
}

// remoteChatFlash is the cockpit flash for a spawned remote chat.
func remoteChatFlash(host, profile string) string {
	return "✓ @" + host + " chat pane" + chatProfileSuffix(profile)
}

// remoteSpawnError renders the failure the cockpit flashes: the REMOTE's own
// first error line where there is one (a missing twin, an unknown profile,
// ssh's own 255), because that line is the whole diagnosis and the flash is
// one line wide.
func remoteSpawnError(host string, code int, stderr, stdout string) error {
	if line := firstNonEmptyLine(stderr); line != "" {
		return fmt.Errorf("@%s: %s", host, strings.TrimPrefix(line, "gv: "))
	}
	if line := firstNonEmptyLine(stdout); line != "" {
		return fmt.Errorf("@%s: %s", host, line)
	}
	return fmt.Errorf("@%s: orchestrator new failed (exit %d)", host, code)
}

// firstNonEmptyLine returns the first line with visible content, "" when the
// whole output is blank.
func firstNonEmptyLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// spawnRemoteChat is the cockpit's `@`-armed spawn (grove-199), injected into
// the TUI as SpawnRemoteOrchestrator. Two moves, in this order:
//
//  1. relay grove-198's verb — the chat is created ON the host, in its twin
//     of this workspace, and the host answers with the session name;
//  2. only then open a LOCAL pane running `ssh -t <host> tmux attach` on that
//     session, tiled into the cockpit window like any local chat.
//
// Order matters: a relay that fails (no twin, unknown profile, dead ssh)
// returns the remote's own error line and NOTHING is spawned here — a pane
// attached to a session that was never created is a dead pane the operator
// has to notice and close.
//
// Both ssh streams are captured, never streamed: this runs inside the tea
// loop, where a stray write to the terminal corrupts the alt-screen.
func spawnRemoteChat(cfg *config.Config, host, profile string) (string, error) {
	h, err := cfg.Host(host)
	if err != nil {
		return "", err
	}
	label := wsLabel()
	if label == "" {
		return "", fmt.Errorf("@%s: no ambient workspace — a remote chat spawns into the HOST's twin of this one, so run the cockpit from inside a workspace", host)
	}
	session := cockpitSessionFor(ambient.ws)
	if !tmux.SessionExists(session) {
		return "", fmt.Errorf("cockpit session %s is not running", session)
	}
	req := chatSpawnReq{Label: label, Profile: profile, OpID: remote.NewOpID(), Host: host}
	var out, errBuf bytes.Buffer
	code, err := runRemoteIdempotentWith(host, req.OpID,
		chatManualRetry(req), &errBuf,
		func() (int, error) {
			return remote.RunDetached(cfg, host, "orchestrator",
				chatHopArgs(req), &out, &errBuf)
		})
	if err != nil {
		return "", fmt.Errorf("@%s: %v", host, err)
	}
	if code != 0 {
		return "", remoteSpawnError(host, code, errBuf.String(), out.String())
	}
	remoteSession := remote.ParseChatSession(out.String())
	if remoteSession == "" {
		return "", fmt.Errorf("@%s: spawned, but the host printed no attach line — attach by hand over ssh", host)
	}
	// The pane's cwd is only the shell's: the agent lives on the host. The
	// workspace root is the one directory guaranteed to exist here.
	dir := ambient.ws.Root
	paneID, err := tmux.SpawnPane(session, dir, remoteChatAttachCmd(h.SSH, remoteSession))
	if err != nil {
		return "", err
	}
	// Pane identity (grove-199): remote panes wear "@<host> · <profile>" and a
	// distinct border color, so a glance separates the chats running here from
	// the ones running there. Cosmetic — a tmux too old for pane options never
	// fails the spawn.
	if err := tmux.SetPaneRemote(paneID, host); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not tag remote chat pane with host %q: %v\n", host, err)
	}
	if profile != "" {
		if err := tmux.SetPaneProfile(paneID, profile); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not tag remote chat pane with profile %q: %v\n", profile, err)
		}
	}
	if err := tmux.SetPaneRemoteBorder(paneID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not color the remote chat pane's border: %v\n", err)
	}
	if err := tmux.ShowPaneBorders(tmux.Exact(session) + ":cockpit"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not show cockpit pane borders: %v\n", err)
	}
	return remoteChatFlash(host, profile), nil
}
