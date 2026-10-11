package statusline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsShim(t *testing.T) {
	for cmd, want := range map[string]bool{
		"/home/u/go/bin/gv statusline":         true,
		"/tmp/gv-grove-472 statusline":         true,
		"gv statusline":                        true,
		"bash ~/.claude/statusline-command.sh": false,
		"/usr/bin/gv statusline install":       false,
		"/usr/bin/ovs statusline":              false,
		"":                                     false,
	} {
		if got := IsShim(cmd); got != want {
			t.Errorf("IsShim(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestWriteCommandCreatesAndRemoves(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	if cmd, present, err := ReadCommand(path); err != nil || present || cmd != "" {
		t.Fatalf("missing file: %q %v %v", cmd, present, err)
	}
	if err := WriteCommand(path, "/x/gv statusline", false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !Installed(raw) {
		t.Fatalf("not installed after write: %s", raw)
	}
	if err := WriteCommand(path, "", true); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "{}\n" {
		t.Fatalf("after remove: %q", raw)
	}
}

func TestReadCommandRefusesBrokenSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadCommand(path); err == nil {
		t.Fatal("broken settings.json parsed")
	}
	if err := WriteCommand(path, "x", false); err == nil {
		t.Fatal("WriteCommand overwrote a broken settings.json")
	}
}
