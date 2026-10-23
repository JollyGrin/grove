package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func guardRun(t *testing.T, toolInput string) string {
	t.Helper()
	payload := `{"session_id":"s","cwd":"/nowhere","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":` + toolInput + `}`
	var out bytes.Buffer
	if err := ReceiveTo(nil, "secrets-guard", strings.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestSecretsGuardDenies(t *testing.T) {
	for name, in := range map[string]string{
		"identity":       `{"command":"cat ~/.local/state/grove/age/identity.txt"}`,
		"identity read":  `{"file_path":"/home/x/.local/state/grove/age/identity.txt"}`,
		"escaped slash":  `{"command":"cat ~/.local\/state\/grove\/age\/identity.txt"}`,
		"keys env":       `{"command":"gv keys env"}`,
		"keys env flags": `{"command":"eval \"$(gv   keys   env --shell)\""}`,
		"secrets dir":    `{"command":"ls ~/.config/grove/secrets/global"}`,
		"nested":         `{"edits":[{"new_string":"x .config/grove/secrets/ y"}]}`,
	} {
		out := guardRun(t, in)
		var m map[string]map[string]string
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Errorf("%s: output %q: %v", name, out, err)
			continue
		}
		h := m["hookSpecificOutput"]
		if h["hookEventName"] != "PreToolUse" || h["permissionDecision"] != "deny" || h["permissionDecisionReason"] == "" {
			t.Errorf("%s: not a deny decision: %q", name, out)
		}
	}
}

func TestSecretsGuardAllows(t *testing.T) {
	for name, in := range map[string]string{
		"keys ls":       `{"command":"gv keys ls --json"}`,
		"keys exec":     `{"command":"gv keys exec OPENROUTER_API_KEY -- sh -c 'curl -H \"$OPENROUTER_API_KEY\" x'"}`,
		"keys envelope": `{"command":"echo gv keys envelope"}`,
		"plain":         `{"command":"go test ./..."}`,
		"empty":         `{}`,
	} {
		if out := guardRun(t, in); out != "" {
			t.Errorf("%s: want silence, got %q", name, out)
		}
	}
	var out bytes.Buffer
	if err := ReceiveTo(nil, "secrets-guard", strings.NewReader(`{"hook_event_name":"PreToolUse"}`), &out); err != nil || out.Len() != 0 {
		t.Errorf("no tool_input: out=%q err=%v", out.String(), err)
	}
}

func TestInstallAddsSecretsGuardBesideForeignEntries(t *testing.T) {
	foreign := `{"matcher":"Bash","hooks":[{"type":"command","command":"/opt/other/guard.sh"}]}`
	settings := `{"hooks":{"PreToolUse":[` + foreign + `]},"theme":"dark"}`
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent: second install must not stack
		if err := install(path, "/home/x/go/bin/gv"); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(path)
	var s struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
		Theme string                       `json:"theme"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	pre := s.Hooks["PreToolUse"]
	if len(pre) != 2 {
		t.Fatalf("PreToolUse entries = %d, want foreign + gv", len(pre))
	}
	// The installer re-marshals the file (keys sort), so "preserved" means
	// the entry decodes to the same value, as TestInstallPreservesOvsEntries.
	var gotF, wantF any
	_ = json.Unmarshal(pre[0], &gotF)
	_ = json.Unmarshal([]byte(foreign), &wantF)
	if !reflect.DeepEqual(gotF, wantF) {
		t.Errorf("foreign entry changed: %s", pre[0])
	}
	if !strings.Contains(string(pre[1]), "/home/x/go/bin/gv hook secrets-guard") {
		t.Errorf("gv entry = %s", pre[1])
	}
	if s.Theme != "dark" {
		t.Error("other settings keys must survive")
	}
	got, _ := installed(path)
	if !got["PreToolUse"] {
		t.Error("installed() misses PreToolUse")
	}
}
