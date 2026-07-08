---
id: task-003
title: "Investigate slow cold-start on first ls"
status: in-progress
priority: high
labels: [perf]
assignee: ""
dependencies: []
created: 2026-07-07
pr: ""
---

## Description

The first `ls` after a shell opens takes noticeably longer than subsequent
runs. Profile where the time goes and propose a fix or a cache.

## Acceptance Criteria

- [ ] Root cause identified with a profile attached
- [ ] Cold-start time reduced or justified as unavoidable
