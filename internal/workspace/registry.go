package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"gopkg.in/yaml.v3"
)

// labelRe: tmux-safe lowercase labels — must start alphanumeric.
var labelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// reserved cockpit session words: cockpit sessions are `grove-<label>`,
// legacy sessions are `grove` and `grove-mobile`, and `dash` is a verb.
var reserved = map[string]bool{"grove": true, "mobile": true, "dash": true}

// ValidateLabel rejects labels that would collide with cockpit session
// names or break tmux session naming.
func ValidateLabel(s string) error {
	if !labelRe.MatchString(s) {
		return fmt.Errorf("invalid label %q: must match [a-z0-9][a-z0-9_-]*", s)
	}
	if reserved[s] {
		return fmt.Errorf("label %q is reserved (cockpit session name)", s)
	}
	return nil
}

func registryDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "grove")
}

func registryPath() string { return filepath.Join(registryDir(), "registry.yaml") }

type registryFile struct {
	Workspaces []Workspace `yaml:"workspaces"`
}

// LoadRegistry reads ~/.config/grove/registry.yaml. A missing file is an
// empty registry, not an error.
func LoadRegistry() ([]Workspace, error) {
	raw, err := os.ReadFile(registryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f registryFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", registryPath(), err)
	}
	return f.Workspaces, nil
}

// withLock runs fn while holding an exclusive flock on registry.lock —
// registry writes are rare and human-initiated; last-writer-wins beyond
// the lock is accepted (§6.5.1).
func withLock(fn func() error) error {
	if err := os.MkdirAll(registryDir(), 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(registryDir(), "registry.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock registry: %w", err)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	return fn()
}

func writeRegistry(list []Workspace) error {
	raw, err := yaml.Marshal(registryFile{Workspaces: list})
	if err != nil {
		return err
	}
	return os.WriteFile(registryPath(), raw, 0o644)
}

// AddToRegistry registers a workspace. Re-adding the same root+label is an
// idempotent update (scope refreshes); the same label on a different root
// is a conflict.
func AddToRegistry(ws Workspace) error {
	if err := ValidateLabel(ws.Label); err != nil {
		return err
	}
	return withLock(func() error {
		list, err := LoadRegistry()
		if err != nil {
			return err
		}
		for i, w := range list {
			if w.Label != ws.Label {
				continue
			}
			if w.Root != ws.Root {
				return fmt.Errorf("label %q already registered for %s", ws.Label, w.Root)
			}
			list[i] = ws // idempotent re-add: refresh the entry
			return writeRegistry(list)
		}
		return writeRegistry(append(list, ws))
	})
}

// RemoveFromRegistry drops the workspace with the given label.
func RemoveFromRegistry(label string) error {
	return withLock(func() error {
		list, err := LoadRegistry()
		if err != nil {
			return err
		}
		for i, w := range list {
			if w.Label == label {
				return writeRegistry(append(list[:i:i], list[i+1:]...))
			}
		}
		return fmt.Errorf("workspace %q not in registry", label)
	})
}
