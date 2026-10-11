package statusline

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsShim(t *testing.T) {
	for cmd, want := range map[string]bool{
		"/home/u/go/bin/gv statusline":               true,
		"/tmp/gv-472 statusline":                     true,
		"/tmp/gv-grove-472 statusline":               true,
		"gv statusline":                              true,
		`'/home/u/my bin/gv' statusline`:             true,
		`"/home/u/my bin/gv" statusline`:             true,
		`/home/u/my\ bin/gv statusline`:              true,
		"exec gv statusline":                         true,
		"exec /home/u/go/bin/gv statusline":          true,
		"GROVE_ACCOUNT=x gv statusline":              true,
		"env GROVE_ACCOUNT=x /usr/bin/gv statusline": true,
		"cd /tmp && gv statusline":                   true,
		"echo hi;gv statusline|cat":                  true,
		"(gv statusline)":                            true,
		"/usr/bin/gv statusline install":             true,
		"bash ~/.claude/statusline-command.sh":       false,
		"/usr/bin/ovs statusline":                    false,
		"gv ls statusline":                           false,
		"echo 'gv statusline'":                       false,
		"":                                           false,
	} {
		if got := IsShim(cmd); got != want {
			t.Errorf("IsShim(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// A binary path with spaces (or a quote) is quoted so sh runs it, and
// IsShim still recognizes the result.
func TestShimCommandQuotesAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"plain", "my bin", "it's"} {
		exe := filepath.Join(dir, sub, "gv")
		if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, []byte("#!/bin/sh\necho \"ran $1\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := ShimCommand(exe)
		if !IsShim(cmd) {
			t.Errorf("IsShim(ShimCommand(%q) = %q) = false", exe, cmd)
		}
		out, err := exec.Command("sh", "-c", cmd).Output()
		if err != nil || string(out) != "ran statusline\n" {
			t.Errorf("sh -c %q: %q, %v", cmd, out, err)
		}
	}
	if got := ShimCommand("/home/u/go/bin/gv"); got != "/home/u/go/bin/gv statusline" {
		t.Errorf("plain path quoted needlessly: %q", got)
	}
}

// A symlinked settings.json (dotfiles) stays a link; its target is
// written, in the target's dir.
func TestWriteCommandFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteCommand(link, "/x/gv statusline", false); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("settings.json is no longer a symlink (%v)", err)
	}
	raw, _ := os.ReadFile(target)
	if !Installed(raw) {
		t.Fatalf("target not updated: %s", raw)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o600 {
		t.Errorf("target mode %#o, want the existing 0600 kept", fi.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(link), ".tmp-*")); len(left) > 0 {
		t.Errorf("temp file in the link's dir: %v", left)
	}
}

// Removing the command keeps the operator's other statusLine keys.
func TestWriteCommandRemoveKeepsOtherKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"statusLine":{"type":"command","command":"/x/gv statusline","padding":2}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteCommand(path, "", true); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var got map[string]map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if sl := got["statusLine"]; sl == nil || sl["padding"] != float64(2) || sl["command"] != nil {
		t.Fatalf("statusLine after remove = %v, want padding kept, command gone", sl)
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
