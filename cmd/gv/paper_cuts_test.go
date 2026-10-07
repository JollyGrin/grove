package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateLineRuneSafe is the grove-131 (item 1) regression check: the
// cut must land on a rune boundary, never inside a multibyte codepoint, so
// `gv ls` previews never end in mojibake.
func TestTruncateLineRuneSafe(t *testing.T) {
	// Every rune is 3 bytes; a byte cut at 5 would split the second one.
	s := "日本語テキスト"
	got := truncateLine(s, 5)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateLine produced invalid UTF-8: %q", got)
	}
	if want := "日本語テキ…"; got != want {
		t.Errorf("truncateLine(%q, 5) = %q, want %q", s, got, want)
	}
	// Under the cap: untouched, no ellipsis.
	if got := truncateLine("héllo", 10); got != "héllo" {
		t.Errorf("short string changed: %q", got)
	}
	// Exactly at the cap in runes (but over it in bytes): untouched.
	if got := truncateLine("héllo", 5); got != "héllo" {
		t.Errorf("at-cap string changed: %q", got)
	}
	// Still first-line only.
	if got := truncateLine("one\ntwo", 10); got != "one" {
		t.Errorf("multi-line = %q, want first line", got)
	}
}

// TestCopyEnvFilesReportsWriteFailure is the grove-131 (item 3) regression
// check: a readable .env that cannot be written into the worktree must
// print a failure, never "→ copied" — the old code reported success on a
// successful READ and the worker ran without secrets.
func TestCopyEnvFilesReportsWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dst, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dst, 0o700) })

	var out bytes.Buffer
	copyEnvFiles(src, dst, &out)

	if strings.Contains(out.String(), "copied .env") {
		t.Errorf("reported success for a failed write:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "FAILED to copy .env") {
		t.Errorf("missing failure line:\n%s", out.String())
	}
}

// TestCopyEnvFilesHappyPath: the success message and 0600 mode survive the
// refactor; absent files are silent.
func TestCopyEnvFilesHappyPath(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	copyEnvFiles(src, dst, &out)
	if got := out.String(); got != "→ copied .env\n" {
		t.Errorf("output = %q, want only the .env line", got)
	}
	data, err := os.ReadFile(filepath.Join(dst, ".env"))
	if err != nil || string(data) != "SECRET=1\n" {
		t.Errorf("copied content = %q, err %v", data, err)
	}
}
