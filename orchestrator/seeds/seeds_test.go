package seeds_test

import (
	"testing"

	"github.com/JollyGrin/grove/internal/bootstrap"
	"github.com/JollyGrin/grove/orchestrator"
	"github.com/JollyGrin/grove/orchestrator/seeds"
)

// Every entry is filed under the stamp of its own body — the key a brain
// carries is the key the migration looks up, with the same function.
func TestArchiveKeysAreTheirStamps(t *testing.T) {
	if err := seeds.Err(); err != nil {
		t.Fatalf("archive unreadable: %v", err)
	}
	stamps := seeds.Stamps()
	if len(stamps) < 20 {
		t.Fatalf("archive holds %d seeds, want the full history (about 27) — run `go generate ./orchestrator/seeds`", len(stamps))
	}
	for _, s := range stamps {
		body, ok := seeds.Lookup(s)
		if !ok {
			t.Fatalf("Stamps lists %s but Lookup misses it", s)
		}
		if got := bootstrap.SeedStamp(body); got != s {
			t.Errorf("entry %s hashes to %s — the archive is corrupt; regenerate it", s, got)
		}
		if body != bootstrap.SeedBody(body) {
			t.Errorf("entry %s is not stored as a bare body (stamp line or trailing newlines present)", s)
		}
	}
}

// The two stamps every live brain carried at design time (brain-overlay
// design §Inventory): a6a0462035c0 on all 20 workspace brains,
// 16a561b74957 on the groveremote global brain.
func TestArchiveHoldsFleetStamps(t *testing.T) {
	for _, s := range []string{"a6a0462035c0", "16a561b74957"} {
		if _, ok := seeds.Lookup(s); !ok {
			t.Errorf("archive missing seed %s", s)
		}
	}
	if _, ok := seeds.Lookup("000000000000"); ok {
		t.Error("an unknown stamp must miss")
	}
}

// The seed this binary ships is always in the archive, so a brain seeded
// today has a base tomorrow — and so a seed edit forces `go generate`.
func TestArchiveHoldsCurrentSeed(t *testing.T) {
	want := bootstrap.SeedStamp(orchestrator.ClaudeMd)
	body, ok := seeds.Lookup(want)
	if !ok {
		t.Fatalf("archive lacks the embedded seed's stamp %s — orchestrator/CLAUDE.md changed; run `go generate ./orchestrator/seeds` and commit %s", want, seeds.ArchiveFile)
	}
	if body != bootstrap.SeedBody(orchestrator.ClaudeMd) {
		t.Error("archived body for the current stamp differs from the embedded seed")
	}
}
