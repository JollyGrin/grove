package statusline

// Install / uninstall edit ONE file, the personal ~/.claude/settings.json
// (design §Decision 2: accounts never apply to another config dir). The
// edit is the `gv hooks install` discipline: decode to a generic map,
// change only `statusLine`, re-encode — every other key keeps its meaning
// (numbers are kept as json.Number so none is re-rounded). The write
// replaces a symlink's target, never the link.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JollyGrin/grove/internal/config"
)

// SettingsPath is the one settings.json the shim installs into.
func SettingsPath(home string) string { return filepath.Join(home, ".claude", "settings.json") }

// ShimCommand is the statusLine.command for the gv binary at exe — an
// absolute path, as the hook commands are, shell-quoted when it needs it.
func ShimCommand(exe string) string { return shellQuote(exe) + " statusline" }

// IsShim reports whether a status line command invokes grove's shim
// anywhere: a gv-named binary word (gv, gv-* throwaway builds, *grove*)
// followed by `statusline`, after shell-word splitting — so a quoted path,
// `exec gv statusline`, `GROVE_ACCOUNT=x gv statusline` or `foo; gv
// statusline` all count. A miss here is not harmless: install would
// record the shim as its own wrap. (Run's GROVE_STATUSLINE_DEPTH marker
// is the backstop for what no parse can see, e.g. a script that calls
// gv.)
func IsShim(cmd string) bool {
	w := shellWords(cmd)
	for i := 0; i+1 < len(w); i++ {
		if w[i+1] == "statusline" && isGvBinary(w[i]) {
			return true
		}
	}
	return false
}

func isGvBinary(word string) bool {
	base := filepath.Base(word)
	return base == "gv" || strings.HasPrefix(base, "gv-") || strings.Contains(base, "grove")
}

// shellWords splits a command into words the way sh would for this check:
// single quotes literal, double quotes with backslash escapes, a bare
// backslash escaping the next byte, and whitespace or an unquoted
// operator byte (; & | ( ) < >) ending a word. Unbalanced quotes end at
// the string's end.
func shellWords(s string) []string {
	var (
		out  []string
		cur  strings.Builder
		have bool
	)
	flush := func() {
		if have {
			out = append(out, cur.String())
		}
		cur.Reset()
		have = false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			have = true
			for i++; i < len(s) && s[i] != '\''; i++ {
				cur.WriteByte(s[i])
			}
		case c == '"':
			have = true
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
		case c == '\\':
			have = true
			if i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			}
		case c == ' ' || c == '\t' || c == '\n' || strings.IndexByte(";&|()<>", c) >= 0:
			flush()
		default:
			have = true
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// shellQuote single-quotes s unless it is made only of shell-safe bytes.
func shellQuote(s string) string {
	safe := s != ""
	for i := 0; i < len(s) && safe; i++ {
		c := s[i]
		safe = c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("/._-+:,@%=", c) >= 0
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
// `statusLine: {type: command}` when absent. remove instead drops only
// `command`, and the statusLine object only when nothing but `type` is
// left (restoring a file that never had one, while keeping keys like
// padding).
func WriteCommand(path, cmd string, remove bool) error {
	s, err := load(path)
	if err != nil {
		return err
	}
	sl, _ := s["statusLine"].(map[string]any)
	if remove {
		if sl != nil {
			delete(sl, "command")
			if _, onlyType := sl["type"]; len(sl) == 0 || len(sl) == 1 && onlyType {
				delete(s, "statusLine")
			}
		}
	} else {
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
	return config.WriteFileAtomic(path, buf.Bytes(), 0o644)
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
