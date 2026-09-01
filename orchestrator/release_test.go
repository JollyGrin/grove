package orchestrator

import (
	"os"
	"strings"
	"testing"
)

// TestReleaseCoversOrchestrator guards the release trigger (grove-241):
// CLAUDE.md is embedded in the binary via //go:embed, so release.yml must
// fire on orchestrator/** pushes. A seed-only merge that misses the path
// filter is merged-but-never-shipped — the fixed seed sits on main and
// reaches no machine until an unrelated Go change happens to land, and
// `gv brains` everywhere reports every workspace current against a seed
// the binary never got.
func TestReleaseCoversOrchestrator(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("cannot read .github/workflows/release.yml: %v", err)
	}
	if !strings.Contains(string(data), "orchestrator/**") {
		t.Error("release.yml does not release on orchestrator/** — the seed is embedded " +
			"in the binary, so a seed-only merge would silently never ship. " +
			"Add 'orchestrator/**' to on.push.paths.")
	}
}
