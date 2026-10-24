package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "migrate", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// asBrain is an edited brain as the fleet has them: the operator's text
// carrying the stamp of the seed it was born from.
func asBrain(edited, base string) string {
	return SeedBody(edited) + "\n\n" + seedStampPrefix + SeedStamp(base) + seedStampSuffix + "\n"
}

// A brain seeded from base and left alone is the pristine case: no hunks.
func TestClassifyPristineHasNothing(t *testing.T) {
	base := fixture(t, "base.md")
	m := ClassifyBrain(StampSeed(base), base)
	if len(m.Hunks) != 0 || len(m.Glossary) != 0 {
		t.Fatalf("pristine brain produced %+v", m)
	}
	if RenderDraft("x", "s", m) == "" {
		t.Fatal("even an empty migration renders a header")
	}
}

// An appended section with its own heading is a workspace duty; the draft
// carries it verbatim and has no Review block.
func TestClassifyAppendedSectionIsAWorkspaceDuty(t *testing.T) {
	base := fixture(t, "base.md")
	m := ClassifyBrain(asBrain(fixture(t, "appended.md"), base), base)
	a, s, r := m.Counts()
	if a != 1 || s != 0 || r != 0 {
		t.Fatalf("counts %d/%d/%d, want 1/0/0: %+v", a, s, r, m.Hunks)
	}
	h := m.Hunks[0]
	if !h.Section || h.Heading != "Guardrails › Never post ticket comments" {
		t.Errorf("hunk = %+v", h)
	}
	draft := RenderDraft("unbrewed", "abc", m)
	for _, want := range []string{
		"# Workspace overlay: unbrewed",
		"## Workspace duties",
		"## Local pair testing",
		"Run both servers from `scripts/pair.sh`",
		`seed section "Guardrails › Never post ticket comments"`,
	} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft missing %q:\n%s", want, draft)
		}
	}
	for _, no := range []string{ReviewHeading, "## Glossary", "## Addenda"} {
		if strings.Contains(draft, no) {
			t.Errorf("draft must not carry %q:\n%s", no, draft)
		}
	}
}

// A prose addition under a seed section is an addendum tagged with the
// section (and the bullet) it sat under.
func TestClassifyAddedProseIsAnAddendum(t *testing.T) {
	base := fixture(t, "base.md")
	m := ClassifyBrain(asBrain(fixture(t, "addendum.md"), base), base)
	a, s, r := m.Counts()
	if a != 1 || s != 0 || r != 0 || m.Hunks[0].Section {
		t.Fatalf("counts %d/%d/%d: %+v", a, s, r, m.Hunks)
	}
	draft := RenderDraft("tableplace", "abc", m)
	if !strings.Contains(draft, "## Addenda\n\n**Addendum to seed section \"Guardrails › Never post ticket comments\":**\n- Read `LEARNINGS.md`") {
		t.Errorf("draft:\n%s", draft)
	}
	if strings.Contains(draft, "## Workspace duties") {
		t.Errorf("prose is not a duty:\n%s", draft)
	}
}

// Consistent DEV-X → grove-N / the operator → Dean everywhere: every hunk
// is a substitution, the draft is two glossary lines and nothing else.
func TestClassifyConsistentSubstitutionIsGlossaryOnly(t *testing.T) {
	base := fixture(t, "base.md")
	m := ClassifyBrain(asBrain(fixture(t, "substituted.md"), base), base)
	a, s, r := m.Counts()
	if a != 0 || r != 0 || s == 0 {
		t.Fatalf("counts %d/%d/%d, want 0/n/0: %+v", a, s, r, m.Hunks)
	}
	want := []Subst{{"the operator", "Dean"}, {"DEV-X", "grove-N"}} // first-seen order
	if len(m.Glossary) != 2 || m.Glossary[0] != want[0] || m.Glossary[1] != want[1] {
		t.Fatalf("glossary %+v, want %+v", m.Glossary, want)
	}
	draft := RenderDraft("grove-repo", "abc", m)
	for _, want := range []string{
		"## Glossary\n- Wherever the seed writes `the operator`, read `Dean`.\n- Wherever the seed writes `DEV-X`, read `grove-N`.\n",
	} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft missing %q:\n%s", want, draft)
		}
	}
	for _, no := range []string{ReviewHeading, "## Addenda", "## Workspace duties"} {
		if strings.Contains(draft, no) {
			t.Errorf("draft must not carry %q:\n%s", no, draft)
		}
	}
}

// A reworded guardrail is neither an addition nor vocabulary: it goes to
// Review with seed and brain text side by side.
func TestClassifyRewrittenGuardrailNeedsReview(t *testing.T) {
	base := fixture(t, "base.md")
	m := ClassifyBrain(asBrain(fixture(t, "rewritten.md"), base), base)
	a, s, r := m.Counts()
	if a != 0 || s != 0 || r != 1 {
		t.Fatalf("counts %d/%d/%d, want 0/0/1: %+v", a, s, r, m.Hunks)
	}
	if !m.NeedsReview() {
		t.Fatal("NeedsReview must be true")
	}
	draft := RenderDraft("unbrewed", "abc", m)
	for _, want := range []string{
		ReviewHeading,
		"### Review 1 — seed section \"Guardrails › Never post ticket comments\"",
		"Seed:\n> - **Never post ticket comments** without the operator's sign-off; never\n>   close an issue.\n",
		"Brain:\n> - **Ticket comments are free** (the operator's standing OK, 2026-07-05);\n",
		"refuses while it exists",
	} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft missing %q:\n%s", want, draft)
		}
	}
}

// An inserted duty renumbers the ones after it: the hunk has one seed line
// and two brain lines, so it is reviewed, never silently applied.
func TestClassifyRenumberedDutyNeedsReview(t *testing.T) {
	base := fixture(t, "base.md")
	m := ClassifyBrain(asBrain(fixture(t, "renumbered.md"), base), base)
	a, s, r := m.Counts()
	if s != 0 || r == 0 {
		t.Fatalf("counts %d/%d/%d, want no substitution and a review: %+v", a, s, r, m.Hunks)
	}
}

// The judgment call, case by case. Adjacent changed lines form ONE hunk;
// lines 1 and 3 of the base are the non-adjacent pair.
func TestClassifyPairsJudgment(t *testing.T) {
	base := "# S\n\n## Duties\n\n1. **A** — grab DEV-X now (duty 2).\n2. **B** — merge DEV-X after the operator says so (duty 2).\n3. **C** — ask the operator about DEV-X. The operator decides.\n"
	cases := []struct {
		name  string
		brain string
		want  []HunkClass
		gloss int
	}{
		{"one identifier swap counts once", strings.Replace(base, "grab DEV-X", "grab grove-N", 1),
			[]HunkClass{HunkSubstituted}, 1},
		{"identifier swap in two hunks", strings.Replace(strings.Replace(base, "grab DEV-X", "grab grove-N", 1), "about DEV-X", "about grove-N", 1),
			[]HunkClass{HunkSubstituted, HunkSubstituted}, 1},
		{"one prose swap is a rewrite", strings.Replace(base, "merge DEV-X", "rebase DEV-X", 1),
			[]HunkClass{HunkReview}, 0},
		{"prose swap twice is vocabulary", strings.ReplaceAll(base, "the operator", "Dean"),
			[]HunkClass{HunkSubstituted}, 1},
		{"sentence-initial casing folds in", strings.ReplaceAll(strings.ReplaceAll(base, "the operator", "Dean"), "The operator", "Dean"),
			[]HunkClass{HunkSubstituted}, 1},
		{"inconsistent map is reviewed", strings.Replace(strings.Replace(base, "grab DEV-X", "grab grove-N", 1), "about DEV-X", "about <task>", 1),
			[]HunkClass{HunkReview, HunkReview}, 0},
		{"renumbered refs are numeric → review", strings.ReplaceAll(base, "(duty 2)", "(duty 3)"),
			[]HunkClass{HunkReview}, 0},
		{"a deleted line is reviewed", strings.Replace(base, "3. **C** — ask the operator about DEV-X. The operator decides.\n", "", 1),
			[]HunkClass{HunkReview}, 0},
		{"a replaced clause is a rewrite", strings.Replace(base, "after the operator says so", "whenever CI is green and nobody objects", 1),
			[]HunkClass{HunkReview}, 0},
		{"a bare word insertion is a rewrite", strings.Replace(base, "grab DEV-X now", "grab DEV-X right now", 1),
			[]HunkClass{HunkReview}, 0},
		{"a swap then appended lines splits into both", strings.Replace(base, "ask the operator about DEV-X. The operator decides.\n", "ask the operator about grove-N. The operator decides.\n\n## Mine\n\nown rule\n", 1),
			[]HunkClass{HunkSubstituted, HunkAdded}, 1},
		{"appended lines then a swap splits into both", strings.Replace(base, "1. **A** — grab DEV-X now", "0. **Z** — mine first.\n1. **A** — grab grove-N now", 1),
			[]HunkClass{HunkAdded, HunkSubstituted}, 1},
		{"a rewrite beside a swap drags the hunk to review", strings.Replace(strings.Replace(base, "grab DEV-X now", "grab grove-N now", 1), "merge DEV-X after", "rebase grove-N before", 1),
			[]HunkClass{HunkReview}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := ClassifyBrain(c.brain, base)
			var got []HunkClass
			for _, h := range m.Hunks {
				got = append(got, h.Class)
			}
			if len(got) != len(c.want) {
				t.Fatalf("hunks %v, want %v: %+v", got, c.want, m.Hunks)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("hunk %d = %s, want %s: %+v", i, got[i], c.want[i], m.Hunks[i])
				}
			}
			if len(m.Glossary) != c.gloss {
				t.Errorf("glossary %+v, want %d entries", m.Glossary, c.gloss)
			}
		})
	}
}

// Stamp lines never show up as a hunk: both sides are read as bodies.
func TestClassifyIgnoresStampLines(t *testing.T) {
	base := fixture(t, "base.md")
	m := ClassifyBrain(StampSeed(base)+"\n<!-- grove-seed: 000000000000 -->\n", StampSeed(base))
	if len(m.Hunks) != 0 {
		t.Fatalf("stamp lines produced hunks: %+v", m.Hunks)
	}
}

func migrateProbe(label, content string) MigrateProbe {
	return MigrateProbe{BrainProbe: BrainProbe{Label: label, Root: "/w/" + label, Dir: "/w/" + label + "/.grove/orchestrator",
		Exists: content != "", Content: content, Leftovers: []string{}}}
}

func TestPlanMigrateRows(t *testing.T) {
	base := fixture(t, "base.md")
	// still writes the tokens the brains substitute, or glossary lines would
	// be orphans (grove-593)
	seed := "# newer seed\n\nthe operator, DEV-X\n"
	oldStamp := SeedStamp(base)
	lookup := func(s string) (string, bool) {
		if s == oldStamp {
			return base, true
		}
		return "", false
	}
	tracked := migrateProbe("thegrid", asBrain(fixture(t, "substituted.md"), base))
	tracked.Tracked = true
	withOverlay := migrateProbe("unbrewed", asBrain(fixture(t, "appended.md"), base))
	withOverlay.Overlay = OverlayStub + "- The operator is Dean.\n"
	ghost := migrateProbe("ghost", "")
	ghost.RootGone = true
	// Edited against the seed THIS binary carries: base is the seed itself,
	// no archive lookup needed.
	editedCurrent := migrateProbe("fresh", StampSeed(seed)+"\n## mine\n\nextra\n")
	probes := []MigrateProbe{
		migrateProbe("pristine", StampSeed(base)),
		migrateProbe("absent", ""),
		ghost,
		migrateProbe("unknown", asBrain("edited text", "some seed nobody archived")),
		migrateProbe("handmade", "# my own brain\n"),
		migrateProbe("edited", asBrain(fixture(t, "rewritten.md"), base)),
		tracked,
		withOverlay,
		editedCurrent,
	}
	rows := PlanMigrate(probes, seed, lookup)
	byLabel := map[string]MigrateRow{}
	for _, r := range rows {
		byLabel[r.Label] = r
	}
	check := func(label string, state MigrateState, note string) MigrateRow {
		t.Helper()
		r := byLabel[label]
		if r.State != state {
			t.Errorf("%s: state %s, want %s (%s)", label, r.State, state, r.Note)
		}
		if !strings.Contains(r.Note, note) {
			t.Errorf("%s: note %q lacks %q", label, r.Note, note)
		}
		if (r.Draft != "") != (state == MigrateDraft) {
			t.Errorf("%s: draft rendered = %v for state %s", label, r.Draft != "", state)
		}
		return r
	}
	check("pristine", MigrateNothing, "nothing to migrate")
	check("absent", MigrateNothing, "nothing to migrate")
	check("ghost", MigrateMissingRoot, "root is gone")
	check("unknown", MigrateUnknownBase, UnknownBaseNote)
	check("handmade", MigrateUnknownBase, "no seed stamp")
	if r := check("edited", MigrateDraft, "0 added / 0 substituted / 1 to review"); r.Review != 1 || r.DraftPath != "/w/edited/.grove/orchestrator/"+DraftFile || len(r.GitCommands) != 0 {
		t.Errorf("edited row %+v", r)
	}
	if r := check("thegrid", MigrateDraft, "substituted / 0 to review"); !r.Tracked || len(r.GitCommands) != 2 ||
		r.GitCommands[0] != "git add -f .grove/orchestrator/CLAUDE.local.md" || r.GitCommands[1] != "git rm --cached .grove/orchestrator/CLAUDE.md" {
		t.Errorf("tracked row %+v", r)
	}
	if r := check("unbrewed", MigrateDraft, "already exists"); !r.OverlayExists || r.Added != 1 {
		t.Errorf("overlay row %+v", r)
	}
	if r := check("fresh", MigrateDraft, "1 added / 0 substituted / 0 to review"); r.Have != SeedStamp(seed) {
		t.Errorf("fresh row %+v", r)
	}
	if byLabel["pristine"].Have != oldStamp {
		t.Errorf("pristine row must still report its stamp: %+v", byLabel["pristine"])
	}
}

// The sweep writes the draft and nothing else: brain, overlay and the
// leftovers beside them are byte-identical afterwards.
func TestMigrateSweepWritesOnlyTheDraft(t *testing.T) {
	base := fixture(t, "base.md")
	dir := t.TempDir()
	brain := asBrain(fixture(t, "appended.md"), base)
	files := map[string]string{
		BrainFile:                            brain,
		OverlayFile:                          OverlayStub,
		BrainFile + ".new":                   "newer",
		BrainFile + ".bak-seed-000000000000": "old",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := MigrateProbe{BrainProbe: ProbeGlobalBrain(dir)}
	rows, err := MigrateSweep([]MigrateProbe{p}, "# other seed\n", func(s string) (string, bool) {
		if s == SeedStamp(base) {
			return base, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].State != MigrateDraft {
		t.Fatalf("row %+v", rows[0])
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != len(files)+1 {
		t.Fatalf("dir now holds %d entries, want %d: %v", len(entries), len(files)+1, entries)
	}
	for name, content := range files {
		got, _ := os.ReadFile(filepath.Join(dir, name))
		if string(got) != content {
			t.Errorf("%s changed under --migrate", name)
		}
	}
	draft, err := os.ReadFile(filepath.Join(dir, DraftFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(draft) != rows[0].Draft || !strings.Contains(string(draft), "## Local pair testing") {
		t.Errorf("draft on disk:\n%s", draft)
	}
	// Re-running refreshes the draft in place.
	if _, err := MigrateSweep([]MigrateProbe{p}, "# other seed\n", func(string) (string, bool) { return base, true }); err != nil {
		t.Fatal(err)
	}
	if entries, _ = os.ReadDir(dir); len(entries) != len(files)+1 {
		t.Errorf("second run added files: %v", entries)
	}
}

// A brain on an archived stamp substitutes a token the CURRENT seed has
// since renamed: the draft's Glossary must be keyed on a token the current
// seed writes, or the line goes to Review — never on text --apply removes.
func TestRebaseGlossaryKeysExistInCurrentSeed(t *testing.T) {
	old := fixture(t, "base.md")
	renamed := StampSeed(strings.ReplaceAll(old, "DEV-X", "<task>"))
	gone := StampSeed(strings.ReplaceAll(old, "DEV-X", "whichever ticket the operator names today"))
	cases := []struct {
		name       string
		current    string
		wantGloss  []Subst
		wantReview int
	}{
		{"unchanged seed", StampSeed(old), []Subst{{"the operator", "Dean"}, {"DEV-X", "grove-N"}}, 0},
		{"token renamed", renamed, []Subst{{"the operator", "Dean"}, {"<task>", "grove-N"}}, 0},
		{"no counterpart", gone, []Subst{{"the operator", "Dean"}}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := ClassifyBrain(asBrain(fixture(t, "substituted.md"), old), old).Rebase(old, c.current)
			if len(m.Glossary) != len(c.wantGloss) {
				t.Fatalf("glossary %+v, want %+v", m.Glossary, c.wantGloss)
			}
			for i, w := range c.wantGloss {
				if m.Glossary[i] != w {
					t.Errorf("glossary[%d] = %+v, want %+v", i, m.Glossary[i], w)
				}
			}
			for _, s := range m.Glossary {
				if !strings.Contains(SeedBody(c.current), s.From) {
					t.Errorf("glossary From %q is not in the current seed", s.From)
				}
			}
			if _, _, r := m.Counts(); r != c.wantReview {
				t.Errorf("review = %d, want %d: %+v", r, c.wantReview, m.Hunks)
			}
		})
	}
}

// Two stamped tokens that the current seed folded into one placeholder
// with different readings keep the first and review the second.
func TestRebaseCollapsedTokensReview(t *testing.T) {
	old := "intro\n\nrun `gv grab DEV-X` now\nand `gv done DEV-42` after\nrun `gv grab DEV-X` again\nand `gv done DEV-42` too\n"
	cur := StampSeed(strings.NewReplacer("DEV-X", "<task>", "DEV-42", "<task>").Replace(old))
	brain := asBrain(strings.NewReplacer("DEV-X", "grove-N", "DEV-42", "grove-42").Replace(old), old)
	m := ClassifyBrain(brain, old).Rebase(old, cur)
	if len(m.Glossary) != 1 || m.Glossary[0] != (Subst{"<task>", "grove-N"}) {
		t.Fatalf("glossary %+v", m.Glossary)
	}
	if _, _, r := m.Counts(); r != 1 {
		t.Fatalf("want one review item: %+v", m.Hunks)
	}
}
