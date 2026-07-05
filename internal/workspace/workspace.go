// Package workspace implements grove workspaces (DESIGN.md §6.5): a
// workspace is a root directory carrying a `.grove/` marker. Find walks
// up from the cwd to the nearest marker (like git's .git discovery), the
// registry (~/.config/grove/registry.yaml) indexes every known workspace,
// and Rollup gives a cheap, read-only fleet summary for the switcher.
package workspace

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Workspace scopes (§6.5): a single/monorepo root, or a parent folder of
// sibling repos.
const (
	ScopeRepo   = "repo"
	ScopeParent = "parent"
)

// Workspace is one grove instance: one orchestrator over one root.
type Workspace struct {
	Root  string `yaml:"root" json:"root"`
	Label string `yaml:"label" json:"label"`
	Scope string `yaml:"scope" json:"scope"`
}

// markerDir reports whether dir contains a `.grove/` directory (a plain
// file named .grove is not a marker).
func markerDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".grove"))
	return err == nil && info.IsDir()
}

// Find walks up from cwd to the nearest directory containing a `.grove/`
// marker — nearest wins, exactly like git's .git walk-up. Returns nil when
// no ancestor is a workspace. The starting path is symlink-resolved so a
// cwd reached through a symlink still lands on the real root.
func Find(cwd string) *Workspace {
	dir := cwd
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		dir = r
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	for {
		if markerDir(dir) {
			ws := describe(dir)
			return &ws
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// describe builds the Workspace for a confirmed root: label and scope come
// from the committable `<root>/.grove/config.yaml` `workspace:` block when
// present (parsed tolerantly), else label = base of the root, scope = repo.
func describe(root string) Workspace {
	ws := Workspace{Root: root, Label: filepath.Base(root), Scope: ScopeRepo}
	raw, err := os.ReadFile(filepath.Join(root, ".grove", "config.yaml"))
	if err != nil {
		return ws
	}
	var c struct {
		Workspace struct {
			Label string `yaml:"label"`
			Scope string `yaml:"scope"`
		} `yaml:"workspace"`
	}
	if yaml.Unmarshal(raw, &c) != nil {
		return ws // tolerant: unparseable config keeps the fallbacks
	}
	if c.Workspace.Label != "" {
		ws.Label = c.Workspace.Label
	}
	if c.Workspace.Scope == ScopeParent {
		ws.Scope = ScopeParent
	}
	return ws
}

// Alive reports whether the workspace's `.grove/` marker still exists —
// dead registry entries (moved/deleted roots) return false.
func Alive(ws Workspace) bool {
	return markerDir(ws.Root)
}
