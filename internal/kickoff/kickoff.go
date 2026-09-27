// Package kickoff renders the worker's initial prompt from task fields.
// Templates stay thin; transition instructions come from the active
// provider's Verbs (DESIGN.md §5.4) — the binary never transitions tasks.
// The linear template set defers to the grid skills the worker auto-loads
// (wrapping-up-task, dev-linear); the generic set assumes nothing beyond
// git, gh, and the STATUS sentinel contract (core, non-negotiable).
package kickoff

import (
	_ "embed"
	"os"
	"strings"
	"text/template"

	"github.com/JollyGrin/grove/internal/provider"
)

//go:embed default.tmpl
var defaultTmpl string

//go:embed manual.tmpl
var manualTmpl string

//go:embed pickup.tmpl
var pickupTmpl string

//go:embed md_default.tmpl
var mdDefaultTmpl string

//go:embed md_manual.tmpl
var mdManualTmpl string

//go:embed md_pickup.tmpl
var mdPickupTmpl string

// Mode selects which prompt a session boots with.
type Mode int

const (
	ModeDefault Mode = iota // autonomous kickoff (grab)
	ModeManual              // task context only, wait for instructions
	ModePickup              // continue prior work on an existing branch (adopt)
)

// data is the template input. It embeds the provider-neutral task and keeps
// Identifier as a legacy alias for ID so pre-existing repo prompt overrides
// ({{.Identifier}}) keep rendering. Base and Feature (grove-374, feature
// trains Decision 3) name the branch a PR targets and, when the task rides
// a feature train, the train it belongs to.
type data struct {
	*provider.Task
	Identifier string
	Verbs      provider.Verbs
	Base       string
	Feature    string
}

// Render produces the kickoff prompt. kind selects the template set
// ("linear" keeps the ovs-era templates; anything else gets the generic
// set). templatePath overrides the embedded default — for ModeDefault only:
// manual and pickup are lifecycle-specific prompts, not repo-specific ones.
// brief is ad-hoc operator text (grove-146); appended verbatim as a final
// "## Operator brief" section after all ticket-derived content, for any
// mode — empty means no section, keeping every existing caller byte-stable.
// base is the branch the PR targets (the caller resolves it: a feature's
// branch, else the repo's base — never empty); feature is the feature's
// slug, empty when the task rides no train (grove-374).
func Render(task *provider.Task, verbs provider.Verbs, kind, templatePath string, mode Mode, brief, base, feature string) (string, error) {
	linearSet := kind == "linear"
	var text string
	switch mode {
	case ModeManual:
		text = mdManualTmpl
		if linearSet {
			text = manualTmpl
		}
	case ModePickup:
		text = mdPickupTmpl
		if linearSet {
			text = pickupTmpl
		}
	default:
		text = mdDefaultTmpl
		if linearSet {
			text = defaultTmpl
		}
		if templatePath != "" {
			raw, err := os.ReadFile(templatePath)
			if err != nil {
				return "", err
			}
			text = string(raw)
		}
	}
	t, err := template.New("kickoff").Parse(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data{Task: task, Identifier: task.ID, Verbs: verbs, Base: base, Feature: feature}); err != nil {
		return "", err
	}
	if brief != "" {
		b.WriteString("\n\n## Operator brief\n\n")
		b.WriteString(brief)
		b.WriteString("\n")
	}
	return b.String(), nil
}
