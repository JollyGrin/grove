// Package ledger is the persistent local spend history: cumulative
// per-ticket cost snapshots appended to <state>/ledger.csv so history
// survives Claude Code transcript pruning (~30 days) and worktree removal.
// Appends follow the events.jsonl discipline — O_APPEND + flock, tolerant
// of concurrent writers — and the file lives in the state dir, which is
// never committed (workspace .grove/.gitignore covers state/).
//
// Row shape (CSV columns, in order): time, ticket, title, desc, repo,
// branch, outcome, input, output, cache_create, cache_read, turns,
// est_usd, models. The trailing `models` column (added 2026-07-07,
// grove-14) captures the per-model mix — e.g. "fable 92% · haiku 8%" —
// at snapshot time so routing stays legible after transcripts prune. The
// reader is tolerant of the pre-existing 13-column shape: older rows
// simply read back with an empty Models field, never dropped.
//
// The recording toggle persists in <state>/cost-recording ("on"/"off"),
// NOT in config.yaml: the cockpit toggles it at runtime, and grove never
// rewrites human-edited config files. Config `cost: {record: true}` seeds
// the default for a state dir that has never been toggled.
package ledger

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JollyGrin/grove/internal/cost"
)

// Row is one cumulative spend snapshot for a ticket. Token counts, turns,
// and USD are running totals at Time, not deltas.
type Row struct {
	Time        time.Time
	Ticket      string
	Title       string
	Desc        string // short description: first ~200 chars of the ticket body, newlines collapsed
	Repo        string
	Branch      string
	Outcome     string // merged | closed | open | none
	Input       int
	Output      int
	CacheCreate int // 5m + 1h cache-write tokens combined
	CacheRead   int
	Turns       int
	USD         float64
	Models      string // compact per-model mix at snapshot time, e.g. "fable 92% · haiku 8%"
}

var header = []string{
	"time", "ticket", "title", "desc", "repo", "branch", "outcome",
	"input", "output", "cache_create", "cache_read", "turns", "est_usd",
	"models",
}

// Path returns the ledger file location inside a state dir.
func Path(stateDir string) string { return filepath.Join(stateDir, "ledger.csv") }

// Append writes one snapshot row under an exclusive flock, adding the CSV
// header when it creates the file. The record is encoded first and written
// with a single Write so concurrent appenders never interleave fields.
func Append(stateDir string, r Row) error {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	rec := []string{
		r.Time.UTC().Format(time.RFC3339), r.Ticket, r.Title, r.Desc,
		r.Repo, r.Branch, r.Outcome,
		strconv.Itoa(r.Input), strconv.Itoa(r.Output),
		strconv.Itoa(r.CacheCreate), strconv.Itoa(r.CacheRead),
		strconv.Itoa(r.Turns), strconv.FormatFloat(r.USD, 'f', 4, 64),
		r.Models,
	}
	if err := w.Write(rec); err != nil {
		return err
	}
	w.Flush()

	f, err := os.OpenFile(Path(stateDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	// Size check happens under the lock so exactly one writer adds the header.
	if fi, err := f.Stat(); err == nil && fi.Size() == 0 {
		var hb bytes.Buffer
		hw := csv.NewWriter(&hb)
		_ = hw.Write(header)
		hw.Flush()
		if _, err := f.Write(hb.Bytes()); err != nil {
			return err
		}
	}
	_, err = f.Write(buf.Bytes())
	return err
}

// Read parses the ledger tolerantly: the header, malformed lines, and rows
// with the wrong shape are skipped, never fatal — a torn concurrent write
// must not hide the rest of the history.
func Read(stateDir string) ([]Row, error) {
	f, err := os.Open(Path(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	var rows []Row
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		// Accept the pre-grove-14 13-column shape as well as the current
		// 14-column one: older rows read back with an empty Models field
		// rather than being dropped as malformed.
		if (len(rec) != 13 && len(rec) != len(header)) || rec[0] == "time" {
			continue
		}
		at, err := time.Parse(time.RFC3339, rec[0])
		if err != nil {
			continue
		}
		usd, _ := strconv.ParseFloat(rec[12], 64)
		models := ""
		if len(rec) > 13 {
			models = rec[13]
		}
		rows = append(rows, Row{
			Time: at, Ticket: rec[1], Title: rec[2], Desc: rec[3],
			Repo: rec[4], Branch: rec[5], Outcome: rec[6],
			Input: atoi(rec[7]), Output: atoi(rec[8]),
			CacheCreate: atoi(rec[9]), CacheRead: atoi(rec[10]),
			Turns: atoi(rec[11]), USD: usd, Models: models,
		})
	}
	return rows, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// Latest collapses snapshots to the newest row per ticket — rows are
// cumulative, so the last one is the ticket's current (or final) state.
func Latest(rows []Row) map[string]Row {
	out := map[string]Row{}
	for _, r := range rows {
		if prev, ok := out[r.Ticket]; !ok || r.Time.After(prev.Time) {
			out[r.Ticket] = r
		}
	}
	return out
}

// Snip collapses all whitespace runs (including newlines) to single spaces
// and truncates to n runes for the ledger's short-description field.
func Snip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Outcome maps a gh PR state (MERGED/CLOSED/OPEN) to the ledger's
// lowercase outcome vocabulary; unknown or absent states are "none".
func Outcome(prState string) string {
	switch prState {
	case "MERGED":
		return "merged"
	case "CLOSED":
		return "closed"
	case "OPEN":
		return "open"
	default:
		return "none"
	}
}

// --- recording toggle ---

func togglePath(stateDir string) string { return filepath.Join(stateDir, "cost-recording") }

// Enabled reports whether spend recording is on for a state dir. An
// explicit toggle file wins; absent, the config default (cost.record)
// applies.
func Enabled(stateDir string, configDefault bool) bool {
	raw, err := os.ReadFile(togglePath(stateDir))
	if err != nil {
		return configDefault
	}
	return strings.TrimSpace(string(raw)) == "on"
}

// SetRecording persists the toggle.
func SetRecording(stateDir string, on bool) error {
	v := "off"
	if on {
		v = "on"
	}
	return os.WriteFile(togglePath(stateDir), []byte(v+"\n"), 0o644)
}

// ModelDeltaSpend attributes each ticket's USD growth at or after since to
// models via the row's recorded mix (grove-87's BY MODEL totals): per
// ticket, each row's delta since the previous snapshot splits by that row's
// MixShares. Rows without a parseable mix (pre-grove-14 13-column rows)
// land under "other" — visible, never silently dropped. Tickets in exclude
// are skipped: their window is counted precisely from live transcripts, and
// double counting would inflate the totals.
func ModelDeltaSpend(rows []Row, exclude map[string]bool, since time.Time) map[string]float64 {
	byTicket := map[string][]Row{}
	for _, r := range rows {
		if exclude[r.Ticket] {
			continue
		}
		byTicket[r.Ticket] = append(byTicket[r.Ticket], r)
	}
	out := map[string]float64{}
	for _, trs := range byTicket {
		sort.Slice(trs, func(i, j int) bool { return trs[i].Time.Before(trs[j].Time) })
		prev := 0.0
		for _, r := range trs {
			d := r.USD - prev
			if r.USD > prev {
				prev = r.USD
			}
			if d <= 0 || r.Time.Before(since) {
				continue
			}
			shares := cost.MixShares(r.Models)
			if shares == nil {
				out["other"] += d
				continue
			}
			for model, share := range shares {
				out[model] += d * share
			}
		}
	}
	return out
}

// DeltaPoints turns cumulative ledger rows into chartable spend deltas:
// per ticket, each row contributes the USD growth since the previous
// snapshot at the row's timestamp (the first row contributes its full
// total — spend up to the first snapshot lands in that bucket). Tickets
// in exclude are skipped — their recent history is charted precisely from
// live transcripts, and double counting would inflate the buckets.
func DeltaPoints(rows []Row, exclude map[string]bool) []cost.Point {
	byTicket := map[string][]Row{}
	for _, r := range rows {
		if exclude[r.Ticket] {
			continue
		}
		byTicket[r.Ticket] = append(byTicket[r.Ticket], r)
	}
	var pts []cost.Point
	for _, trs := range byTicket {
		sort.Slice(trs, func(i, j int) bool { return trs[i].Time.Before(trs[j].Time) })
		prev := 0.0
		for _, r := range trs {
			if d := r.USD - prev; d > 0 {
				pts = append(pts, cost.Point{Time: r.Time, USD: d})
			}
			if r.USD > prev {
				prev = r.USD
			}
		}
	}
	return pts
}
