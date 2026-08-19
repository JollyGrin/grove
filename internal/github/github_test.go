package github

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestGHTimesOut verifies a wedged gh (stalled network, offline) is bounded
// by ghTimeout instead of hanging the caller forever — grove-164.
func TestGHTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "gh")
	script := "#!/bin/sh\nsleep 5\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub gh: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)

	oldTimeout := ghTimeout
	ghTimeout = 100 * time.Millisecond
	defer func() { ghTimeout = oldTimeout }()

	start := time.Now()
	_, err := gh(dir, "pr", "list")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from timed-out gh, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("gh() took %v to return after a wedged call, want well under the deadline bound", elapsed)
	}
}
