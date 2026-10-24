package bootstrap

import (
	"strings"
	"testing"

	"github.com/JollyGrin/grove/orchestrator/seeds"
)

// The brain-overlay design's Done-when line 7: the classifier places each
// of the 20 inventoried brains (design §Inventory, 2026-10-19) in the class
// the table gives it — a table test, never a live run. Every fixture is
// built here from the archived seed every live brain carried, a6a0462035c0,
// by edits that reproduce the row's described shape; the archive freezes
// that seed, so car 05's seed move cannot rot these rows.
//
// Inventory letters → classifier classes:
//
//	pristine  → no hunks (nothing to migrate); H on a pristine row changes nothing
//	I  identifier substitution   → substituted, the map as Glossary lines
//	A  added section / paragraph → added: Workspace duties (own heading) or Addenda
//	C  context / bootstrap pointer → added (Addenda) — a pure addition
//	B  backend wording → review when it rewrites seed text; added when it is
//	   an extra sentence (deanbook, waterhouse, grove-repo's dev-linear note)
//	O  rule override → review when it rewrites a guardrail; added when the
//	   brain appends the rule as new text (deanbook's SECRETARY MODE)
//	N  renumbering → review (numeric spans are never vocabulary)
//	F  fossil (seed text since reworded, kept as "ours") → review
//	R  redundant / host-wrong path → added; the classifier cannot judge it,
//	   the operator deletes it from the draft
//	H  host drift → not a classifier class: the same shape classifies the
//	   same on either host
const inventorySeed = "a6a0462035c0"

// edit replaces old with new in body, failing loudly when the archived
// seed does not carry old — the fixture would silently stop meaning what
// the row says.
func edit(t *testing.T, body, old, new string, n int) string {
	t.Helper()
	if c := strings.Count(body, old); c == 0 || (n > 0 && c < n) {
		t.Fatalf("archived seed %s has %d occurrence(s) of %q, fixture needs %d", inventorySeed, c, old, n)
	}
	return strings.Replace(body, old, new, n)
}

func TestClassifyInventory(t *testing.T) {
	base, ok := seeds.Lookup(inventorySeed)
	if !ok {
		t.Fatalf("archive lacks %s", inventorySeed)
	}
	// Shared building blocks, each one inventory class.
	swapIDs := func(b, to string) string { return edit(t, b, "DEV-X", to, -1) }             // I
	swapOperator := func(b string) string { return edit(t, b, "the operator", "Dean", -1) } // I
	backendRewrite := func(b string) string {                                               // B (rewrite)
		return edit(t, b, "(Linear, GitHub issues, task files)", "(GitHub issues)", 1)
	}
	fossil := func(b string) string { // F: an older wording kept where the seed moved on
		return edit(t, b, "- `gv cost --context DEV-X` — `avg_ctx`/`max_ctx` past ~200k with zero",
			"- `dev-core:cleanup-local-state` when a worktree is wedged, then", 1)
	}
	learnings := func(b string) string { // C
		return b + "\nRead `LEARNINGS.md` next to this file at session start.\n"
	}
	section := func(b, heading, text string) string { // A (own heading)
		return b + "\n## " + heading + "\n\n" + text + "\n"
	}
	commentOverride := func(b string) string { // O (rewrite of a guardrail)
		return edit(t, b, "- **Never post ticket comments** (Linear or GitHub) without the operator's",
			"- **Ticket comments are free** (the operator's standing OK, 2026-07-05); but without the operator's", 1)
	}
	renumber := func(b string) string { // N: an own duty 10 pushes Land to 11, Learnings to 12, refs follow
		b = edit(t, b, "11. **Learnings review**", "12. **Learnings review**", 1)
		b = edit(t, b, "10. **Land a feature**", "10. **Review feedback loop** — after a review lands, relay each\n    comment to the worker with `gv nudge`.\n11. **Land a feature**", 1)
		return edit(t, b, "(duty 10)", "(duty 11)", -1)
	}
	waterhouseMap := func(b string) string { // C + B (added sentence) + R, all additions (20 / 0)
		return edit(t, b, "## Your tools\n", "## Workspace map\n\n- Backend: GitHub issues provider on the waterhouse repo.\n"+
			"- `apps/web` is the booking site, `apps/api` the calendar service.\n"+
			"- Read `/Users/grins/git/waterhouse/CLAUDE.md` first.\n\n## Your tools\n", 1)
	}
	deanbook := func(b string) string { // A + B + O, all appended (42 / 0)
		return section(b, "SECRETARY MODE", "The provider is markdown: ignore every Linear instruction above.\n"+
			"No PRs here — a local `git merge --no-ff` on the operator's yes, one ticket at a time,\n"+
			"with a review gate per ticket. Anthropic models only.")
	}
	groveRepoMac := func(b string) string { // I + B + A + F
		b = fossil(b)
		b = swapOperator(swapIDs(b, "grove-N"))
		b = backendRewrite(b)
		b = edit(t, b, "tools — `gh issue` for GitHub, the **dev-linear MCP tools** for Linear,",
			"tools — `gh issue` for GitHub, the **dev-linear MCP tools** for Linear,\nthe dev-linear MCP tools apply only in thegrid, never here,", 1)
		return section(b, "Conventions", "- Merge train: cars land green on main one by one.\n- Rote routing: a `rote` ticket runs at low effort first.")
	}
	thegrid := func(b string) string { // I (operator only) + B + A + N + O
		b = swapOperator(b)
		b = edit(t, b, "or mutate the task\n  backend (Linear, GitHub issues, task files)", "or mutate Linear", 1)
		b = edit(t, b, "**never move any ticket to Done or close an issue**", "**never move any ticket to Done**", 1)
		b = edit(t, b, "   what needs Dean (questions, blockers, review-ready), one line each, then\n   the quiet rest. Draft a suggested answer for every open question.\n",
			"   what needs Dean (questions, blockers, review-ready), one line each, then\n   the quiet rest. Draft a suggested answer for every open question.\n   Review handoff package: for every review-ready row, link the PR and the\n   acceptance list in one block.\n", 1)
		b = renumber(b)
		b += "\n- **Never open CDP unprompted.** The browser tools wait for an explicit ask.\n" // body has no trailing newline
		return section(b, "Browser (CDP)", "Chrome DevTools attach only on request; close the tab when done.")
	}
	unbrewed := func(b string, glm bool) string { // I + B + A + C + O + F (mac adds GLM)
		b = fossil(b)
		b = commentOverride(b) // on seed wording, before the operator swap
		b = swapOperator(swapIDs(b, "<task>"))
		b = backendRewrite(b)
		if glm {
			b = edit(t, b, "## Monitoring", "GLM means Flash: `--profile zai-plan-glm-flash` unless the ticket says otherwise.\n\n## Monitoring", 1)
		}
		b = learnings(b)
		return section(b, "Local pair testing", "Run both servers from `scripts/pair.sh` before any PR that touches the wire.")
	}

	type row struct {
		n       int
		brain   string
		classes string
		build   func(string) string
		gloss   []Subst // exact; nil = none
		added   bool    // at least one Addenda / Workspace-duty hunk
		review  bool    // at least one Review hunk
	}
	pristine := func(b string) string { return b }
	rows := []row{
		{1, "bruce-explroe (mac)", "pristine", pristine, nil, false, false},
		{2, "deancafe-admin (mac)", "pristine", pristine, nil, false, false},
		{3, "deanlol (mac)", "pristine", pristine, nil, false, false},
		{4, "monacum-v2 (mac)", "pristine", pristine, nil, false, false},
		{5, "runelite (mac)", "pristine", pristine, nil, false, false},
		{6, "warcraft (mac)", "pristine", pristine, nil, false, false},
		{7, "xteink (mac)", "pristine", pristine, nil, false, false},
		{8, "deanlol (remote)", "pristine", pristine, nil, false, false},
		{9, "runelite (remote)", "pristine", pristine, nil, false, false},
		{10, "tableplace (remote)", "pristine, H", pristine, nil, false, false},
		{11, "wc-stylized-maps (remote)", "pristine", pristine, nil, false, false},
		{12, "grove-repo (remote)", "I (stray), H",
			func(b string) string { return edit(t, b, "gv grab DEV-X --repo Y", "gv grab grove-N --repo Y", 1) },
			[]Subst{{"DEV-X", "grove-N"}}, false, false},
		{13, "tableplace (mac)", "C, H", learnings, nil, true, false},
		{14, "waterhouse (mac)", "C, B, R", waterhouseMap, nil, true, false},
		{15, "waterhouse (remote)", "C, B, R, H", waterhouseMap, nil, true, false},
		{16, "deanbook (mac)", "A, B, O", deanbook, nil, true, false},
		{17, "grove-repo (mac)", "I, B, A, F, H", groveRepoMac,
			[]Subst{{"DEV-X", "grove-N"}, {"the operator", "Dean"}}, true, true},
		{18, "thegrid (mac)", "I, B, A, N, O", thegrid,
			[]Subst{{"the operator", "Dean"}}, true, true},
		{19, "unbrewed (mac)", "I, B, A, C, O, F", func(b string) string { return unbrewed(b, true) },
			[]Subst{{"DEV-X", "<task>"}, {"the operator", "Dean"}}, true, true},
		{20, "unbrewed (remote)", "I, B, A, C, O, F, H", func(b string) string { return unbrewed(b, false) },
			[]Subst{{"DEV-X", "<task>"}, {"the operator", "Dean"}}, true, true},
	}
	if len(rows) != 20 {
		t.Fatalf("inventory has 20 rows, table has %d", len(rows))
	}
	for _, r := range rows {
		brain := asBrain(r.build(base), base) // built outside the subtest: edit's fatals name the parent
		t.Run(r.brain, func(t *testing.T) {
			m := ClassifyBrain(brain, base)
			a, s, k := m.Counts()
			t.Logf("row %d (%s): %d added / %d substituted / %d to review; glossary %v\n%s", r.n, r.classes, a, s, k, m.Glossary, describeHunks(m))
			if r.gloss == nil && r.classes != "" && strings.HasPrefix(r.classes, "pristine") {
				if len(m.Hunks) != 0 || !Pristine(brain) {
					t.Fatalf("row %d %s: pristine brain must yield no hunks, got %d (pristine=%v)", r.n, r.classes, len(m.Hunks), Pristine(brain))
				}
				return
			}
			if (a > 0) != r.added || (k > 0) != r.review || (s > 0) != (r.gloss != nil) {
				t.Errorf("row %d (%s): added %d / substituted %d / review %d; want added=%v glossary=%v review=%v\n%s",
					r.n, r.classes, a, s, k, r.added, r.gloss != nil, r.review, describeHunks(m))
			}
			if len(m.Glossary) != len(r.gloss) {
				t.Errorf("row %d (%s): glossary %+v, want %+v", r.n, r.classes, m.Glossary, r.gloss)
			} else {
				for i := range r.gloss {
					if m.Glossary[i] != r.gloss[i] {
						t.Errorf("row %d (%s): glossary[%d] = %+v, want %+v", r.n, r.classes, i, m.Glossary[i], r.gloss[i])
					}
				}
			}
			// Whatever the classes, the draft must round-trip: a Review block
			// exactly when a review hunk exists, a Glossary exactly when a map does.
			draft := RenderDraft("x", inventorySeed, m)
			if strings.Contains(draft, ReviewHeading) != r.review || strings.Contains(draft, "## Glossary") != (r.gloss != nil) {
				t.Errorf("row %d (%s): draft sections disagree with the classes:\n%s", r.n, r.classes, draft)
			}
		})
	}
}

func describeHunks(m Migration) string {
	var b strings.Builder
	for _, h := range m.Hunks {
		if h.Class == HunkSubstituted {
			continue
		}
		b.WriteString(string(h.Class) + " [" + h.Heading + "]\n")
		for _, l := range h.Seed {
			b.WriteString("  - " + l + "\n")
		}
		for _, l := range h.Brain {
			b.WriteString("  + " + l + "\n")
		}
	}
	return b.String()
}
