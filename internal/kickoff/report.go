package kickoff

import (
	"sort"

	"github.com/JollyGrin/grove/internal/config"
)

// States a repo's kickoff template can be in (grove-568).
const (
	StateDefault  = "default"  // the embedded template, untouched
	StateOverlay  = "overlay"  // embedded template + the repo's prompt_overlay fragment
	StateOverride = "override" // prompt: replaces the template wholesale
)

// OverrideNote is the note an override row carries: a whole-file template
// is frozen at the day it was copied and misses every later embedded move.
const OverrideNote = "whole-file override: misses every embedded template move; migrate to prompt_overlay"

// Row is one configured repo's kickoff state — an element of the additive
// `kickoffs` array in `gv brains --json` (docs/plugins.md).
type Row struct {
	Repo  string `json:"repo"`
	State string `json:"state"`
	Path  string `json:"path"` // the file that applies; "" for default
	Note  string `json:"note"`
}

// State classifies a repo: prompt: wins over prompt_overlay:.
func State(r *config.Repo) string {
	switch {
	case r.Prompt != "":
		return StateOverride
	case r.PromptOverlay != "":
		return StateOverlay
	}
	return StateDefault
}

// Report is one row per configured repo, sorted by name.
func Report(repos map[string]*config.Repo) []Row {
	rows := make([]Row, 0, len(repos))
	for name, r := range repos {
		if r == nil {
			continue
		}
		row := Row{Repo: name, State: State(r)}
		switch row.State {
		case StateOverride:
			row.Path, row.Note = r.Prompt, OverrideNote
			if r.PromptOverlay != "" {
				row.Note += "; prompt_overlay is ignored while prompt is set"
			}
		case StateOverlay:
			row.Path = r.PromptOverlay
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Repo < rows[j].Repo })
	return rows
}

// Overrides counts the repos replacing the kickoff template wholesale.
func Overrides(rows []Row) int {
	n := 0
	for _, r := range rows {
		if r.State == StateOverride {
			n++
		}
	}
	return n
}
