package statusline

// Install / uninstall edit ONE file, the personal ~/.claude/settings.json
// (design §Decision 2: accounts never apply to another config dir). The
// edit is the `gv hooks install` discipline: decode to a generic map,
// change only `statusLine`, re-encode — every other key keeps its meaning
// (numbers are kept as json.Number so none is re-rounded).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SettingsPath is the one settings.json the shim installs into.
func SettingsPath(home string) string { return filepath.Join(home, ".claude", "settings.json") }

// ShimCommand is the statusLine.command for the gv binary at exe — an
// absolute path, as the hook commands are.
func ShimCommand(exe string) string { return exe + " statusline" }

// IsShim reports whether a statusLine.command is grove's shim: `<bin>
// statusline` with a gv/grove-named binary — the hooks' basename rule plus
// `gv-*`, the throwaway-build name (`go build -o /tmp/gv-<ticket>`). A
// miss here is not harmless: install would record the shim as its own
// wrap.
func IsShim(cmd string) bool {
	f := strings.Fields(cmd)
	if len(f) != 2 || f[1] != "statusline" {
		return false
	}
	base := filepath.Base(f[0])
	return base == "gv" || strings.HasPrefix(base, "gv-") || strings.Contains(base, "grove")
}

// Installed reports whether the settings file's statusLine runs the shim.
func Installed(settingsRaw []byte) bool {
	s, err := decode(settingsRaw)
	if err != nil {
		return false
	}
	cmd, _ := commandOf(s)
	return IsShim(cmd)
}

// ReadCommand returns the settings file's statusLine.command; present is
// false when the file or its statusLine has none.
func ReadCommand(path string) (cmd string, present bool, err error) {
	s, err := load(path)
	if err != nil {
		return "", false, err
	}
	cmd, present = commandOf(s)
	return cmd, present, nil
}

// WriteCommand sets statusLine.command to cmd, creating
// `statusLine: {type: command}` when absent; remove instead drops the
// whole statusLine key (restoring a file that never had one).
func WriteCommand(path, cmd string, remove bool) error {
	s, err := load(path)
	if err != nil {
		return err
	}
	if remove {
		delete(s, "statusLine")
	} else {
		sl, _ := s["statusLine"].(map[string]any)
		if sl == nil {
			sl = map[string]any{"type": "command"}
		}
		sl["command"] = cmd
		s["statusLine"] = sl
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // a command's `&&`/`>` stays readable
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	return writeAtomic(path, buf.Bytes(), mode)
}

func load(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	s, err := decode(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w (fix it manually first)", path, err)
	}
	return s, nil
}

func decode(raw []byte) (map[string]any, error) {
	s := map[string]any{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return s, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&s); err != nil {
		return nil, err
	}
	if s == nil { // a literal `null`
		s = map[string]any{}
	}
	return s, nil
}

func commandOf(s map[string]any) (string, bool) {
	sl, _ := s["statusLine"].(map[string]any)
	cmd, ok := sl["command"].(string)
	return cmd, ok
}
