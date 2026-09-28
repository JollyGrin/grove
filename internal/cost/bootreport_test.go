package cost

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- classifier ---

func TestClassifyBootDir(t *testing.T) {
	// Deliberately disjoint from workerRoots below — several cases test
	// the orchestrator-vs-worker boundary in isolation, and a nested path
	// would let a case match via the OTHER rule for the wrong reason.
	const orchDir = "/Users/dean/.grove-brains/grove/orchestrator"
	workerRoots := []string{
		"/Users/dean/git/.worktrees/grove",
		"/Users/dean/git/.worktrees/thegrid",
	}
	enc := func(s string) string { return orchOrWorkerEncode(s) }

	cases := []struct {
		name     string
		dir      string
		wantKind string
		wantOK   bool
	}{
		{
			name:     "exact orchestrator dir",
			dir:      enc(orchDir),
			wantKind: "orchestrator",
			wantOK:   true,
		},
		{
			name:     "orchestrator profile subdir",
			dir:      enc(orchDir) + "-" + enc("openrouter-glm"),
			wantKind: "orchestrator",
			wantOK:   true,
		},
		{
			name:     "worker worktree (ticket subdir)",
			dir:      enc(workerRoots[0]) + "-grove-405-abc123",
			wantKind: "worker",
			wantOK:   true,
		},
		{
			name: "dotted path segment (double dash) still matches at the real boundary",
			// A worker root itself containing a dotted segment: the "/."
			// encodes to "--", and a real ticket subdir beneath it must
			// still classify as worker.
			dir:      enc("/Users/dean/git/.worktrees/grove") + "-ticket-1",
			wantKind: "worker",
			wantOK:   true,
		},
		{
			name:     "unrelated project dir",
			dir:      enc("/Users/dean/git/some-other-repo"),
			wantKind: "",
			wantOK:   false,
		},
		{
			name: "shares a prefix string with the orchestrator dir but no - boundary",
			// orchDir encoded ends in "...orchestrator"; this name extends
			// that same string with more characters directly (no
			// separating "-"), the way a sibling dir name "...orchestrator2"
			// would — must NOT match.
			dir:      enc(orchDir) + "2",
			wantKind: "",
			wantOK:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, ok := ClassifyBootDir(tc.dir, orchDir, workerRoots)
			if ok != tc.wantOK || kind != tc.wantKind {
				t.Fatalf("ClassifyBootDir(%q) = (%q, %v), want (%q, %v)", tc.dir, kind, ok, tc.wantKind, tc.wantOK)
			}
		})
	}
}

// orchOrWorkerEncode mirrors transcript.EncodePath without importing it
// twice in the test (kept local so the test data above reads plainly).
func orchOrWorkerEncode(s string) string {
	out := make([]byte, len(s))
	for i := range s {
		switch s[i] {
		case '/', '.':
			out[i] = '-'
		default:
			out[i] = s[i]
		}
	}
	return string(out)
}

// --- discovery (impure: real ReadDir over a temp dir) ---

func TestDiscoverBootDirs(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "projects")
	orchDir := "/ws/.grove/orchestrator"
	workerRoot := "/ws/../.worktrees/repo"

	mk := func(name string) {
		if err := os.MkdirAll(filepath.Join(projects, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk(orchOrWorkerEncode(orchDir))
	mk(orchOrWorkerEncode(workerRoot) + "-ticket-1")
	mk(orchOrWorkerEncode("/unrelated/thing"))
	// a plain file (not a dir) directly under projects/ must be ignored
	if err := os.WriteFile(filepath.Join(projects, "not-a-dir"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := DiscoverBootDirs(root, orchDir, []string{workerRoot})
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 {
		t.Fatalf("got %d dirs, want 2: %+v", len(dirs), dirs)
	}
	byKind := map[string]int{}
	for _, d := range dirs {
		byKind[d.Kind]++
	}
	if byKind["orchestrator"] != 1 || byKind["worker"] != 1 {
		t.Fatalf("kind counts = %+v, want 1 orchestrator + 1 worker", byKind)
	}
}

func TestDiscoverBootDirsMissingProjectsIsEmptyNotError(t *testing.T) {
	root := t.TempDir()
	dirs, err := DiscoverBootDirs(root, "/ws/.grove/orchestrator", nil)
	if err != nil {
		t.Fatalf("missing projects/ must not error: %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("got %d dirs, want 0", len(dirs))
	}
}

// --- rollup ---

func mkBoot(model string, tokens, cacheRead int, hasImage bool, at string, parts ...BootPart) bootSession {
	return bootSession{
		Boot: Boot{
			Tokens:    tokens,
			CacheRead: cacheRead,
			Model:     model,
			Timestamp: at,
			HasImage:  hasImage,
			Parts:     parts,
		},
		At: mustParse(at),
	}
}

func mustParse(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestPercentileRule(t *testing.T) {
	cases := []struct {
		name string
		vals []int
		p    float64
		want int
	}{
		{"n=1 p50", []int{42}, 0.5, 42},
		{"n=1 p90", []int{42}, 0.9, 42},
		{"n=2 p50", []int{10, 20}, 0.5, 20},
		{"n=2 p90", []int{10, 20}, 0.9, 20},
		{"n=10 p50", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 0.5, 6},
		{"n=10 p90", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 0.9, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentile(tc.vals, tc.p); got != tc.want {
				t.Fatalf("percentile(%v, %v) = %d, want %d", tc.vals, tc.p, got, tc.want)
			}
		})
	}
}

func TestBuildGroupsPercentilesAndCacheShare(t *testing.T) {
	sessions := []bootSession{
		mkBoot("claude-sonnet-5", 10, 2, false, "2026-09-01T00:00:00Z"),
		mkBoot("claude-sonnet-5", 20, 4, false, "2026-09-01T00:00:01Z"),
	}
	groups := buildGroups(sessions)
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	g := groups[0]
	if g.Sessions != 2 || g.P50 != 20 || g.P90 != 20 || g.Max != 20 || g.Mean != 15 {
		t.Fatalf("group = %+v, want sessions=2 p50=20 p90=20 max=20 mean=15", g)
	}
	wantShare := 6.0 / 30.0
	if g.CacheReadShare != wantShare {
		t.Fatalf("CacheReadShare = %v, want %v", g.CacheReadShare, wantShare)
	}
}

func TestBuildGroupsExcludesImageSessionsFromPartsOnly(t *testing.T) {
	sessions := []bootSession{
		mkBoot("claude-sonnet-5", 100, 0, false, "2026-09-01T00:00:00Z",
			BootPart{Name: "repo_instructions", EstTokens: 100}),
		mkBoot("claude-sonnet-5", 200, 0, true, "2026-09-01T00:00:01Z",
			BootPart{Name: "repo_instructions", EstTokens: 9999}),
	}
	groups := buildGroups(sessions)
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	g := groups[0]
	if g.Sessions != 2 {
		t.Fatalf("Sessions = %d, want 2 (image session still counted)", g.Sessions)
	}
	if len(g.Parts) != 1 || g.Parts[0].EstTokens != 100 {
		t.Fatalf("Parts = %+v, want one part at 100 (image session excluded)", g.Parts)
	}
	wantShare := 100.0 / float64(g.Mean)
	if g.Parts[0].Share != wantShare {
		t.Fatalf("Share = %v, want %v", g.Parts[0].Share, wantShare)
	}
}

func TestBuildGroupsOrderedBySessionsDesc(t *testing.T) {
	sessions := []bootSession{
		mkBoot("claude-opus-5", 10, 0, false, "2026-09-01T00:00:00Z"),
		mkBoot("claude-sonnet-5", 10, 0, false, "2026-09-01T00:00:01Z"),
		mkBoot("claude-sonnet-5", 20, 0, false, "2026-09-01T00:00:02Z"),
		mkBoot("claude-sonnet-5", 30, 0, false, "2026-09-01T00:00:03Z"),
	}
	groups := buildGroups(sessions)
	if len(groups) != 2 || groups[0].Model != "claude-sonnet-5" || groups[0].Sessions != 3 {
		t.Fatalf("groups = %+v, want claude-sonnet-5 (3 sessions) first", groups)
	}
}

func TestBuildTrendOldestFirst(t *testing.T) {
	sessions := []bootSession{
		mkBoot("claude-sonnet-5", 10, 0, false, "2026-09-15T00:00:00Z"), // W38
		mkBoot("claude-sonnet-5", 20, 0, false, "2026-09-08T00:00:00Z"), // W37
	}
	sessions[0].Kind = "worker"
	sessions[1].Kind = "worker"
	trend := buildTrend(sessions)
	if len(trend) != 2 {
		t.Fatalf("got %d trend points, want 2", len(trend))
	}
	if trend[0].Week != "2026-W37" || trend[1].Week != "2026-W38" {
		t.Fatalf("trend = %+v, want W37 before W38", trend)
	}
}

func TestBuildTopFilesOrdering(t *testing.T) {
	sessions := []bootSession{
		mkBoot("claude-sonnet-5", 100, 0, false, "2026-09-01T00:00:00Z",
			BootPart{Name: "repo_instructions", Path: "/repo/CLAUDE.md", EstTokens: 100}),
		mkBoot("claude-sonnet-5", 100, 0, false, "2026-09-01T00:00:01Z",
			BootPart{Name: "repo_instructions", Path: "/repo/CLAUDE.md", EstTokens: 100}),
		mkBoot("claude-sonnet-5", 100, 0, false, "2026-09-01T00:00:02Z",
			BootPart{Name: "memory_index", Path: "/home/MEMORY.md", EstTokens: 5000}),
	}
	files := buildTopFiles(sessions)
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	// MEMORY.md: 5000*1=5000 beats CLAUDE.md: 100*2=200
	if files[0].Path != "/home/MEMORY.md" || files[1].Path != "/repo/CLAUDE.md" {
		t.Fatalf("files = %+v, want MEMORY.md first (est_tokens x sessions)", files)
	}
	if files[1].Sessions != 2 || files[1].EstTokens != 100 {
		t.Fatalf("CLAUDE.md row = %+v, want sessions=2 est_tokens=100", files[1])
	}
}

func TestFilterSinceInjectedClock(t *testing.T) {
	now := mustParse("2026-09-28T00:00:00Z")
	sessions := []bootSession{
		mkBoot("claude-sonnet-5", 10, 0, false, "2026-09-27T00:00:00Z"), // 24h ago: in
		mkBoot("claude-sonnet-5", 20, 0, false, "2026-09-20T00:00:00Z"), // 8d ago: out at 48h
	}
	got := filterSince(sessions, 48*time.Hour, now)
	if len(got) != 1 || got[0].Boot.Tokens != 10 {
		t.Fatalf("filterSince = %+v, want only the 24h-old session", got)
	}
}

func TestBuildBootReportEndToEnd(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "projects")
	orchDir := "/ws/.grove/orchestrator"
	workerRoot := "/ws/.worktrees/repo"

	orchSession := orchOrWorkerEncode(orchDir)
	workerSession := orchOrWorkerEncode(workerRoot) + "-grove-1"
	if err := os.MkdirAll(filepath.Join(projects, orchSession), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projects, workerSession), 0o755); err != nil {
		t.Fatal(err)
	}

	fixture, err := os.ReadFile("../transcript/testdata/boot.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, orchSession, "s1.jsonl"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, workerSession, "s2.jsonl"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := BuildBootReport(BootWorkspace{
		ConfigDir:       root,
		OrchestratorDir: orchDir,
		WorkerRoots:     []string{workerRoot},
		TicketByPath:    map[string]string{filepath.Join(projects, workerSession): "grove-1"},
		SinceLabel:      "720h",
		Since:           720 * time.Hour,
		Now:             mustParse("2026-09-28T12:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Since != "720h" {
		t.Fatalf("Since = %q, want 720h", rep.Since)
	}
	if len(rep.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2: %+v", len(rep.Sessions), rep.Sessions)
	}
	var sawTicket bool
	for _, s := range rep.Sessions {
		if s.Kind == "worker" {
			if s.Ticket != "grove-1" {
				t.Fatalf("worker session ticket = %q, want grove-1", s.Ticket)
			}
			sawTicket = true
		}
		if s.Kind == "orchestrator" && s.Ticket != "" {
			t.Fatalf("orchestrator session unexpectedly carries a ticket: %q", s.Ticket)
		}
	}
	if !sawTicket {
		t.Fatal("no worker session found")
	}
	if len(rep.Groups) == 0 {
		t.Fatal("Groups is empty")
	}
}
