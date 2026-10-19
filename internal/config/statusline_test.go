package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatuslineWrapRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, ok := StatuslineWrapFrom(path); ok {
		t.Fatal("missing file reads as recorded")
	}
	orig := "# keep me\nnotify:\n  ntfy: https://ntfy.sh/x\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, wrap := range []string{`bash ~/.claude/s.sh && echo "#1: yes" | tr a b`, "", "true"} {
		if err := SaveStatuslineWrap(path, wrap); err != nil {
			t.Fatal(err)
		}
		if got, ok := StatuslineWrapFrom(path); !ok || got != wrap {
			t.Fatalf("wrap = %q (%v), want %q", got, ok, wrap)
		}
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "# keep me") || NotifySettingsFrom(path).Ntfy != "https://ntfy.sh/x" {
		t.Fatalf("save lost unrelated content:\n%s", raw)
	}
	if err := ClearStatuslineWrap(path); err != nil {
		t.Fatal(err)
	}
	if _, ok := StatuslineWrapFrom(path); ok {
		t.Fatal("wrap still recorded after clear")
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "statusline") {
		t.Errorf("an emptied statusline block was left behind:\n%s", raw)
	}
	// Clear removes only wrap: a sibling key keeps the block.
	if err := os.WriteFile(path, []byte("statusline:\n  wrap: \"x\"\n  other: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ClearStatuslineWrap(path); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), "other: 1") || strings.Contains(string(raw), "wrap") {
		t.Errorf("clear touched more than wrap:\n%s", raw)
	}
	if err := ClearStatuslineWrap(filepath.Join(t.TempDir(), "absent.yaml")); err != nil {
		t.Fatalf("clear on a missing file: %v", err)
	}
}

// A symlinked config.yaml (dotfiles) stays a link; its target is written.
func TestSaveStatuslineWrapFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("notify:\n  ntfy: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := SaveStatuslineWrap(link, "true"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.yaml is no longer a symlink (%v)", err)
	}
	if got, ok := StatuslineWrapFrom(target); !ok || got != "true" {
		t.Fatalf("target wrap = %q (%v)", got, ok)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o600 {
		t.Errorf("target mode %#o, want the existing 0600 kept", fi.Mode().Perm())
	}
}
