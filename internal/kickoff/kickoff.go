// Package kickoff renders the worker's initial prompt from ticket fields.
// Templates stay thin and defer to the grid skills the worker auto-loads
// (wrapping-up-task, dev-linear) — see DESIGN.md.
package kickoff

import (
	_ "embed"
	"os"
	"strings"
	"text/template"

	"github.com/JollyGrin/grove/internal/linear"
)

//go:embed default.tmpl
var defaultTmpl string

//go:embed manual.tmpl
var manualTmpl string

//go:embed pickup.tmpl
var pickupTmpl string

// Mode selects which prompt a session boots with.
type Mode int

const (
	ModeDefault Mode = iota // autonomous kickoff (grab)
	ModeManual              // ticket context only, wait for instructions
	ModePickup              // continue prior work on an existing branch (adopt)
)

// Render produces the kickoff prompt. templatePath overrides the embedded
// default — for ModeDefault only: manual and pickup are lifecycle-specific
// prompts, not repo-specific ones.
func Render(issue *linear.Issue, templatePath string, mode Mode) (string, error) {
	var text string
	switch mode {
	case ModeManual:
		text = manualTmpl
	case ModePickup:
		text = pickupTmpl
	default:
		text = defaultTmpl
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
	if err := t.Execute(&b, issue); err != nil {
		return "", err
	}
	return b.String(), nil
}
