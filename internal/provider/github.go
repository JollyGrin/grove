package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// GitHub is the github-issues adapter (DESIGN.md §5.3, Phase 3): a native
// `gh` CLI wrapper, repo-rooted like markdown. gh runs with the repo as
// its working dir so owner/repo comes from the git remote — no token
// plumbing (gh auth owns it; the doctor's gh rows are the gate).
//
// Ids are fleet-unique `<repoName>-<n>`: two repos in one workspace share
// one tasks.json, so bare issue numbers would collide. Transitions follow
// DESIGN OQ3 → labels: the AGENT labels in-progress/in-review and opens
// the PR with `Closes #<n>` — the human's merge is the terminal
// transition; neither the binary nor the agent ever closes an issue.
type GitHub struct {
	repoPath string
	repoName string
	capped   bool // last List filled the fetch limit

	// run is injectable for tests: exec gh with args in dir.
	run func(dir string, args ...string) ([]byte, error)
}

const ghListLimit = 200

func NewGitHub(repoPath, repoName string) *GitHub {
	return &GitHub{repoPath: repoPath, repoName: repoName, run: runGH}
}

func runGH(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	// CombinedOutput mixes stderr in; gh keeps --json payloads on stdout
	// and only writes stderr on failure, so success output is clean.
	return out, nil
}

func (g *GitHub) Kind() string { return "github" }

var (
	ghNumRe = regexp.MustCompile(`^#?(\d+)$`)
	ghURLRe = regexp.MustCompile(`^https://github\.com/[^/]+/[^/]+/issues/(\d+)(?:[/?#].*)?$`)
)

// ParseID canonicalizes 7 / #7 / an issue URL / <repoName>-7 to
// <repoName>-7.
func (g *GitHub) ParseID(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if m := ghNumRe.FindStringSubmatch(s); m != nil {
		return g.repoName + "-" + m[1], nil
	}
	if m := ghURLRe.FindStringSubmatch(s); m != nil {
		return g.repoName + "-" + m[1], nil
	}
	low := strings.ToLower(s)
	if rest, ok := strings.CutPrefix(low, g.repoName+"-"); ok && ghNumRe.MatchString(rest) {
		return low, nil
	}
	return "", fmt.Errorf("cannot read %q as a %s issue (want 7, #7, an issue URL, or %s-7)", raw, g.repoName, g.repoName)
}

func (g *GitHub) number(id string) (string, error) {
	rest, ok := strings.CutPrefix(id, g.repoName+"-")
	if !ok || !ghNumRe.MatchString(rest) {
		return "", fmt.Errorf("not a %s issue id: %q", g.repoName, id)
	}
	return rest, nil
}

type ghIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Comments []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		Body string `json:"body"`
	} `json:"comments"`
}

func (g *GitHub) task(is ghIssue) *Task {
	t := &Task{
		ID:          fmt.Sprintf("%s-%d", g.repoName, is.Number),
		Title:       is.Title,
		Description: is.Body,
		URL:         is.URL,
		Status:      strings.ToLower(is.State),
	}
	for _, l := range is.Labels {
		t.Labels = append(t.Labels, l.Name)
	}
	for _, c := range is.Comments {
		author := c.Author.Login
		if author == "" {
			author = "unknown"
		}
		t.Comments = append(t.Comments, Comment{Author: author, Body: c.Body})
	}
	return t
}

// Get fetches one issue (closed ones included — adopt needs them).
func (g *GitHub) Get(id string) (*Task, error) {
	n, err := g.number(id)
	if err != nil {
		return nil, err
	}
	out, err := g.run(g.repoPath, "issue", "view", n,
		"--json", "number,title,body,url,state,labels,comments")
	if err != nil {
		return nil, err
	}
	var is ghIssue
	if err := json.Unmarshal(out, &is); err != nil {
		return nil, fmt.Errorf("parse gh issue view: %w", err)
	}
	return g.task(is), nil
}

// List returns the open issues (the grabbable backlog), oldest first.
// A fetch that fills the limit sets ListCapped — printBacklog surfaces
// it (no silent caps).
func (g *GitHub) List() ([]*Task, error) {
	out, err := g.run(g.repoPath, "issue", "list", "--state", "open",
		"--limit", fmt.Sprint(ghListLimit), "--json", "number,title,labels")
	if err != nil {
		return nil, err
	}
	var issues []ghIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parse gh issue list: %w", err)
	}
	g.capped = len(issues) >= ghListLimit
	tasks := make([]*Task, 0, len(issues))
	for _, is := range issues {
		is.State = "open"
		tasks = append(tasks, g.task(is))
	}
	sort.Slice(tasks, func(i, j int) bool {
		return len(tasks[i].ID) < len(tasks[j].ID) || (len(tasks[i].ID) == len(tasks[j].ID) && tasks[i].ID < tasks[j].ID)
	})
	return tasks, nil
}

// ListCapped reports whether the last List filled the fetch limit.
func (g *GitHub) ListCapped() bool { return g.capped }

func (g *GitHub) Verbs() Verbs {
	return Verbs{
		Start: "Label this task's GitHub issue as started: `gh issue edit <issue-number> --add-label in-progress` " +
			"(if the label doesn't exist yet, create it once: `gh label create in-progress`), and add a one-line " +
			"comment saying you've started.",
		Review: "open the PR with `Closes #<issue-number>` in its body (GitHub auto-links it, and the issue closes " +
			"when a HUMAN merges — NEVER close the issue yourself), then swap the label: " +
			"`gh issue edit <issue-number> --remove-label in-progress --add-label in-review`.",
	}
}

func (g *GitHub) Capabilities() Capabilities { return Capabilities{CanList: true} }
