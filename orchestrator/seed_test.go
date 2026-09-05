package orchestrator

import (
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/remote"
)

// seedCoveredRemoteVerbs is the golden list of verbs orchestrator/CLAUDE.md
// is known to teach. When internal/remote.Supported changes, this test
// forces a human to confirm the seed was taught the new verb before the
// golden list is updated to match.
var seedCoveredRemoteVerbs = map[string]bool{
	"grab": true, "ls": true, "adopt": true, "handoff": true,
	"answer": true, "nudge": true, "diff": true, "pause": true,
	"untrack": true, "orchestrator": true,
}

// TestSeedDocumentsRemoteVerbs fails the moment internal/remote.Supported
// drifts from the verbs orchestrator/CLAUDE.md is known to teach.
func TestSeedDocumentsRemoteVerbs(t *testing.T) {
	for verb := range remote.Supported {
		if !seedCoveredRemoteVerbs[verb] {
			t.Errorf("remote verb set changed (%v): a verb that takes --host must be taught in "+
				"orchestrator/CLAUDE.md (duty 3 Dispatch + the tools block) before it ships. "+
				"Update the seed, then update seedCoveredRemoteVerbs here.", verb)
		}
	}
	for verb := range seedCoveredRemoteVerbs {
		if !remote.Supported[verb] {
			t.Errorf("remote verb set changed (%v): a verb that takes --host must be taught in "+
				"orchestrator/CLAUDE.md (duty 3 Dispatch + the tools block) before it ships. "+
				"Update the seed, then update seedCoveredRemoteVerbs here.", verb)
		}
	}
}

// TestSeedTeachesHostFlag guards the --host teaching added for grove-234
// against silent deletion.
func TestSeedTeachesHostFlag(t *testing.T) {
	if n := strings.Count(ClaudeMd, "--host"); n < 4 {
		t.Errorf("orchestrator/CLAUDE.md mentions --host only %d times (want >= 4): "+
			"the seed's --host teaching (duty 3 Dispatch + the tools block) has been trimmed — "+
			"restore it in orchestrator/CLAUDE.md", n)
	}
	if !strings.Contains(ClaudeMd, "gv grab DEV-X --host") {
		t.Error("orchestrator/CLAUDE.md is missing the `gv grab DEV-X --host` example — " +
			"restore it in orchestrator/CLAUDE.md's tools block")
	}
	if !strings.Contains(ClaudeMd, "handoff MOVES a task that is already running") {
		t.Error("orchestrator/CLAUDE.md is missing the `handoff MOVES a task that is already " +
			"running` warning that steers workers away from misusing gv handoff for remote " +
			"dispatch — restore it in orchestrator/CLAUDE.md's duty 3 Dispatch section")
	}
	if !strings.Contains(ClaudeMd, "BEFORE the ticket") {
		t.Error("orchestrator/CLAUDE.md is missing the relay-verb `BEFORE the ticket` " +
			"position rule (answer/nudge take --host only before the ticket; after it, " +
			"everything is payload) — restore it in orchestrator/CLAUDE.md's tools block " +
			"--host entry (grove-242)")
	}
}

// TestSeedTeachesSupervise guards the grove-253 `gv supervise` teaching
// against silent deletion: an orchestrator on a headless host (no desk
// cockpit) must know this verb exists instead of reinventing a monitor
// script.
func TestSeedTeachesSupervise(t *testing.T) {
	if !strings.Contains(ClaudeMd, "gv supervise") {
		t.Error("orchestrator/CLAUDE.md is missing `gv supervise` — restore it in the tools block")
	}
	if !strings.Contains(ClaudeMd, "pr_ready") {
		t.Error("orchestrator/CLAUDE.md is missing `pr_ready` — restore the Monitoring section's " +
			"delivery/liveness event-type list")
	}
}

// TestSeedTeachesLaneBilling guards the #234 lane-billing paragraph
// (zai-plan vs openrouter-) against silent deletion.
func TestSeedTeachesLaneBilling(t *testing.T) {
	if n := strings.Count(ClaudeMd, "zai-plan"); n < 2 {
		t.Errorf("orchestrator/CLAUDE.md mentions zai-plan only %d times (want >= 2): "+
			"the seed's lane-billing paragraph has been trimmed — restore it in orchestrator/CLAUDE.md", n)
	}
	if n := strings.Count(ClaudeMd, "openrouter-"); n < 2 {
		t.Errorf("orchestrator/CLAUDE.md mentions openrouter- only %d times (want >= 2): "+
			"the seed's lane-billing paragraph has been trimmed — restore it in orchestrator/CLAUDE.md", n)
	}
}

// TestSeedLabelsTicketNumbers guards the number-labelling rule: the
// operator cannot keep bare #N references straight, so every first
// mention carries a short parenthetical and multi-number messages end
// with a `Numbers` addendum. Added grove-wide 2026-09-05 after the rule
// lived only in one workspace's memory.
func TestSeedLabelsTicketNumbers(t *testing.T) {
	for _, want := range []string{
		"Label every ticket and PR number",
		"`Numbers` addendum",
	} {
		if !strings.Contains(ClaudeMd, want) {
			t.Errorf("orchestrator/CLAUDE.md is missing %q — the ticket-number labelling "+
				"guardrail has been trimmed; restore it in the Guardrails section", want)
		}
	}
}
