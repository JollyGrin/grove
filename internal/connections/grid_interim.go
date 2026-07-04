package connections

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// PackGridInterim tags the Grid team's conventions, compiled in as data
// until the 1b pack loader lifts this file into the first real pack
// (plan re-scoping note / review S-3). Doctor renders these rows under
// their own heading.
const PackGridInterim = "grid-interim"

// gridPlugins are the workspace plugins workers are conventionless
// without (LEARNINGS.md 2026-06-10 incident).
var gridPlugins = []string{
	"dev-core@workspace",
	"dev-superpowers@workspace",
	"dev-linear@workspace",
	"dev-safety@workspace",
}

// GridInterim returns the grid-pack connection instances, in render order.
func GridInterim(env Env) []Connection {
	var conns []Connection

	// Universal CLAUDE.md at each repos' parent dir (grid convention),
	// deduped per parent.
	if env.Cfg != nil && env.CfgErr == nil {
		seen := map[string]bool{}
		for _, name := range repoNames(env.Cfg) {
			parent := filepath.Dir(env.Cfg.Repos[name].Path)
			if seen[parent] {
				continue
			}
			seen[parent] = true
			conns = append(conns, Connection{
				ID:       "grid:universal-claude-md:" + parent,
				Kind:     KindFile,
				Severity: SeverityError,
				Pack:     PackGridInterim,
				Title:    "universal CLAUDE.md at " + parent,
				Fix:      "ln -sn <workspace>/plugins/dev-core/templates/grid-claude-md.md " + filepath.Join(parent, "CLAUDE.md"),
				Check:    checkFile(filepath.Join(parent, "CLAUDE.md")),
			})
		}
	}

	conns = append(conns, Connection{
		ID:       "grid:ccwork-plugins",
		Kind:     KindAgentPlugins,
		Severity: SeverityError,
		Pack:     PackGridInterim,
		Title:    "grid plugins in ~/.cc-work",
		Fix:      "CLAUDE_CONFIG_DIR=~/.cc-work claude plugin marketplace add <workspace-clone> && claude plugin install dev-core@workspace ...",
		Check:    checkCcworkPlugins,
	})

	// dev-linear MCP auth can't be probed cheaply (`mcp-auth` probing is
	// 1c); a static always-warn reminder keeps it visible without lying
	// about having checked it — deliberate change from the P0 doctor's
	// always-green row, logged in docs/seed-manifest.md.
	conns = append(conns, Connection{
		ID:       "grid:dev-linear-mcp",
		Kind:     KindMCPAuth,
		Severity: SeverityWarn,
		Pack:     PackGridInterim,
		Title:    "dev-linear MCP authed (manual check)",
		Fix:      "open a worker session, call a Linear tool once (OAuth persists)",
		Check: func(Env) Status {
			return Status{State: StateWarn, Info: "cannot be probed cheaply — verify once per profile"}
		},
	})

	return conns
}

// checkCcworkPlugins reads the ccwork profile's installed_plugins.json and
// requires every grid plugin to be present.
func checkCcworkPlugins(e Env) Status {
	raw, err := e.ReadFile(filepath.Join(e.Home, ".cc-work", "plugins", "installed_plugins.json"))
	if err != nil {
		return Status{State: StateMissing, Info: "no installed_plugins.json"}
	}
	var data struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return Status{State: StateMissing, Info: err.Error()}
	}
	var missing []string
	for _, n := range gridPlugins {
		if _, ok := data.Plugins[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return Status{State: StateMissing, Info: "missing: " + strings.Join(missing, ", ")}
	}
	return Status{State: StateOK, Info: fmt.Sprintf("%d plugins", len(data.Plugins))}
}
