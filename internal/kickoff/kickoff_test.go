package kickoff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/provider"
)

var linearVerbs = provider.NewLinear("test-key").Verbs()

var mdVerbs = provider.NewMarkdownAt("/tmp/x/.grove/tasks", ".grove/tasks").Verbs()

var testTask = &provider.Task{
	ID:          "DEV-99",
	Title:       "Fix the frobnicator",
	URL:         "https://linear.app/x/issue/DEV-99",
	Description: "It frobs when it should nicate.",
	Comments:    []provider.Comment{{Author: "dean", Body: "see screenshot"}},
}

const sentinelContract = `STATUS: QUESTION — <the question, one line>
   STATUS: BLOCKED — <what is blocking you>
   STATUS: DONE — `

// goldenTask mirrors the fixture used to generate testdata/golden_linear_*
// from the pre-generalization (byte-identical ovs copy) templates.
var goldenTask = &provider.Task{
	ID:          "DEV-1234",
	Title:       "Persist filter state in the URL",
	Description: "Filters reset on reload.\n\nMake them survive.",
	URL:         "https://linear.app/grid/issue/DEV-1234",
	Labels:      []string{"frontend"},
	Comments: []provider.Comment{
		{Author: "dean", Body: "see screenshot"},
		{Author: "unknown", Body: "second comment\nwith two lines"},
	},
}

// TestLinearGoldenParity is the extraction guarantee: for the linear
// provider, the generalized render is byte-identical to the ovs-era output
// (empty learnings corpus, same fixture).
func TestLinearGoldenParity(t *testing.T) {
	for golden, mode := range map[string]Mode{
		"golden_linear_default.txt": ModeDefault,
		"golden_linear_manual.txt":  ModeManual,
		"golden_linear_pickup.txt":  ModePickup,
	} {
		want, err := os.ReadFile(filepath.Join("testdata", golden))
		if err != nil {
			t.Fatal(err)
		}
		got, err := Render(goldenTask, linearVerbs, "linear", "", mode, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s: render diverged from ovs-era output\n--- got ---\n%s\n--- want ---\n%s", golden, got, want)
		}
	}
}

func TestRenderDefaultUnchanged(t *testing.T) {
	got, err := Render(testTask, linearVerbs, "linear", "", ModeDefault, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"DEV-99: Fix the frobnicator",
		"It frobs when it should nicate.",
		"[dean]: see screenshot",
		"Work autonomously:",
		`Move the ticket to "In Progress" using the dev-linear Linear tools.`,
		sentinelContract,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("default render missing %q", want)
		}
	}
}

// grove-146: an empty brief must not alter existing renders (every other
// call site in this file passes "" and depends on byte-identical output).
func TestRenderNoBriefUnchanged(t *testing.T) {
	withBrief, err := Render(testTask, linearVerbs, "linear", "", ModeDefault, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(withBrief, "Operator brief") {
		t.Error("empty brief must not add an Operator brief section")
	}
}

func TestRenderOperatorBrief(t *testing.T) {
	got, err := Render(testTask, linearVerbs, "linear", "", ModeDefault, "Only touch the staging config, do not deploy.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "## Operator brief") {
		t.Fatalf("brief render missing section header:\n%s", got)
	}
	if !strings.Contains(got, "Only touch the staging config, do not deploy.") {
		t.Fatalf("brief render missing brief text:\n%s", got)
	}
	sentinelIdx := strings.Index(got, "STATUS: DONE")
	briefIdx := strings.Index(got, "## Operator brief")
	if sentinelIdx == -1 || briefIdx == -1 || briefIdx < sentinelIdx {
		t.Errorf("operator brief must come after ticket-derived content, got:\n%s", got)
	}
}

func TestRenderManualUnchanged(t *testing.T) {
	got, err := Render(testTask, linearVerbs, "linear", "", ModeManual, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "WAIT for my instructions") {
		t.Error("manual render missing wait instruction")
	}
	if strings.Contains(got, "Work autonomously") {
		t.Error("manual render must not contain the autonomous instructions")
	}
}

func TestRenderPickup(t *testing.T) {
	got, err := Render(testTask, linearVerbs, "linear", "", ModePickup, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"DEV-99: Fix the frobnicator",
		"https://linear.app/x/issue/DEV-99",
		"existing branch",
		"git log",
		sentinelContract,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("pickup render missing %q", want)
		}
	}
}

// The per-repo prompt override applies to the default mode only —
// pickup/manual are lifecycle-specific, not repo-specific. Pre-existing
// overrides use {{.Identifier}}, which must keep working as an alias.
func TestRenderOverrideIsolation(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom.tmpl")
	if err := os.WriteFile(custom, []byte("CUSTOM {{.Identifier}}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Render(testTask, linearVerbs, "linear", custom, ModeDefault, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "CUSTOM DEV-99" {
		t.Errorf("default mode should use the override, got %q", got)
	}

	for _, mode := range []Mode{ModeManual, ModePickup} {
		got, err := Render(testTask, linearVerbs, "linear", custom, mode, "")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "CUSTOM") {
			t.Errorf("mode %v must ignore the repo prompt override", mode)
		}
	}
}

// --- generic (markdown) template set ---

var mdTask = &provider.Task{
	ID:          "task-001",
	Title:       "Persist filter state in the URL",
	Description: "## Description\n\nFilters reset on reload.\n\n## Acceptance Criteria\n- [ ] Filters survive a reload",
	URL:         "/repo/.grove/tasks/task-001.md",
	Status:      "todo",
}

func TestRenderMarkdownDefault(t *testing.T) {
	got, err := Render(mdTask, mdVerbs, "markdown", "", ModeDefault, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"task task-001: Persist filter state in the URL",
		"Filters survive a reload",
		"status: in-progress",
		"status: review",
		"task-001: ...",
		"STATUS: QUESTION",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown default render missing %q\n%s", want, got)
		}
	}
	for _, banned := range []string{"Linear", "dev-linear", "wrapping-up-task", "pr-reviewer", "deploy/*", "In Progress", "In Review"} {
		if strings.Contains(got, banned) {
			t.Errorf("markdown render leaks provider/Grid-ism %q", banned)
		}
	}
	if strings.Contains(got, mdTask.URL) {
		t.Error("markdown render must not print the main-checkout file path")
	}
}

func TestRenderMarkdownManualAndPickup(t *testing.T) {
	man, err := Render(mdTask, mdVerbs, "markdown", "", ModeManual, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(man, "WAIT for my instructions") || strings.Contains(man, "Linear") {
		t.Errorf("markdown manual render wrong:\n%s", man)
	}

	pick, err := Render(mdTask, mdVerbs, "markdown", "", ModePickup, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"picking up task task-001", "git log", "status: in-progress", "STATUS: QUESTION"} {
		if !strings.Contains(pick, want) {
			t.Errorf("markdown pickup render missing %q", want)
		}
	}
	if strings.Contains(pick, "Linear") {
		t.Error("markdown pickup render leaks Linear")
	}
}
