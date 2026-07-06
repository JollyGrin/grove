package resource

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/state"
)

func TestComputeMem(t *testing.T) {
	// Fixture: 16 KiB pages, 36 GiB machine. Reclaimable = free + speculative
	// + purgeable + external.
	const page = 16384
	got := computeMem(pageStats{
		pageSize:    page,
		free:        337406,
		speculative: 197012,
		purgeable:   44977,
		external:    631677,
		total:       38654705664,
	})
	wantAvail := uint64((337406 + 197012 + 44977 + 631677) * page)
	if got.AvailBytes != wantAvail {
		t.Errorf("AvailBytes = %d, want %d", got.AvailBytes, wantAvail)
	}
	if got.TotalBytes != 38654705664 {
		t.Errorf("TotalBytes = %d, want 38654705664", got.TotalBytes)
	}
	if !got.OK() {
		t.Error("OK() = false, want true")
	}
}

func TestComputeMemClamps(t *testing.T) {
	// Counters can transiently sum past total; available must not exceed it.
	got := computeMem(pageStats{pageSize: 4096, free: 1 << 30, total: 8 << 30})
	if got.AvailBytes != got.TotalBytes {
		t.Errorf("AvailBytes = %d, want clamped to total %d", got.AvailBytes, got.TotalBytes)
	}
}

func TestLevel(t *testing.T) {
	cases := []struct {
		avail uint64
		want  Level
	}{
		{1 << 30, Red},          // 1 GiB
		{3 * (1 << 30), Amber},  // 3 GiB
		{10 * (1 << 30), Green}, // 10 GiB
	}
	for _, c := range cases {
		m := Mem{AvailBytes: c.avail, TotalBytes: 32 << 30}
		if got := m.Level(); got != c.want {
			t.Errorf("Level(%d GiB) = %v, want %v", c.avail>>30, got, c.want)
		}
	}
	// A failed read (zero total) reports Green, never phantom pressure.
	if (Mem{}).Level() != Green {
		t.Error("unusable reading should Level() Green")
	}
}

func TestLiveWorkers(t *testing.T) {
	tasks := []*state.Task{
		{Agent: state.AgentSetup},
		{Agent: state.AgentWorking},
		{Agent: state.AgentWorking},
		{Agent: state.AgentIdle},
		{Agent: state.AgentWaiting},
		{Agent: state.AgentBlocked},
		{Agent: state.AgentDead},
	}
	if got := LiveWorkers(tasks); got != 3 {
		t.Errorf("LiveWorkers = %d, want 3 (setup + 2 working)", got)
	}
	if got := LiveWorkers(nil); got != 0 {
		t.Errorf("LiveWorkers(nil) = %d, want 0", got)
	}
}

func TestTailFrom(t *testing.T) {
	data := []byte("aaaa\nbbbb\ncccc\ndddd\n")
	// Keep ~6 bytes: drops to a line boundary, retains the last whole line(s).
	got := string(tailFrom(data, 6))
	if strings.Contains(got, "aaaa") {
		t.Errorf("tailFrom kept the head: %q", got)
	}
	if !strings.HasSuffix(got, "dddd\n") {
		t.Errorf("tailFrom lost the tail: %q", got)
	}
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if line != "" && len(line) != 4 {
			t.Errorf("tailFrom left a partial line: %q", line)
		}
	}
	// Under target: unchanged.
	if string(tailFrom(data, 1000)) != string(data) {
		t.Error("tailFrom mutated data under target")
	}
}

func TestLogAppendsAndCaps(t *testing.T) {
	dir := t.TempDir()
	// Write well past the cap; the file must ring-truncate, not grow forever.
	for i := range 40000 {
		if err := Log(dir, Sample{Avail: uint64(i), Workers: i % 5, Kind: KindSample}); err != nil {
			t.Fatalf("Log: %v", err)
		}
	}
	fi, err := os.Stat(logPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > maxBytes {
		t.Errorf("resource.jsonl = %d bytes, exceeds cap %d", fi.Size(), maxBytes)
	}
	if fi.Size() == 0 {
		t.Fatal("resource.jsonl is empty after 40k writes")
	}
	// Every retained line must be valid JSON (no partial head line), and the
	// very last sample written must survive.
	raw, err := os.ReadFile(logPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	for _, l := range lines {
		var s Sample
		if err := json.Unmarshal([]byte(l), &s); err != nil {
			t.Fatalf("retained line is not valid JSON: %q (%v)", l, err)
		}
	}
	var last Sample
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last.Avail != 39999 {
		t.Errorf("last retained sample Avail = %d, want 39999", last.Avail)
	}
}

func TestLogNeverFoldedByState(t *testing.T) {
	// The hard constraint: samples live in their own file and must never be
	// folded into the task map. resource.jsonl and events.jsonl are distinct
	// paths, and state.Load reads only events.jsonl.
	dir := t.TempDir()
	if err := Log(dir, Sample{Avail: 123, Kind: KindGrab, Ticket: "grove-3"}); err != nil {
		t.Fatal(err)
	}
	tasks, err := state.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Errorf("state.Load folded resource.jsonl: got %d tasks, want 0", len(tasks))
	}
	if _, err := os.Stat(filepath.Join(dir, "resource.jsonl")); err != nil {
		t.Errorf("resource.jsonl missing: %v", err)
	}
}
