// Boot-cost rollup (grove-423): every session's boot record (car 01's
// transcript.ParseBoot + BootOf), discovered across a whole workspace and
// grouped so the operator can see "what does a fresh chat cost here, and
// is it growing" — workers and orchestrator chats together. Pure read;
// nothing here writes a transcript, events.jsonl, or tasks.json.
package cost

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/transcript"
)

// BootGroupPart is one named part's mean share within a BootGroup — the
// same names BootPart carries (system_prompt, repo_instructions,
// tool_schemas_est, …), averaged across the group's sessions.
type BootGroupPart struct {
	Name      string  `json:"name"`
	EstTokens int     `json:"est_tokens"`
	Share     float64 `json:"share"`
}

// BootGroup is one (kind, model) rollup: every session sharing a workspace
// role (orchestrator/worker) and a model, reduced to percentiles and a
// part breakdown. Percentiles/mean/max are over Boot.Tokens; CacheReadShare
// is Σcache_read / Σtokens across the group, not an average of shares.
type BootGroup struct {
	Kind           string          `json:"kind"`
	Model          string          `json:"model"`
	Sessions       int             `json:"sessions"`
	P50            int             `json:"p50"`
	P90            int             `json:"p90"`
	Max            int             `json:"max"`
	Mean           int             `json:"mean"`
	CacheReadShare float64         `json:"cache_read_share"`
	Parts          []BootGroupPart `json:"parts"`
}

// BootTrendPoint is one (ISO week, kind) bucket's session count and p50 —
// the "is it growing" series.
type BootTrendPoint struct {
	Week     string `json:"week"`
	Kind     string `json:"kind"`
	Sessions int    `json:"sessions"`
	P50      int    `json:"p50"`
}

// BootTopFile is one distinct instructions-file path's mean cost and reach
// across every session that loaded it (any kind, any group).
type BootTopFile struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	EstTokens int    `json:"est_tokens"`
	Sessions  int    `json:"sessions"`
}

// BootSessionRow is one session's raw boot record, with its workspace role
// and (when known) the ticket it belongs to.
type BootSessionRow struct {
	SessionID string `json:"session_id"`
	Kind      string `json:"kind"`
	Ticket    string `json:"ticket,omitempty"`
	Boot      Boot   `json:"boot"`
}

// BootReport is the whole `gv cost --boot` answer.
type BootReport struct {
	Since    string           `json:"since"`
	Groups   []BootGroup      `json:"groups"`
	Trend    []BootTrendPoint `json:"trend"`
	TopFiles []BootTopFile    `json:"top_files"`
	Sessions []BootSessionRow `json:"sessions"`
}

// BootWorkspace bundles everything BuildBootReport needs about one
// workspace — resolved once by main.go's impure glue (config, registry,
// the clock) and handed in so the decision logic underneath never touches
// any of it directly. Now is the injected clock: --since filters against
// it, and nothing in this file calls time.Now() itself.
type BootWorkspace struct {
	// ConfigDir is workspaceClaudeConfigDir's answer (cmd/gv/chat_ls.go) —
	// "" resolves to the default ~/.claude, same as gv chat ls.
	ConfigDir string
	// OrchestratorDir is this workspace's brain dir (orchestratorDirAt).
	OrchestratorDir string
	// WorkerRoots is one entry per repo configured in this workspace — the
	// base worktree directory under which every ticket's worktree for that
	// repo lives (worktree.DefaultPath(repoPath, "")), not a specific
	// ticket's worktree.
	WorkerRoots []string
	// TicketByPath maps a worker session directory's full path (exactly
	// what DiscoverBootDirs returns as BootSessionDir.Path) to the ticket
	// tracked at that worktree. A directory absent from this map has no
	// ticket — BootSessionRow.Ticket is left empty.
	TicketByPath map[string]string
	// SinceLabel is the raw --since flag value, carried verbatim into the
	// report (e.g. "720h") — never round-tripped through time.Duration's
	// own formatting, which would print "720h0m0s".
	SinceLabel string
	Since      time.Duration
	Now        time.Time
}

// BuildBootReport is the one impure entrypoint: it discovers, decodes,
// filters, and rolls up every session under ws. Every helper it calls is
// independently pure and table-tested against a []bootSession the tests
// construct by hand.
func BuildBootReport(ws BootWorkspace) (BootReport, error) {
	dirs, err := DiscoverBootDirs(ws.ConfigDir, ws.OrchestratorDir, ws.WorkerRoots)
	if err != nil {
		return BootReport{}, err
	}
	sessions := loadBootSessions(dirs, ws.TicketByPath)
	sessions = filterSince(sessions, ws.Since, ws.Now)
	return BootReport{
		Since:    ws.SinceLabel,
		Groups:   buildGroups(sessions),
		Trend:    buildTrend(sessions),
		TopFiles: buildTopFiles(sessions),
		Sessions: buildSessionRows(sessions),
	}, nil
}

// --- discovery (the only impure half: lists the filesystem) ---

// BootSessionDir is one classified <configDir>/projects/<name> directory.
type BootSessionDir struct {
	Path string
	Kind string // "orchestrator" | "worker"
}

// resolveConfigDir mirrors transcript.ProjectDirIn's own env resolution
// (GV_CLAUDE_CONFIG_DIR, else ~/.claude) — duplicated rather than exported
// from session.go, which (like parse.go) stays byte-comparable with the
// ovs original; new transcript-adjacent code goes in a new file, and this
// one lives in cost instead of adding one.
func resolveConfigDir(configDir string) string {
	if configDir == "" {
		configDir = os.Getenv("GV_CLAUDE_CONFIG_DIR")
	}
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		configDir = filepath.Join(home, ".claude")
	}
	return configDir
}

// hasEncodedPrefix reports whether an encoded project-dir name IS base, or
// has it as a prefix at a real path boundary (base + "-"). EncodePath
// turns both "/" and "." into "-", so a directory boundary and a dotted
// path segment look identical once encoded; the boundary check still
// holds either way, since it only asks whether the next encoded byte after
// base is a "-" — it never re-derives which original character produced
// it. A dir that merely shares base as a leading substring without that
// separating "-" (e.g. base "…-grove" against name "…-grover-x") is never
// a match.
func hasEncodedPrefix(name, base string) bool {
	if base == "" {
		return false
	}
	return name == base || strings.HasPrefix(name, base+"-")
}

// ClassifyBootDir names one <configDir>/projects/<name> directory's boot
// role: "orchestrator" when name is (or is a profile subdir of)
// orchestratorDir; "worker" when it is (or is a ticket subdir of) one of
// workerRoots; ok is false for anything else, which the caller skips.
func ClassifyBootDir(name, orchestratorDir string, workerRoots []string) (kind string, ok bool) {
	if hasEncodedPrefix(name, transcript.EncodePath(orchestratorDir)) {
		return "orchestrator", true
	}
	for _, root := range workerRoots {
		if hasEncodedPrefix(name, transcript.EncodePath(root)) {
			return "worker", true
		}
	}
	return "", false
}

// DiscoverBootDirs lists <configDir>/projects and classifies every entry.
// A missing projects/ dir (nothing has ever run there) is empty, not an
// error.
func DiscoverBootDirs(configDir, orchestratorDir string, workerRoots []string) ([]BootSessionDir, error) {
	projectsDir := filepath.Join(resolveConfigDir(configDir), "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []BootSessionDir
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		kind, ok := ClassifyBootDir(e.Name(), orchestratorDir, workerRoots)
		if !ok {
			continue
		}
		out = append(out, BootSessionDir{Path: filepath.Join(projectsDir, e.Name()), Kind: kind})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// sessionFilesIn lists *.jsonl files directly inside dir — no recursion:
// subagent transcripts live in subdirectories and are never boots.
func sessionFilesIn(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out
}

// bootSession is one decoded session: the unit every pure rollup function
// below consumes. At is parsed from Boot.Timestamp once here so --since
// filtering and week-bucketing never re-parse it.
type bootSession struct {
	SessionID string
	Kind      string
	Ticket    string
	Boot      Boot
	At        time.Time
}

// loadBootSessions decodes every *.jsonl directly under each dir via car
// 01's ParseBoot + BootOf. A session with no billable call, or an
// unparseable timestamp, is dropped silently — an unreadable dir or file
// degrades that dir/file, never the whole report.
func loadBootSessions(dirs []BootSessionDir, ticketByPath map[string]string) []bootSession {
	var out []bootSession
	for _, d := range dirs {
		ticket := ticketByPath[d.Path]
		for _, path := range sessionFilesIn(d.Path) {
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			first, lines := transcript.ParseBoot(f)
			f.Close()
			b, ok := BootOf(first, lines)
			if !ok {
				continue
			}
			at, err := time.Parse(time.RFC3339, b.Timestamp)
			if err != nil {
				continue
			}
			id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
			out = append(out, bootSession{SessionID: id, Kind: d.Kind, Ticket: ticket, Boot: b, At: at})
		}
	}
	return out
}

// filterSince keeps sessions whose boot landed at or after now-since —
// FROM the injected clock, never time.Now() itself.
func filterSince(sessions []bootSession, since time.Duration, now time.Time) []bootSession {
	cutoff := now.Add(-since)
	var out []bootSession
	for _, s := range sessions {
		if !s.At.Before(cutoff) {
			out = append(out, s)
		}
	}
	return out
}

// --- rollup (pure: every function below takes already-decoded sessions) ---

// percentile: sort ascending, index min(n-1, int(n*p)) — the rule the
// ticket pins down explicitly so p50/p90 read the same everywhere in
// grove. sorted must already be ascending.
func percentile(sorted []int, p float64) int {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := min(int(float64(n)*p), n-1)
	return sorted[idx]
}

func round(f float64) int {
	return int(math.Round(f))
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

type groupKey struct{ kind, model string }

type groupAcc struct {
	tokens        []int
	sumTokens     int
	sumCacheRead  int
	nonImageCount int
	partSum       map[string]int
}

// buildGroups rolls sessions up by (kind, model), biggest sessions first.
// Parts are the mean est_tokens per part name across the group's
// NON-image sessions only (an image-carrying session's char-based estimate
// is wildly wrong for the image blocks, so it would skew every part's
// mean); those sessions still count fully toward Sessions/percentiles/
// CacheReadShare — only the part breakdown excludes them.
func buildGroups(sessions []bootSession) []BootGroup {
	byKey := map[groupKey]*groupAcc{}
	var order []groupKey
	for _, s := range sessions {
		k := groupKey{s.Kind, s.Boot.Model}
		a, ok := byKey[k]
		if !ok {
			a = &groupAcc{partSum: map[string]int{}}
			byKey[k] = a
			order = append(order, k)
		}
		a.tokens = append(a.tokens, s.Boot.Tokens)
		a.sumTokens += s.Boot.Tokens
		a.sumCacheRead += s.Boot.CacheRead
		if !s.Boot.HasImage {
			a.nonImageCount++
			for _, p := range s.Boot.Parts {
				a.partSum[p.Name] += p.EstTokens
			}
		}
	}

	groups := make([]BootGroup, 0, len(order))
	for _, k := range order {
		a := byKey[k]
		sort.Ints(a.tokens)
		n := len(a.tokens)
		g := BootGroup{
			Kind:           k.kind,
			Model:          k.model,
			Sessions:       n,
			P50:            percentile(a.tokens, 0.5),
			P90:            percentile(a.tokens, 0.9),
			Max:            a.tokens[n-1],
			Mean:           round(float64(a.sumTokens) / float64(n)),
			CacheReadShare: safeDiv(float64(a.sumCacheRead), float64(a.sumTokens)),
			Parts:          []BootGroupPart{},
		}

		var partNames []string
		for name := range a.partSum {
			partNames = append(partNames, name)
		}
		sort.Strings(partNames)
		for _, name := range partNames {
			mean := 0
			if a.nonImageCount > 0 {
				mean = round(float64(a.partSum[name]) / float64(a.nonImageCount))
			}
			g.Parts = append(g.Parts, BootGroupPart{
				Name:      name,
				EstTokens: mean,
				Share:     safeDiv(float64(mean), float64(g.Mean)),
			})
		}
		sort.SliceStable(g.Parts, func(i, j int) bool {
			return g.Parts[i].EstTokens > g.Parts[j].EstTokens
		})

		groups = append(groups, g)
	}

	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Sessions != groups[j].Sessions {
			return groups[i].Sessions > groups[j].Sessions
		}
		if groups[i].Kind != groups[j].Kind {
			return groups[i].Kind < groups[j].Kind
		}
		return groups[i].Model < groups[j].Model
	})
	return groups
}

type trendKey struct{ week, kind string }

// buildTrend buckets sessions into ISO-week x kind, oldest week first.
func buildTrend(sessions []bootSession) []BootTrendPoint {
	byKey := map[trendKey][]int{}
	var order []trendKey
	for _, s := range sessions {
		year, week := s.At.UTC().ISOWeek()
		k := trendKey{fmt.Sprintf("%d-W%02d", year, week), s.Kind}
		if _, ok := byKey[k]; !ok {
			order = append(order, k)
		}
		byKey[k] = append(byKey[k], s.Boot.Tokens)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].week != order[j].week {
			return order[i].week < order[j].week
		}
		return order[i].kind < order[j].kind
	})
	out := make([]BootTrendPoint, 0, len(order))
	for _, k := range order {
		toks := byKey[k]
		sort.Ints(toks)
		out = append(out, BootTrendPoint{Week: k.week, Kind: k.kind, Sessions: len(toks), P50: percentile(toks, 0.5)})
	}
	return out
}

type fileAcc struct {
	name     string
	sum      int
	sessions int
}

// buildTopFiles ranks every distinct instructions-file path (across every
// kind and group) by est_tokens x sessions, top 10.
func buildTopFiles(sessions []bootSession) []BootTopFile {
	byPath := map[string]*fileAcc{}
	var order []string
	for _, s := range sessions {
		for _, p := range s.Boot.Parts {
			if p.Path == "" {
				continue
			}
			a, ok := byPath[p.Path]
			if !ok {
				a = &fileAcc{name: p.Name}
				byPath[p.Path] = a
				order = append(order, p.Path)
			}
			a.sum += p.EstTokens
			a.sessions++
		}
	}
	files := make([]BootTopFile, 0, len(order))
	for _, path := range order {
		a := byPath[path]
		files = append(files, BootTopFile{
			Path:      path,
			Name:      a.name,
			EstTokens: round(float64(a.sum) / float64(a.sessions)),
			Sessions:  a.sessions,
		})
	}
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].EstTokens*files[i].Sessions > files[j].EstTokens*files[j].Sessions
	})
	if len(files) > 10 {
		files = files[:10]
	}
	return files
}

// buildSessionRows projects every session into its JSON row, ordered
// deterministically (kind, then session id) so two runs over the same
// files diff cleanly.
func buildSessionRows(sessions []bootSession) []BootSessionRow {
	rows := make([]BootSessionRow, 0, len(sessions))
	for _, s := range sessions {
		rows = append(rows, BootSessionRow{SessionID: s.SessionID, Kind: s.Kind, Ticket: s.Ticket, Boot: s.Boot})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].SessionID < rows[j].SessionID
	})
	return rows
}
