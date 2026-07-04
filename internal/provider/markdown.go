package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Markdown is the default, git-native provider (DESIGN.md §5.2): one file
// per task under <repo>/.grove/tasks/, YAML frontmatter + markdown body.
// The directory is the backlog queue; live status of grabbed tasks is
// grove's event state (frontmatter becomes canonical when the task branch
// merges).
type Markdown struct {
	dir     string // where task files are read from (usually the main checkout)
	display string // repo-relative dir used in agent-facing instructions
}

func NewMarkdown(dir string) *Markdown { return &Markdown{dir: dir, display: dir} }

// NewMarkdownAt roots reads at dir but renders display (the repo-relative
// task dir) in agent instructions — a worker acts inside its own worktree,
// so an absolute main-checkout path would be wrong there.
func NewMarkdownAt(dir, display string) *Markdown { return &Markdown{dir: dir, display: display} }

func (m *Markdown) Kind() string { return "markdown" }

var mdIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ParseID normalizes and validates a task id. Ids double as branch and
// tmux-window names, so they must be slug-safe.
func (m *Markdown) ParseID(raw string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(raw))
	id = strings.TrimSuffix(id, ".md")
	if !mdIDRe.MatchString(id) {
		return "", fmt.Errorf("invalid task id %q (want e.g. task-001 — lowercase letters, digits, - and _)", raw)
	}
	return id, nil
}

// frontmatter is the Backlog.md/task-master converged schema (DESIGN §5.2).
type frontmatter struct {
	ID           string   `yaml:"id"`
	Title        string   `yaml:"title"`
	Status       string   `yaml:"status"`
	Priority     string   `yaml:"priority"`
	Labels       []string `yaml:"labels"`
	Assignee     string   `yaml:"assignee"`
	Dependencies []string `yaml:"dependencies"`
	Created      string   `yaml:"created"`
	PR           string   `yaml:"pr"`
}

func (m *Markdown) Get(id string) (*Task, error) {
	id, err := m.ParseID(id)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(m.dir, id+".md")
	if _, err := os.Stat(path); err == nil {
		return parseTaskFile(path)
	}
	// Fallback: the file may be named differently from its frontmatter id.
	tasks, err := m.scan()
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("task %s not found in %s", id, m.dir)
}

// List returns the grabbable backlog: tasks whose frontmatter status is
// todo or backlog (or empty, treated as todo). In-flight exclusion is the
// caller's job — grove's event state is authoritative for grabbed tasks.
func (m *Markdown) List() ([]*Task, error) {
	tasks, err := m.scan()
	if err != nil {
		return nil, err
	}
	var out []*Task
	for _, t := range tasks {
		switch t.Status {
		case "todo", "backlog", "":
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Markdown) scan() ([]*Task, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, fmt.Errorf("read task dir: %w (run `gv init` to scaffold it)", err)
	}
	var out []*Task
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		t, err := parseTaskFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func parseTaskFile(path string) (*Task, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fm, body, err := splitFrontmatter(string(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var f frontmatter
	if err := yaml.Unmarshal([]byte(fm), &f); err != nil {
		return nil, fmt.Errorf("%s: parse frontmatter: %w", path, err)
	}
	if f.ID == "" {
		f.ID = strings.TrimSuffix(filepath.Base(path), ".md")
	}
	if f.Title == "" {
		return nil, fmt.Errorf("%s: frontmatter has no title", path)
	}
	return &Task{
		ID:          f.ID,
		Title:       f.Title,
		Description: strings.TrimSpace(body),
		URL:         path,
		Status:      f.Status,
		Labels:      f.Labels,
	}, nil
}

func splitFrontmatter(s string) (fm, body string, err error) {
	// Tolerate a BOM and \r\n line endings.
	s = strings.TrimPrefix(s, "\ufeff")
	norm := strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.HasPrefix(norm, "---\n") {
		return "", "", fmt.Errorf("no YAML frontmatter (file must start with ---)")
	}
	rest := norm[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", fmt.Errorf("unterminated YAML frontmatter (missing closing ---)")
	}
	fm = rest[:end]
	body = rest[end+len("\n---"):]
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	} else {
		body = ""
	}
	return fm, body, nil
}

func (m *Markdown) Verbs() Verbs {
	return Verbs{
		Start:  fmt.Sprintf("Open this task's file under %s/ in your worktree (the file whose `id:` matches this task), set `status: in-progress` in its frontmatter, and commit that change on your branch.", m.display),
		Review: fmt.Sprintf("set `status: review` in this task's file under %s/ and commit it on your branch. Do NOT set `status: done` — a human does that after merge.", m.display),
	}
}

func (m *Markdown) Capabilities() Capabilities { return Capabilities{CanList: true} }
