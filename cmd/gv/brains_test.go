package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/bootstrap"
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
