// Package watch is the read-only events.jsonl follower behind `gv watch`
// (grove-205): the supported way for a monitor — orchestrator, plugin, or
// operator shell script — to ask "tell me when this task changes state".
//
// It exists because every hand-rolled alternative misleads. Two false DONEs
// inside a minute on 2026-08-29 came from grepping a worker's tmux pane for
// `STATUS: DONE`: that line is in the kickoff prompt itself
// (internal/kickoff/md_default.tmpl:29), so it is in every worker's pane
// from second zero. The authoritative signal is the Stop hook's
// classification of the agent's OWN last message — prompt-proof by
// construction — appended to events.jsonl as an `agent_status` record. This
// package makes that stream consumable.
//
// Two invariants earn the trust:
//
//   - Baseline correct by construction. The default start offset is the
//     log's size at Tailer construction — i.e. at process start, before any
//     probe is armed. A caller can no longer sample a "before" snapshot
//     after the thing it is watching for has already happened.
//   - Pure read. Never appends, never rewrites the derived tasks.json (so
//     this is NOT state.Folder, which does both by design).
package watch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/state"
)

// DefaultTypes is the coverage contract: every terminal or actionable
// transition, not only the happy one. A crashed, wedged, parked, or
// silently-idle worker must still produce a line — silence must never read
// as success. Note that `agent_status` covers the idle stop with no STATUS
// sentinel (status "idle", sentinel "none"), which is exactly the shape a
// wandered-off agent emits.
var DefaultTypes = []string{
	state.EvAgentStatus,
	state.EvNotification,
	state.EvSessionEnded,
	state.EvTaskDone,
	state.EvTaskUntracked,
	state.EvTaskPaused,
	// grove-252: every delivery/liveness type is actionable or terminal —
	// the whole point of the supervisor train is that these transitions
	// stop needing a bash script watching `gh pr view` / tmux capture-pane.
	state.EvPROpened,
	state.EvPRUpdated,
	state.EvPRCIFailed,
	state.EvPRConflicting,
	state.EvPRReady,
	state.EvPRMerged,
	state.EvPRClosed,
	state.EvWorkerWaiting,
	state.EvWorkerVanished,
	state.EvWorkerErrored,
	state.EvWorkerRecovered,
}

// KnownTypes is the full event vocabulary `--type` accepts (docs/plugins.md).
// Validating against it turns a typo into an error instead of an
// indistinguishable silence — the same failure class this command exists to
// remove.
var KnownTypes = []string{
	state.EvTaskCreated,
	state.EvSessionStarted,
	state.EvAgentStatus,
	state.EvNotification,
	state.EvSessionEnded,
	state.EvAttached,
	state.EvAnswered,
	state.EvHumanStatus,
	state.EvTaskDone,
	state.EvTaskUntracked,
	state.EvTaskAdopted,
	state.EvTaskPaused,
	state.EvTaskHandedOff,
	state.EvOrchestratorClosed,
	state.EvOrchestratorSpawned,
	state.EvWorkspaceParked,
	state.EvPROpened,
	state.EvPRUpdated,
	state.EvPRCIFailed,
	state.EvPRConflicting,
	state.EvPRReady,
	state.EvPRMerged,
	state.EvPRClosed,
	state.EvWorkerWaiting,
	state.EvWorkerVanished,
	state.EvWorkerErrored,
	state.EvWorkerRecovered,
}

// KnownSentinels is the classifier's vocabulary (internal/hooks.classify):
// the three the kickoff prompt teaches, plus "none" for a stop with no
// STATUS line.
var KnownSentinels = []string{"question", "blocked", "done", "none"}

// TypeAll is the `--type` escape hatch: every record, known or not.
const TypeAll = "all"

// DefaultInterval is the poll beat. events.jsonl is appended under flock by
// short-lived hook processes; a quarter-second poll is well under human
// latency and costs one stat() on an idle fleet.
const DefaultInterval = 250 * time.Millisecond

// Options is one `gv watch` invocation.
type Options struct {
	StateDir  string
	Tickets   []string  // empty = every ticket
	Types     []string  // empty = DefaultTypes; [TypeAll] = everything
	Sentinels []string  // empty = no sentinel constraint
	Since     time.Time // zero = no time floor
	Replay    bool      // read the log from byte 0 (implied by Since)
	Until     string    // sentinel that ends the stream; empty = follow forever
	JSON      bool      // emit the raw event record instead of the human row
	Interval  time.Duration
}

// Filter decides whether one event reaches stdout.
type Filter struct {
	tickets   map[string]bool
	types     map[string]bool // nil = every type
	sentinels map[string]bool
	since     time.Time
}

// NewFilter builds the predicate. Empty ticket/sentinel sets are
// unconstrained; a nil types slice means DefaultTypes, and [TypeAll] means
// no type constraint at all.
func NewFilter(tickets, types, sentinels []string, since time.Time) Filter {
	f := Filter{tickets: set(tickets), sentinels: set(sentinels), since: since}
	switch {
	case len(types) == 0:
		f.types = set(DefaultTypes)
	case len(types) == 1 && types[0] == TypeAll:
		f.types = nil
	default:
		f.types = set(types)
	}
	return f
}

func set(vals []string) map[string]bool {
	if len(vals) == 0 {
		return nil
	}
	m := make(map[string]bool, len(vals))
	for _, v := range vals {
		m[v] = true
	}
	return m
}

// Match reports whether ev passes every active constraint.
func (f Filter) Match(ev state.Event) bool {
	if f.types != nil && !f.types[ev.Type] {
		return false
	}
	if f.tickets != nil && !f.tickets[ev.Ticket] {
		return false
	}
	if !f.since.IsZero() && ev.Time.Before(f.since) {
		return false
	}
	if f.sentinels != nil {
		// A sentinel constraint is a question about agent transitions;
		// nothing else can answer it.
		if ev.Type != state.EvAgentStatus || !f.sentinels[ev.Data["sentinel"]] {
			return false
		}
	}
	return true
}

// Tailer follows events.jsonl from a byte offset. It is the reader half of
// state.Folder's job with the folding and the tasks.json write removed —
// see the package doc for why that separation matters.
type Tailer struct {
	path   string
	offset int64
}

// NewTailer baselines the follower. fromStart=false (the default) starts at
// the log's current end, so only records appended after this call are ever
// seen — construction happens at process start, which is what makes the
// from-now guarantee structural rather than a caller's discipline. A log
// that does not exist yet baselines at 0: everything it ever gets is new.
func NewTailer(stateDir string, fromStart bool) *Tailer {
	t := &Tailer{path: filepath.Join(stateDir, "events.jsonl")}
	if !fromStart {
		if st, err := os.Stat(t.path); err == nil {
			t.offset = st.Size()
		}
	}
	return t
}

// Poll returns the complete lines appended since the last call, oldest
// first. A trailing line with no newline is a torn append still in flight:
// it is left unconsumed and returned by a later Poll once complete. A
// missing log is not an error — the fleet may simply not have started yet.
// A log that shrank was replaced (scratch reuse, manual surgery); the
// follower restarts from its byte 0, since every record in the new file is
// by definition one this process has not seen.
func (t *Tailer) Poll() ([][]byte, error) {
	f, err := os.Open(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			t.offset = 0
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() < t.offset {
		t.offset = 0
	}
	if st.Size() == t.offset {
		return nil, nil
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, err
	}
	// bufio.Reader, not Scanner: no line-length cap to silently trip (#123).
	r := bufio.NewReader(f)
	var lines [][]byte
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			break // no trailing newline: torn append, leave it unconsumed
		}
		t.offset += int64(len(line))
		lines = append(lines, line)
	}
	return lines, nil
}

// flusher is any writer that buffers. os.Stdout does not — each Write is a
// syscall — but a caller wrapping it must still see one event per line as
// it lands: the Claude Code Monitor tool turns each stdout LINE into one
// notification, and a buffered event is an event nobody ever sees.
type flusher interface{ Flush() error }

// Run follows the log until ctx is done, or until an `--until` sentinel
// lands. It reports whether that sentinel fired: `gv watch --until done`
// exiting 0 must mean "the transition happened", never "I was interrupted".
func Run(ctx context.Context, opts Options, out io.Writer) (fired bool, err error) {
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	filter := NewFilter(opts.Tickets, opts.Types, opts.Sentinels, opts.Since)
	tail := NewTailer(opts.StateDir, opts.Replay || !opts.Since.IsZero())

	for {
		lines, err := tail.Poll()
		if err != nil {
			return false, err
		}
		for _, raw := range lines {
			var ev state.Event
			if json.Unmarshal(raw, &ev) != nil {
				continue // complete but malformed: it will never become valid
			}
			if !filter.Match(ev) {
				continue
			}
			if err := emit(out, ev, raw, opts.JSON); err != nil {
				return false, err
			}
			if opts.Until != "" {
				// The original form: an agent_status sentinel landing.
				sentinelFired := ev.Type == state.EvAgentStatus && ev.Data["sentinel"] == opts.Until
				// grove-252: --until also accepts a bare event type, e.g.
				// `--until pr_merged` or `--until worker_waiting`.
				typeFired := ev.Type == opts.Until
				if sentinelFired || typeFired {
					return true, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return false, nil
		case <-time.After(interval):
		}
	}
}

func emit(out io.Writer, ev state.Event, raw []byte, asJSON bool) error {
	line := Row(ev)
	if asJSON {
		// The raw record, verbatim. Re-marshaling through state.Event would
		// silently drop any top-level field a newer writer added, which is
		// exactly the additive-only guarantee docs/plugins.md makes to
		// plugins — so the bytes pass through untouched.
		line = strings.TrimRight(string(raw), "\r\n")
	}
	if _, err := io.WriteString(out, line+"\n"); err != nil {
		return err
	}
	if f, ok := out.(flusher); ok {
		return f.Flush()
	}
	return nil
}

// detailCap bounds the human row's trailing detail, in runes (rune-safe,
// never mid-codepoint — grove-131 class).
const detailCap = 100

// Row renders one event as the human line: `HH:MM  ticket  label  detail`.
func Row(ev state.Event) string {
	ticket := ev.Ticket
	if ticket == "" {
		ticket = "-" // workspace-scoped record (workspace_parked, …)
	}
	return strings.TrimRight(fmt.Sprintf("%s  %-16s  %-13s  %s",
		ev.Time.Format("15:04"), ticket, Label(ev), detail(ev)), " ")
}

// Label is the transition's one-word name: the sentinel for a classified
// stop, the agent status for an unclassified one ("idle" — the wandered-off
// worker), the event type for everything else.
func Label(ev state.Event) string {
	if ev.Type != state.EvAgentStatus {
		return ev.Type
	}
	if s := ev.Data["sentinel"]; s != "" && s != "none" {
		return s
	}
	if st := ev.Data["status"]; st != "" {
		return st
	}
	return state.EvAgentStatus
}

func detail(ev state.Event) string {
	d := ev.Data
	s := d["question"]
	if s == "" {
		s = d["message"]
	}
	if s == "" && ev.Type == state.EvTaskHandedOff {
		s = "→ " + d["host"]
	}
	if s == "" {
		s = deliveryLivenessDetail(ev.Type, d)
	}
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > detailCap {
		s = string(r[:detailCap]) + "…"
	}
	return s
}

// deliveryLivenessDetail renders the trailing detail for the grove-252
// delivery/liveness types not covered by question/message/handoff above —
// e.g. `#98 · CLEAN` for pr_ready, `usage_limit · Usage limit reached…` for
// worker_errored.
func deliveryLivenessDetail(evType string, d map[string]string) string {
	switch evType {
	case state.EvPROpened, state.EvPRUpdated, state.EvPRMerged, state.EvPRClosed:
		return "#" + d["pr"]
	case state.EvPRCIFailed:
		return "#" + d["pr"] + " · " + d["failing"]
	case state.EvPRConflicting, state.EvPRReady:
		return "#" + d["pr"] + " · " + d["merge_state"]
	case state.EvWorkerWaiting:
		return d["marker"]
	case state.EvWorkerErrored:
		return d["reason"] + " · " + d["line"]
	case state.EvWorkerRecovered:
		return "recovered from " + d["from"]
	default:
		return ""
	}
}

// Validate checks the filter vocabularies up front. An unknown value is an
// error, never a silent empty stream.
func Validate(types, sentinels []string, until string) error {
	known := set(KnownTypes)
	for _, t := range types {
		if t != TypeAll && !known[t] {
			return fmt.Errorf("unknown event type %q (known: %s, or %q)",
				t, strings.Join(KnownTypes, ", "), TypeAll)
		}
	}
	knownSentinels := set(KnownSentinels)
	for _, s := range sentinels {
		if !knownSentinels[s] {
			return fmt.Errorf("unknown sentinel %q (known: %s)", s, strings.Join(KnownSentinels, ", "))
		}
	}
	// --until widens to an event type (grove-252: `--until pr_merged`,
	// `--until worker_waiting`) on top of the original sentinel form.
	if until != "" && !knownSentinels[until] && !known[until] {
		return fmt.Errorf("unknown --until value %q (want a sentinel: %s; or an event type: %s)",
			until, strings.Join(KnownSentinels, ", "), strings.Join(KnownTypes, ", "))
	}
	return nil
}

// Split parses a comma-separated flag value into a trimmed, non-empty list.
func Split(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
