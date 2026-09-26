package config

// grove-293: pinning an orchestrator chat to a Claude model, and saying
// honestly which model a spawn will run before it runs.

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultOrchestratorModels is the built-in tier list — Claude Code's own
// aliases, which a model profile maps onto its slugs through the
// ANTHROPIC_DEFAULT_*_MODEL vars WrapProfile exports.
var DefaultOrchestratorModels = []string{"opus", "sonnet", "haiku"}

// AccountDefault is what RunsModel says when nothing grove can read names
// the model: the Claude account's own default. A literal, never a guess.
const AccountDefault = "account default"

// OrchestratorModels is the tier list a new chat can be pinned to, in
// config order: orchestrator.models when set, else the built-in default.
func (c *Config) OrchestratorModels() []string {
	var out []string
	for _, m := range c.Orchestrator.Models {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return append([]string(nil), DefaultOrchestratorModels...)
	}
	return out
}

// CheckOrchestratorModel refuses a model outside OrchestratorModels, the
// way ResolveProfile refuses an unknown profile: a typo must fail before a
// chat exists, never spawn on the host default. "" is always fine — it is
// the host default.
func (c *Config) CheckOrchestratorModel(model string) error {
	if model == "" {
		return nil
	}
	models := c.OrchestratorModels()
	for _, m := range models {
		if m == model {
			return nil
		}
	}
	return fmt.Errorf("unknown model %q (configured: %s — add it to orchestrator.models to allow it)", model, strings.Join(models, ", "))
}

// modelFlagAny matches a --model flag in any spelling a hand-written
// orchestrator.claude might use: `--model x`, `--model=x`, single- or
// double-quoted. modelFlagValue (WithModel's own quoted form) is narrower
// on purpose — it reads only what grove wrote.
var modelFlagAny = regexp.MustCompile(`(?:^|\s)--model(?:=|\s+)('[^']*'|"[^"]*"|[^\s'"]+)`)

// ModelFlag returns the --model value a command carries, unquoted, or ""
// when it carries none. The last one wins, as it does for claude's parser.
func ModelFlag(cmd string) string {
	all := modelFlagAny.FindAllStringSubmatch(cmd, -1)
	if len(all) == 0 {
		return ""
	}
	return strings.Trim(all[len(all)-1][1], `'"`)
}

// PinModel sets a command's model to exactly model: any --model already in
// it (a hand-written one in orchestrator.claude) is removed first, then
// WithModel injects the quoted flag. Without the strip, the operator's own
// flag would come AFTER the injected one and silently win. "" returns cmd
// unchanged — the host default.
func PinModel(cmd, model string) string {
	if strings.TrimSpace(model) == "" {
		return cmd
	}
	stripped := modelFlagAny.ReplaceAllString(cmd, "")
	return WithModel(stripped, model)
}

// RunsModel names the model a launch will actually run, for a picker row
// and a pane tag. launch is the bare (un-wrapped) orchestrator launch with
// any pin already applied; p the profile it will be wrapped in (nil = the
// host's own Claude); settingsModel the `model` key of the Claude config
// dir's settings.json ("" when absent or unreadable).
//
// Precedence is claude's own: an explicit --model, then settings.json, then
// the account default. On a profile, claude's --model beats the
// ANTHROPIC_MODEL WrapProfile exports, and a tier ALIAS resolves through the
// ANTHROPIC_DEFAULT_*_MODEL slugs the wrap also exports — so a flag of any
// spelling naming a tier is that tier's slug, no flag is the wrap's own
// ANTHROPIC_MODEL (modelSlot, the same function WrapProfile uses), and a
// full model id is sent to the backend as written.
func RunsModel(launch string, p *ModelProfile, settingsModel string) string {
	if p != nil {
		switch flag := ModelFlag(launch); strings.ToLower(flag) {
		case "":
			return p.slugFor(modelSlot(launch))
		case "opus", "sonnet", "haiku":
			return p.slugFor(strings.ToLower(flag))
		default:
			return flag
		}
	}
	if m := ModelFlag(launch); m != "" {
		return m
	}
	if settingsModel != "" {
		return settingsModel
	}
	return AccountDefault
}
