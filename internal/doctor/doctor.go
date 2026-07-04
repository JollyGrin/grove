// Package doctor renders the connections manifest (internal/connections).
// It owns presentation only — marks, fix lines, the grid-pack heading, the
// exit-code policy; every check lives in the manifest. Exists because of a
// real incident: the ccwork profile had zero grid plugins installed, which
// would have spawned conventionless workers (LEARNINGS.md 2026-06-10).
package doctor

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/connections"
)

// Row is one evaluated connection, flattened for rendering and --json.
type Row struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Step     string `json:"step,omitempty"`
	Kind     string `json:"kind"`
	Pack     string `json:"pack,omitempty"`
	Severity string `json:"severity"`
	State    string `json:"state"`
	Info     string `json:"info,omitempty"`
	Fix      string `json:"fix,omitempty"`
}

// Run evaluates the full manifest against the real machine.
func Run(cfg *config.Config, cfgErr error) []Row {
	return FromResults(connections.EvaluateAll(connections.NewEnv(cfg, cfgErr)))
}

// FromResults flattens manifest results into rows — the seam tests use to
// inject a fake connections.Env.
func FromResults(results []connections.Result) []Row {
	rows := make([]Row, 0, len(results))
	for _, r := range results {
		rows = append(rows, Row{
			ID:       r.Connection.ID,
			Title:    r.Connection.Title,
			Step:     r.Connection.Step,
			Kind:     r.Connection.Kind,
			Pack:     r.Connection.Pack,
			Severity: string(r.Connection.Severity),
			State:    string(r.Status.State),
			Info:     r.Status.Info,
			Fix:      r.Connection.Fix,
		})
	}
	return rows
}

// Errors counts rows that block: error-severity connections not ok.
// Warnings never count — `gv doctor` exits 0 on a warnings-only board
// (flutter taxonomy; deliberate change from the P0 doctor).
func Errors(rows []Row) int {
	n := 0
	for _, r := range rows {
		if r.Severity == string(connections.SeverityError) && r.State != string(connections.StateOK) {
			n++
		}
	}
	return n
}

const (
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiDim    = "\033[2m"
	ansiReset  = "\033[0m"
)

// Render prints the board: ✓/!/✗ per row, an indented fix line per
// failure, grid-interim rows under their own heading, then "N/M passed"
// and the 🌳 terminal state when nothing blocks.
func Render(w io.Writer, rows []Row) {
	passed := 0
	inGrid := false
	for _, r := range rows {
		if r.Pack == connections.PackGridInterim && !inGrid {
			inGrid = true
			fmt.Fprintf(w, "\n %s── grid pack (interim) ──%s\n", ansiDim, ansiReset)
		}
		var mark string
		switch r.State {
		case string(connections.StateOK):
			mark = ansiGreen + "✓" + ansiReset
			passed++
		case string(connections.StateWarn):
			mark = ansiYellow + "!" + ansiReset
		default:
			mark = ansiRed + "✗" + ansiReset
		}
		line := fmt.Sprintf(" %s %s", mark, r.Title)
		if r.Info != "" {
			line += "  (" + r.Info + ")"
		}
		fmt.Fprintln(w, line)
		if r.State != string(connections.StateOK) && r.Fix != "" {
			fmt.Fprintf(w, "   → %s\n", r.Fix)
		}
	}
	fmt.Fprintf(w, "\n %d/%d passed\n", passed, len(rows))
	if Errors(rows) == 0 {
		fmt.Fprintln(w, " 🌳 ready to grow")
	}
}

// RenderJSON emits the rows for the orchestrator (`gv doctor --json`).
func RenderJSON(w io.Writer, rows []Row) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}
