// Fleet-wide brain sweep (grove-236). `gv update` swaps in a binary that
// carries a NEW orchestrator seed, and every workspace on the box is
// suddenly running an older brain — silently, because grove-190's refresh
// path is per-workspace, opt-in and manual. This is the read half that
// makes the drift visible: walk the registry, InspectBrain each root, and
// hand back one row per workspace saying what it has and what command
// brings it current.
//
// The sweep itself NEVER mutates. The writing half is RefreshSweep
// (`gv brains --refresh`, grove-511), and it widens grove-190's invariant
// by exactly one case: a brain grove can PROVE it wrote unchanged — the
// stamp is the hash of the body — is grove's own file and is replaced in
// place. An edited brain still gets CLAUDE.md.new beside it for the human
// to diff; an unstamped brain is still only reported.
package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
)

// BrainMissingRoot is a sweep-only state: the workspace is in the
// registry but its root is gone. Reported, never an error — a laptop with
// a stale registry entry must still get a sweep for the other ten.
const BrainMissingRoot BrainState = "missing-root"

// GlobalBrainLabel is the row label of the brain used outside any
// workspace (`cfg.Orchestrator.Dir`). It cannot collide with a registry
// label: workspace labels never carry parentheses.
const GlobalBrainLabel = "(global)"

// The refresh commands the sweep hands out. The two `gv init` steps are
// run FROM the workspace root (init works on the cwd); `gv brains
// --refresh` is fleet-wide and runs from anywhere.
const (
	refreshCmd      = "gv init --only orchestrator-md"
	refreshForceCmd = refreshCmd + " --force-orchestrator-md"
	// RefreshSweepCmd is the one command that brings every pristine brain
	// (and the global one) current in place.
	RefreshSweepCmd = "gv brains --refresh"
)

// BrainProbe is one brain as the sweep found it on disk. It is the sweep's
// entire read surface, which is what lets PlanSweep below be pure and
// table-testable.
type BrainProbe struct {
	Label     string
	Root      string   // the registered workspace root (the dir itself for the global brain)
	Dir       string   // the orchestrator dir holding CLAUDE.md
	RootGone  bool     // the registered root is not a directory any more
	Exists    bool     // a CLAUDE.md is present under Dir
	Content   string   // that brain's bytes (empty when Exists is false)
	Leftovers []string // CLAUDE.md.new / CLAUDE.md.bak-* basenames beside the brain
}

// BrainRow is one brain's line in the sweep report — also the
// `gv brains --json` row shape (plugin contract: additive only).
type BrainRow struct {
	Label    string `json:"label"`
	Root     string `json:"root"`
	State    string `json:"state"`    // current · stale · unstamped · absent · missing-root
	Pristine bool   `json:"pristine"` // the brain is byte-for-byte a seed grove wrote (grove-511)
	Have     string `json:"have"`     // the stamp on disk ("" when unstamped/absent)
	Want     string `json:"want"`     // the stamp of the seed THIS binary carries
	Command  string `json:"command"`  // what to run ("" when nothing to do)
	Note     string `json:"note"`     // the one-line human reason
	// Action is what `--refresh` did about the row: seed · none · refreshed
	// · new · report. Empty under a plain (read-only) sweep.
	Action string `json:"action"`
	// Leftovers are the CLAUDE.md.new / CLAUDE.md.bak-* files beside the
	// brain — safe to delete once the operator is happy; grove never does.
	Leftovers []string `json:"leftovers"`
}

// NeedsAttention reports whether the row is something the operator has to
// act on — everything except an up-to-date brain, or one `--refresh` has
// just brought current.
func (r BrainRow) NeedsAttention() bool { return r.State != string(BrainCurrent) && !r.Acted() }

// Acted reports whether a refresh wrote the brain itself (seeded or
// replaced) — the rows a `--refresh` report shows as done.
func (r BrainRow) Acted() bool {
	return r.Action == string(ActionSeed) || r.Action == string(ActionRefreshed)
}

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
	p := BrainProbe{Label: label, Root: root, Dir: OrchestratorDirAt(root)}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		p.RootGone = true
		return p
	}
	probeDir(&p)
	return p
}

// ProbeGlobalBrain probes the brain used outside any workspace
// (`cfg.Orchestrator.Dir`). A dir that does not exist yet is the absent
// case, never missing-root — `--refresh` creates it when it seeds.
func ProbeGlobalBrain(dir string) BrainProbe {
	p := BrainProbe{Label: GlobalBrainLabel, Root: dir, Dir: dir}
	probeDir(&p)
	return p
}

func probeDir(p *BrainProbe) {
	if b, err := os.ReadFile(filepath.Join(p.Dir, BrainFile)); err == nil {
		p.Exists, p.Content = true, string(b)
	}
	p.Leftovers = []string{}
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		return
	}
	for _, e := range entries { // ReadDir sorts by name
		if isLeftover(e.Name()) {
			p.Leftovers = append(p.Leftovers, e.Name())
		}
	}
}

// isLeftover names the files earlier refreshes and hand merges leave
// beside a brain: the .new to diff and the .bak-* copies the merge kept.
func isLeftover(name string) bool {
	return name == BrainFile+".new" || strings.HasPrefix(name, BrainFile+".bak-")
}

// PlanSweep is the whole sweep decision, pure: probes in, one row out per
// probe, in registry order. force is deliberately absent — a sweep is a
// report, and the force flag it prints is the human's to type.
//
// A stale brain's command depends on whether grove may touch it: a
// pristine one (and the global brain, which no `gv init` step reaches)
// gets `gv brains --refresh`; an edited one keeps the `.new` path.
func PlanSweep(probes []BrainProbe, seed string) []BrainRow {
	want := SeedStamp(seed)
	rows := make([]BrainRow, 0, len(probes))
	for _, p := range probes {
		row := BrainRow{Label: p.Label, Root: p.Root, Want: want, Leftovers: p.Leftovers}
		if row.Leftovers == nil {
			row.Leftovers = []string{}
		}
		if p.RootGone {
			row.State = string(BrainMissingRoot)
			row.Note = "registered root is gone — `gv workspaces rm " + p.Label + "` if it left for good"
			rows = append(rows, row)
			continue
		}
		global := p.Label == GlobalBrainLabel
		plan := PlanBrain(p.Content, p.Exists, seed, false)
		row.State, row.Have, row.Pristine = string(plan.State), plan.Have, plan.Pristine
		switch plan.State {
		case BrainCurrent:
			row.Note = "up to date (seed " + want + ")"
		case BrainStale:
			switch {
			case plan.Pristine:
				row.Command = RefreshSweepCmd
				row.Note = "seed moved " + plan.Have + " → " + want + " — pristine, replaced in place by " + RefreshSweepCmd
			case global:
				row.Command = RefreshSweepCmd
				row.Note = "seed moved " + plan.Have + " → " + want + " — edited; " + RefreshSweepCmd + " writes " + BrainFile + ".new to diff"
			default:
				row.Command = refreshCmd
				row.Note = "seed moved " + plan.Have + " → " + want + " — edited, lands as " + BrainFile + ".new to diff"
			}
		case BrainUnstamped:
			if !global {
				row.Command = refreshForceCmd
			}
			row.Note = "hand-managed, predates seed stamping"
		case BrainAbsent:
			row.Command = refreshCmd
			if global {
				row.Command = RefreshSweepCmd
			}
			row.Note = "no orchestrator brain yet — this installs the seed"
		}
		rows = append(rows, row)
	}
	return rows
}

// RefreshAction is `--refresh`'s decision for one planned row, pure.
// Only the stale-pristine case differs from grove-190's RefreshBrain:
// grove wrote that file and nobody touched it, so grove may replace it.
func RefreshAction(row BrainRow) BrainAction {
	switch BrainState(row.State) {
	case BrainAbsent:
		return ActionSeed
	case BrainCurrent:
		return ActionNone
	case BrainStale:
		if row.Pristine {
			return ActionRefreshed
		}
		return ActionNew
	}
	return ActionReport // unstamped, missing-root
}

// RefreshSweep is `gv brains --refresh` (grove-511): plan every probe,
// then carry each row's RefreshAction out and record it in Action. It
// writes exactly two things — a stamped seed over an absent or PRISTINE
// brain, and CLAUDE.md.new beside an edited one — and never deletes
// anything (leftovers are listed, not removed). Rows keep the state they
// were FOUND in: a replaced brain reads `stale` + `refreshed`, which is
// what the report shows as done.
//
// A write failure fills that row's Note and keeps walking; the first
// error is returned after the whole sweep so one unwritable workspace
// never costs the others their refresh.
func RefreshSweep(probes []BrainProbe, seed string) ([]BrainRow, error) {
	rows := PlanSweep(probes, seed)
	stamped := []byte(StampSeed(seed))
	var firstErr error
	fail := func(i int, err error) {
		rows[i].Note = "refresh failed: " + err.Error()
		if firstErr == nil {
			firstErr = err
		}
	}
	for i, row := range rows {
		action := RefreshAction(row)
		rows[i].Action = string(action)
		dir := probes[i].Dir
		switch action {
		case ActionSeed:
			if err := os.MkdirAll(dir, 0o755); err != nil {
				fail(i, err)
				continue
			}
			if err := os.WriteFile(filepath.Join(dir, BrainFile), stamped, 0o644); err != nil {
				fail(i, err)
				continue
			}
			rows[i].Command = ""
			rows[i].Note = "installed the orchestrator brain (seed " + row.Want + ")"
		case ActionRefreshed:
			if err := os.WriteFile(filepath.Join(dir, BrainFile), stamped, 0o644); err != nil {
				fail(i, err)
				continue
			}
			rows[i].Command = ""
			rows[i].Note = "replaced in place — pristine seed " + row.Have + " → " + row.Want
		case ActionNew:
			if err := os.WriteFile(filepath.Join(dir, BrainFile+".new"), stamped, 0o644); err != nil {
				fail(i, err)
				continue
			}
			rows[i].Command = ""
			rows[i].Note = "seed moved " + row.Have + " → " + row.Want + " — edited; wrote " + BrainFile + ".new beside your brain, diff it in"
			if !contains(rows[i].Leftovers, BrainFile+".new") {
				rows[i].Leftovers = append(rows[i].Leftovers, BrainFile+".new")
			}
		}
	}
	return rows, firstErr
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
