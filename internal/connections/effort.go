package connections

// grove-435: the effort-override row. Claude Code resolves effort as
// CLAUDE_CODE_EFFORT_LEVEL env → --effort flag → settings → model default
// (https://code.claude.com/docs/en/model-config), so an env var in the
// shell grove launches from silently beats every `gv grab --effort`,
// and a settings-scope maxEffortLevel silently caps it. Neither warns at
// launch; this row does.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JollyGrin/grove/internal/hooks"
)

// EffortEnvVar is the Claude Code environment override for effort.
const EffortEnvVar = "CLAUDE_CODE_EFFORT_LEVEL"

// effortConnection is the doctor row: a warn-severity env check that is
// ok when nothing in the launching environment or an applicable settings
// scope overrides the --effort gv passes.
func effortConnection() Connection {
	return Connection{
		ID:          "effort-override",
		Kind:        KindEnv,
		Severity:    SeverityWarn,
		RequiredFor: []string{"grab", "adopt"},
		Title:       "--effort not overridden by the environment",
		Fix:         "unset " + EffortEnvVar + " / drop maxEffortLevel from settings.json",
		Check:       checkEffortOverride,
	}
}

// checkEffortOverride warns when CLAUDE_CODE_EFFORT_LEVEL is set in the
// process env, or when a settings.json that applies to a worker — each
// worker command's config dir (the same scopes the hooks rows check) and
// each repo's committed .claude/settings.json — sets maxEffortLevel.
func checkEffortOverride(e Env) Status {
	var found []string
	if v := e.Getenv(EffortEnvVar); v != "" {
		found = append(found, fmt.Sprintf("%s=%s in the launching env (beats every --effort, silently)", EffortEnvVar, v))
	}
	for _, path := range effortSettingsPaths(e) {
		if v := settingsMaxEffort(e, path); v != "" {
			found = append(found, fmt.Sprintf("maxEffortLevel=%s in %s (caps every --effort above it)", v, path))
		}
	}
	if len(found) == 0 {
		return Status{State: StateOK, Info: EffortEnvVar + " unset, no maxEffortLevel"}
	}
	return Status{State: StateWarn, Info: strings.Join(found, "; ")}
}

// effortSettingsPaths lists the settings.json files whose effort keys
// reach a worker: the user scope of each worker command's config dir,
// then each repo's project scope. Order is deterministic (repos sorted
// by name) so the Info line is stable.
func effortSettingsPaths(e Env) []string {
	if e.Cfg == nil || e.CfgErr != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if e.HookSettingsPaths != nil {
		for _, p := range e.HookSettingsPaths(hooks.WorkerCommands(e.Cfg)) {
			add(p)
		}
	}
	for _, name := range repoNames(e.Cfg) {
		r := e.Cfg.Repos[name]
		if r == nil || r.Path == "" {
			continue
		}
		add(filepath.Join(r.Path, ".claude", "settings.json"))
		add(filepath.Join(r.Path, ".claude", "settings.local.json"))
	}
	return out
}

// settingsMaxEffort returns the maxEffortLevel a settings.json sets, or
// "" when the file is absent, unparseable, or has no such key — none of
// which is this row's problem.
func settingsMaxEffort(e Env, path string) string {
	raw, err := e.ReadFile(path)
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	if v, ok := m["maxEffortLevel"].(string); ok {
		return v
	}
	return ""
}
