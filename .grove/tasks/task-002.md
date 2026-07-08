---
id: task-002
title: "Add a --json flag to the status command"
status: backlog
priority: low
labels: [cli]
assignee: ""
dependencies: []
created: 2026-07-07
pr: ""
---

## Description

The status command only prints a human table. Add a `--json` flag that emits
machine-readable output for scripting, matching the shape of the other
read commands.

## Acceptance Criteria

- [ ] `status --json` emits valid JSON
- [ ] Field names match the existing `ls --json` convention
- [ ] Human output is unchanged when the flag is absent
