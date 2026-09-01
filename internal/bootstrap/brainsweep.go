// Fleet-wide brain sweep (grove-236). `gv update` swaps in a binary that
// carries a NEW orchestrator seed, and every workspace on the box is
// suddenly running an older brain — silently, because grove-190's refresh
// path is per-workspace, opt-in and manual. This is the read half that
// makes the drift visible: walk the registry, InspectBrain each root, and
// hand back one row per workspace saying what it has and what command
// brings it current.
//
// The sweep NEVER mutates. grove-190's invariant stands: grove does not
// overwrite a brain — the refresh writes CLAUDE.md.new and the human
// diffs. All this does is name the workspaces and the commands.
package bootstrap

import (
	"os"
	"path/filepath"
)

// BrainMissingRoot is a sweep-only state: the workspace is in the
// registry but its root is gone. Reported, never an error — a laptop with
// a stale registry entry must still get a sweep for the other ten.
const BrainMissingRoot BrainState = "missing-root"

// The two refresh commands the sweep hands out. Both are `gv init` steps,
// so both are run FROM the workspace root (init works on the cwd).
const (
	refreshCmd      = "gv init --only orchestrator-md"
	refreshForceCmd = refreshCmd + " --force-orchestrator-md"
)

// BrainProbe is one registered workspace as the sweep found it on disk.
// It is the sweep's entire I/O surface, which is what lets PlanSweep
// below be pure and table-testable.
type BrainProbe struct {
	Label    string
	Root     string
	RootGone bool   // the registered root is not a directory any more
	Exists   bool   // a CLAUDE.md is present under the root's orchestrator dir
	Content  string // that brain's bytes (empty when Exists is false)
}

// BrainRow is one workspace's line in the sweep report — also the
// `gv brains --json` row shape (plugin contract: additive only).
type BrainRow struct {
	Label   string `json:"label"`
	Root    string `json:"root"`
	State   string `json:"state"`   // current · stale · unstamped · absent · missing-root
	Have    string `json:"have"`    // the stamp on disk ("" when unstamped/absent)
	Want    string `json:"want"`    // the stamp of the seed THIS binary carries
	Command string `json:"command"` // what to run from Root ("" when nothing to do)
	Note    string `json:"note"`    // the one-line human reason
}

// NeedsAttention reports whether the row is something the operator has to
// act on — everything except an up-to-date brain.
func (r BrainRow) NeedsAttention() bool { return r.State != string(BrainCurrent) }

// OrchestratorDirAt is a workspace root's brain directory — what a
// cockpit opened at that root would use.
func OrchestratorDirAt(root string) string {
	return filepath.Join(root, ".grove", "orchestrator")
}

// ProbeBrain is the I/O for one workspace: does the root still exist, and
// what does its brain say. An unreadable brain reads as absent — the
// suggested refresh then fails loudly at the root rather than here, and
// the sweep's job is to keep walking.
func ProbeBrain(label, root string) BrainProbe {
	p := BrainProbe{Label: label, Root: root}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		p.RootGone = true
		return p
	}
	if b, err := os.ReadFile(filepath.Join(OrchestratorDirAt(root), BrainFile)); err == nil {
		p.Exists, p.Content = true, string(b)
	}
	return p
}

// PlanSweep is the whole sweep decision, pure: probes in, one row out per
// probe, in registry order. force is deliberately absent — a sweep is a
// report, and the force flag it prints is the human's to type.
func PlanSweep(probes []BrainProbe, seed string) []BrainRow {
	want := SeedStamp(seed)
	rows := make([]BrainRow, 0, len(probes))
	for _, p := range probes {
		row := BrainRow{Label: p.Label, Root: p.Root, Want: want}
		if p.RootGone {
			row.State = string(BrainMissingRoot)
			row.Note = "registered root is gone — `gv workspaces rm " + p.Label + "` if it left for good"
			rows = append(rows, row)
			continue
		}
		plan := PlanBrain(p.Content, p.Exists, seed, false)
		row.State, row.Have = string(plan.State), plan.Have
		switch plan.State {
		case BrainCurrent:
			row.Note = "up to date (seed " + want + ")"
		case BrainStale:
			row.Command = refreshCmd
			row.Note = "seed moved " + plan.Have + " → " + want
		case BrainUnstamped:
			row.Command = refreshForceCmd
			row.Note = "hand-managed, predates seed stamping"
		case BrainAbsent:
			row.Command = refreshCmd
			row.Note = "no orchestrator brain yet — this installs the seed"
		}
		rows = append(rows, row)
	}
	return rows
}
