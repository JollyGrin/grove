package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestOrchestratorModelsDefaultAndOverride(t *testing.T) {
	var c Config
	if got := c.OrchestratorModels(); !reflect.DeepEqual(got, []string{"opus", "sonnet", "haiku"}) {
		t.Fatalf("default = %v", got)
	}
	// The returned slice is a copy: a caller appending to it must not grow
	// the package default.
	_ = append(c.OrchestratorModels(), "x")
	if len(DefaultOrchestratorModels) != 3 {
		t.Fatal("OrchestratorModels leaked the package default")
	}
	c.Orchestrator.Models = []string{" claude-opus-4-5 ", "", "haiku"}
	if got := c.OrchestratorModels(); !reflect.DeepEqual(got, []string{"claude-opus-4-5", "haiku"}) {
		t.Fatalf("override = %v", got)
	}
}

func TestCheckOrchestratorModel(t *testing.T) {
	var c Config
	for _, ok := range []string{"", "opus", "sonnet", "haiku"} {
		if err := c.CheckOrchestratorModel(ok); err != nil {
			t.Fatalf("%q refused: %v", ok, err)
		}
	}
	err := c.CheckOrchestratorModel("opsu")
	if err == nil || !strings.Contains(err.Error(), `unknown model "opsu"`) || !strings.Contains(err.Error(), "opus, sonnet, haiku") {
		t.Fatalf("typo err = %v", err)
	}
}

func TestModelFlag(t *testing.T) {
	cases := map[string]string{
		"claude --dangerously-skip-permissions":                              "",
		"claude --model opus --x":                                            "opus",
		"claude --model 'claude-opus-4-5'":                                   "claude-opus-4-5",
		`claude --model "haiku"`:                                             "haiku",
		"claude --model=sonnet":                                              "sonnet",
		"claude --model 'opus' --dangerously-skip-permissions --model haiku": "haiku",
		"claude --models-dir x":                                              "",
	}
	for cmd, want := range cases {
		if got := ModelFlag(cmd); got != want {
			t.Errorf("ModelFlag(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestPinModelReplacesAHandWrittenFlag(t *testing.T) {
	base := "claude --model sonnet --dangerously-skip-permissions"
	got := PinModel(base, "opus")
	if got != "claude --model 'opus' --dangerously-skip-permissions" {
		t.Fatalf("PinModel = %q", got)
	}
	if ModelFlag(got) != "opus" {
		t.Fatalf("pinned command reads back %q", ModelFlag(got))
	}
	if PinModel(base, "") != base {
		t.Fatal("an empty pin must leave the command byte-identical")
	}
}

// TestPinModelStripsRepoHardcodedModel is grove-142's exact repro: a repo's
// `claude:` line hardcodes its own --model (e.g. unbrewed-p2p's `claude
// --dangerously-skip-permissions --model opus`). Before PinModel existed,
// grab/adopt ran this through the bare WithModel, which left the repo's
// --model in place; claude's own last-flag-wins parser then silently ran
// opus while gv reported the sonnet pin as applied.
func TestPinModelStripsRepoHardcodedModel(t *testing.T) {
	cases := []struct {
		name, cmd string
	}{
		{"space form", "claude --dangerously-skip-permissions --model opus"},
		{"equals form", "claude --dangerously-skip-permissions --model=opus"},
		{"quoted form", "claude --dangerously-skip-permissions --model 'opus'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PinModel(tc.cmd, "sonnet")
			if ModelFlag(got) != "sonnet" {
				t.Fatalf("PinModel(%q, sonnet) = %q, reads back %q, want sonnet", tc.cmd, got, ModelFlag(got))
			}
			if strings.Contains(got, "opus") {
				t.Fatalf("PinModel(%q, sonnet) = %q, the repo's opus flag survived", tc.cmd, got)
			}
		})
	}
}

func TestRunsModel(t *testing.T) {
	p := &ModelProfile{Opus: "glm-5", Sonnet: "glm-4.6", Haiku: "glm-air"}
	cases := []struct {
		name, launch, settings string
		p                      *ModelProfile
		want                   string
	}{
		{"flag wins over settings", "claude --model opus", "sonnet", nil, "opus"},
		{"settings when no flag", "claude --x", "claude-sonnet-4-5", nil, "claude-sonnet-4-5"},
		{"nothing readable", "claude --x", "", nil, AccountDefault},
		{"profile default tier", "claude --x", "opus", p, "glm-4.6"},
		{"profile pinned tier", PinModel("claude --x", "opus"), "", p, "glm-5"},
		{"profile haiku", PinModel("claude", "haiku"), "", p, "glm-air"},
		{"profile, hand-written alias", "claude --model opus --x", "", p, "glm-5"},
		{"profile, full id goes as written", PinModel("claude", "claude-opus-4-5"), "", p, "claude-opus-4-5"},
	}
	for _, c := range cases {
		if got := RunsModel(c.launch, c.p, c.settings); got != c.want {
			t.Errorf("%s: RunsModel = %q, want %q", c.name, got, c.want)
		}
	}
	// The label and the spawn cannot disagree: RunsModel's profile answer is
	// the ANTHROPIC_MODEL WrapProfile exports for the same launch.
	launch := PinModel("claude --x", "opus")
	if w := WrapProfile(launch, p, "/dev/null"); !strings.Contains(w, "ANTHROPIC_MODEL='glm-5'") {
		t.Fatalf("WrapProfile disagrees with RunsModel: %s", w)
	}
}

// TestTierForModel (grove-337): a transcript's concrete model id maps back
// to a tier only when it names exactly one.
func TestTierForModel(t *testing.T) {
	var c Config
	cases := map[string]string{
		"claude-haiku-4-5-20251001": "haiku",
		"claude-Opus-4-1":           "opus",
		"sonnet":                    "sonnet",
		"":                          "",
		"<synthetic>":               "",
		"z-ai/glm-4.5-air":          "",
		"opus-vs-sonnet":            "", // ambiguous
	}
	for id, want := range cases {
		if got := c.TierForModel(id); got != want {
			t.Errorf("TierForModel(%q) = %q, want %q", id, got, want)
		}
	}
	c.Orchestrator.Models = []string{"opus"}
	if got := c.TierForModel("claude-haiku-4-5"); got != "" {
		t.Errorf("unconfigured tier mapped to %q", got)
	}
}

// TestTierForModelFable (grove-442): a Fable id names the "fable" tier when
// the operator lists it, and with the default tier list answers "" (host
// default) rather than collapsing onto opus — a revive must never silently
// downgrade the lane.
func TestTierForModelFable(t *testing.T) {
	var c Config
	for _, id := range []string{"claude-fable-5-1", "claude-fable-5", "fable"} {
		if got := c.TierForModel(id); got != "" {
			t.Errorf("default tiers: TierForModel(%q) = %q, want \"\" (host default, not a downgrade)", id, got)
		}
	}
	c.Orchestrator.Models = []string{"fable", "opus", "sonnet", "haiku"}
	for _, id := range []string{"claude-fable-5-1", "claude-fable-5", "fable", "Claude-Fable-5-1"} {
		if got := c.TierForModel(id); got != "fable" {
			t.Errorf("fable configured: TierForModel(%q) = %q, want \"fable\"", id, got)
		}
	}
	if got := c.TierForModel("claude-opus-5-5"); got != "opus" {
		t.Errorf("TierForModel(claude-opus-5-5) = %q, want \"opus\"", got)
	}
}
