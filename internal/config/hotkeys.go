// Orchestrator hotkey persistence (grove-105): the `)` picker binds digits
// 1–8 to model profiles, written back to the config file the binding
// belongs to — the workspace's <root>/.grove/config.yaml when in a
// workspace (orchestrator is workspace-scoped, see LoadAt), else the
// global file. The write is yaml.Node surgery, not a re-marshal of the
// parsed Config: comments and unrelated content survive, though not
// byte-for-byte — the encoder re-indents and may rewrite equivalent
// syntax (anchors/merge keys, quoting style).
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
// preserving the rest of the yaml's content and comments (structure may
// be re-indented — see the package comment). Binding a taken digit
// steals it (overwrite); a profile holds at most one digit, so its
// previous digit (if any) is dropped. profile == "" unbinds the digit.
// A missing file is created with just the hotkeys block — the workspace
// .grove/ dir may exist without a config.yaml.
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
	// A comments-only (or whitespace-only) file parses to an empty
	// document — node surgery would drop those lines wholesale, so they
	// are re-emitted verbatim ahead of the fresh mapping instead.
	var preserved []byte
	if doc.Kind == 0 || len(doc.Content) == 0 {
		if len(bytes.TrimSpace(raw)) > 0 {
			preserved = append(bytes.TrimRight(raw, "\n"), '\n')
		}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{
			{Kind: yaml.MappingNode, Tag: "!!map"},
		}}
	}
	root := doc.Content[0]
	// A `null`, `~` or bare `---` file parses to one null scalar rather
	// than an empty document, so it slips the guard above; coerce it in
	// place for the same reason ensureMap does (grove-201).
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		root.Kind = yaml.MappingNode
		root.Tag = "!!map"
		root.Value = ""
	}
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
	return os.WriteFile(path, append(preserved, buf.Bytes()...), 0o644)
}

// ensureMap returns the mapping node at key inside m, appending an empty
// one when absent. A present-but-non-mapping value (a bare `orchestrator:`
// or `hotkeys:` key with all children commented out — the shape
// config.example.yaml teaches — parses as a null scalar) is coerced to a
// mapping in place: appending into the scalar would encode to nothing and
// silently drop the binding.
func ensureMap(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if v.Kind != yaml.MappingNode {
				v.Kind = yaml.MappingNode
				v.Tag = "!!map"
				v.Value = ""
				v.Style = 0
				v.Content = nil
			}
			return v
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
