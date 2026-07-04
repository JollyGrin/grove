// Package bootstrap implements the P0 `gv init` scaffold: register the
// current git repo in the global config and create the markdown provider's
// task dir with a sample task. Deterministic only — the probe/wizard/
// AGENTS.md bootstrap agent is Phase 1 (DESIGN.md §6).
package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/JollyGrin/grove/internal/git"
)

// Result reports what init did, for the CLI to narrate.
type Result struct {
	RepoName    string
	RepoPath    string
	Base        string
	ConfigPath  string
	WroteConfig bool // false when the repo entry already existed
	TasksDir    string
	WroteSample bool // false when the tasks dir already existed
}

const sampleTaskName = "task-001.md"

const sampleTask = `---
id: task-001
title: "Replace me: a first task for this repo"
status: todo
priority: medium
labels: []
created: %s
pr: ""
---

## Description

Describe the change in enough detail that an agent working alone can do
it. Name the files or areas involved if you know them.

## Acceptance Criteria

- [ ] A concrete, checkable outcome
- [ ] Another one
`

// Run initializes grove for the git repo containing cwd. Idempotent: it
// only ever adds what is missing and never overwrites existing config
// values or task files.
func Run(cwd, configPath, created string) (*Result, error) {
	root, err := git.RepoRoot(cwd)
	if err != nil {
		return nil, fmt.Errorf("gv init must run inside a git repo: %w", err)
	}
	name := filepath.Base(root)
	base, err := git.DefaultBranch(root)
	if err != nil {
		if cur, curErr := git.CurrentBranch(root); curErr == nil && cur != "" {
			base = cur
		} else {
			base = "main"
		}
	}

	res := &Result{RepoName: name, RepoPath: root, Base: base, ConfigPath: configPath}

	if err := ensureConfig(res, configPath); err != nil {
		return nil, err
	}
	if err := ensureTasksDir(res, root, created); err != nil {
		return nil, err
	}
	return res, nil
}

// ensureConfig adds the repo to the global config, creating the file (with
// provider: markdown) when missing. Round-tripping through yaml.Node keeps
// existing comments and ordering intact.
func ensureConfig(res *Result, path string) error {
	var doc yaml.Node
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	case os.IsNotExist(err):
		if err := yaml.Unmarshal([]byte("provider:\n  kind: markdown\nrepos: {}\n"), &doc); err != nil {
			return err
		}
	default:
		return err
	}
	root := doc.Content[0]

	repos := mapValue(root, "repos")
	if repos == nil {
		repos = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		appendKey(root, "repos", repos)
	}
	repos.Style = 0 // block style — an empty `repos: {}` seed would stay flow
	if mapValue(repos, res.RepoName) != nil {
		return nil // already registered — never touch an existing entry
	}

	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	appendKey(entry, "path", strNode(res.RepoPath))
	appendKey(entry, "base", strNode(res.Base))
	appendKey(repos, res.RepoName, entry)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	res.WroteConfig = true
	return nil
}

func ensureTasksDir(res *Result, root, created string) error {
	dir := filepath.Join(root, ".grove", "tasks")
	res.TasksDir = dir
	if _, err := os.Stat(dir); err == nil {
		return nil // existing task dir — leave it alone
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sample := fmt.Sprintf(sampleTask, created)
	if err := os.WriteFile(filepath.Join(dir, sampleTaskName), []byte(sample), 0o644); err != nil {
		return err
	}
	res.WroteSample = true
	return nil
}

// --- yaml.Node helpers ---

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func appendKey(m *yaml.Node, key string, val *yaml.Node) {
	m.Content = append(m.Content, strNode(key), val)
}

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}
