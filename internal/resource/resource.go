// Package resource is grove's runtime pressure gauge: a cheap read of macOS
// available memory plus a live-worker count, feeding the cockpit gauge and a
// capped resource.jsonl breadcrumb/sample log. It exists because spawning one
// worker too many can push RAM over the cliff — macOS jetsam then reaps the
// backgrounded tmux server and every session dies at once, with no crash
// report. This package makes that pressure visible and recoverable after the
// fact (grove-3).
//
// Reads are pure sysctls (see resource_darwin.go) — no shelling out on the 1s
// tick. The compute + logging halves here are platform-neutral so they unit
// test anywhere; the raw counter read is the only darwin-specific piece.
package resource

import (
	"bytes"
	"os/exec"
)

// Mem is a single memory reading. AvailBytes is *reclaimable* memory — free
// plus the eviction-without-swap pages (speculative/purgeable/file-backed) —
// not the misleading "free" figure, which stays low on a healthy macOS.
type Mem struct {
	AvailBytes uint64
	TotalBytes uint64
}

// pageStats are the raw mach VM counters (in pages) plus page/total sizes, as
// read by Read() on darwin. Split out so computeMem is testable from a fixture.
type pageStats struct {
	pageSize    uint64
	free        uint64
	speculative uint64
	purgeable   uint64
	external    uint64 // file-backed pages — evictable without swapping
	total       uint64 // hw.memsize, bytes
}

// computeMem folds raw page counters into an available/total byte pair. It is
// the tested seam: Read() reads live sysctls into pageStats and calls this.
func computeMem(s pageStats) Mem {
	avail := (s.free + s.speculative + s.purgeable + s.external) * s.pageSize
	avail = min(avail, s.total) // reclaimable can transiently overshoot; clamp
	return Mem{AvailBytes: avail, TotalBytes: s.total}
}

// AvailGB / TotalGB render the reading in gibibytes for the gauge.
func (m Mem) AvailGB() float64 { return float64(m.AvailBytes) / (1 << 30) }
func (m Mem) TotalGB() float64 { return float64(m.TotalBytes) / (1 << 30) }

// OK reports whether the reading is usable (Read succeeded / platform
// supported). A zero total means the read failed — callers skip the gauge.
func (m Mem) OK() bool { return m.TotalBytes > 0 }

// Level buckets available memory against a floor for color-coding.
type Level int

const (
	Green Level = iota // plenty of headroom
	Amber              // getting tight — drain before grabbing
	Red                // near the cliff — one more spawn may trip jetsam
)

// Floors are deliberately absolute-byte constants, not fractions: jetsam fires
// on absolute pressure, not a percentage of a big machine. These are the knobs
// to tune from real resource.jsonl data once it accumulates (the follow-up
// grab-preflight ticket reads the same Level).
const (
	floorRed   = 2 * (1 << 30) // < 2 GiB available → red
	floorAmber = 4 * (1 << 30) // < 4 GiB available → amber
)

// Level classifies the reading against the floors. An unusable reading reports
// Green so a failed read never masquerades as pressure.
func (m Mem) Level() Level {
	switch {
	case !m.OK():
		return Green
	case m.AvailBytes < floorRed:
		return Red
	case m.AvailBytes < floorAmber:
		return Amber
	default:
		return Green
	}
}

// workerComm is the process name every Claude Code session runs under (the
// argv0 that `ps`/`pgrep` report — NOT the kernel's kp_proc.p_comm, which
// reads "node" for these and can't be told apart from unrelated node tooling).
const workerComm = "claude"

// LiveWorkers counts Claude worker processes machine-wide — every `claude`
// across all tmux sessions, ovs, and orchestrator chats, which is the number
// that predicts jetsam, not the handful grove itself spawned. It shells out to
// `pgrep -x claude`; the cockpit already runs tmux per tick (detect.DetectLive),
// so one more short-lived exec per second is negligible. A read error (pgrep
// missing, or no match → exit 1) yields 0 — the gauge degrades, never lies.
func LiveWorkers() int {
	out, _ := exec.Command("pgrep", "-x", workerComm).Output()
	return countPIDs(out)
}

// countPIDs counts the non-empty lines of pgrep output — the testable seam.
func countPIDs(out []byte) int {
	n := 0
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}
