---
id: task-001
title: "Persist filter state in the URL"
status: todo
priority: medium
labels: [frontend]
assignee: ""
dependencies: []
created: 2026-07-07
pr: ""
---
zz
## Description

When a user applies filters on the list view, the state is lost on reload.
Encode the active filters in the URL query string so links are shareable and
survive a refresh.

## Acceptance Criteria

- [ ] Filters survive a page reload
- [ ] The URL updates as filters change (back/forward works)
- [ ] A shared URL reproduces the same filtered view

## Implementation Plan

(optional; the agent may fill this in)
