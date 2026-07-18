// Orchestrator hotkey persistence (grove-105): the `)` picker binds digits
// 1–8 to model profiles, written back to the config file the binding
// belongs to — the workspace's <root>/.grove/config.yaml when in a
// workspace (orchestrator is workspace-scoped, see LoadAt), else the
// global file. The write is yaml.Node surgery, not a re-marshal of the
// parsed Config: comments and unrelated keys survive untouched.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// PathAt returns the config file a hotkey binding persists to for the
// workspace rooted at root ("" = legacy global).
func PathAt(root string) string {
	if root == "" {
		return filepath.Join(Dir(), "config.yaml")
	}
	return filepath.Join(root, ".grove", "config.yaml")
}

// SaveHotkey binds digit → profile in orchestrator.hotkeys at path,
// preserving every other byte of yaml structure (comments included).
// Binding a taken digit steals it (overwrite); a profile holds at most
// one digit, so its previous digit (if any) is dropped. profile == ""
// unbinds the digit. A missing file is created with just the hotkeys
// block — the workspace .grove/ dir may exist without a config.yaml.
func SaveHotkey(path, digit, profile string) error {
	var doc yaml.Node
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	case os.IsNotExist(err):
		// Start an empty document; the mapping is created below.
	default:
		return err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{
			{Kind: yaml.MappingNode, Tag: "!!map"},
		}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top level is not a mapping", path)
	}

	hotkeys := ensureMap(ensureMap(root, "orchestrator"), "hotkeys")
	removeKey(hotkeys, digit)
	if profile != "" {
		// One digit per profile: drop the profile's previous binding.
		for i := 0; i+1 < len(hotkeys.Content); i += 2 {
			if hotkeys.Content[i+1].Value == profile {
				hotkeys.Content = append(hotkeys.Content[:i], hotkeys.Content[i+2:]...)
				break
			}
		}
		hotkeys.Content = append(hotkeys.Content,
			// The key is force-quoted so "1" round-trips as a string, never
			// re-parsed as a yaml int.
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: digit, Style: yaml.DoubleQuotedStyle},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: profile})
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// ensureMap returns the mapping node at key inside m, appending an empty
// one when absent.
func ensureMap(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
	return v
}

// removeKey drops key (and its value) from mapping node m if present.
func removeKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
