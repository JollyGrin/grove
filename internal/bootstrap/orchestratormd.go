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
	"regexp"
	"strings"
)

const (
	seedStampPrefix = "<!-- grove-seed: "
	seedStampSuffix = " -->"
	seedStampLen    = 12 // short sha256: collision-proof enough for a marker
)

// BrainFile is the brain's basename inside an orchestrator dir.
const BrainFile = "CLAUDE.md"

// OverlayFile is the operator-owned overlay beside the brain (grove-512,
// brain-overlay design §Files). Claude Code appends it after CLAUDE.md,
// from the orchestrator dir and from every profile subdir under it, with
// or without git. Grove writes it exactly once — as OverlayStub, on the
// first seed, when no file is there — and never rewrites it.
const OverlayFile = "CLAUDE.local.md"

// OverlayDocURL is where the overlay convention lives; the stub points
// at it so an operator opening the empty file knows the shape.
const OverlayDocURL = "https://github.com/JollyGrin/grove/blob/main/docs/orchestrator-overlay.md"

// OverlayStub is the first-seed overlay: one HTML comment per line, so
// Claude Code — which strips block HTML comments before injection —
// loads it at zero context cost, and a shell `grep -v '^<!--'` finds
// nothing. The headings are the convention's four sections, in order.
const OverlayStub = "<!-- Workspace overlay (operator-owned; grove never rewrites this file). Convention: " + OverlayDocURL + " -->\n" +
	"<!-- ## Glossary -->\n" +
	"<!-- ## Overrides -->\n" +
	"<!-- ## Addenda -->\n" +
	"<!-- ## Workspace duties -->\n"

// htmlComment matches one block HTML comment, across lines, non-greedy —
// the same thing Claude Code drops before a CLAUDE*.md reaches the model.
var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// StripHTMLComments removes every block HTML comment from s. What is
// left is what the model would see.
func StripHTMLComments(s string) string {
	return htmlComment.ReplaceAllString(s, "")
}

// OverlayLines counts the lines of overlay content that reach the model:
// non-blank lines once block HTML comments are gone. The stub scores 0.
func OverlayLines(content string) int {
	n := 0
	for _, ln := range strings.Split(StripHTMLComments(content), "\n") {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}

// HasOverlay reports whether overlay content carries any real line. A
// file holding only HTML comments (the stub) is no overlay — everywhere:
// the report, and the later migration verbs.
func HasOverlay(content string) bool { return OverlayLines(content) > 0 }

// WriteOverlayStub installs OverlayStub as orchDir/CLAUDE.local.md when
// nothing is there, and returns the path written ("" when a file already
// existed). It is the only way grove ever writes the overlay on its own,
// and O_EXCL makes the never-over-an-existing-file rule the kernel's.
func WriteOverlayStub(orchDir string) (string, error) {
	target := filepath.Join(orchDir, OverlayFile)
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return "", nil
		}
		return "", err
	}
	if _, err := f.WriteString(OverlayStub); err != nil {
		f.Close()
		return "", err
	}
	return target, f.Close()
}

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
	// ActionRefreshed is `gv brains --refresh` only (grove-511): a stale
	// brain grove can PROVE it wrote unchanged is replaced in place.
	ActionRefreshed BrainAction = "refreshed"
)

// MigrateHint is appended to every edited-stale note (grove-512): the
// edits belong in the overlay, and the verb that moves them there arrives
// with brain-overlay car 03 — the note may name it now.
const MigrateHint = "move your edits to " + OverlayFile + ": `gv brains --migrate`"

// BrainPlan is the decision for one brain: never any I/O.
type BrainPlan struct {
	State    BrainState
	Action   BrainAction
	Pristine bool   // the brain is byte-for-byte a seed grove wrote (see Pristine)
	Have     string // the stamp found in the brain ("" when absent/unstamped)
	Want     string // the current seed's stamp
	Note     string // the one-line human report
}

// SeedBody is a seed or brain with every stamp line and trailing blank
// space removed — the bytes the stamp is computed over. Stripping first
// makes stamping idempotent: StampSeed(StampSeed(x)) == StampSeed(x). The
// seed archive generator (orchestrator/seeds/gen.go) and the migration
// diff use the same body.
func SeedBody(s string) string {
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
	sum := sha256.Sum256([]byte(SeedBody(seed)))
	return hex.EncodeToString(sum[:])[:seedStampLen]
}

// StampSeed returns the seed with exactly one trailing stamp line. Every
// seed write goes through it, so a stamp can never be duplicated.
func StampSeed(seed string) string {
	body := SeedBody(seed)
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

// Pristine reports whether a brain is exactly what grove wrote: the stamp
// it carries is the hash of its own body, so nobody edited it since (an
// edit anywhere outside the stamp line moves the hash; an edit to the
// stamp line makes it a different seed's stamp). It needs no extra state
// — the stamp IS the record. Unstamped content is never pristine.
//
// A pristine brain is grove's own file, which is what lets `gv brains
// --refresh` (grove-511) replace it in place without a .new or a backup:
// the old bytes are reproducible from the seed history by stamp.
func Pristine(content string) bool {
	have := BrainStamp(content)
	return have != "" && have == SeedStamp(content)
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
	have, pristine := BrainStamp(content), Pristine(content)
	switch {
	case have == want:
		return BrainPlan{State: BrainCurrent, Action: ActionNone, Pristine: pristine, Have: have, Want: want,
			Note: "orchestrator brain up to date (seed " + want + ")"}
	case have != "":
		note := fmt.Sprintf("seed moved %s → %s — wrote %s.new beside your brain; grove never overwrites it", have, want, BrainFile)
		if !pristine {
			note += "; " + MigrateHint
		}
		return BrainPlan{State: BrainStale, Action: ActionNew, Pristine: pristine, Have: have, Want: want, Note: note}
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
// same bytes at most. A first seed also drops the overlay stub beside the
// brain (grove-512) — only when no CLAUDE.local.md is there.
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
		if _, err := WriteOverlayStub(orchDir); err != nil {
			return plan, target, err
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
// the brain is absent, plus the overlay stub when no CLAUDE.local.md is
// there (grove-512). Never writes .new — an unattended cockpit open
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
	if _, err := WriteOverlayStub(orchDir); err != nil {
		return target, err
	}
	return target, nil
}
