// Workspace-aware config loading: the global file deep-merged with a
// workspace's <root>/.grove/config.yaml (DESIGN §6.5). The merge is
// yaml-level and presence-aware — layers merge as raw maps BEFORE the
// parse/default/validate pipeline runs, so a zero value set explicitly
// in either layer survives instead of being clobbered by defaulting.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// wholesaleKeys are top-level sections a workspace replaces outright when
// it sets them — no cross-layer merging of individual repos or provider
// sub-fields. Everything else (linear, notify, cost, …) merges field-wise.
var wholesaleKeys = map[string]bool{"repos": true, "provider": true}

// LoadAt loads config for the workspace rooted at root. root == "" is the
// legacy fallback: exactly Load() (global file only). Otherwise the global
// file and <root>/.grove/config.yaml are each read as raw yaml maps
// (either may be missing; both missing is Load's missing-config error),
// the workspace map is deep-merged over the global one, and the merged
// bytes run through the same parse/default/validate pipeline as Load.
func LoadAt(root string) (*Config, error) {
	if root == "" {
		return Load()
	}
	globalPath := filepath.Join(Dir(), "config.yaml")
	wsPath := filepath.Join(root, ".grove", "config.yaml")

	global, gErr := readLayer(globalPath)
	if gErr != nil && !errors.Is(gErr, fs.ErrNotExist) {
		return nil, gErr
	}
	// orchestrator is workspace-scoped, like the brain dir it configures:
	// a workspace cockpit must not inherit the global claude command
	// (typically a machine/work-specific wrapper — ccwork on the Grid).
	// Dropped from the global layer, not wholesale-merged, so it stays
	// out even when the workspace sets no orchestrator block at all.
	delete(global, "orchestrator")
	// claude_config_dir is workspace-scoped for the same reason and with a
	// sharper edge (grove-227): it names WHICH subscription's transcripts a
	// reader opens. A global value would silently redirect every workspace's
	// `gv chat` at one profile — the exact blindness this key exists to fix,
	// inverted. Dropped from the global layer, never inherited.
	delete(global, "claude_config_dir")
	ws, wErr := readLayer(wsPath)
	if wErr != nil && !errors.Is(wErr, fs.ErrNotExist) {
		return nil, wErr
	}
	if gErr != nil && wErr != nil { // both missing
		return nil, fmt.Errorf("read config: %w (create %s — see config.example.yaml)", gErr, globalPath)
	}

	merged, err := yaml.Marshal(deepMerge(global, ws, wholesaleKeys))
	if err != nil {
		return nil, fmt.Errorf("merge config %s: %w", wsPath, err)
	}
	src := wsPath
	if wErr != nil {
		src = globalPath
	}
	return parse(merged, src)
}

// readLayer reads one config layer as a raw yaml map. A missing file
// surfaces as the fs.ErrNotExist read error so the caller can treat it
// as an empty layer; malformed yaml is an error naming the file.
func readLayer(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// deepMerge merges over onto base: maps merge recursively, scalars and
// sequences from over replace, and top-level keys in wholesale replace
// outright when over sets them. Inputs are not mutated.
func deepMerge(base, over map[string]any, wholesale map[string]bool) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if wholesale[k] {
			out[k] = v
			continue
		}
		bm, baseIsMap := out[k].(map[string]any)
		om, overIsMap := v.(map[string]any)
		if baseIsMap && overIsMap {
			out[k] = deepMerge(bm, om, nil)
			continue
		}
		out[k] = v
	}
	return out
}

// StateDirAt resolves the state directory for the workspace rooted at
// root, creating it if needed. root == "" is the legacy fallback (global
// state dir). GROVE_STATE_DIR overrides all resolution either way — the
// test-harness contract that keeps E2E off live fleet state.
func StateDirAt(root string) string {
	if root == "" || os.Getenv("GROVE_STATE_DIR") != "" {
		return StateDir()
	}
	d := filepath.Join(root, ".grove", "state")
	_ = os.MkdirAll(d, 0o755)
	return d
}
