package main

import (
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
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
