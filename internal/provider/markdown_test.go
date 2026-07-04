package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTask(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const sampleTask = `---
id: task-001
title: Persist filter state in the URL
status: todo
priority: medium
labels: [frontend, ux]
created: 2026-07-04
---

## Description

Filters reset on reload.

## Acceptance Criteria
- [ ] Filters survive a page reload
`

func TestMarkdownGet(t *testing.T) {
	dir := t.TempDir()
	writeTask(t, dir, "task-001.md", sampleTask)
	m := NewMarkdown(dir)

	task, err := m.Get("task-001")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "task-001" || task.Title != "Persist filter state in the URL" {
		t.Errorf("got %+v", task)
	}
	if task.Status != "todo" {
		t.Errorf("status = %q", task.Status)
	}
	if len(task.Labels) != 2 || task.Labels[0] != "frontend" {
		t.Errorf("labels = %v", task.Labels)
	}
	if !strings.Contains(task.Description, "Filters survive a page reload") {
		t.Errorf("description lost body: %q", task.Description)
	}
	if strings.Contains(task.Description, "id: task-001") {
		t.Error("description must not contain frontmatter")
	}

	// Uppercase + .md-suffixed input normalizes.
	if _, err := m.Get("TASK-001.md"); err != nil {
		t.Errorf("normalized get: %v", err)
	}
}

func TestMarkdownGetByFrontmatterID(t *testing.T) {
	dir := t.TempDir()
	writeTask(t, dir, "some-other-name.md", sampleTask)
	m := NewMarkdown(dir)
	task, err := m.Get("task-001")
	if err != nil {
		t.Fatalf("frontmatter-id fallback: %v", err)
	}
	if task.ID != "task-001" {
		t.Errorf("id = %q", task.ID)
	}
}

func TestMarkdownGetMissing(t *testing.T) {
	dir := t.TempDir()
	m := NewMarkdown(dir)
	if _, err := m.Get("task-404"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("want not-found error, got %v", err)
	}
}

func TestMarkdownList(t *testing.T) {
	dir := t.TempDir()
	writeTask(t, dir, "task-002.md", "---\nid: task-002\ntitle: B\nstatus: todo\n---\nbody")
	writeTask(t, dir, "task-001.md", "---\nid: task-001\ntitle: A\nstatus: backlog\n---\nbody")
	writeTask(t, dir, "task-003.md", "---\nid: task-003\ntitle: C\nstatus: in-progress\n---\nbody")
	writeTask(t, dir, "task-004.md", "---\nid: task-004\ntitle: D\nstatus: done\n---\nbody")
	writeTask(t, dir, "task-005.md", "---\nid: task-005\ntitle: E\n---\nno status = todo")
	writeTask(t, dir, "README.txt", "not a task")

	m := NewMarkdown(dir)
	got, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, task := range got {
		ids = append(ids, task.ID)
	}
	want := "task-001 task-002 task-005"
	if strings.Join(ids, " ") != want {
		t.Errorf("List ids = %v, want %s (sorted; in-progress/done excluded)", ids, want)
	}
}

func TestMarkdownListMissingDir(t *testing.T) {
	m := NewMarkdown(filepath.Join(t.TempDir(), "nope"))
	if _, err := m.List(); err == nil || !strings.Contains(err.Error(), "gv init") {
		t.Errorf("missing dir must name the fix, got %v", err)
	}
}

func TestMarkdownMalformed(t *testing.T) {
	cases := []struct {
		name, content, wantErr string
	}{
		{"no frontmatter", "just a plain file\n", "no YAML frontmatter"},
		{"unterminated", "---\nid: x\ntitle: y\n", "unterminated"},
		{"bad yaml", "---\n\tid: [broken\n---\nbody", "frontmatter"},
		{"no title", "---\nid: task-009\n---\nbody", "no title"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTask(t, dir, "task-009.md", c.content)
			_, err := NewMarkdown(dir).Get("task-009")
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("want error containing %q, got %v", c.wantErr, err)
			}
			if err != nil && !strings.Contains(err.Error(), "task-009.md") {
				t.Errorf("error must name the file, got %v", err)
			}
		})
	}
}

func TestMarkdownCRLFAndBOM(t *testing.T) {
	dir := t.TempDir()
	content := "\ufeff---\r\nid: task-007\r\ntitle: Windows-authored\r\nstatus: todo\r\n---\r\nbody line\r\n"
	writeTask(t, dir, "task-007.md", content)
	task, err := NewMarkdown(dir).Get("task-007")
	if err != nil {
		t.Fatal(err)
	}
	if task.Title != "Windows-authored" || task.Description != "body line" {
		t.Errorf("got %+v", task)
	}
}

func TestMarkdownParseID(t *testing.T) {
	m := NewMarkdown(t.TempDir())
	for raw, want := range map[string]string{
		"task-001":    "task-001",
		"TASK-001":    "task-001",
		" task-001 ":  "task-001",
		"task-001.md": "task-001",
		"fix_login-2": "fix_login-2",
	} {
		got, err := m.ParseID(raw)
		if err != nil || got != want {
			t.Errorf("ParseID(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, bad := range []string{"", "-leading-dash", "has space", "sl/ash", "semi;colon", "../escape"} {
		if got, err := m.ParseID(bad); err == nil {
			t.Errorf("ParseID(%q) = %q, want error (ids become branch/window names)", bad, got)
		}
	}
}

func TestMarkdownVerbsUseDisplayDir(t *testing.T) {
	m := NewMarkdownAt("/abs/main/checkout/.grove/tasks", ".grove/tasks")
	v := m.Verbs()
	if strings.Contains(v.Start, "/abs/main") || !strings.Contains(v.Start, ".grove/tasks") {
		t.Errorf("Start verb must use the repo-relative dir: %q", v.Start)
	}
	if !strings.Contains(v.Review, "status: review") || strings.Contains(v.Review, "status: done`, do it") {
		t.Errorf("Review verb: %q", v.Review)
	}
	if !strings.Contains(v.Review, "NOT") {
		t.Error("Review verb must forbid the terminal transition (humans finish)")
	}
}
