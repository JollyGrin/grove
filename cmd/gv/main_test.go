package main

import (
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
)

// TestOrchestratorCmdProfileWrapsBothLimbs is the grove-36 T4 acceptance
// check (design §3.1 / S6): a --continue failure must retry on the SAME
// profile backend, never fall through to a bare, unwrapped relaunch on the
// operator's own Claude sub.
func TestOrchestratorCmdProfileWrapsBothLimbs(t *testing.T) {
	cfg := &config.Config{}
	cfg.Orchestrator.Claude = "claude --dangerously-skip-permissions"
	p := &config.ModelProfile{
		BaseURL: "https://openrouter.ai/api", AuthTokenEnv: "OPENROUTER_API_KEY",
		Opus: "z-ai/glm-5.2", Sonnet: "z-ai/glm-5.2", Haiku: "z-ai/glm-4.5-air",
	}

	got := orchestratorCmdProfile(cfg, "", p)

	parts := strings.Split(got, " || ")
	if len(parts) != 2 {
		t.Fatalf("expected exactly one `||` split into two limbs, got %d: %s", len(parts), got)
	}
	for i, limb := range parts {
		if !strings.Contains(limb, "ANTHROPIC_BASE_URL='https://openrouter.ai/api'") {
			t.Errorf("limb %d missing the profile env wrap: %s", i, limb)
		}
		if !strings.Contains(limb, "exec claude --dangerously-skip-permissions") {
			t.Errorf("limb %d does not exec the orchestrator command: %s", i, limb)
		}
	}
	if !strings.Contains(parts[0], "--continue 2>/dev/null") {
		t.Errorf("first limb should attempt --continue: %s", parts[0])
	}
	if strings.Contains(parts[1], "--continue") {
		t.Errorf("second (fresh) limb must not carry --continue: %s", parts[1])
	}
}

// TestOrchestratorCmdProfileNilIsUnchanged pins that a nil profile produces
// today's exact orchestratorCmd, unwrapped.
func TestOrchestratorCmdProfileNilIsUnchanged(t *testing.T) {
	cfg := &config.Config{}
	cfg.Orchestrator.Claude = "claude --dangerously-skip-permissions"
	got := orchestratorCmdProfile(cfg, "", nil)
	want := orchestratorCmd(cfg, "")
	if got != want {
		t.Errorf("orchestratorCmdProfile(nil) = %q, want %q", got, want)
	}
}
