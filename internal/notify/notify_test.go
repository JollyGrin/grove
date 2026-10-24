package notify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCapRunesNeverSplitsCodepoint(t *testing.T) {
	// grove-452 item 2: body[:120]/body[:200] byte cuts could land inside
	// a multibyte rune and put a garbage char in the notification.
	body := ""
	for i := 0; i < 150; i++ {
		body += "é" // 2 bytes each: every byte-cut at 120 would be mid-rune
	}
	for _, n := range []int{120, 200} {
		got := capRunes(body, n)
		if !utf8.ValidString(got) {
			t.Fatalf("capRunes(…, %d) produced invalid UTF-8: %q", n, got)
		}
		r := []rune(got)
		if n >= 150 {
			if got != body {
				t.Fatalf("capRunes(…, %d) altered a body under the cap", n)
			}
			continue
		}
		if len(r) != n+1 || r[n] != '…' {
			t.Fatalf("capRunes(…, %d) = %d runes, want %d + ellipsis", n, len(r), n)
		}
	}
	if got := capRunes("short", 120); got != "short" {
		t.Fatalf("short body altered: %q", got)
	}
}

func TestDesktopReapsChild(t *testing.T) {
	// grove-571: Start without Wait left one zombie per push under the
	// cockpit supervisor (14 in 17.6 h under one dash). A stub
	// terminal-notifier on PATH exits at once; the child must leave the
	// process table (ps prints nothing) rather than linger as Z.
	dir := t.TempDir()
	stub := filepath.Join(dir, "terminal-notifier")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd := desktop("t", "b")
	if cmd == nil || cmd.Process == nil {
		t.Fatal("desktop did not start the stub")
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, _ := exec.Command("ps", "-o", "stat=", "-p", pid).Output()
		stat := strings.TrimSpace(string(out))
		if stat == "" { // reaped: no such process
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("child %s still in the process table after 5s, stat=%q (Z = zombie, never Wait()ed)", pid, stat)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
