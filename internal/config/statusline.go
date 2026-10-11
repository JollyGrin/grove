package config

// statusline.wrap (claude-accounts §Decision 8, grove-472): the operator's
// own status line command, recorded by `gv statusline install` in the
// GLOBAL config — the shim wraps ~/.claude/settings.json only, which is
// not a workspace thing.

import (
	"os"

	"gopkg.in/yaml.v3"
)

// StatuslineWrapFrom is the minimal tolerant read `gv statusline` runs on
// every status line refresh: no validation, no defaults. recorded is false
// when the key is absent (or the file unreadable); a recorded "" means
// settings.json had no status line before install.
func StatuslineWrapFrom(path string) (wrap string, recorded bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var c struct {
		Statusline struct {
			Wrap *string `yaml:"wrap"`
		} `yaml:"statusline"`
	}
	if yaml.Unmarshal(raw, &c) != nil || c.Statusline.Wrap == nil {
		return "", false
	}
	return *c.Statusline.Wrap, true
}

// SaveStatuslineWrap records wrap as statusline.wrap at path, preserving
// the rest of the file (node surgery, like SaveHotkey).
func SaveStatuslineWrap(path, wrap string) error {
	return editDoc(path, func(root *yaml.Node) {
		sl := ensureMap(root, "statusline")
		removeKey(sl, "wrap")
		sl.Content = append(sl.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "wrap"},
			// Force-quoted: a command is arbitrary text and must come back
			// as the same string, never re-typed by yaml.
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: wrap, Style: yaml.DoubleQuotedStyle})
	})
}

// ClearStatuslineWrap drops the statusline block from the file at path.
func ClearStatuslineWrap(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return editDoc(path, func(root *yaml.Node) { removeKey(root, "statusline") })
}
