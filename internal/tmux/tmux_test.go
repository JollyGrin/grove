package tmux

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSessionName(t *testing.T) {
	tests := []struct {
		repo string
		want string
	}{
		{"myrepo", "pr-myrepo"},
		{"my.repo", "pr-my-repo"},
		{"repo:name", "pr-repo-name"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := SessionName(tt.repo)
			if got != tt.want {
				t.Errorf("SessionName(%q) = %q, want %q", tt.repo, got, tt.want)
			}
		})
	}
}

func TestWindowName(t *testing.T) {
	tests := []struct {
		wt   string
		want string
	}{
		{"feat-x", "feat-x"},
		{"fix:bug", "fix-bug"},
		{"task.123", "task-123"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := WindowName(tt.wt)
			if got != tt.want {
				t.Errorf("WindowName(%q) = %q, want %q", tt.wt, got, tt.want)
			}
		})
	}
}

func TestWindowTarget(t *testing.T) {
	got := WindowTarget("myrepo", "feat-x")
	want := "pr-myrepo:feat-x"
	if got != want {
		t.Errorf("WindowTarget = %q, want %q", got, want)
	}
}

func TestPaneTarget(t *testing.T) {
	got := PaneTarget("myrepo", "feat-x", 1)
	want := "pr-myrepo:feat-x.1"
	if got != want {
		t.Errorf("PaneTarget = %q, want %q", got, want)
	}
}

func TestSessionExistsNonexistent(t *testing.T) {
	// A session with this name should never exist
	if SessionExists("pr-test-nonexistent-session-xyz") {
		t.Error("expected false for nonexistent session")
	}
}

func TestWindowExistsNonexistent(t *testing.T) {
	if WindowExists("pr-test-nonexistent-session-xyz", "some-window") {
		t.Error("expected false for nonexistent window")
	}
}

func TestMatchesWindowName(t *testing.T) {
	tests := []struct {
		name string
		live string
		want bool
	}{
		{"exact match", "name", true},
		{"glyph suffix pause", "name ⏸", true},
		{"glyph suffix dot", "name ●", true},
		{"longer name is not a suffix match", "name-longer", false},
		{"unrelated name", "other", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesWindowName(tt.live, "name")
			if got != tt.want {
				t.Errorf("matchesWindowName(%q, %q) = %v, want %v", tt.live, "name", got, tt.want)
			}
		})
	}
}

func TestCapturePaneNonexistent(t *testing.T) {
	out, err := CapturePane("pr-test-nonexistent-session-xyz:0.0")
	if err != nil {
		t.Errorf("expected nil error for nonexistent pane, got %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}

// grove-579: every tmux exec is bounded. A tmux that never answers (the
// stub execs into a 30s sleep after writing its pid) must fail within the
// read bound and leave no child behind — the leak class was one parked
// goroutine plus one stuck client per cockpit tick, forever.
func TestRunBoundedKillsStuckClient(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	stub := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\necho $$ > " + pidFile + "\nexec sleep 30\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// 1s, not less: on macOS a fresh script's first exec can take a few
	// hundred ms to reach its first line, and the pid must land first.
	prevRead, prevWait := readTimeout, waitDelay
	readTimeout, waitDelay = time.Second, 200*time.Millisecond
	defer func() { readTimeout, waitDelay = prevRead, prevWait }()

	start := time.Now()
	_, err := run("list-sessions", "-F", "#{session_name}")
	took := time.Since(start)
	if err == nil {
		t.Fatal("run against a stuck tmux returned nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "server not answering") {
		t.Errorf("error does not name the bound: %v", err)
	}
	if took > 3*time.Second {
		t.Errorf("run took %s, want well under the 30s the stub sleeps", took)
	}

	raw, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("stub never wrote its pid: %v", rerr)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if perr != nil {
		t.Fatalf("bad pid %q: %v", raw, perr)
	}
	// Reaped means signal 0 fails with ESRCH; a zombie would still answer.
	if kerr := syscall.Kill(pid, 0); kerr == nil {
		t.Errorf("stub pid %d still alive after run returned — the child leaked", pid)
	}
}

// has-session is the one exec that bypassed run; it rides the same bound.
func TestSessionExistsBounded(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "tmux")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	prevRead, prevWait := readTimeout, waitDelay
	readTimeout, waitDelay = 200*time.Millisecond, 200*time.Millisecond
	defer func() { readTimeout, waitDelay = prevRead, prevWait }()

	start := time.Now()
	if SessionExists("grove-579-nope") {
		t.Error("stuck tmux read as an existing session")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("SessionExists took %s, want under the bound", took)
	}
}

// Read verbs get the short bound, everything else the longer write one.
func TestVerbTimeout(t *testing.T) {
	cases := map[string]time.Duration{
		"list-panes":      readTimeout,
		"show-options":    readTimeout,
		"display-message": readTimeout,
		"capture-pane":    readTimeout,
		"start-server":    readTimeout,
		"has-session":     readTimeout,
		"send-keys":       writeTimeout,
		"paste-buffer":    writeTimeout,
		"swap-pane":       writeTimeout,
		"load-buffer":     writeTimeout,
		"set-option":      writeTimeout,
	}
	for verb, want := range cases {
		if got := verbTimeout([]string{verb, "-t", "x"}); got != want {
			t.Errorf("verbTimeout(%s) = %s, want %s", verb, got, want)
		}
	}
	if got := verbTimeout(nil); got != writeTimeout {
		t.Errorf("verbTimeout(nil) = %s, want the write bound", got)
	}
}
