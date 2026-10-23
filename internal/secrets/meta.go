package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	metaExt = ".meta.yaml"

	// Preview bounds (design §11): never reveal more than revealCap leading
	// characters, always the last previewTail, and keep minHidden unseen.
	revealCap   = 16
	previewTail = 4
	minValueLen = 24
	minHidden   = 16
	minDistinct = 8
)

// Meta is the plaintext sidecar NAME.meta.yaml. It holds nothing derived
// from the value except Preview — no length, no hash.
type Meta struct {
	Type    string `yaml:"type,omitempty" json:"type,omitempty"`
	Preview string `yaml:"preview,omitempty" json:"preview,omitempty"`
	Reveal  int    `yaml:"reveal,omitempty" json:"reveal,omitempty"`
	SetAt   string `yaml:"set_at,omitempty" json:"set_at,omitempty"`
}

func metaPath(ns, name string) string { return filepath.Join(nsDir(ns), name+metaExt) }

// WriteMeta writes ns/NAME.meta.yaml atomically. SetAt defaults to now.
func WriteMeta(ns, name string, m Meta) error {
	if err := checkNS(ns); err != nil {
		return err
	}
	if err := checkName(name); err != nil {
		return err
	}
	if m.SetAt == "" {
		m.SetAt = time.Now().UTC().Format(time.RFC3339)
	}
	raw, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	if err := mkdirPrivate(nsDir(ns)); err != nil {
		return err
	}
	return writeAtomic(metaPath(ns, name), raw)
}

// ReadMeta reads the sidecar; a missing one is a zero Meta, not an error.
// Unknown fields are ignored.
func ReadMeta(ns, name string) (Meta, error) {
	var m Meta
	if err := checkNS(ns); err != nil {
		return m, err
	}
	if err := checkName(name); err != nil {
		return m, err
	}
	raw, err := os.ReadFile(metaPath(ns, name))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return Meta{}, fmt.Errorf("parse %s: %w", metaPath(ns, name), err)
	}
	return m, nil
}

// PreviewFor renders the opt-in masked preview: the first reveal bytes,
// `***`, the last 4. reveal 0 means no preview and no error. A refusal
// names the rule that fired; the caller still stores the secret.
func PreviewFor(value []byte, reveal int) (string, error) {
	if reveal == 0 {
		return "", nil
	}
	n := len(value)
	switch {
	case reveal < 0:
		return "", fmt.Errorf("--reveal must be 0–%d", revealCap)
	case reveal > revealCap:
		return "", fmt.Errorf("--reveal %d exceeds the cap of %d", reveal, revealCap)
	case n < minValueLen:
		return "", fmt.Errorf("value is shorter than %d characters — too short to preview safely", minValueLen)
	case n-reveal-previewTail < minHidden:
		return "", fmt.Errorf("--reveal %d would leave fewer than %d hidden characters", reveal, minHidden)
	}
	seen := map[byte]bool{}
	for _, b := range value {
		seen[b] = true
	}
	if len(seen) < minDistinct {
		return "", fmt.Errorf("value has fewer than %d distinct characters — a low-entropy prefix reveals too much", minDistinct)
	}
	return string(value[:reveal]) + "***" + string(value[n-previewTail:]), nil
}

// trimNL mirrors Set's one-trailing-newline strip so previews match the
// stored value.
func trimNL(v []byte) []byte { return []byte(strings.TrimSuffix(string(v), "\n")) }

// SetWithMeta stores value, then its sidecar. A refused preview never
// blocks the store: the secret is saved without one and the refusal comes
// back as previewErr. An existing type label is kept when typ is empty.
func SetWithMeta(ns, name string, value []byte, typ string, reveal int) (previewErr error, err error) {
	if err := Set(ns, name, value); err != nil {
		return nil, err
	}
	preview, previewErr := PreviewFor(trimNL(value), reveal)
	m := Meta{Type: typ, Preview: preview}
	if preview != "" {
		m.Reveal = reveal
	}
	if m.Type == "" {
		if old, err := ReadMeta(ns, name); err == nil {
			m.Type = old.Type
		}
	}
	return previewErr, WriteMeta(ns, name, m)
}

// Preview returns the opt-in masked preview stored beside name's
// ciphertext in the namespace Resolve would answer from. "" when none was
// stored, the name is unset, or it comes from the process environment.
func Preview(label, name string) string {
	_, ns, err := Lookup(label, name)
	if err != nil || ns == "env" {
		return ""
	}
	m, _ := ReadMeta(ns, name)
	return m.Preview
}
