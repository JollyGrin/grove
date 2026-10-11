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
	if err := ClearStatuslineWrap(filepath.Join(t.TempDir(), "absent.yaml")); err != nil {
		t.Fatalf("clear on a missing file: %v", err)
	}
}
