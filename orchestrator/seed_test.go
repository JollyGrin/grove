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
