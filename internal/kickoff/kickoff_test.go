package kickoff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/linear"
)

var testIssue = &linear.Issue{
	Identifier:  "DEV-99",
	Title:       "Fix the frobnicator",
	URL:         "https://linear.app/x/issue/DEV-99",
	Description: "It frobs when it should nicate.",
	Comments:    []linear.Comment{{Author: "dean", Body: "see screenshot"}},
}

const sentinelContract = `STATUS: QUESTION — <the question, one line>
   STATUS: BLOCKED — <what is blocking you>
   STATUS: DONE — <one paragraph: what changed, what to click-test in the preview>`

func TestRenderDefaultUnchanged(t *testing.T) {
	got, err := Render(testIssue, "", ModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"DEV-99: Fix the frobnicator",
		"It frobs when it should nicate.",
		"[dean]: see screenshot",
		"Work autonomously:",
		sentinelContract,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("default render missing %q", want)
		}
	}
}

func TestRenderManualUnchanged(t *testing.T) {
	got, err := Render(testIssue, "", ModeManual)
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
	got, err := Render(testIssue, "", ModePickup)
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
// pickup/manual are lifecycle-specific, not repo-specific.
func TestRenderOverrideIsolation(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom.tmpl")
	if err := os.WriteFile(custom, []byte("CUSTOM {{.Identifier}}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Render(testIssue, custom, ModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	if got != "CUSTOM DEV-99" {
		t.Errorf("default mode should use the override, got %q", got)
	}

	for _, mode := range []Mode{ModeManual, ModePickup} {
		got, err := Render(testIssue, custom, mode)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "CUSTOM") {
			t.Errorf("mode %v must ignore the repo prompt override", mode)
		}
	}
}
