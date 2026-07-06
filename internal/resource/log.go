package resource

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Sample kinds. "sample" is the 1Hz cockpit trajectory; the discrete kinds are
// spawn breadcrumbs — the tipping spawn survives in the log even when the tmux
// server (and its sessions) do not.
const (
	KindSample       = "sample"
	KindGrab         = "grab"
	KindOrchestrator = "orchestrator"
)

// Sample is one line in resource.jsonl. It is deliberately NOT a state.Event:
// these live in their own file so state.Load() never folds them — a 1Hz
// sample stream would bloat events.jsonl and slow every read (grove-3 hard
// constraint).
type Sample struct {
	Time    time.Time `json:"time"`
	Avail   uint64    `json:"avail_mem"`
	Total   uint64    `json:"total_mem,omitempty"`
	Workers int       `json:"worker_count"`
	Kind    string    `json:"kind"`
	Ticket  string    `json:"ticket,omitempty"`
}

// maxBytes caps resource.jsonl. On overflow it is ring-truncated to the last
// keepBytes so it never grows unbounded (acceptance criterion). ~80-byte lines
// → ~13k samples per cap window, hours of 1Hz trajectory retained.
const (
	maxBytes  = 1 << 20 // 1 MiB
	keepBytes = maxBytes / 2
)

func logPath(dir string) string { return filepath.Join(dir, "resource.jsonl") }

// Log appends one sample under an exclusive flock, ring-truncating the file in
// place when it exceeds the cap. Best-effort telemetry: it uses the same
// lock discipline as state.Append (concurrent gv processes may write spawn
// breadcrumbs while the cockpit writes samples), but a failure here never
// blocks a grab or a tick — callers ignore the error.
func Log(stateDir string, s Sample) error {
	if s.Time.IsZero() {
		s.Time = time.Now()
	}
	line, err := json.Marshal(s)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	// O_RDWR (not O_APPEND) so the write and the in-place truncate happen on
	// one fd under one lock — no rename, no inode swap to race other writers.
	f, err := os.OpenFile(logPath(stateDir), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if _, err := f.Write(line); err != nil {
		return err
	}

	fi, err := f.Stat()
	if err != nil || fi.Size() <= maxBytes {
		return nil
	}
	// Over cap — rewrite in place with the tail. Rare (once per cap window),
	// so reading the whole file back is fine.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil // sample already durable; skip the trim this round
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	tail := tailFrom(data, keepBytes)
	if err := f.Truncate(0); err != nil {
		return nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	_, _ = f.Write(tail)
	return nil
}

// tailFrom returns the last whole lines of data fitting in ~target bytes,
// starting at a line boundary so the first retained line is never partial.
func tailFrom(data []byte, target int) []byte {
	if len(data) <= target {
		return data
	}
	cut := len(data) - target
	if i := bytes.IndexByte(data[cut:], '\n'); i >= 0 {
		cut += i + 1
	}
	return data[cut:]
}
