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
		{Label: "deanlol", Root: "/w/deanlol", Exists: true, Content: bootstrap.StampSeed("old seed")},
		{Label: "waterhouse", Root: "/w/waterhouse", Exists: true, Content: bootstrap.StampSeed("new seed")},
		{Label: "ghost", Root: "/w/ghost", RootGone: true},
	}, "new seed")
}

// The report's whole job is to stay readable at 11+ workspaces: only the
// rows that need attention get lines, the current ones collapse to a count.
func TestPrintBrainSweepCollapsesCurrentWorkspaces(t *testing.T) {
	var buf bytes.Buffer
	printBrainSweep(&buf, sweepRows())
	out := buf.String()

	for _, want := range []string{
		"grove-repo", "unstamped", "--force-orchestrator-md",
		"deanlol", "stale", "cd /w/deanlol && gv init --only orchestrator-md",
		"ghost", "missing-root",
		"✓ 1 workspace current",
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
}

// The never-overwrite invariant has to be visible in the report itself —
// the operator reads this, not grove-190's source comments.
func TestPrintBrainSweepStatesTheNeverOverwriteInvariant(t *testing.T) {
	var buf bytes.Buffer
	printBrainSweep(&buf, sweepRows())
	out := buf.String()
	if !strings.Contains(out, "never overwrites") || !strings.Contains(out, "CLAUDE.md.new") {
		t.Errorf("output must say grove never overwrites a brain and names CLAUDE.md.new:\n%s", out)
	}
	if !strings.Contains(out, "3 of 4 workspaces need attention") {
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
	printBrainSweep(&buf, rows)
	if got := buf.String(); got != "✓ 2 workspaces current\n" {
		t.Errorf("all-current sweep = %q, want one line", got)
	}
}

func TestPrintBrainSweepEmptyRegistry(t *testing.T) {
	var buf bytes.Buffer
	printBrainSweep(&buf, nil)
	if !strings.Contains(buf.String(), "no workspaces registered") {
		t.Errorf("empty registry = %q", buf.String())
	}
}

// The post-update report shells out to the REPLACED binary — this process
// still carries the OLD embedded seed, so an in-process sweep would report
// every workspace current against the seed the update just superseded.
func TestReportBrainSweepAfterUpdateRunsTheReplacedBinary(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "gv")
	script := "#!/bin/sh\n[ \"$1\" = brains ] || { echo \"wrong verb: $*\" >&2; exit 2; }\necho NEW-BINARY-SWEEP\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { reportBrainSweepAfterUpdate(stub) })
	if !strings.Contains(out, "NEW-BINARY-SWEEP") {
		t.Errorf("post-update report must print the replaced binary's sweep, got %q", out)
	}
	if !strings.Contains(out, "the new binary carries") {
		t.Errorf("report needs a header saying whose seed it is, got %q", out)
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
