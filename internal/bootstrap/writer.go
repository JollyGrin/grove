// The reconfigure writer: comment-preserving field-level updates to the
// global config (plan 2026-07-04-phase-1a Task 4). ensureConfig only ever
// ADDS missing entries (correct for first-run); the wizard's re-run path
// needs to update exactly the fields the human confirmed while leaving
// every other node — comments, ordering, unknown keys, hand edits —
// intact. yaml.Node round-trips preserve comments; formatting normalizes
// to the encoder's style, so callers skip the write entirely when nothing
// changed (the byte-identical guarantee for no-op runs).
package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Doc is a loaded config document plus dirty tracking, so a wizard run
// that confirms zero changes never rewrites (or reformats) the file.
type Doc struct {
	node  yaml.Node
	path  string
	dirty bool
}

// LoadDoc reads the config at path, or starts an empty document when the
// file does not exist yet.
func LoadDoc(path string) (*Doc, error) {
	d := &Doc{path: path}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(raw, &d.node); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	case os.IsNotExist(err):
		if err := yaml.Unmarshal([]byte("{}\n"), &d.node); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	// Settings-free files (empty, comment-only, `null`, bare `---`) become
	// an empty mapping so root() has something to write into; a scalar or
	// sequence root is rejected rather than overwritten. See
	// ensureRootMapping.
	if err := ensureRootMapping(&d.node, nil, path); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Doc) root() *yaml.Node { return d.node.Content[0] }

// Get returns the scalar value at the mapping path, or "" when absent.
func (d *Doc) Get(path ...string) string {
	n := d.root()
	for _, key := range path {
		n = mapValue(n, key)
		if n == nil {
			return ""
		}
	}
	return n.Value
}

// Set updates (or creates) the scalar at the mapping path, marking the doc
// dirty only on an actual change. Intermediate mappings are created as
// needed; existing nodes keep their comments and neighbors untouched.
func (d *Doc) Set(val string, path ...string) {
	if len(path) == 0 {
		return
	}
	n := d.root()
	for _, key := range path[:len(path)-1] {
		next := mapValue(n, key)
		if next == nil {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			appendKey(n, key, next)
		}
		n.Style = 0 // a `{}` seed would otherwise stay flow
		n = next
	}
	key := path[len(path)-1]
	if existing := mapValue(n, key); existing != nil {
		if existing.Value == val {
			return
		}
		existing.SetString(val)
	} else {
		n.Style = 0
		appendKey(n, key, strNode(val))
	}
	d.dirty = true
}

// SetRepoField updates one field of repos.<name>.
func (d *Doc) SetRepoField(repo, key, val string) {
	d.Set(val, "repos", repo, key)
}

// Dirty reports whether any Set actually changed a value.
func (d *Doc) Dirty() bool { return d.dirty }

// Save writes the document back — only when dirty, so confirmed-nothing
// wizard runs leave the file byte-identical.
func (d *Doc) Save() error {
	if !d.dirty {
		return nil
	}
	// Backstop: the emitter silently discards Content hung off a non-mapping
	// root, so a write that could not land is reported instead of succeeding
	// with the settings dropped (grove-201). LoadDoc already guarantees this.
	if len(d.node.Content) != 1 || d.node.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top level is not a mapping — refusing to write", d.path)
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o755); err != nil {
		return err
	}
	out, err := yaml.Marshal(&d.node)
	if err != nil {
		return err
	}
	return os.WriteFile(d.path, out, 0o644)
}
