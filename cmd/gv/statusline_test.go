package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/statusline"
)

// original is a settings.json with the keys install must not touch.
const originalSettings = `{
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "/x/gv hook stop"}]}]
  },
  "modelSettings": {"opus": {"effort": "high", "budget": 1.50}},
  "statusLine": {"type": "command", "command": "bash ~/.claude/statusline-command.sh && echo '<ok>'", "padding": 0},
  "theme": "dark"
}
`

func jsonOf(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return v
}

func TestStatuslineInstallUninstallRoundTrip(t *testing.T) {
	for name, orig := range map[string]string{"with status line": originalSettings, "without": `{"theme":"dark","hooks":{}}`} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			settings := statusline.SettingsPath(home)
			cfgPath := filepath.Join(home, ".config", "grove", "config.yaml")
			if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(settings, []byte(orig), 0o644); err != nil {
				t.Fatal(err)
			}
			origCmd, _, _ := statusline.ReadCommand(settings)

			for i := 0; i < 2; i++ { // idempotent: twice changes nothing more
				if err := statuslineInstall(settings, cfgPath, "/home/u/go/bin/gv"); err != nil {
					t.Fatal(err)
				}
			}
			if cmd, _, _ := statusline.ReadCommand(settings); cmd != "/home/u/go/bin/gv statusline" {
				t.Fatalf("statusLine.command = %q", cmd)
			}
			if wrap, ok := config.StatuslineWrapFrom(cfgPath); !ok || wrap != origCmd {
				t.Fatalf("statusline.wrap = %q (%v), want %q", wrap, ok, origCmd)
			}
			installed, _ := os.ReadFile(settings)
			got, want := jsonOf(t, installed).(map[string]any), jsonOf(t, []byte(orig)).(map[string]any)
			for _, k := range []string{"hooks", "modelSettings", "theme"} {
				if !reflect.DeepEqual(got[k], want[k]) {
					t.Errorf("install changed %s: %v → %v", k, want[k], got[k])
				}
			}

			// A new binary path refreshes the shim, never records itself.
			if err := statuslineInstall(settings, cfgPath, "/tmp/gv-new"); err != nil {
				t.Fatal(err)
			}
			if wrap, _ := config.StatuslineWrapFrom(cfgPath); wrap != origCmd {
				t.Fatalf("reinstall recorded %q", wrap)
			}

			if err := statuslineUninstall(settings, cfgPath); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(settings)
			if !reflect.DeepEqual(jsonOf(t, after), jsonOf(t, []byte(orig))) {
				t.Fatalf("round trip changed settings.json:\n%s\nwant (JSON-equal)\n%s", after, orig)
			}
			if _, ok := config.StatuslineWrapFrom(cfgPath); ok {
				t.Error("statusline.wrap still recorded after uninstall")
			}
			if err := statuslineUninstall(settings, cfgPath); err != nil { // no-op now
				t.Fatal(err)
			}
		})
	}
}

// The shim present with no statusline.wrap recorded is broken (a blank
// status line): install must say so and how to repair, not "installed".
func TestStatuslineInstallFlagsShimWithoutWrap(t *testing.T) {
	home := t.TempDir()
	settings := statusline.SettingsPath(home)
	cfgPath := filepath.Join(home, ".config", "grove", "config.yaml")
	if err := statusline.WriteCommand(settings, "/home/u/go/bin/gv statusline", false); err != nil {
		t.Fatal(err)
	}
	err := statuslineInstall(settings, cfgPath, "/home/u/go/bin/gv")
	if err == nil || !strings.Contains(err.Error(), "no statusline.wrap") || !strings.Contains(err.Error(), "repair") {
		t.Fatalf("install over an unrecorded shim: %v, want a repair error", err)
	}
	if _, ok := config.StatuslineWrapFrom(cfgPath); ok {
		t.Fatal("install recorded something for a broken shim")
	}
	// The repair it names: the operator's command back, install again.
	if err := statusline.WriteCommand(settings, "echo mine", false); err != nil {
		t.Fatal(err)
	}
	if err := statuslineInstall(settings, cfgPath, "/home/u/go/bin/gv"); err != nil {
		t.Fatal(err)
	}
	if wrap, _ := config.StatuslineWrapFrom(cfgPath); wrap != "echo mine" {
		t.Fatalf("after repair wrap = %q", wrap)
	}
}
