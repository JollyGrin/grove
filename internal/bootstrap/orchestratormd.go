// Orchestrator brain refresh (grove-190). `buildCockpit` installs the
// embedded orchestrator seed only when the workspace brain is absent —
// correct (a customized brain must never be clobbered), but it means a
// seed improvement never reaches an already-seeded workspace. This is
// the refresh path, mirroring the AGENTS.md rule one file over: grove
// never overwrites an existing brain; a moved seed lands beside it as
// CLAUDE.md.new for the human to diff.
//
// Every seed write ends with a stamp line
//
//	<!-- grove-seed: <short sha256 of the seed body> -->
//
// and drift is decided from the stamp ALONE — an operator who edited the
// prose around it still gets told when the seed moved. A brain with no
// stamp predates this change: hand-managed, reported once, never
// rewritten unless the human forces it.
package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	seedStampPrefix = "<!-- grove-seed: "
	seedStampSuffix = " -->"
	seedStampLen    = 12 // short sha256: collision-proof enough for a marker
)

// BrainFile is the brain's basename inside an orchestrator dir.
const BrainFile = "CLAUDE.md"

// BrainState classifies an on-disk brain against the current seed.
type BrainState string

const (
	BrainAbsent    BrainState = "absent"    // no brain yet — seed it
	BrainCurrent   BrainState = "current"   // stamp matches the seed
	BrainStale     BrainState = "stale"     // stamped, but the seed moved
	BrainUnstamped BrainState = "unstamped" // hand-managed from before stamping
)

// BrainAction is what a refresh run should do about that state.
type BrainAction string

const (
	ActionSeed   BrainAction = "seed"   // write CLAUDE.md
	ActionNone   BrainAction = "none"   // nothing to do
	ActionNew    BrainAction = "new"    // write CLAUDE.md.new beside it
	ActionReport BrainAction = "report" // say so; touch nothing
)

// BrainPlan is the decision for one brain: never any I/O.
type BrainPlan struct {
	State  BrainState
	Action BrainAction
	Have   string // the stamp found in the brain ("" when absent/unstamped)
	Want   string // the current seed's stamp
	Note   string // the one-line human report
}

// seedBody is a seed or brain with every stamp line and trailing blank
// space removed — the bytes the stamp is computed over. Stripping first
// makes stamping idempotent: StampSeed(StampSeed(x)) == StampSeed(x).
func seedBody(s string) string {
	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), seedStampPrefix) {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\n")
}

// SeedStamp is the short sha256 identifying a seed's content.
func SeedStamp(seed string) string {
	sum := sha256.Sum256([]byte(seedBody(seed)))
	return hex.EncodeToString(sum[:])[:seedStampLen]
}

// StampSeed returns the seed with exactly one trailing stamp line. Every
// seed write goes through it, so a stamp can never be duplicated.
func StampSeed(seed string) string {
	body := seedBody(seed)
	return body + "\n\n" + seedStampPrefix + SeedStamp(body) + seedStampSuffix + "\n"
}

// BrainStamp extracts the stamp from brain content, "" when unstamped.
// The LAST stamp line wins — the seed writes it trailing.
func BrainStamp(content string) string {
	stamp := ""
	for _, ln := range strings.Split(content, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, seedStampPrefix) || !strings.HasSuffix(ln, seedStampSuffix) {
			continue
		}
		stamp = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(ln, seedStampPrefix), seedStampSuffix))
	}
	return stamp
}

// PlanBrain is the whole decision, pure: what to do about a brain whose
// content is `content` (exists=false when there is none) given `seed`.
// force only ever widens the hand-managed case into a .new write — it
// never authorizes overwriting an existing brain.
func PlanBrain(content string, exists bool, seed string, force bool) BrainPlan {
	want := SeedStamp(seed)
	if !exists {
		return BrainPlan{State: BrainAbsent, Action: ActionSeed, Want: want,
			Note: "installed the orchestrator brain (seed " + want + ")"}
	}
	have := BrainStamp(content)
	switch {
	case have == want:
		return BrainPlan{State: BrainCurrent, Action: ActionNone, Have: have, Want: want,
			Note: "orchestrator brain up to date (seed " + want + ")"}
	case have != "":
		return BrainPlan{State: BrainStale, Action: ActionNew, Have: have, Want: want,
			Note: fmt.Sprintf("seed moved %s → %s — wrote %s.new beside your brain; grove never overwrites it", have, want, BrainFile)}
	case force:
		return BrainPlan{State: BrainUnstamped, Action: ActionNew, Want: want,
			Note: fmt.Sprintf("brain has no seed stamp (hand-managed) — forced: wrote %s.new for a manual diff", BrainFile)}
	default:
		return BrainPlan{State: BrainUnstamped, Action: ActionReport, Want: want,
			Note: "orchestrator brain has no seed stamp — treated as hand-managed and left alone (--force-orchestrator-md writes " + BrainFile + ".new to diff against the current seed)"}
	}
}

// InspectBrain reads the brain under orchDir and plans against seed.
// A missing dir or file is the absent case, not an error.
func InspectBrain(orchDir, seed string, force bool) (BrainPlan, error) {
	b, err := os.ReadFile(filepath.Join(orchDir, BrainFile))
	if err != nil {
		if os.IsNotExist(err) {
			return PlanBrain("", false, seed, force), nil
		}
		return BrainPlan{}, err
	}
	return PlanBrain(string(b), true, seed, force), nil
}

// RefreshBrain is `gv init --only orchestrator-md`: inspect, then carry
// the plan out. Returns the plan and the path written ("" when nothing
// was). Idempotent — a second run finds the same state and rewrites the
// same bytes at most.
func RefreshBrain(orchDir, seed string, force bool) (BrainPlan, string, error) {
	plan, err := InspectBrain(orchDir, seed, force)
	if err != nil {
		return plan, "", err
	}
	switch plan.Action {
	case ActionSeed:
		if err := os.MkdirAll(orchDir, 0o755); err != nil {
			return plan, "", err
		}
		target := filepath.Join(orchDir, BrainFile)
		if err := os.WriteFile(target, []byte(StampSeed(seed)), 0o644); err != nil {
			return plan, "", err
		}
		return plan, target, nil
	case ActionNew:
		target := filepath.Join(orchDir, BrainFile+".new")
		if err := os.WriteFile(target, []byte(StampSeed(seed)), 0o644); err != nil {
			return plan, "", err
		}
		return plan, target, nil
	}
	return plan, "", nil
}

// SeedBrain is the first-run half only (the cockpit build and the
// detached chat spawn): create the dir and install the stamped seed when
// the brain is absent. Never writes .new — an unattended cockpit open
// must not litter a workspace; drift is `gv doctor`'s row to raise and
// the refresh step's job to act on.
func SeedBrain(orchDir, seed string) (string, error) {
	if err := os.MkdirAll(orchDir, 0o755); err != nil {
		return "", err
	}
	plan, err := InspectBrain(orchDir, seed, false)
	if err != nil || plan.Action != ActionSeed {
		return "", err
	}
	target := filepath.Join(orchDir, BrainFile)
	if err := os.WriteFile(target, []byte(StampSeed(seed)), 0o644); err != nil {
		return "", err
	}
	return target, nil
}
