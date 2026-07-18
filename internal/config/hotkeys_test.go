package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func readHotkeys(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return c.Orchestrator.Hotkeys
}

// Bind → rebind (steal) → unbind, round-tripped through a scratch file,
// preserving unrelated keys AND comments — the write is node surgery,
// never a re-marshal of the parsed struct.
func TestSaveHotkeyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	seed := `# grove config — hand-written comment that must survive
provider:
  kind: markdown # trailing comment
orchestrator:
  dir: ~/.config/grove/orchestrator
  claude: claude --dangerously-skip-permissions
notify:
  ntfy: https://ntfy.sh/secret-topic
`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SaveHotkey(path, "1", "kimi"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if hk := readHotkeys(t, path); hk["1"] != "kimi" {
		t.Fatalf("after bind: hotkeys = %v, want 1→kimi", hk)
	}

	// Unrelated keys and comments survive the write.
	out, _ := os.ReadFile(path)
	for _, want := range []string{
		"hand-written comment that must survive",
		"trailing comment",
		"ntfy.sh/secret-topic",
		"dir: ~/.config/grove/orchestrator",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("after bind, file lost %q:\n%s", want, out)
		}
	}

	// Stealing: binding "1" to another profile overwrites.
	if err := SaveHotkey(path, "1", "glm"); err != nil {
		t.Fatalf("steal: %v", err)
	}
	if hk := readHotkeys(t, path); hk["1"] != "glm" || len(hk) != 1 {
		t.Fatalf("after steal: hotkeys = %v, want exactly 1→glm", hk)
	}

	// One digit per profile: rebinding glm to "3" drops its old "1" slot.
	if err := SaveHotkey(path, "3", "glm"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if hk := readHotkeys(t, path); hk["3"] != "glm" || hk["1"] != "" || len(hk) != 1 {
		t.Fatalf("after move: hotkeys = %v, want exactly 3→glm", hk)
	}

	// Unbind: empty profile deletes the digit.
	if err := SaveHotkey(path, "3", ""); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if hk := readHotkeys(t, path); len(hk) != 0 {
		t.Fatalf("after unbind: hotkeys = %v, want empty", hk)
	}
}

// A missing file is created with just the hotkeys block — a workspace
// .grove/ may exist without a config.yaml.
func TestSaveHotkeyCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".grove", "config.yaml")
	if err := SaveHotkey(path, "2", "kimi"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if hk := readHotkeys(t, path); hk["2"] != "kimi" {
		t.Fatalf("created file: hotkeys = %v, want 2→kimi", hk)
	}
	// The written digit key is a string, so a full Load-path parse into
	// map[string]string works (keys are force-quoted on write).
	out, _ := os.ReadFile(path)
	if !strings.Contains(string(out), `"2": kimi`) {
		t.Errorf("digit key not quoted:\n%s", out)
	}
}

func TestPathAt(t *testing.T) {
	if got, want := PathAt("/ws/root"), filepath.Join("/ws/root", ".grove", "config.yaml"); got != want {
		t.Errorf("PathAt(root) = %q, want %q", got, want)
	}
	if got, want := PathAt(""), filepath.Join(Dir(), "config.yaml"); got != want {
		t.Errorf("PathAt(\"\") = %q, want %q", got, want)
	}
}
