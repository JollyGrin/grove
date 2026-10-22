package kickoff

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// TestLinearGoldenParity pins the linear set's full render against
// reviewed goldens. Until grove-433 the goldens were the ovs-era output
// (the extraction guarantee); grove-433 rewrote the autonomous templates to
// goals and constraints (docs/seed-manifest.md records the divergence) and
// the goldens now pin that shape — regenerate deliberately, review line by
// line, never to make a red test green.
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
		got, err := Render(goldenTask, linearVerbs, "linear", "", mode, "", "main", "")
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s: render diverged from ovs-era output\n--- got ---\n%s\n--- want ---\n%s", golden, got, want)
		}
	}
}

func TestRenderDefaultUnchanged(t *testing.T) {
	got, err := Render(testTask, linearVerbs, "linear", "", ModeDefault, "", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"DEV-99: Fix the frobnicator",
		"It frobs when it should nicate.",
		"[dean]: see screenshot",
		"Before you begin:",
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
	withBrief, err := Render(testTask, linearVerbs, "linear", "", ModeDefault, "", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(withBrief, "Operator brief") {
		t.Error("empty brief must not add an Operator brief section")
	}
}

func TestRenderOperatorBrief(t *testing.T) {
	got, err := Render(testTask, linearVerbs, "linear", "", ModeDefault, "Only touch the staging config, do not deploy.", "main", "")
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
	got, err := Render(testTask, linearVerbs, "linear", "", ModeManual, "", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "WAIT for my instructions") {
		t.Error("manual render missing wait instruction")
	}
	if strings.Contains(got, "Done means:") || strings.Contains(got, "STATUS: DONE") {
		t.Error("manual render must not contain the autonomous instructions")
	}
}

func TestRenderPickup(t *testing.T) {
	got, err := Render(testTask, linearVerbs, "linear", "", ModePickup, "", "main", "")
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

	got, err := Render(testTask, linearVerbs, "linear", custom, ModeDefault, "", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "CUSTOM DEV-99" {
		t.Errorf("default mode should use the override, got %q", got)
	}

	for _, mode := range []Mode{ModeManual, ModePickup} {
		got, err := Render(testTask, linearVerbs, "linear", custom, mode, "", "main", "")
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
	got, err := Render(mdTask, mdVerbs, "markdown", "", ModeDefault, "", "main", "")
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
	man, err := Render(mdTask, mdVerbs, "markdown", "", ModeManual, "", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(man, "WAIT for my instructions") || strings.Contains(man, "Linear") {
		t.Errorf("markdown manual render wrong:\n%s", man)
	}

	pick, err := Render(mdTask, mdVerbs, "markdown", "", ModePickup, "", "main", "")
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

// featureParagraph is the fixed paragraph every PR-mentioning template adds
// when the task rides a feature train (grove-374, feature trains Decision 3).
func featureParagraph(feature, base string) string {
	return fmt.Sprintf("This task belongs to feature `%s`. Your PR targets\n`%s`, not main. If you must catch up, rebase only on\n`origin/%s` — never on main.", feature, base, base)
}

// TestRenderNamesThePRBase covers the kickoff-03 acceptance criteria: every
// template that mentions a PR must name Base explicitly, and add the
// feature paragraph verbatim only when the task rides a feature train —
// across both template sets (linear, markdown) and both PR-mentioning
// modes (default, pickup).
func TestRenderNamesThePRBase(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		task  *provider.Task
		verbs provider.Verbs
		mode  Mode
	}{
		{"linear default", "linear", testTask, linearVerbs, ModeDefault},
		{"markdown default", "markdown", mdTask, mdVerbs, ModeDefault},
		{"linear pickup", "linear", testTask, linearVerbs, ModePickup},
		{"markdown pickup", "markdown", mdTask, mdVerbs, ModePickup},
	}
	for _, c := range cases {
		t.Run(c.name+"/no feature", func(t *testing.T) {
			got, err := Render(c.task, c.verbs, c.kind, "", c.mode, "", "main", "")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, "gh pr create --base main") {
				t.Errorf("%s: missing `gh pr create --base main`:\n%s", c.name, got)
			}
			if strings.Contains(got, "belongs to feature") {
				t.Errorf("%s: unexpected feature paragraph with no feature set:\n%s", c.name, got)
			}
		})
		t.Run(c.name+"/feature", func(t *testing.T) {
			got, err := Render(c.task, c.verbs, c.kind, "", c.mode, "", "feature/x", "x")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, "gh pr create --base feature/x") {
				t.Errorf("%s: missing `gh pr create --base feature/x`:\n%s", c.name, got)
			}
			if want := featureParagraph("x", "feature/x"); !strings.Contains(got, want) {
				t.Errorf("%s: missing feature paragraph verbatim, want:\n%s\ngot:\n%s", c.name, want, got)
			}
		})
	}
}

// sentinelBlock is the STATUS contract exactly as every autonomous
// template has carried it since the ovs era: three lines, three-space
// indent, this order, last. The DONE placeholder differs per template set.
const (
	sentinelBlockMD = "   STATUS: QUESTION — <the question, one line>\n" +
		"   STATUS: BLOCKED — <what is blocking you>\n" +
		"   STATUS: DONE — <one paragraph: what changed and how you verified it>\n"
	sentinelBlockLinear = "   STATUS: QUESTION — <the question, one line>\n" +
		"   STATUS: BLOCKED — <what is blocking you>\n" +
		"   STATUS: DONE — <one paragraph: what changed, what to click-test in the preview>\n"
)

// TestRenderEndsWithStatusSentinels (grove-433): every autonomous render —
// both template sets, default and pickup, with and without a feature train
// — ends with the three STATUS lines byte-identical to the pre-rewrite
// templates, in that order, exactly once; and carries no numbered step
// script, no "do not ask for confirmation" hedge (Claude Code injects the
// autonomy block itself — .claude/skills/claude-code-facts), and no ALLCAPS
// ALWAYS. The grep-level acceptance criteria of grove-433, enforced.
func TestRenderEndsWithStatusSentinels(t *testing.T) {
	stepLine := regexp.MustCompile(`(?m)^\s*[0-9]+\. `)
	cases := []struct {
		name  string
		kind  string
		task  *provider.Task
		verbs provider.Verbs
		mode  Mode
		want  string
	}{
		{"linear default", "linear", testTask, linearVerbs, ModeDefault, sentinelBlockLinear},
		{"linear pickup", "linear", testTask, linearVerbs, ModePickup, sentinelBlockLinear},
		{"markdown default", "markdown", mdTask, mdVerbs, ModeDefault, sentinelBlockMD},
		{"markdown pickup", "markdown", mdTask, mdVerbs, ModePickup, sentinelBlockMD},
	}
	for _, c := range cases {
		for _, f := range []struct{ base, feature string }{{"main", ""}, {"feature/x", "x"}} {
			name := c.name
			if f.feature != "" {
				name += "/feature"
			}
			t.Run(name, func(t *testing.T) {
				got, err := Render(c.task, c.verbs, c.kind, "", c.mode, "", f.base, f.feature)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasSuffix(got, c.want) {
					t.Errorf("render must END with the sentinel block, got tail:\n%s", got[max(0, len(got)-300):])
				}
				if n := strings.Count(got, "STATUS: DONE"); n != 1 {
					t.Errorf("STATUS: DONE must appear exactly once, got %d", n)
				}
				if m := stepLine.FindString(got); m != "" {
					t.Errorf("render carries a numbered step line %q — the template states goals, not choreography", m)
				}
				for _, banned := range []string{"do not ask for confirmation", "ALWAYS", "Work autonomously"} {
					if strings.Contains(got, banned) {
						t.Errorf("render carries dropped wording %q", banned)
					}
				}
			})
		}
	}
}

// memoryParagraph is the grove-437 auto-memory paragraph every autonomous
// template carries, verbatim: where the line between the three memory
// surfaces is (auto memory for corrections and confirmed approaches,
// LEARNINGS.md for dated harness/tooling surprises, nothing the repo
// already records). The file format itself is Claude Code's own system
// prompt's job, so the paragraph names it in one clause and no more. The
// linear set says "ticket" where the generic set says "task".
func memoryParagraph(noun string) string {
	return "Your auto memory for this repo is shared by every worktree of it: read\n" +
		"its `MEMORY.md` before you start, and record corrections and confirmed\n" +
		"approaches there as you go — one lesson per file, a one-line summary at\n" +
		"the top, and why it mattered. A verified surprise about the harness or\n" +
		"the tooling that every machine needs goes to the repo's LEARNINGS.md\n" +
		"instead, dated, when the repo keeps one. Don't save what the repo, the\n" +
		noun + " or git already records.\n"
}

// TestRenderMemoryParagraph (grove-437): every autonomous render — both
// sets, default and pickup — carries the memory paragraph exactly once,
// before the Done/STATUS tail; the manual prompts (wait for instructions)
// carry none of it.
func TestRenderMemoryParagraph(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		task  *provider.Task
		verbs provider.Verbs
		mode  Mode
		noun  string
	}{
		{"linear default", "linear", testTask, linearVerbs, ModeDefault, "ticket"},
		{"linear pickup", "linear", testTask, linearVerbs, ModePickup, "ticket"},
		{"markdown default", "markdown", mdTask, mdVerbs, ModeDefault, "task"},
		{"markdown pickup", "markdown", mdTask, mdVerbs, ModePickup, "task"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Render(c.task, c.verbs, c.kind, "", c.mode, "", "main", "")
			if err != nil {
				t.Fatal(err)
			}
			want := memoryParagraph(c.noun)
			if n := strings.Count(got, want); n != 1 {
				t.Errorf("memory paragraph must appear exactly once, got %d:\n%s", n, got)
			}
			if strings.Index(got, want) > strings.Index(got, "STATUS: DONE") {
				t.Error("memory paragraph must come before the STATUS block")
			}
		})
	}
	for _, c := range []struct {
		kind  string
		task  *provider.Task
		verbs provider.Verbs
	}{{"linear", testTask, linearVerbs}, {"markdown", mdTask, mdVerbs}} {
		got, err := Render(c.task, c.verbs, c.kind, "", ModeManual, "", "main", "")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "auto memory") {
			t.Errorf("%s manual render must not carry the memory paragraph", c.kind)
		}
	}
}
