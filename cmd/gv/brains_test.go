package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/bootstrap"
	"github.com/JollyGrin/grove/orchestrator"
	"github.com/JollyGrin/grove/orchestrator/seeds"
)

func sweepRows() []bootstrap.BrainRow {
	return bootstrap.PlanSweep([]bootstrap.BrainProbe{
		{Label: "grove-repo", Root: "/w/grove", Exists: true, Content: "# hand written\n"},
		{Label: "deanlol", Root: "/w/deanlol", Exists: true, Content: bootstrap.StampSeed("old seed") + "\n## mine\n"},
		{Label: "tableplace", Root: "/w/tableplace", Exists: true, Content: bootstrap.StampSeed("old seed"),
			Leftovers: []string{"CLAUDE.md.bak-seed-000000000000"}},
		{Label: "waterhouse", Root: "/w/waterhouse", Exists: true, Content: bootstrap.StampSeed("new seed")},
		{Label: "ghost", Root: "/w/ghost", RootGone: true},
	}, "new seed")
}

// The report's whole job is to stay readable at 11+ workspaces: only the
// rows that need attention get lines, the current ones collapse to a count.
func TestPrintBrainSweepCollapsesCurrentWorkspaces(t *testing.T) {
	var buf bytes.Buffer
	printBrainSweep(&buf, sweepRows(), false)
	out := buf.String()

	for _, want := range []string{
		"grove-repo", "unstamped", "--force-orchestrator-md",
		"deanlol", "stale", "cd /w/deanlol && gv init --only orchestrator-md",
		// grove-511: a pristine stale brain names the fleet-wide refresh,
		// with no `cd` — it runs from anywhere.
		"tableplace", "pristine", "→ gv brains --refresh\n",
		"1 pristine brain (grove's own, unedited) — `gv brains --refresh` replaces them in place",
		"ghost", "missing-root",
		"✓ 1 brain current",
		// Leftovers are named with their full path and never removed.
		"safe to delete once you're happy", "/w/tableplace/.grove/orchestrator/CLAUDE.md.bak-seed-000000000000",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sweep output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "waterhouse") {
		t.Errorf("an up-to-date workspace must collapse into the count, not get a row:\n%s", out)
	}
	// missing-root has nowhere to run anything: no command line for it.
	if strings.Contains(out, "cd /w/ghost") {
		t.Errorf("missing-root must not suggest a command:\n%s", out)
	}
	if strings.Contains(out, "cd /w/tableplace") {
		t.Errorf("the fleet-wide refresh must not be prefixed with a cd:\n%s", out)
	}
}

// grove-512: a real overlay shows on its brain's row and in a fleet count;
// the comments-only stub shows nowhere.
func TestPrintBrainSweepShowsOverlays(t *testing.T) {
	rows := bootstrap.PlanSweep([]bootstrap.BrainProbe{
		{Label: "edited", Root: "/w/edited", Exists: true, Content: bootstrap.StampSeed("old") + "\n## mine\n",
			Overlay: bootstrap.OverlayStub + "- The operator is Dean.\n- Backend: GitHub.\n"},
		{Label: "stubbed", Root: "/w/stubbed", Exists: true, Content: "# hand\n", Overlay: bootstrap.OverlayStub},
	}, "new")
	var buf bytes.Buffer
	printBrainSweep(&buf, rows, false)
	out := buf.String()
	for _, want := range []string{"overlay: 2 lines", "1 brain carries a CLAUDE.local.md overlay", "gv brains --migrate"} {
		if !strings.Contains(out, want) {
			t.Errorf("sweep output missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "overlay:") != 1 {
		t.Errorf("the stub-only brain must not show an overlay:\n%s", out)
	}
	var quiet bytes.Buffer
	printBrainSweep(&quiet, bootstrap.PlanSweep([]bootstrap.BrainProbe{
		{Label: "a", Root: "/w/a", Exists: true, Content: bootstrap.StampSeed("s"), Overlay: bootstrap.OverlayStub},
	}, "s"), false)
	if got := quiet.String(); got != "✓ 1 brain current\n" {
		t.Errorf("stub-only fleet must stay one line, got %q", got)
	}
}

// The never-overwrite invariant has to be visible in the report itself —
// the operator reads this, not grove-190's source comments.
func TestPrintBrainSweepStatesTheNeverOverwriteInvariant(t *testing.T) {
	var buf bytes.Buffer
	printBrainSweep(&buf, sweepRows(), false)
	out := buf.String()
	if !strings.Contains(out, "never overwrites an edited brain") || !strings.Contains(out, "CLAUDE.md.new") {
		t.Errorf("output must say grove never overwrites an edited brain and names CLAUDE.md.new:\n%s", out)
	}
	if !strings.Contains(out, "4 of 5 brains need attention") {
		t.Errorf("output must count what is behind:\n%s", out)
	}
}

// An all-current fleet is one quiet line — the routine case after most
// updates, and the reason the report is worth reading at all.
func TestPrintBrainSweepAllCurrentIsOneLine(t *testing.T) {
	rows := bootstrap.PlanSweep([]bootstrap.BrainProbe{
		{Label: "a", Root: "/w/a", Exists: true, Content: bootstrap.StampSeed("s")},
		{Label: "b", Root: "/w/b", Exists: true, Content: bootstrap.StampSeed("s")},
	}, "s")
	var buf bytes.Buffer
	printBrainSweep(&buf, rows, false)
	if got := buf.String(); got != "✓ 2 brains current\n" {
		t.Errorf("all-current sweep = %q, want one line", got)
	}
}

func TestPrintBrainSweepEmptyRegistry(t *testing.T) {
	var buf bytes.Buffer
	printBrainSweep(&buf, nil, false)
	if !strings.Contains(buf.String(), "no workspaces registered") {
		t.Errorf("empty registry = %q", buf.String())
	}
}

// Under --refresh (grove-511) the rows just acted on print with a ✓ and
// count as done; the edited brain still points at its .new to diff, and
// nothing offers `gv brains --refresh` as the fix for itself.
func TestPrintBrainSweepRefreshShowsWhatItDid(t *testing.T) {
	base := t.TempDir()
	mk := func(label, content string) bootstrap.BrainProbe {
		root := filepath.Join(base, label)
		orch := bootstrap.OrchestratorDirAt(root)
		if err := os.MkdirAll(orch, 0o755); err != nil {
			t.Fatal(err)
		}
		if content != "" {
			if err := os.WriteFile(filepath.Join(orch, bootstrap.BrainFile), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return bootstrap.ProbeBrain(label, root)
	}
	rows, err := bootstrap.RefreshSweep([]bootstrap.BrainProbe{
		mk("pristine", bootstrap.StampSeed("old seed")),
		mk("edited", bootstrap.StampSeed("old seed")+"\n## mine\n"),
		mk("handmade", "# mine\n"),
		mk("current", bootstrap.StampSeed("new seed")),
		bootstrap.ProbeGlobalBrain(filepath.Join(base, "global")),
	}, "new seed")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	printBrainSweep(&buf, rows, true)
	out := buf.String()
	for _, want := range []string{
		"✓ pristine", "replaced in place",
		"✓ (global)", "installed the orchestrator brain",
		"edited", "wrote CLAUDE.md.new", "→ diff " + filepath.Join(base, "edited", ".grove", "orchestrator", "CLAUDE.md") + " ",
		"handmade", "hand-managed",
		"2 of 5 brains need attention",
		"✓ 2 brains refreshed",
		"✓ 1 brain current",
		"safe to delete", filepath.Join(base, "edited", ".grove", "orchestrator", "CLAUDE.md.new"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("refresh output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "replaces them in place") {
		t.Errorf("a refresh run must not advertise itself as the next step:\n%s", out)
	}
}

// The post-update report shells out to the REPLACED binary — this process
// still carries the OLD embedded seed, so an in-process sweep would report
// every workspace current against the seed the update just superseded.
// Since grove-511 (design OQ1, decided: automatic) it runs `brains
// --refresh`, so the pristine brains are brought current in the same
// step, and the header says so.
func TestReportBrainSweepAfterUpdateRunsTheReplacedBinaryRefresh(t *testing.T) {
	if !updateRefreshesBrains {
		t.Fatal("gv update must refresh brains automatically (OQ1 decided 2026-10-24)")
	}
	stub := filepath.Join(t.TempDir(), "gv")
	script := "#!/bin/sh\n[ \"$1\" = brains ] && [ \"$2\" = --refresh ] && [ $# = 2 ] || { echo \"wrong verb: $*\" >&2; exit 2; }\necho NEW-BINARY-REFRESH\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { reportBrainSweepAfterUpdate(stub) })
	if !strings.Contains(out, "NEW-BINARY-REFRESH") {
		t.Errorf("post-update report must run the replaced binary's refresh and print its report, got %q", out)
	}
	if !strings.Contains(out, "refreshed against the seed the new binary carries") {
		t.Errorf("header must say the brains were refreshed, not only swept, got %q", out)
	}
}

// It is a report hung off a finished update, never part of it: a binary
// that cannot sweep (an older release without the verb) warns and returns.
func TestReportBrainSweepAfterUpdateNeverFails(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "gv")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho boom >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// No panic, no os.Exit: the warning goes to stderr and update exits 0.
	out := captureStdout(t, func() { reportBrainSweepAfterUpdate(stub) })
	if out != "" {
		t.Errorf("a failed sweep must print nothing on stdout, got %q", out)
	}
}

// grove-513: the dry-run report names each draft and its path, prints a
// tracked brain's two git commands (and runs none — this is a printer), and
// collapses the nothing-to-migrate rows into a count.
func TestPrintBrainMigrate(t *testing.T) {
	base := "# seed\n\n## Guardrails\n\n- **Propose, then act.** Never grab without the operator's yes.\n- **Never post ticket comments** without the operator's sign-off.\n"
	stamp := bootstrap.SeedStamp(base)
	brain := func(body string) string { return body + "\n<!-- grove-seed: " + stamp + " -->\n" }
	probe := func(label, content string, tracked bool) bootstrap.MigrateProbe {
		return bootstrap.MigrateProbe{BrainProbe: bootstrap.BrainProbe{Label: label, Root: "/w/" + label,
			Dir: "/w/" + label + "/.grove/orchestrator", Exists: content != "", Content: content}, Tracked: tracked}
	}
	rows := bootstrap.PlanMigrate([]bootstrap.MigrateProbe{
		probe("pristine", bootstrap.StampSeed(base), false),
		probe("thegrid", brain(strings.ReplaceAll(base, "the operator", "Dean")+"\n## Browser (CDP)\n\nnever open it unprompted\n"), true),
		probe("unbrewed", brain(strings.Replace(base, "Never post ticket comments", "Comments are free", 1)), false),
		probe("handmade", "# mine\n", false),
		{BrainProbe: bootstrap.BrainProbe{Label: "(global)", Root: "/g", Dir: "/g", Exists: true,
			Content: "other seed, edited\n<!-- grove-seed: 000000000000 -->\n", Leftovers: []string{"CLAUDE.md.new"}}},
	}, "# newer seed\n", func(s string) (string, bool) {
		if s == stamp {
			return base, true
		}
		return "", false
	})
	var buf bytes.Buffer
	printBrainMigrate(&buf, rows)
	out := buf.String()
	for _, want := range []string{
		"thegrid", "draft", "1 added / 1 substituted / 0 to review → CLAUDE.local.md.draft",
		"→ /w/thegrid/.grove/orchestrator/CLAUDE.local.md.draft",
		"git tracks this brain",
		"cd /w/thegrid && git add -f .grove/orchestrator/CLAUDE.local.md",
		"cd /w/thegrid && git rm --cached .grove/orchestrator/CLAUDE.md",
		"unbrewed", "0 added / 0 substituted / 1 to review",
		"handmade", "unknown-base", "no seed stamp", bootstrap.UnknownBaseNote,
		"(global)", "is not in the archive",
		"2 drafts drafted — dry run, nothing else written",
		"1 draft carries a `Review before applying` block",
		"✓ 1 brain has nothing to migrate",
		"leftovers — safe to delete", "/g/CLAUDE.md.new",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("migrate report missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\n  pristine") {
		t.Errorf("a pristine brain collapses into the count:\n%s", out)
	}
	if strings.Contains(out, "cd /w/unbrewed && git") {
		t.Errorf("an untracked brain gets no git commands:\n%s", out)
	}
}

// grove-513, the realism check: the fleet's commonest edit — every `<task>`
// in the REAL seed swapped for `grove-N`, every `the operator` for `Dean` —
// must come out as glossary lines alone, no Review block. And the archive
// finds the real seed's base by stamp.
func TestMigrateRealSeedSubstitutionIsGlossaryOnly(t *testing.T) {
	seed := orchestrator.ClaudeMd
	stamp := bootstrap.SeedStamp(seed)
	base, ok := seeds.Lookup(stamp)
	if !ok {
		t.Fatalf("archive lacks the embedded seed %s — run `go generate ./orchestrator/seeds`", stamp)
	}
	edited := strings.ReplaceAll(strings.ReplaceAll(seed, "<task>", "grove-N"), "the operator", "Dean")
	brain := bootstrap.SeedBody(edited) + "\n\n<!-- grove-seed: " + stamp + " -->\n"
	m := bootstrap.ClassifyBrain(brain, base)
	a, s, r := m.Counts()
	if a != 0 || r != 0 || s == 0 {
		var reviews []string
		for _, h := range m.Hunks {
			if h.Class != bootstrap.HunkSubstituted {
				reviews = append(reviews, fmt.Sprintf("%s [%s]\n  seed:  %q\n  brain: %q", h.Class, h.Heading, h.Seed, h.Brain))
			}
		}
		t.Fatalf("counts %d/%d/%d, want 0/n/0; non-substituted hunks:\n%s", a, s, r, strings.Join(reviews, "\n"))
	}
	// Glossary order is first occurrence in the seed, which moves as the
	// seed is edited — assert the set, not the order.
	got := map[string]string{}
	for _, g := range m.Glossary {
		got[g.From] = g.To
	}
	if len(m.Glossary) != 2 || got["<task>"] != "grove-N" || got["the operator"] != "Dean" {
		t.Errorf("glossary %+v", m.Glossary)
	}
}
