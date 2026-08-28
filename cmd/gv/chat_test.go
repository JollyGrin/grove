package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/workspace"
)

// TestChatHopArgs pins the relayed argv (grove-198). Order is fixed on
// purpose: a retry must be byte-equal to the hop it repeats, and only
// NAMES travel — the host resolves the workspace label and the profile
// name against its own registry and config.
func TestChatHopArgs(t *testing.T) {
	got := chatHopArgs("deadbeef", "groveremote", "unbrewed", "")
	want := []string{"new", "--op-id", "deadbeef", "--as", "groveremote", "--workspace", "unbrewed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chatHopArgs = %v, want %v", got, want)
	}
	got = chatHopArgs("deadbeef", "groveremote", "unbrewed", "openrouter-glm")
	want = append(want, "--profile", "openrouter-glm")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chatHopArgs(profile) = %v, want %v", got, want)
	}
	manual := chatManualRetry("deadbeef", "groveremote", "unbrewed", "openrouter-glm")
	if manual != "gv orchestrator new --host groveremote --op-id deadbeef --workspace unbrewed --profile openrouter-glm" {
		t.Fatalf("chatManualRetry = %q", manual)
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

	plan, err := chatSpawnPlan(cfg, ws, "", nil)
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

	plan, err = chatSpawnPlan(cfg, ws, "openrouter-glm", []string{"grove-chat-unbrewed-1"})
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
	if _, err := chatSpawnPlan(cfg, ws, "nope", nil); err == nil || !strings.Contains(err.Error(), "unknown model profile") {
		t.Fatalf("unknown profile = %v, want an unknown-model-profile error", err)
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
	err := spawnWorkspaceChat("unbrewed", "", "", "groveremote")
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
		if err := spawnWorkspaceChat("unbrewed", "", "op-1", "groveremote"); err != nil {
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
		err = spawnWorkspaceChat("unbrewed", "", "op-1", "groveremote")
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
	if strings.Contains(out, "attach: tmux attach -t =\n") {
		t.Errorf("a bare attach line was printed: %q", out)
	}
	// A spawn event with no session name is the same class of lie.
	if err := state.Append(config.StateDirAt(root), state.Event{
		Type: state.EvOrchestratorSpawned,
		Data: map[string]string{"op_id": "op-2", "workspace": "unbrewed"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := spawnWorkspaceChat("unbrewed", "", "op-2", "groveremote"); err == nil ||
		!strings.Contains(err.Error(), "names no session") {
		t.Fatalf("sessionless receipt = %v, want a refusal", err)
	}
}
