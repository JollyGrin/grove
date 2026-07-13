package main

import (
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
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

	got := orchestratorLaunchProfile(cfg, "", p)

	if strings.Contains(got, "--continue") {
		t.Errorf("profiled spawn must start fresh, found --continue: %s", got)
	}
	if !strings.Contains(got, "ANTHROPIC_BASE_URL='https://openrouter.ai/api'") {
		t.Errorf("launch missing the profile env wrap: %s", got)
	}
	if !strings.Contains(got, "exec claude --dangerously-skip-permissions") {
		t.Errorf("launch does not exec the orchestrator command: %s", got)
	}
}

// TestOrchestratorLaunchProfileNilIsUnchanged pins that a nil profile
// produces today's exact unprofiled fresh launch, unwrapped — the same
// command spawnOrchestrator uses.
func TestOrchestratorLaunchProfileNilIsUnchanged(t *testing.T) {
	cfg := &config.Config{}
	cfg.Orchestrator.Claude = "claude --dangerously-skip-permissions"
	got := orchestratorLaunchProfile(cfg, "", nil)
	want := orchestratorLaunch(cfg, "")
	if got != want {
		t.Errorf("orchestratorLaunchProfile(nil) = %q, want %q", got, want)
	}
}

// TestRequireAmbientWorkspace is grove-78's containment gate: grab derives
// the worker's tmux session from the workspace, so any mismatch between
// where gv runs and where the repo lives must fail closed — never fall
// back to the legacy global session or a sibling workspace's.
func TestRequireAmbientWorkspace(t *testing.T) {
	wsA := &workspace.Workspace{Root: "/w/a", Label: "a"}
	wsA2 := &workspace.Workspace{Root: "/w/a", Label: "a"}
	wsB := &workspace.Workspace{Root: "/w/b", Label: "b"}
	cases := []struct {
		name       string
		ambient    *workspace.Workspace
		repo       *workspace.Workspace
		ok         bool
		errMention string // guidance the error must carry
	}{
		{"true legacy: no workspaces anywhere", nil, nil, true, ""},
		{"repo inside the ambient workspace", wsA, wsA2, true, ""},
		{"repo outside any workspace (the gv-remarkable escape)", wsA, nil, false, "outside the a workspace"},
		{"repo under a sibling workspace", wsA, wsB, false, "belongs to workspace b"},
		{"legacy run on a workspaced repo", nil, wsB, false, "belongs to workspace b"},
	}
	for _, tc := range cases {
		err := requireAmbientWorkspace(tc.ambient, tc.repo, "r", "/w/x/r")
		if tc.ok && err != nil {
			t.Errorf("%s: got error %v, want nil", tc.name, err)
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("%s: got nil, want fail-closed error", tc.name)
			} else if !strings.Contains(err.Error(), tc.errMention) {
				t.Errorf("%s: error %q missing guidance %q", tc.name, err, tc.errMention)
			}
		}
	}
}
