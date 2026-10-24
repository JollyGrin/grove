// Brain migration, dry run (grove-513, brain-overlay car 03). An edited
// orchestrator brain is one file with two authors; the overlay design gives
// each a file. This is the one-time move: diff the brain against the seed
// it was born from (found by its stamp in the embedded seed archive), sort
// every hunk into what the overlay convention can hold, and render a
// CLAUDE.local.md.draft beside the brain. Nothing else is written here —
// `--apply` (car 04) is the step that swaps files, and it refuses while a
// `## Review before applying` block remains.
//
// The classifier is pure: (brain, base) in, hunks out. Three classes:
//
//   - added: a hunk with only `+` lines. Copied verbatim into the draft
//     under `## Addenda` (prose) or `## Workspace duties` (it carries its
//     own heading), tagged with the seed section it sat under.
//   - substituted: every `−`/`+` pair in the hunk differs only by a token
//     map that is consistent across the whole brain — `DEV-X` → `grove-N`,
//     `the operator` → `Dean`. The hunks are dropped; the map becomes
//     `## Glossary` lines, which the behavioural probe in the design showed
//     models apply.
//   - review: everything else — rewrites, deletions, renumbered duties,
//     fossils of seed text since reworded. Rendered side by side for the
//     operator to turn into an `Override —` line or delete.
//
// What counts as a consistent substitution is the judgment call this file
// makes, spelled out at classifyPairs.
package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// DraftFile is the dry run's one output, beside the brain. Re-running the
// dry run rewrites it; it is grove's rendering, never operator text.
const DraftFile = OverlayFile + ".draft"

// MigrateCmd is the verb this file implements; MigrateApplyCmd is car 04's.
const (
	MigrateCmd      = "gv brains --migrate"
	MigrateApplyCmd = MigrateCmd + " --apply --label "
)

// HunkClass is where a hunk of brain edits lands in the draft.
type HunkClass string

const (
	HunkAdded       HunkClass = "added"       // `+` lines only → Addenda / Workspace duties
	HunkSubstituted HunkClass = "substituted" // a consistent token map → Glossary
	HunkReview      HunkClass = "review"      // anything else → Review before applying
)

// Hunk is one contiguous run of differing lines between base and brain.
type Hunk struct {
	Class   HunkClass
	Heading string   // the seed section the hunk sat under ("" before the first heading)
	Seed    []string // base lines the brain removed or replaced
	Brain   []string // brain lines added or substituted
	// Section says an added hunk carries a markdown heading of its own,
	// so it is a workspace duty, not an addendum to a seed section.
	Section bool
	// Map is the token map a substituted hunk was explained by.
	Map []Subst
}

// Subst is one glossary entry: wherever the seed writes From, read To.
type Subst struct{ From, To string }

// Migration is the classified diff of one brain against its base.
type Migration struct {
	Hunks    []Hunk
	Glossary []Subst // the brain-wide token map, first-seen order, one entry per From
}

// Counts is the N added / M substituted / K to review of the report.
func (m Migration) Counts() (added, substituted, review int) {
	for _, h := range m.Hunks {
		switch h.Class {
		case HunkAdded:
			added++
		case HunkSubstituted:
			substituted++
		default:
			review++
		}
	}
	return
}

// NeedsReview reports whether the draft would carry a Review block — the
// thing `--apply` refuses on.
func (m Migration) NeedsReview() bool {
	_, _, k := m.Counts()
	return k > 0
}

// ClassifyBrain diffs brain against base (both read as their stamp-free
// bodies) and classifies every hunk. An identical pair yields no hunks.
func ClassifyBrain(brain, base string) Migration {
	hunks := hunksOf(strings.Split(SeedBody(base), "\n"), strings.Split(SeedBody(brain), "\n"))
	var m Migration
	m.Hunks, m.Glossary = classifyHunks(hunks)
	return m
}

// ── line diff ────────────────────────────────────────────────────────────

type lineOp struct {
	kind byte // '=', '-', '+'
	text string
}

// lineDiff is a plain LCS edit script: '=' kept, '-' only in a, '+' only in
// b. Seeds run a few hundred lines, so the quadratic table is nothing.
func lineDiff(a, b []string) []lineOp {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var ops []lineOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, lineOp{'=', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, lineOp{'-', a[i]})
			i++
		default:
			ops = append(ops, lineOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, lineOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, lineOp{'+', b[j]})
	}
	return ops
}

var (
	mdHeading = regexp.MustCompile(`^#{1,6}\s+(.*\S)\s*$`)
	// A seed item: a numbered duty (`3. **Dispatch** — …`) or a bold
	// guardrail bullet (`- **Propose, then act on confirmation.** …`).
	mdItem = regexp.MustCompile(`^(?:\d+\.|-)\s+\*\*([^*]+?)\*\*`)
)

// hunksOf groups the edit script into hunks — maximal runs of non-'='
// ops — and tags each with the seed section it sits under: the nearest
// heading at or above it in the base, plus the nearest numbered duty or
// bold bullet under that heading ("Duties › 3. Dispatch"), so an addendum
// can cite the duty it extends, as the overlay convention asks. A hunk
// that replaces a bullet is tagged with that bullet — the rule it rewrote.
func hunksOf(base, brain []string) []Hunk {
	var hunks []Hunk
	heading, item := "", ""
	var cur *Hunk
	flush := func() {
		if cur != nil {
			hunks = append(hunks, *cur)
			cur = nil
		}
	}
	for _, op := range lineDiff(base, brain) {
		if op.kind != '+' { // base lines move the section cursor, removed ones included
			if m := mdHeading.FindStringSubmatch(op.text); m != nil {
				heading, item = m[1], ""
			} else if m := mdItem.FindStringSubmatch(op.text); m != nil {
				item = strings.TrimRight(strings.TrimSpace(m[1]), ".")
				if num := strings.Fields(op.text)[0]; num != "-" { // "3." — keep the duty number
					item = num + " " + item
				}
			}
		}
		if op.kind == '=' {
			flush()
			continue
		}
		if cur == nil {
			cur = &Hunk{Heading: sectionTag(heading, item)}
		}
		if op.kind == '-' {
			cur.Seed = append(cur.Seed, op.text)
		} else {
			cur.Brain = append(cur.Brain, op.text)
		}
	}
	flush()
	return hunks
}

func sectionTag(heading, item string) string {
	switch {
	case heading == "":
		return ""
	case item == "":
		return heading
	}
	return heading + " › " + item
}

// ── classification ───────────────────────────────────────────────────────

// classifyHunks assigns a class to every hunk and infers the glossary.
// Pass 1 sorts additions out and asks classifyPairs for each remaining
// hunk's candidate map. Pass 2 checks the candidates brain-wide: a From
// that maps two ways is inconsistent, and a map entry that occurs once and
// does not look like an identifier is a one-off edit, not vocabulary. A
// hunk whose map fails either test is reviewed, not dropped.
func classifyHunks(hunks []Hunk) ([]Hunk, []Subst) {
	hunks = splitTrailingAdditions(hunks)
	candidates := make([][]Subst, len(hunks))
	sites := map[string]int{}     // folded From → occurrences across the brain
	target := map[string]string{} // folded From → folded To ("" once it conflicts)
	for i, h := range hunks {
		if len(h.Seed) == 0 {
			hunks[i].Class = HunkAdded
			hunks[i].Section = hasHeading(h.Brain)
			continue
		}
		hunks[i].Class = HunkReview
		m, ok := classifyPairs(h.Seed, h.Brain)
		if !ok {
			continue
		}
		candidates[i] = m
		for _, s := range m {
			k, v := strings.ToLower(s.From), strings.ToLower(s.To)
			sites[k]++
			if prev, seen := target[k]; !seen {
				target[k] = v
			} else if prev != v {
				target[k] = ""
			}
		}
	}
	var glossary []Subst
	seen := map[string]bool{}
	for i, m := range candidates {
		if m == nil {
			continue
		}
		ok := true
		for _, s := range m {
			k := strings.ToLower(s.From)
			if target[k] == "" || (sites[k] < 2 && !identifierLike(s.From)) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		hunks[i].Class, hunks[i].Map = HunkSubstituted, m
		for _, s := range m {
			if k := strings.ToLower(s.From); !seen[k] {
				seen[k] = true
				glossary = append(glossary, s)
			}
		}
	}
	return hunks, glossary
}

// splitTrailingAdditions handles the commonest mixed shape: a substituted
// line with new lines appended right after it (an edited guardrail, then
// an own section at the file's end — nothing equal in between, so the
// line diff makes one hunk). When the seed lines pair as substitutions
// with the first (or last) as-many brain lines, the hunk becomes a
// substitution plus a pure addition. Anything else stays one hunk.
func splitTrailingAdditions(hunks []Hunk) []Hunk {
	out := make([]Hunk, 0, len(hunks))
	for _, h := range hunks {
		k := len(h.Seed)
		if k == 0 || len(h.Brain) <= k {
			out = append(out, h)
			continue
		}
		if _, ok := classifyPairs(h.Seed, h.Brain[:k]); ok {
			out = append(out, Hunk{Heading: h.Heading, Seed: h.Seed, Brain: h.Brain[:k]},
				Hunk{Heading: h.Heading, Brain: h.Brain[k:]})
			continue
		}
		if _, ok := classifyPairs(h.Seed, h.Brain[len(h.Brain)-k:]); ok {
			out = append(out, Hunk{Heading: h.Heading, Brain: h.Brain[:len(h.Brain)-k]},
				Hunk{Heading: h.Heading, Seed: h.Seed, Brain: h.Brain[len(h.Brain)-k:]})
			continue
		}
		out = append(out, h)
	}
	return out
}

func hasHeading(lines []string) bool {
	for _, ln := range lines {
		if mdHeading.MatchString(ln) {
			return true
		}
	}
	return false
}

// maxSubstTokens bounds one side of a replaced span: `the operator` → `Dean`
// is vocabulary; a replaced clause is a rewrite.
const maxSubstTokens = 4

// classifyPairs is the substitution test for one hunk, the judgment call
// of this car. A hunk is substitution-only when:
//
//  1. it has as many `−` lines as `+` lines, paired in order;
//  2. each pair's token diff is replacement spans only — never a bare
//     insertion or deletion of words — each span at most maxSubstTokens
//     tokens a side;
//  3. no replaced span is purely numeric or punctuation (a renumbered
//     duty is a review item, not vocabulary).
//
// It returns the hunk's candidate map; classifyHunks still has to confirm
// it brain-wide (consistency, recurrence or identifier shape).
func classifyPairs(seed, brain []string) ([]Subst, bool) {
	if len(seed) != len(brain) {
		return nil, false
	}
	var m []Subst
	for i := range seed {
		spans, ok := replacedSpans(seed[i], brain[i])
		if !ok {
			return nil, false
		}
		m = append(m, spans...)
	}
	return m, len(m) > 0
}

// identifierLike says a ONE-token span reads as a name or id rather than
// prose — a hyphen, underscore, digit, angle bracket or an inner capital
// letter — so a single `DEV-X` → `grove-N` still counts as vocabulary. A
// multi-word span is prose however it is spelled, and has to recur.
func identifierLike(s string) bool {
	if strings.ContainsAny(s, " \t") {
		return false
	}
	if strings.ContainsAny(s, "-_<>") {
		return true
	}
	for i, r := range s {
		if unicode.IsDigit(r) || (i > 0 && unicode.IsUpper(r)) {
			return true
		}
	}
	return false
}

func numericOnly(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// tokenRe splits a line into words (letters, digits, `_<>-` so `DEV-X`,
// `<task>` and `unbrewed-pro-server-42` stay whole) and single punctuation
// marks; whitespace separates and is dropped.
var tokenRe = regexp.MustCompile(`[\p{L}\p{N}_<>-]+|[^\p{L}\p{N}_<>\s-]`)

type token struct {
	text       string
	start, end int
}

func tokenize(line string) []token {
	var out []token
	for _, loc := range tokenRe.FindAllStringIndex(line, -1) {
		out = append(out, token{line[loc[0]:loc[1]], loc[0], loc[1]})
	}
	return out
}

// replacedSpans token-diffs one `−`/`+` pair and returns the replaced spans
// as (seed text, brain text), or false when the pair differs by anything
// other than bounded replacements. Span text is the original substring
// from first to last token, so `the operator's` yields `the operator`.
func replacedSpans(a, b string) ([]Subst, bool) {
	ta, tb := tokenize(a), tokenize(b)
	wa, wb := make([]string, len(ta)), make([]string, len(tb))
	for i, t := range ta {
		wa[i] = t.text
	}
	for i, t := range tb {
		wb[i] = t.text
	}
	var spans []Subst
	var del, ins []token
	flush := func() bool {
		if len(del) == 0 && len(ins) == 0 {
			return true
		}
		if len(del) == 0 || len(ins) == 0 || len(del) > maxSubstTokens || len(ins) > maxSubstTokens {
			return false
		}
		from := a[del[0].start:del[len(del)-1].end]
		if numericOnly(from) {
			return false
		}
		spans = append(spans, Subst{From: from, To: b[ins[0].start:ins[len(ins)-1].end]})
		del, ins = nil, nil
		return true
	}
	i, j := 0, 0
	for _, op := range lineDiff(wa, wb) {
		switch op.kind {
		case '=':
			if !flush() {
				return nil, false
			}
			i++
			j++
		case '-':
			del = append(del, ta[i])
			i++
		case '+':
			ins = append(ins, tb[j])
			j++
		}
	}
	if !flush() {
		return nil, false
	}
	return spans, true
}

// ── rendering ────────────────────────────────────────────────────────────

// ReviewHeading is the draft section `--apply` refuses on while present.
const ReviewHeading = "## Review before applying"

// RenderDraft is the CLAUDE.local.md.draft for one brain: the overlay
// convention's sections, filled from the classified hunks. Added hunks are
// copied verbatim under a tag naming the seed section they extended;
// substitutions become glossary lines; review hunks show seed and brain
// text side by side. Sections with nothing in them are left out.
func RenderDraft(label, stamp string, m Migration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Workspace overlay: %s\n", label)
	fmt.Fprintf(&b, "<!-- Drafted by `%s` from the edits in %s against seed %s. Convention: %s -->\n",
		MigrateCmd, BrainFile, stamp, OverlayDocURL)
	fmt.Fprintf(&b, "<!-- Read it, edit it, then `%s%s` swaps it in (car 04). -->\n", MigrateApplyCmd, label)

	if len(m.Glossary) > 0 {
		b.WriteString("\n## Glossary\n")
		for _, s := range m.Glossary {
			fmt.Fprintf(&b, "- Wherever the seed writes `%s`, read `%s`.\n", s.From, s.To)
		}
	}
	var addenda, duties []Hunk
	for _, h := range m.Hunks {
		if h.Class != HunkAdded {
			continue
		}
		if h.Section {
			duties = append(duties, h)
		} else {
			addenda = append(addenda, h)
		}
	}
	if len(addenda) > 0 {
		b.WriteString("\n## Addenda\n")
		for _, h := range addenda {
			fmt.Fprintf(&b, "\n**Addendum to %s:**\n", describe(h.Heading))
			writeLines(&b, h.Brain)
		}
	}
	if len(duties) > 0 {
		b.WriteString("\n## Workspace duties\n")
		for _, h := range duties {
			fmt.Fprintf(&b, "\n**Workspace duty** (sat after %s in %s; name it, never number it):\n", describe(h.Heading), BrainFile)
			writeLines(&b, h.Brain)
		}
	}
	if m.NeedsReview() {
		b.WriteString("\n" + ReviewHeading + "\n")
		fmt.Fprintf(&b, "<!-- Each item is seed text your brain rewrote or removed. Turn it into an `**Override — <section> › <rule>:**` line (restating what stays forbidden) or delete it, then remove this whole section — `%s<label>` refuses while it exists. -->\n", MigrateApplyCmd)
		n := 0
		for _, h := range m.Hunks {
			if h.Class != HunkReview {
				continue
			}
			n++
			fmt.Fprintf(&b, "\n### Review %d — %s\n", n, describe(h.Heading))
			b.WriteString("Seed:\n")
			writeQuoted(&b, h.Seed)
			b.WriteString("Brain:\n")
			writeQuoted(&b, h.Brain)
		}
	}
	return b.String()
}

func describe(heading string) string {
	if heading == "" {
		return "the seed's preamble"
	}
	return fmt.Sprintf("seed section %q", heading)
}

func writeLines(b *strings.Builder, lines []string) {
	for _, ln := range lines {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
}

func writeQuoted(b *strings.Builder, lines []string) {
	if len(lines) == 0 {
		b.WriteString("> (nothing)\n")
		return
	}
	for _, ln := range lines {
		if ln == "" {
			b.WriteString(">\n")
			continue
		}
		b.WriteString("> " + ln + "\n")
	}
}

// ── the sweep ────────────────────────────────────────────────────────────

// MigrateProbe is a brain as the migration found it: the sweep probe plus
// whether the workspace repo tracks the brain (thegrid and waterhouse do,
// force-added past `.grove/.gitignore`). Never probed for the global brain.
type MigrateProbe struct {
	BrainProbe
	Tracked bool
}

// MigrateState is the dry run's verdict for one brain.
type MigrateState string

const (
	MigrateNothing     MigrateState = "nothing"      // pristine or absent — nothing to migrate
	MigrateDraft       MigrateState = "draft"        // edited; the draft was rendered
	MigrateUnknownBase MigrateState = "unknown-base" // stamp not in the archive (or no stamp): merge by hand
	MigrateMissingRoot MigrateState = "missing-root"
)

// MigrateRow is one brain's line in the migration report.
type MigrateRow struct {
	Label       string
	Root        string
	Dir         string
	State       MigrateState
	Have        string
	Added       int
	Substituted int
	Review      int
	Draft       string // the rendered draft ("" unless State is draft)
	DraftPath   string
	Tracked     bool
	GitCommands []string // what --apply leaves for the operator to run; printed, never run
	// OverlayExists says a real CLAUDE.local.md already sits beside the
	// brain, so --apply will refuse and the draft is merged by hand.
	OverlayExists bool
	Leftovers     []string
	Note          string
}

// UnknownBaseNote is the message for a stamp the archive does not hold —
// today's merge path, still open.
const UnknownBaseNote = "unknown base; run `" + refreshCmd + "` and merge by hand"

// TrackedBrainCommands are the two git commands a tracked brain needs
// after --apply: version the overlay, stop versioning the seed. Grove
// prints them and never runs them (the design's open question 2).
func TrackedBrainCommands() []string {
	rel := filepath.Join(".grove", "orchestrator")
	return []string{
		"git add -f " + filepath.Join(rel, OverlayFile),
		"git rm --cached " + filepath.Join(rel, BrainFile),
	}
}

// PlanMigrate is the whole dry run, pure: probes in, rows out, drafts
// rendered but not written. lookup finds an archived seed by stamp; the
// seed this binary carries is consulted first, so a brain edited since
// today's seed has a base even before the archive is regenerated.
func PlanMigrate(probes []MigrateProbe, seed string, lookup func(stamp string) (string, bool)) []MigrateRow {
	want := SeedStamp(seed)
	rows := make([]MigrateRow, 0, len(probes))
	for _, p := range probes {
		row := MigrateRow{Label: p.Label, Root: p.Root, Dir: p.Dir, Leftovers: p.Leftovers,
			DraftPath: filepath.Join(p.Dir, DraftFile), OverlayExists: HasOverlay(p.Overlay)}
		if row.Leftovers == nil {
			row.Leftovers = []string{}
		}
		switch {
		case p.RootGone:
			row.State = MigrateMissingRoot
			row.Note = "registered root is gone — `gv workspaces rm " + p.Label + "` if it left for good"
		case !p.Exists:
			row.State = MigrateNothing
			row.Note = "no brain yet — nothing to migrate"
		case Pristine(p.Content):
			row.State, row.Have = MigrateNothing, BrainStamp(p.Content)
			row.Note = "pristine (seed " + row.Have + ") — nothing to migrate"
		default:
			row.Have = BrainStamp(p.Content)
			base, ok := "", false
			switch {
			case row.Have == "":
			case row.Have == want:
				base, ok = seed, true
			default:
				base, ok = lookup(row.Have)
			}
			if !ok {
				row.State = MigrateUnknownBase
				if row.Have == "" {
					row.Note = "no seed stamp (hand-managed) — " + UnknownBaseNote
				} else {
					row.Note = "seed " + row.Have + " is not in the archive — " + UnknownBaseNote
				}
				break
			}
			m := ClassifyBrain(p.Content, base)
			row.State = MigrateDraft
			row.Added, row.Substituted, row.Review = m.Counts()
			row.Draft = RenderDraft(p.Label, row.Have, m)
			row.Note = fmt.Sprintf("%d added / %d substituted / %d to review → %s", row.Added, row.Substituted, row.Review, DraftFile)
			if row.OverlayExists {
				row.Note += " (a " + OverlayFile + " already exists: merge the draft into it by hand; --apply will refuse)"
			}
			if p.Tracked {
				row.Tracked, row.GitCommands = true, TrackedBrainCommands()
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// MigrateSweep is `gv brains --migrate` without --apply: PlanMigrate, then
// write each draft row's CLAUDE.local.md.draft — the only file this verb
// ever touches. A write failure fills that row's Note and keeps walking;
// the first error comes back after the whole sweep.
func MigrateSweep(probes []MigrateProbe, seed string, lookup func(string) (string, bool)) ([]MigrateRow, error) {
	rows := PlanMigrate(probes, seed, lookup)
	var firstErr error
	for i, row := range rows {
		if row.State != MigrateDraft {
			continue
		}
		if err := os.WriteFile(row.DraftPath, []byte(row.Draft), 0o644); err != nil {
			rows[i].Note = "draft not written: " + err.Error()
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return rows, firstErr
}
