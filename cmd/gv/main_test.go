package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/workspace"
)

// TestOrchestratorLaunchProfileIsFresh is the grove-43 regression check: a
// profiled orchestrator spawn ()/orchestrator new --profile) must start a
// CLEAN conversation. The old orchestratorCmdProfile carried a `--continue`
// limb, so every profiled spawn resumed the previous chat instead.
func TestOrchestratorLaunchProfileIsFresh(t *testing.T) {
	cfg := &config.Config{}
	cfg.Orchestrator.Claude = "claude --dangerously-skip-permissions"
	p := &config.ModelProfile{
		BaseURL: "https://openrouter.ai/api", AuthTokenEnv: "OPENROUTER_API_KEY",
		Opus: "z-ai/glm-5.2", Sonnet: "z-ai/glm-5.2", Haiku: "z-ai/glm-4.5-air",
	}

	got, id, err := mintedOrchestratorLaunch(orchestratorLaunch(cfg, ""), p)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(got, "--continue") {
		t.Errorf("profiled spawn must start fresh, found --continue: %s", got)
	}
	if !strings.Contains(got, "ANTHROPIC_BASE_URL='https://openrouter.ai/api'") {
		t.Errorf("launch missing the profile env wrap: %s", got)
	}
	if !strings.Contains(got, "exec claude --dangerously-skip-permissions") {
		t.Errorf("launch does not exec the orchestrator command: %s", got)
	}
	// grove-222: the pane's identity is minted here, and the flag must land
	// INSIDE the wrapper's exec — after the `)` it is the shell's argument,
	// not claude's, and the chat spawns with no id at all.
	if id == "" || !strings.Contains(got, "--session-id "+id+" )") {
		t.Errorf("the minted --session-id %q must be inside the profile wrapper's exec: %s", id, got)
	}
}

// TestOrchestratorLaunchProfileNilIsUnchanged pins that a nil profile
// produces today's exact unprofiled fresh launch, unwrapped — the same
// command spawnOrchestrator uses, plus the minted id it is stamped with.
func TestOrchestratorLaunchProfileNilIsUnchanged(t *testing.T) {
	cfg := &config.Config{}
	cfg.Orchestrator.Claude = "claude --dangerously-skip-permissions"
	got, id, err := mintedOrchestratorLaunch(orchestratorLaunch(cfg, ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := orchestratorLaunch(cfg, "") + " --session-id " + id
	if got != want {
		t.Errorf("unprofiled minted launch = %q, want %q", got, want)
	}
}

// TestRequireAmbientWorkspace is grove-78's containment gate: grab derives
// the worker's tmux session from the workspace, so any mismatch between
// where gv runs and where the repo lives must fail closed — never fall
// back to the legacy global session or a sibling workspace's. The one
// exception (grove-191): a legacy-run repo that belongs to a workspace no
// longer refuses — the gate hands back the owning workspace as a route
// target and grab re-execs inside it.
func TestRequireAmbientWorkspace(t *testing.T) {
	wsA := &workspace.Workspace{Root: "/w/a", Label: "a"}
	wsA2 := &workspace.Workspace{Root: "/w/a", Label: "a"}
	wsB := &workspace.Workspace{Root: "/w/b", Label: "b"}
	cases := []struct {
		name       string
		ambient    *workspace.Workspace
		repo       *workspace.Workspace
		route      string // label of the workspace to re-exec into ("" = none)
		errMention string // guidance the error must carry
	}{
		{"true legacy: no workspaces anywhere", nil, nil, "", ""},
		{"repo inside the ambient workspace", wsA, wsA2, "", ""},
		{"repo outside any workspace (the gv-remarkable escape)", wsA, nil, "", "outside the a workspace"},
		{"repo under a sibling workspace", wsA, wsB, "", "belongs to workspace b"},
		{"legacy run on a workspaced repo routes (grove-191)", nil, wsB, "b", ""},
	}
	for _, tc := range cases {
		route, err := requireAmbientWorkspace(tc.ambient, tc.repo, "r", "/w/x/r")
		if tc.errMention == "" && err != nil {
			t.Errorf("%s: got error %v, want nil", tc.name, err)
		}
		if tc.errMention != "" {
			if err == nil {
				t.Errorf("%s: got nil, want fail-closed error", tc.name)
			} else if !strings.Contains(err.Error(), tc.errMention) {
				t.Errorf("%s: error %q missing guidance %q", tc.name, err, tc.errMention)
			}
		}
		got := ""
		if route != nil {
			got = route.Label
		}
		if got != tc.route {
			t.Errorf("%s: route = %q, want %q", tc.name, got, tc.route)
		}
	}
}

// TestCmdRelayOpIDDedups (grove-186) pins the receiver's receipt check: a
// relayed answer/nudge whose op id is already in the log must print
// "already applied", append NOTHING, and return before relayText — the
// tmux send path — so a re-delivered hop can never paste twice. (The e2e
// fake-ssh retry in e2e/handoff.sh proves the full loop, paste included.)
func TestCmdRelayOpIDDedups(t *testing.T) {
	dir := t.TempDir()
	oldDir := ambient.stateDir
	ambient.stateDir = dir
	t.Cleanup(func() { ambient.stateDir = oldDir })

	// The tmux coordinates are deliberately unresolvable: if the dedup
	// regresses and cmdRelay reaches relayText, the test dies on the
	// "no live worker window" error instead of touching any real server.
	if err := state.Append(dir, state.Event{Type: state.EvTaskCreated, Ticket: "task-1",
		Data: map[string]string{"tmux_session": "grove-186-no-such-session", "tmux_window": "w"}}); err != nil {
		t.Fatal(err)
	}
	if err := state.Append(dir, state.Event{Type: state.EvAnswered, Ticket: "task-1",
		Data: map[string]string{"op_id": "abc123"}}); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := cmdRelay([]string{"--op-id", "abc123", "task-1", "keep going"}, false); err != nil {
			t.Errorf("deduped relay returned an error: %v", err)
		}
	})
	if !strings.Contains(out, "✓ already applied (op abc123)") {
		t.Errorf("output %q missing the already-applied receipt", out)
	}
	evs, err := state.ReadEvents(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Errorf("a deduped relay must append nothing: %d events in the log, want the 2 seeded ones", len(evs))
	}
}

// TestCmdRelayPostTicketHostHint (grove-242) pins the wiring: a relay
// verb whose --host landed after the ticket runs LOCALLY and dies on the
// ticket miss — that error must carry the position rule when the payload
// still has a --host token, and stay byte-identical to the old miss when
// it does not. (The helper itself is unit-tested in internal/remote.)
func TestCmdRelayPostTicketHostHint(t *testing.T) {
	dir := t.TempDir()
	oldDir := ambient.stateDir
	oldWS := ambient.ws
	ambient.stateDir = dir
	ambient.ws = nil // findTask takes the legacy global path, like a bare checkout
	t.Cleanup(func() { ambient.stateDir = oldDir; ambient.ws = oldWS })

	// A live local task so the miss is a genuine miss, not an empty log —
	// the field evidence had other tasks tracked; the relayed one lives on
	// the remote host only.
	if err := state.Append(dir, state.Event{Type: state.EvTaskCreated, Ticket: "task-1"}); err != nil {
		t.Fatal(err)
	}

	err := cmdRelay([]string{"gv-242-missing", "--host", "vps", "rebase please"}, false)
	if err == nil {
		t.Fatal("relay of an untracked ticket returned nil, want the no-active-task error")
	}
	if !strings.Contains(err.Error(), "no active task gv-242-missing") {
		t.Errorf("error %q missing the ticket miss itself", err)
	}
	if !strings.Contains(err.Error(), "BEFORE the ticket") {
		t.Errorf("error %q missing the post-ticket --host hint", err)
	}

	err = cmdRelay([]string{"gv-242-missing", "rebase please"}, false)
	if err == nil {
		t.Fatal("relay of an untracked ticket returned nil, want the no-active-task error")
	}
	if strings.Contains(err.Error(), "BEFORE the ticket") {
		t.Errorf("error %q grew the hint without a --host in the payload", err)
	}
}

// captureStdout swaps os.Stdout for a pipe around f and returns what f
// printed (cmdRelay's receipts go straight to stdout).
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	f()
	os.Stdout = old
	w.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
