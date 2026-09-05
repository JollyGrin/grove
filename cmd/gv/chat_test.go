package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/workspace"
)

// TestChatHopArgs pins the relayed argv (grove-198). Order is fixed on
// purpose: a retry must be byte-equal to the hop it repeats, and only
// NAMES travel — the host resolves the workspace label and the profile
// name against its own registry and config.
func TestChatHopArgs(t *testing.T) {
	req := chatSpawnReq{Label: "unbrewed", OpID: "deadbeef", Host: "groveremote"}
	got := chatHopArgs(req)
	want := []string{"new", "--op-id", "deadbeef", "--as", "groveremote", "--workspace", "unbrewed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chatHopArgs = %v, want %v", got, want)
	}
	profiled := req
	profiled.Profile = "openrouter-glm"
	got = chatHopArgs(profiled)
	if !reflect.DeepEqual(got, append(append([]string{}, want...), "--profile", "openrouter-glm")) {
		t.Fatalf("chatHopArgs(profile) = %v", got)
	}
	manual := chatManualRetry(profiled)
	if manual != "gv orchestrator new --host groveremote --op-id deadbeef --workspace unbrewed --profile openrouter-glm" {
		t.Fatalf("chatManualRetry = %q", manual)
	}
	// grove-217: the resumed id is one more NAME that travels — the host
	// resolves it against its own transcripts. Same fixed order, so a retry
	// is byte-equal to the hop it repeats.
	revive := req
	revive.Resume = "eeeb1a2b-3c4d"
	got = chatHopArgs(revive)
	if !reflect.DeepEqual(got, append(append([]string{}, want...), "--resume", "eeeb1a2b-3c4d")) {
		t.Fatalf("chatHopArgs(resume) = %v", got)
	}
	if manual := chatManualRetry(revive); manual != "gv orchestrator new --host groveremote --op-id deadbeef --workspace unbrewed --resume eeeb1a2b-3c4d" {
		t.Fatalf("chatManualRetry(resume) = %q", manual)
	}
}

// TestChatResumeConflict: --resume carries its own backend, so pairing it
// with --profile is a hard error rather than a precedence rule.
func TestChatResumeConflict(t *testing.T) {
	if err := chatResumeConflict("", ""); err != nil {
		t.Errorf("neither flag = no conflict, got %v", err)
	}
	if err := chatResumeConflict("glm", ""); err != nil {
		t.Errorf("--profile alone = no conflict, got %v", err)
	}
	if err := chatResumeConflict("", "aaaa1111"); err != nil {
		t.Errorf("--resume alone = no conflict, got %v", err)
	}
	err := chatResumeConflict("glm", "aaaa1111")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("both flags = %v, want a mutual-exclusion refusal", err)
	}
}

// TestChatSpawnPlan covers the decisions made before anything is created:
// profile-name validation against the TWIN's config, the per-profile cwd
// convention (grove-36 T4 — Claude Code keys --continue by cwd), and the
// session name.
func TestChatSpawnPlan(t *testing.T) {
	cfg := &config.Config{}
	cfg.Orchestrator.Claude = "claude --dangerously-skip-permissions"
	cfg.ModelProfiles = map[string]*config.ModelProfile{
		"openrouter-glm": {
			BaseURL: "https://openrouter.ai/api", AuthTokenEnv: "OPENROUTER_API_KEY",
			Opus: "z-ai/glm-5.2", Sonnet: "z-ai/glm-5.2", Haiku: "z-ai/glm-4.5-air",
		},
	}
	ws := &workspace.Workspace{Root: "/w/unbrewed", Label: "unbrewed", Scope: workspace.ScopeRepo}
	orchDir := filepath.Join("/w/unbrewed", ".grove", "orchestrator")

	plan, err := chatSpawnPlan(cfg, ws, "", "", "", nil)
	if err != nil {
		t.Fatalf("default plan: %v", err)
	}
	if plan.Session != "grove-chat-unbrewed-1" {
		t.Errorf("session = %q", plan.Session)
	}
	if plan.Dir != orchDir || plan.OrchDir != orchDir {
		t.Errorf("dirs = %q/%q, want the twin's brain dir %q", plan.OrchDir, plan.Dir, orchDir)
	}
	if plan.Profile != "" || !strings.HasPrefix(plan.Cmd, "claude --dangerously-skip-permissions") {
		t.Errorf("default plan cmd = %q (profile %q), want the twin's own claude", plan.Cmd, plan.Profile)
	}
	if !strings.Contains(plan.Cmd, "--add-dir '/w/unbrewed'") {
		t.Errorf("cmd must --add-dir the twin's root, got %q", plan.Cmd)
	}

	plan, err = chatSpawnPlan(cfg, ws, "openrouter-glm", "", "", []string{"grove-chat-unbrewed-1"})
	if err != nil {
		t.Fatalf("profiled plan: %v", err)
	}
	if plan.Session != "grove-chat-unbrewed-2" {
		t.Errorf("session = %q, want the next free one", plan.Session)
	}
	if plan.Profile != "openrouter-glm" || plan.Dir != filepath.Join(orchDir, "openrouter-glm") {
		t.Errorf("profiled plan = %+v, want its own cwd under the brain dir", plan)
	}
	if !strings.Contains(plan.Cmd, "ANTHROPIC_BASE_URL='https://openrouter.ai/api'") {
		t.Errorf("profiled cmd missing the backend wrap: %q", plan.Cmd)
	}
	if strings.Contains(plan.Cmd, "--continue") {
		t.Errorf("a fresh chat must not resume a previous conversation: %q", plan.Cmd)
	}

	// A profile the HOST doesn't have is a hard error — decided before any
	// dir or session exists.
	if _, err := chatSpawnPlan(cfg, ws, "nope", "", "", nil); err == nil || !strings.Contains(err.Error(), "unknown model profile") {
		t.Fatalf("unknown profile = %v, want an unknown-model-profile error", err)
	}
}

// TestChatSpawnPlanResume (grove-217): a revival's launch carries
// `--resume <id>`, and for a PROFILED conversation the flag must land
// INSIDE the backend wrapper — WrapProfile ends in `exec <cmd> )`, so a
// flag appended after the wrap would be handed to the shell, not claude.
func TestChatSpawnPlanResume(t *testing.T) {
	cfg := &config.Config{}
	cfg.Orchestrator.Claude = "claude --dangerously-skip-permissions"
	cfg.ModelProfiles = map[string]*config.ModelProfile{
		"openrouter-glm": {
			BaseURL: "https://openrouter.ai/api", AuthTokenEnv: "OPENROUTER_API_KEY",
			Opus: "z-ai/glm-5.2", Sonnet: "z-ai/glm-5.2", Haiku: "z-ai/glm-4.5-air",
		},
	}
	ws := &workspace.Workspace{Root: "/w/unbrewed", Label: "unbrewed", Scope: workspace.ScopeRepo}

	plan, err := chatSpawnPlan(cfg, ws, "", "aaaa1111", "", nil)
	if err != nil {
		t.Fatalf("resume plan: %v", err)
	}
	if plan.Resume != "aaaa1111" || !strings.HasSuffix(plan.Cmd, "--resume aaaa1111") {
		t.Errorf("resume cmd = %q (resume %q)", plan.Cmd, plan.Resume)
	}
	if strings.Contains(plan.Cmd, "--continue") {
		t.Errorf("a revival resumes one NAMED conversation, never --continue: %q", plan.Cmd)
	}

	plan, err = chatSpawnPlan(cfg, ws, "openrouter-glm", "bbbb2222", "", nil)
	if err != nil {
		t.Fatalf("profiled resume plan: %v", err)
	}
	if !strings.HasSuffix(plan.Cmd, "--resume bbbb2222 )") {
		t.Errorf("the flag must be inside the backend wrapper's exec: %q", plan.Cmd)
	}
	if plan.Dir != filepath.Join("/w/unbrewed", ".grove", "orchestrator", "openrouter-glm") {
		t.Errorf("a profiled conversation resumes in ITS cwd, got %q", plan.Dir)
	}

	// The id reaches a shell command line, so a malformed one never gets
	// past the plan — belt to internal/chat's braces.
	if _, err := chatSpawnPlan(cfg, ws, "", "a; rm -rf /", "", nil); err == nil {
		t.Error("a shell-hostile --resume id must be refused before anything is created")
	}
}

// mkTwin creates a registered workspace twin under home and returns its
// root — the fixture the receiving half resolves a label against.
func mkTwin(t *testing.T, home, label string) string {
	t.Helper()
	root := filepath.Join(home, label)
	if err := os.MkdirAll(filepath.Join(root, ".grove"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".grove", "config.yaml"),
		[]byte("workspace:\n  label: "+label+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := workspace.AddToRegistry(workspace.Workspace{
		Root: root, Label: label, Scope: workspace.ScopeRepo,
	}); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestSpawnWorkspaceChatRefusesMissingTwin: the receiving half never falls
// back to the host's global layer (wrong brain, wrong claude command —
// the 2026-07-05 ccwork-inheritance incident). No twin ⇒ error, and
// nothing created.
func TestSpawnWorkspaceChatRefusesMissingTwin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GROVE_STATE_DIR", "")
	err := spawnWorkspaceChat(chatSpawnReq{Label: "unbrewed", Host: "groveremote"})
	if err == nil || !strings.Contains(err.Error(), "no workspace 'unbrewed' on @groveremote") {
		t.Fatalf("missing twin = %v, want the hard refusal", err)
	}
}

// TestSpawnWorkspaceChatDedupsOpID is the retry gate (grove-198): a
// relayed hop whose op id is already in the twin's log must reprint the
// first run's answer — the same session name — and create nothing. The
// receipt check runs before any tmux call, so this exercises the real
// path without a tmux server.
func TestSpawnWorkspaceChatDedupsOpID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROVE_STATE_DIR", "")

	root := mkTwin(t, home, "unbrewed")
	// The first run's receipt, as spawnWorkspaceChat writes it.
	if err := state.Append(config.StateDirAt(root), state.Event{
		Type: state.EvOrchestratorSpawned,
		Data: map[string]string{"op_id": "op-1", "workspace": "unbrewed", "session": "grove-chat-unbrewed-1"},
	}); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := spawnWorkspaceChat(chatSpawnReq{Label: "unbrewed", OpID: "op-1", Host: "groveremote"}); err != nil {
			t.Fatalf("re-run with a seen op id: %v", err)
		}
	})
	if !strings.Contains(out, "already applied (op op-1)") {
		t.Errorf("re-run output = %q, want the already-applied receipt", out)
	}
	if !strings.Contains(out, "grove-chat-unbrewed-1") {
		t.Errorf("re-run must reprint the FIRST run's session: %q", out)
	}
	// Exactly one spawn event: the re-run appended nothing.
	events, err := state.ReadEvents(config.StateDirAt(root), 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range events {
		if ev.Type == state.EvOrchestratorSpawned {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("spawn events = %d, want exactly 1 (the re-run must not spawn)", n)
	}
}

// TestSpawnWorkspaceChatRejectsForeignOpID: --op-id is operator-facing and
// every relayed mutation shares one event log, so an id that landed on
// some OTHER kind of event is not a receipt for a chat. Believing it would
// print a bare attach line, exit 0, and spawn nothing — a silent success
// for a chat that never happened.
func TestSpawnWorkspaceChatRejectsForeignOpID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROVE_STATE_DIR", "")
	root := mkTwin(t, home, "unbrewed")

	// The same op id, but on a relayed answer (grove-186's event).
	if err := state.Append(config.StateDirAt(root), state.Event{
		Type: state.EvAnswered, Ticket: "grove-7",
		Data: map[string]string{"op_id": "op-1"},
	}); err != nil {
		t.Fatal(err)
	}

	var err error
	out := captureStdout(t, func() {
		err = spawnWorkspaceChat(chatSpawnReq{Label: "unbrewed", OpID: "op-1", Host: "groveremote"})
	})
	if err == nil {
		t.Fatalf("a foreign op id must be refused, got nil (stdout %q)", out)
	}
	for _, want := range []string{"op-1", "answered", "different operation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal = %q, want it to mention %q", err, want)
		}
	}
	if strings.Contains(out, "already applied") {
		t.Errorf("a foreign op id must not read as a receipt: %q", out)
	}
	if strings.Contains(out, "attach: tmux attach -t ''\n") {
		t.Errorf("a bare attach line was printed: %q", out)
	}
	// A spawn event with no session name is the same class of lie.
	if err := state.Append(config.StateDirAt(root), state.Event{
		Type: state.EvOrchestratorSpawned,
		Data: map[string]string{"op_id": "op-2", "workspace": "unbrewed"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := spawnWorkspaceChat(chatSpawnReq{Label: "unbrewed", OpID: "op-2", Host: "groveremote"}); err == nil ||
		!strings.Contains(err.Error(), "names no session") {
		t.Fatalf("sessionless receipt = %v, want a refusal", err)
	}
}

// --- grove-217: reviving an archived chat ---

// TestResumeTarget: which cwd a conversation gets revived in is the whole
// question — transcripts key on the encoded cwd, so `--resume` from the
// wrong dir is looking somewhere the id is not. Unknown ids, malformed
// ones and already-live ones are refused instead of launched hopefully.
func TestResumeTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROVE_STATE_DIR", "")
	t.Setenv("GV_CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	root := mkTwin(t, home, "unbrewed")
	orch := filepath.Join(root, ".grove", "orchestrator")
	glm := filepath.Join(orch, "openrouter-glm")
	if err := os.MkdirAll(glm, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, orch, "aaaa1111", "triage the artgen backlog", time.Now())
	writeTranscript(t, glm, "bbbb2222", "the cheap lane", time.Now())
	ws := &workspace.Workspace{Root: root, Label: "unbrewed", Scope: workspace.ScopeRepo}

	profile, s, err := resumeTarget(ws, "aaaa1111", nil)
	if err != nil || profile != "" || s.FirstPrompt != "triage the artgen backlog" {
		t.Fatalf("brain-dir chat = %q/%+v/%v, want the operator's own Claude", profile, s, err)
	}
	if profile, _, err := resumeTarget(ws, "bbbb2222", nil); err != nil || profile != "openrouter-glm" {
		t.Fatalf("profiled chat = %q/%v, want its own backend inferred from its cwd", profile, err)
	}

	_, _, err = resumeTarget(ws, "cccc3333", nil)
	if err == nil || !strings.Contains(err.Error(), "no chat cccc3333 in workspace unbrewed") {
		t.Fatalf("unknown id = %v, want a hard refusal naming the workspace", err)
	}
	if _, _, err := resumeTarget(ws, "a; rm -rf /", nil); err == nil ||
		!strings.Contains(err.Error(), "not a Claude session id") {
		t.Fatalf("shell-hostile id = %v, want a shape refusal", err)
	}
	// A conversation a live pane already holds: two claude processes on one
	// append-only transcript is not a revival, it is corruption.
	held := []tmux.LivePane{{Session: "grove-chat-unbrewed-2", ChatSession: "aaaa1111"}}
	if _, _, err := resumeTarget(ws, "aaaa1111", held); err == nil ||
		!strings.Contains(err.Error(), "already live in grove-chat-unbrewed-2") {
		t.Fatalf("live id = %v, want a refusal naming the holder", err)
	}
}

// An unknown id must die BEFORE anything is created — no session, no dir,
// no event. (The spawn would need a tmux server; this never reaches one.)
func TestSpawnWorkspaceChatRefusesUnknownResume(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROVE_STATE_DIR", "")
	t.Setenv("GV_CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	root := mkTwin(t, home, "unbrewed")

	err := spawnWorkspaceChat(chatSpawnReq{Label: "unbrewed", Resume: "nosuchid", Host: "groveremote"})
	if err == nil || !strings.Contains(err.Error(), "nothing to resume") {
		t.Fatalf("unknown --resume = %v, want a hard refusal", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".grove", "state", "events.jsonl")); err == nil {
		t.Error("a refused revival must leave no event behind")
	}
	// --profile is refused for a resume before the registry is even read.
	err = spawnWorkspaceChat(chatSpawnReq{Label: "unbrewed", Profile: "glm", Resume: "aaaa1111"})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("--resume with --profile = %v, want the refusal", err)
	}
}

// The success line names the revived conversation by id AND by first
// prompt: the id is what a client joins on, the prompt is what a human
// recognises.
func TestChatResumeSuffix(t *testing.T) {
	if got := chatResumeSuffix("", ""); got != "" {
		t.Errorf("a fresh chat says nothing about resuming, got %q", got)
	}
	if got, want := chatResumeSuffix("aaaa1111", "triage"), ", resumed aaaa1111 (triage)"; got != want {
		t.Errorf("chatResumeSuffix = %q, want %q", got, want)
	}
	if got, want := chatResumeSuffix("aaaa1111", ""), ", resumed aaaa1111"; got != want {
		t.Errorf("a labelless transcript = %q, want %q", got, want)
	}
}

// --- grove-199: the cockpit's remote spawn ---

// The local pane attaches over ssh to the session the HOST named, with the
// session exact-anchored (grove-99) so a longer host-side name can't be
// grabbed instead — and the anchored target quoted, because the pane's
// shell is zsh on the Mac this feature exists for (grove-207).
func TestRemoteChatAttachCmd(t *testing.T) {
	got := remoteChatAttachCmd("groveremote", "grove-chat-unbrewed-2")
	want := "ssh -t groveremote tmux attach -t '=grove-chat-unbrewed-2'"
	if got != want {
		t.Errorf("remoteChatAttachCmd = %q, want %q", got, want)
	}
	// A dial name with a shell metachar is quoted for the pane's shell.
	if got := remoteChatAttachCmd("box;rm -rf /", "grove-chat-x-1"); !strings.Contains(got, "'box;rm -rf /'") {
		t.Errorf("hostile ssh target not quoted: %q", got)
	}
}

// The flash the cockpit shows names the host, and the profile when there is
// one — an unprofiled remote chat says nothing extra, like its local twin.
func TestRemoteChatFlash(t *testing.T) {
	if got, want := remoteChatFlash("groveremote", ""), "✓ @groveremote chat pane"; got != want {
		t.Errorf("remoteChatFlash = %q, want %q", got, want)
	}
	if got, want := remoteChatFlash("groveremote", "glm"), "✓ @groveremote chat pane, profile glm"; got != want {
		t.Errorf("remoteChatFlash = %q, want %q", got, want)
	}
}

// A failed spawn flashes the REMOTE's own diagnosis: its stderr line wins,
// its stdout is the fallback, and a silent failure still says which host and
// what exit code — never a bare "failed".
func TestRemoteSpawnError(t *testing.T) {
	cases := []struct {
		name           string
		code           int
		stderr, stdout string
		want           string
	}{
		{"remote error line", 1, "gv: no workspace 'ws' on @pc — register a twin there\n", "",
			"@pc: no workspace 'ws' on @pc — register a twin there"},
		{"stdout fallback", 1, "  \n", "unknown model profile \"glm\"\n", "@pc: unknown model profile \"glm\""},
		{"silent failure", 255, "", "", "@pc: orchestrator new failed (exit 255)"},
	}
	for _, tc := range cases {
		if got := remoteSpawnError("pc", tc.code, tc.stderr, tc.stdout); got.Error() != tc.want {
			t.Errorf("%s: remoteSpawnError = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// cockpitSessionCheck answers the guard's one question from the REGISTRY,
// never from the session name's shape (grove-199): a workspace labelled
// `chat-app` owns the cockpit session `grove-chat-app`, which is also
// exactly the shape of a chat session. Registered ⇒ cockpit ⇒ its first
// pane is a dashboard the guard must keep protecting.
func TestCockpitSessionCheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mkTwin(t, home, "unbrewed")
	mkTwin(t, home, "chat-app")

	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		t.Fatalf("cockpitSessionCheck: %v", err)
	}
	cases := []struct {
		session string
		want    bool
		why     string
	}{
		{"grove", true, "the legacy global cockpit"},
		{"grove-mobile", true, "the phone cockpit's single pane is its dashboard"},
		{"grove-unbrewed", true, "a registered workspace's cockpit"},
		{"grove-chat-app", true, "the cockpit of the workspace labelled chat-app — NOT a chat session"},
		{"grove-chat-unbrewed-1", false, "a chat session: chat-unbrewed-1 is no registered label"},
		{"grove-chat-app-1", false, "chat 1 of the chat-app workspace"},
		{"grove-nosuch", false, "no such workspace is registered"},
		{"pr-unbrewed-p2p", false, "not a grove session at all"},
	}
	for _, tc := range cases {
		if got := isCockpit(tc.session); got != tc.want {
			t.Errorf("isCockpit(%q) = %v, want %v — %s", tc.session, got, tc.want, tc.why)
		}
	}
}

// grove-203: park must never leak a chat silently. Two halves — what the
// durable event records, and what the operator is told.
func TestParkedEventRecordsChats(t *testing.T) {
	if ev := parkedEvent(nil, false); ev.Data != nil {
		t.Errorf("a park with no chats must keep the pre-grove-203 record shape, got Data %v", ev.Data)
	}
	chats := []tmux.ChatSession{
		{Session: "grove-chat-unbrewed-1", PID: 201, Command: "claude"},
		{Session: "grove-chat-unbrewed-2", PID: 202, Command: "node"},
	}
	ev := parkedEvent(chats, false)
	if ev.Type != state.EvWorkspaceParked {
		t.Fatalf("event type = %q", ev.Type)
	}
	if got := ev.Data["chats"]; got != "grove-chat-unbrewed-1,grove-chat-unbrewed-2" {
		t.Errorf("chats = %q, want both session names", got)
	}
	if _, ok := ev.Data["chats_killed"]; ok {
		t.Errorf("a default park kills no chats — chats_killed must be absent: %v", ev.Data)
	}
	if got := parkedEvent(chats, true).Data["chats_killed"]; got != "true" {
		t.Errorf("chats_killed = %q, want true for gv park --chats", got)
	}
}

func TestParkChatLines(t *testing.T) {
	if got := parkChatLines(nil, false); got != nil {
		t.Errorf("no chats = no extra output, got %v", got)
	}
	chats := []tmux.ChatSession{{Session: "grove-chat-unbrewed-1", PID: 201, Command: "claude"}}

	left := strings.Join(parkChatLines(chats, false), "\n")
	for _, want := range []string{"grove-chat-unbrewed-1", "pid 201", "still running", "gv park --chats", "gv audit"} {
		if !strings.Contains(left, want) {
			t.Errorf("a leave-behind park must mention %q:\n%s", want, left)
		}
	}
	// The attach line is quoted (grove-207) — the operator pastes it into zsh.
	if !strings.Contains(left, "tmux attach -t '=grove-chat-unbrewed-1'") {
		t.Errorf("the survivor line must carry a paste-able attach hint:\n%s", left)
	}

	killed := strings.Join(parkChatLines(chats, true), "\n")
	if !strings.Contains(killed, "killed") {
		t.Errorf("--chats must say what it killed:\n%s", killed)
	}
	if strings.Contains(killed, "survive") {
		t.Errorf("--chats leaves nothing behind — no survivor line:\n%s", killed)
	}
}

// TestChatHopArgsBrief (grove-271): the standing brief travels as the LAST
// argument, after every other name, so a hop and its by-hand retry stay
// byte-equal — the op-id receipt is only trustworthy while argv equality
// holds. --brief-file never travels: the path is the caller's, and its
// text is what the host is given.
func TestChatHopArgsBrief(t *testing.T) {
	req := chatSpawnReq{Label: "unbrewed", OpID: "deadbeef", Host: "groveremote"}
	base := []string{"new", "--op-id", "deadbeef", "--as", "groveremote", "--workspace", "unbrewed"}
	if got := chatHopArgs(req); !reflect.DeepEqual(got, base) {
		t.Fatalf("no brief changed the argv: %v", got)
	}

	briefed := req
	briefed.Brief = "watch grove-1\nit's yours until the PR is up\n"
	want := append(append([]string{}, base...), "--brief", briefed.Brief)
	if got := chatHopArgs(briefed); !reflect.DeepEqual(got, want) {
		t.Fatalf("chatHopArgs(brief) = %v, want %v", got, want)
	}

	// With a profile too, the brief still goes last.
	both := briefed
	both.Profile = "openrouter-glm"
	want = append(append([]string{}, base...), "--profile", "openrouter-glm", "--brief", briefed.Brief)
	if got := chatHopArgs(both); !reflect.DeepEqual(got, want) {
		t.Fatalf("chatHopArgs(profile+brief) = %v, want the brief last", got)
	}

	// The manual retry is paste-able: a multi-line brief holding an
	// apostrophe survives remote.Quote's single-quoting intact.
	manual := chatManualRetry(briefed)
	const wantManual = `gv orchestrator new --host groveremote --op-id deadbeef --workspace unbrewed --brief 'watch grove-1
it'\''s yours until the PR is up
'`
	if manual != wantManual {
		t.Fatalf("chatManualRetry(brief) = %q, want %q", manual, wantManual)
	}
}

// TestChatBriefText: the two front doors resolve to one text, and every
// way of asking for an empty brief is refused rather than silently
// spawning a chat with nothing to do.
func TestChatBriefText(t *testing.T) {
	if got, err := chatBriefText("", false, ""); err != nil || got != "" {
		t.Errorf("no flags = %q, %v; want no brief", got, err)
	}
	if got, err := chatBriefText("watch grove-1", true, ""); err != nil || got != "watch grove-1" {
		t.Errorf("--brief = %q, %v", got, err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "brief.md")
	body := "watch grove-1\nnudge it if it's idle 20m\nthen ping me\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := chatBriefText("", false, path)
	if err != nil || got != body {
		t.Errorf("--brief-file = %q, %v; want the file's bytes verbatim", got, err)
	}

	if _, err := chatBriefText("watch grove-1", true, path); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("--brief + --brief-file = %v, want a mutual-exclusion refusal", err)
	}
	if _, err := chatBriefText("", true, ""); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("--brief '' = %v, want a refusal (never a silent no-op)", err)
	}
	if _, err := chatBriefText("   \n", true, ""); err == nil {
		t.Errorf("whitespace-only --brief must be refused too")
	}
	blank := filepath.Join(dir, "blank.md")
	if err := os.WriteFile(blank, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := chatBriefText("", false, blank); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("an empty --brief-file = %v, want a refusal", err)
	}
	if _, err := chatBriefText("", false, filepath.Join(dir, "nope.md")); err == nil {
		t.Errorf("a missing --brief-file must be a hard error")
	}
}

// TestChatBriefConflict: a revival already has a conversation, so a brief
// would be an unrelated turn dropped into the middle of one.
func TestChatBriefConflict(t *testing.T) {
	if err := chatBriefConflict("", ""); err != nil {
		t.Errorf("neither flag = no conflict, got %v", err)
	}
	if err := chatBriefConflict("watch grove-1", ""); err != nil {
		t.Errorf("--brief alone = no conflict, got %v", err)
	}
	if err := chatBriefConflict("", "aaaa1111"); err != nil {
		t.Errorf("--resume alone = no conflict, got %v", err)
	}
	err := chatBriefConflict("watch grove-1", "aaaa1111")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("both flags = %v, want a mutual-exclusion refusal", err)
	}
}

// TestChatSpawnPlanBrief (grove-271): the brief is handed over as a
// positional prompt read by the shell at launch — the worker kickoff's
// exact shape — and for a PROFILED chat it must land INSIDE the backend
// wrapper, because WrapProfile ends in `exec <cmd> )`.
func TestChatSpawnPlanBrief(t *testing.T) {
	cfg := &config.Config{}
	cfg.Orchestrator.Claude = "claude --dangerously-skip-permissions"
	cfg.ModelProfiles = map[string]*config.ModelProfile{
		"openrouter-glm": {
			BaseURL: "https://openrouter.ai/api", AuthTokenEnv: "OPENROUTER_API_KEY",
			Opus: "z-ai/glm-5.2", Sonnet: "z-ai/glm-5.2", Haiku: "z-ai/glm-4.5-air",
		},
	}
	ws := &workspace.Workspace{Root: "/w/unbrewed", Label: "unbrewed", Scope: workspace.ScopeRepo}
	orchDir := filepath.Join("/w/unbrewed", ".grove", "orchestrator")
	brief := "watch grove-1 and grove-2\n"

	plan, err := chatSpawnPlan(cfg, ws, "", "", brief, nil)
	if err != nil {
		t.Fatalf("briefed plan: %v", err)
	}
	wantPath := filepath.Join(orchDir, "briefs", plan.SessionID+".md")
	if plan.BriefPath != wantPath || plan.Brief != brief {
		t.Fatalf("plan brief = %q at %q, want %q at %q", plan.Brief, plan.BriefPath, brief, wantPath)
	}
	// chatSpawnPlan creates nothing: the caller writes the file.
	if _, err := os.Stat(plan.BriefPath); err == nil {
		t.Errorf("chatSpawnPlan must not create %s", plan.BriefPath)
	}
	if !strings.HasSuffix(plan.Cmd, ` "$(cat "`+wantPath+`")"`) {
		t.Fatalf("cmd = %q, want it to end in the kickoff-shaped prompt argv", plan.Cmd)
	}
	if !strings.Contains(plan.Cmd, "--session-id "+plan.SessionID+" \"$(cat") {
		t.Errorf("the brief must follow --session-id on the bare launch: %q", plan.Cmd)
	}

	// Profiled: the prompt argv sits inside the wrap, ahead of its closing
	// paren — outside it, the shell would swallow the prompt.
	plan, err = chatSpawnPlan(cfg, ws, "openrouter-glm", "", brief, nil)
	if err != nil {
		t.Fatalf("profiled briefed plan: %v", err)
	}
	if strings.HasSuffix(plan.Cmd, `)"`) || !strings.HasSuffix(plan.Cmd, ")") {
		t.Fatalf("profiled cmd must still end in the profile wrap's paren: %q", plan.Cmd)
	}
	// The brief still lives under the BRAIN dir, not the per-profile cwd.
	if !strings.Contains(plan.Cmd, `"$(cat "`+filepath.Join(orchDir, "briefs", plan.SessionID+".md")+`")"`) {
		t.Fatalf("profiled cmd lost the brief: %q", plan.Cmd)
	}

	// No brief, no argv, no path.
	plan, err = chatSpawnPlan(cfg, ws, "", "", "", nil)
	if err != nil {
		t.Fatalf("unbriefed plan: %v", err)
	}
	if plan.BriefPath != "" || strings.Contains(plan.Cmd, "$(cat") {
		t.Fatalf("an unbriefed spawn must be untouched: %+v", plan)
	}
}

// TestWriteChatBrief: the file is created with its dir, byte-for-byte.
func TestWriteChatBrief(t *testing.T) {
	orchDir := t.TempDir()
	body := "line one\nit's got an apostrophe\n\tand a tab\n"
	path := chatBriefPath(orchDir, "aaaa1111-2222-3333-4444-555555555555")
	if err := writeChatBrief(path, body); err != nil {
		t.Fatalf("writeChatBrief: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != body {
		t.Errorf("brief round-trip = %q, want %q", got, body)
	}
}
