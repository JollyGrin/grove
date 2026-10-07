package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateLineRuneSafe is the grove-131 regression: a byte cut could
// split a multibyte rune, so `gv ls` question previews ended in mojibake.
func TestTruncateLineRuneSafe(t *testing.T) {
	s := strings.Repeat("é", 10) // 20 bytes, 10 runes
	got := truncateLine(s, 5)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateLine produced invalid UTF-8: %q", got)
	}
	if want := strings.Repeat("é", 5) + "…"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := truncateLine("short", 90); got != "short" {
		t.Errorf("short string altered: %q", got)
	}
	if got := truncateLine("first\nsecond", 90); got != "first" {
		t.Errorf("first line not kept: %q", got)
	}
}

// TestCopyEnvFilesReportsWriteFailure is the grove-131 regression: a
// discarded write error printed "copied .env" while the worker ran without
// secrets. A read-only destination must print the failure, never success.
func TestCopyEnvFilesReportsWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dst, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dst, 0o700) })

	var out bytes.Buffer
	copyEnvFiles(src, dst, &out)
	if strings.Contains(out.String(), "copied") {
		t.Errorf("reported success on a failed write:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "copy .env failed") {
		t.Errorf("failure not reported:\n%s", out.String())
	}

	// And the happy path still says copied.
	out.Reset()
	ok := t.TempDir()
	copyEnvFiles(src, ok, &out)
	if !strings.Contains(out.String(), "→ copied .env") {
		t.Errorf("success not reported:\n%s", out.String())
	}
	if data, err := os.ReadFile(filepath.Join(ok, ".env")); err != nil || string(data) != "SECRET=1\n" {
		t.Errorf("copy content = %q, %v", data, err)
	}
}
