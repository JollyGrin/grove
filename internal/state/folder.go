// Folder is the cockpit's incremental events.jsonl reader (grove-126).
// The 1s beat used to pay two full-log scans per tick (Load + ReadEvents)
// plus an unconditional tasks.json rewrite — costs that grow forever on an
// append-only log. A Folder remembers the byte offset already consumed and
// the running fold state, so each Refresh parses only appended bytes and
// answers both the task map and the feed tail from that single pass.
// New file, same package: state.go stays byte-comparable with ovs.
package state

import (
	"bufio"
	"encoding/json"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type Folder struct {
	mu       sync.Mutex
	stateDir string
	tailCap  int              // feed tail bound; 0 = unbounded (don't)
	offset   int64            // bytes of events.jsonl already consumed
	tasks    map[string]*Task // running fold state
	tail     []Event          // last tailCap events, oldest-first
	viewHash uint64           // fnv-64a of the last tasks.json written
	wrote    bool             // view written at least once this process
}

func NewFolder(stateDir string, tailCap int) *Folder {
	return &Folder{stateDir: stateDir, tailCap: tailCap, tasks: map[string]*Task{}}
}

// Refresh consumes any bytes appended since the last call and returns the
// folded task map plus the feed tail (oldest-first) — the same shapes Load
// and ReadEvents answer, from one pass. Tasks are per-call copies: the
// render loop holds a result across ticks while later folds mutate the
// internal structs in place. The derived tasks.json view is rewritten only
// when the folded result actually changed (grove-126 item 2 — this also
// shrinks the #121 non-atomic-write race window).
//
// Unlike Load, which stops folding at the first malformed line, a
// complete-but-malformed line is skipped and folding continues (matching
// ReadEvents) — after a crash-torn append buried by later appends, that
// keeps the log's tail alive instead of silently dropping it.
func (f *Folder) Refresh() (map[string]*Task, []Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	dirty, err := f.consume()
	if err != nil {
		return nil, nil, err
	}
	if dirty || !f.wrote {
		f.writeView()
	}

	tasks := make(map[string]*Task, len(f.tasks))
	for k, t := range f.tasks {
		c := *t
		tasks[k] = &c
	}
	tail := make([]Event, len(f.tail))
	copy(tail, f.tail)
	return tasks, tail, nil
}

// consume advances over newly appended complete lines, folding each event.
// Reports whether anything folded (i.e. the derived view may differ).
func (f *Folder) consume() (bool, error) {
	file, err := os.Open(eventsPath(f.stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			// Log gone: an empty fleet, and any prior state is a ghost.
			dirty := len(f.tasks) > 0 || len(f.tail) > 0
			f.reset()
			return dirty, nil
		}
		return false, err
	}
	defer file.Close()

	st, err := file.Stat()
	if err != nil {
		return false, err
	}
	dirty := false
	if st.Size() < f.offset {
		// The log is append-only; a shrink means the file was replaced
		// (scratch e2e reuse, manual surgery). Refold from scratch.
		f.reset()
		dirty = true
	}
	if st.Size() == f.offset {
		return dirty, nil
	}
	if _, err := file.Seek(f.offset, io.SeekStart); err != nil {
		return dirty, err
	}
	// bufio.Reader, not Scanner: no line-length cap to silently trip (#123).
	r := bufio.NewReader(file)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			// No trailing newline — a torn append still in flight. Leave it
			// unconsumed; the completed line folds on a later tick.
			break
		}
		f.offset += int64(len(line))
		var ev Event
		if json.Unmarshal(line, &ev) != nil {
			continue // complete but malformed: skip, it will never become valid
		}
		fold(f.tasks, ev)
		f.tail = append(f.tail, ev)
		if f.tailCap > 0 && len(f.tail) > f.tailCap {
			f.tail = f.tail[1:]
		}
		dirty = true
	}
	return dirty, nil
}

func (f *Folder) reset() {
	f.offset = 0
	f.tasks = map[string]*Task{}
	f.tail = nil
}

// writeView refreshes the derived tasks.json, skipping the disk write when
// the marshaled view is byte-identical to the last one written (a folded
// event may still be a view no-op, e.g. ticket-less feed events).
func (f *Folder) writeView() {
	view, err := json.MarshalIndent(tasksSlice(f.tasks), "", "  ")
	if err != nil {
		return
	}
	h := fnv.New64a()
	h.Write(view)
	sum := h.Sum64()
	if f.wrote && sum == f.viewHash {
		return
	}
	if os.WriteFile(filepath.Join(f.stateDir, "tasks.json"), view, 0o644) == nil {
		f.viewHash = sum
		f.wrote = true
	}
}
