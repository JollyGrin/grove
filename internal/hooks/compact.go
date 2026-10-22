package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/git"
	"github.com/JollyGrin/grove/internal/provider"
	"github.com/JollyGrin/grove/internal/state"
)

// The SessionStart re-orientation after a compaction (grove-440).
//
// Hooks reference: SessionStart fires with `source: "compact"` after an
// auto or manual compaction, and plain-text stdout from the hook is
// added to Claude's context. Anthropic's "Effective harnesses for
// long-running agents": what survives a context window is a progress
// log, a machine-readable task list and git history — the agent needs a
// way to quickly understand the state of work with a fresh context.
// Grove's equivalents are the ticket's acceptance criteria, `git log` /
// `git status` in the worktree, and the PR-body handoff (duty 8's five
// headings). Every line below is read from those sources, never from
// the compaction summary, so a summary that drifted is corrected rather
// than compounded.
//
// Contract: the receiver stays exit-0-always and never blocks. Each
// lookup is bounded and degrades to a one-line note; the ticket id and
// title always print, even with an unreadable worktree.

// reorientFetchTimeout bounds the provider fetch (a GitHub/Linear ticket
// is a network call) — a wedged backend costs the model one line, not
// the hook deadline.
var reorientFetchTimeout = 5 * time.Second

// reorientMaxLines caps each free-text block (criteria, PR body) so a
// sprawling ticket cannot re-fill the context the compaction just freed.
const reorientMaxLines = 60

// reorientation builds the ground-truth block for a compact restart.
// root is the owning workspace's root ("" for the legacy global fleet).
func reorientation(root string, task *state.Task, p Payload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Context was compacted. Ground truth for %s (read from the ticket, the worktree and the PR — not from the summary):\n\n", task.Ticket)

	title, criteria, src := ticketFacts(root, task)
	fmt.Fprintf(&b, "Task: %s — %s\n", task.Ticket, title)
	if criteria != "" {
		fmt.Fprintf(&b, "%s:\n%s\n", src, criteria)
	} else {
		fmt.Fprintf(&b, "(acceptance criteria not readable: %s)\n", src)
	}

	wt := task.Worktree
	if wt == "" {
		wt = p.Cwd
	}
	b.WriteString("\n")
	if fi, err := os.Stat(wt); err != nil || !fi.IsDir() {
		fmt.Fprintf(&b, "git log --oneline -8: (worktree %s unreadable)\n", wt)
		fmt.Fprintf(&b, "git status --short: (worktree %s unreadable)\n", wt)
	} else {
		fmt.Fprintf(&b, "git log --oneline -8 (%s, branch %s):\n%s\n", wt, branchOf(wt, task.Branch), gitLines(wt, "log", "--oneline", "-8"))
		status := gitLines(wt, "status", "--short")
		if strings.TrimSpace(status) == "" {
			status = "(clean)"
		}
		fmt.Fprintf(&b, "git status --short:\n%s\n", status)
		b.WriteString("\n")
		b.WriteString(prHandoff(wt, branchOf(wt, task.Branch)))
	}

	b.WriteString("\nSTATUS contract: end your final message with exactly one line — STATUS: QUESTION — <q> | STATUS: BLOCKED — <why> | STATUS: DONE — <what changed and how you verified it>. Finish the acceptance criteria above before DONE.\n")
	return b.String()
}

// ticketFacts returns the task's title and acceptance criteria from its
// provider, falling back to the title state recorded at grab. src labels
// the criteria block ("Acceptance criteria" / "Ticket body (head)") or,
// when criteria is "", says why nothing was readable.
func ticketFacts(root string, task *state.Task) (title, criteria, src string) {
	title = task.Title
	cfg, err := config.LoadAt(root)
	if err != nil {
		return title, "", "config: " + err.Error()
	}
	r, ok := cfg.Repos[task.Repo]
	if !ok {
		return title, "", fmt.Sprintf("repo %q not in config", task.Repo)
	}
	prov, err := provider.FromConfigKind(cfg, cfg.ProviderKindFor(r), task.Repo, r.Path)
	if err != nil {
		return title, "", "provider: " + err.Error()
	}
	type res struct {
		t   *provider.Task
		err error
	}
	ch := make(chan res, 1)
	go func() {
		t, err := prov.Get(task.Ticket)
		ch <- res{t, err}
	}()
	var fetched *provider.Task
	select {
	case r := <-ch:
		if r.err != nil {
			return title, "", "fetch: " + r.err.Error()
		}
		fetched = r.t
	case <-time.After(reorientFetchTimeout):
		return title, "", fmt.Sprintf("fetch timed out after %s", reorientFetchTimeout)
	}
	if fetched == nil {
		return title, "", "fetch: no such ticket"
	}
	if fetched.Title != "" {
		title = fetched.Title
	}
	if ac := acceptanceCriteria(fetched.Description); ac != "" {
		return title, capLines(ac, reorientMaxLines), "Acceptance criteria"
	}
	if body := strings.TrimSpace(fetched.Description); body != "" {
		return title, capLines(body, reorientMaxLines), "Ticket body (head; no acceptance-criteria heading)"
	}
	return title, "", "ticket has no body"
}

var (
	acHeading  = regexp.MustCompile(`(?i)^#{1,6}\s*acceptance\s+criteria\b`)
	anyHeading = regexp.MustCompile(`^#{1,6}\s`)
)

// acceptanceCriteria returns the body of the first "Acceptance criteria"
// markdown section (any heading level), up to the next heading, or ""
// when the description has none.
func acceptanceCriteria(desc string) string {
	lines := strings.Split(desc, "\n")
	start := -1
	for i, l := range lines {
		if acHeading.MatchString(strings.TrimSpace(l)) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if anyHeading.MatchString(lines[i]) {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

// capLines keeps the first n lines of s, noting what was dropped.
func capLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… (%d more lines)", len(lines)-n)
}

// gitLines runs one read-only git command in dir and returns its stdout
// trimmed, or a parenthesised failure note — never an error.
func gitLines(dir string, args ...string) string {
	out, err := git.Run(dir, args...)
	if err != nil {
		return fmt.Sprintf("(git %s failed: %v)", strings.Join(args, " "), err)
	}
	return strings.TrimRight(out, "\n")
}

func branchOf(wt, recorded string) string {
	if recorded != "" {
		return recorded
	}
	b, _ := git.CurrentBranch(wt)
	return b
}

// prHandoff is the PR block: number, URL and body via `gh pr view`
// bounded by ghTimeout; when gh cannot answer (no gh, no remote, timeout)
// the git-only fallback reports what the worktree knows about the
// branch's upstream instead.
func prHandoff(wt, branch string) string {
	pr, err := prView(wt, branch)
	if err != nil {
		return "PR: not readable via gh (" + err.Error() + ") — " + upstreamNote(wt, branch) + "\n"
	}
	var b strings.Builder
	state := strings.ToLower(pr.State)
	if pr.IsDraft {
		state = "draft"
	}
	fmt.Fprintf(&b, "PR #%d (%s) %s\n", pr.Number, state, pr.URL)
	if body := strings.TrimSpace(pr.Body); body != "" {
		fmt.Fprintf(&b, "PR body (the handoff — Goal / Done + verified / Verified surprises / Remaining / Next step):\n%s\n", capLines(body, reorientMaxLines))
	} else {
		b.WriteString("PR body: empty\n")
	}
	return b.String()
}

type prInfo struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	State   string `json:"state"`
	IsDraft bool   `json:"isDraft"`
	Body    string `json:"body"`
}

// prView asks gh for the branch's PR, bounded by ghTimeout.
func prView(dir, branch string) (*prInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	args := []string{"pr", "view"}
	if branch != "" {
		args = append(args, branch)
	}
	args = append(args, "--json", "number,url,state,isDraft,body")
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("gh pr view timed out after %s", ghTimeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gh pr view: %s", firstLine(msg))
	}
	var pr prInfo
	if err := json.Unmarshal(stdout.Bytes(), &pr); err != nil {
		return nil, fmt.Errorf("gh pr view: %w", err)
	}
	return &pr, nil
}

// upstreamNote is the git-only PR fallback: whether the branch has an
// upstream and how far ahead it is, so the model knows whether "open a
// PR" is even possible yet.
func upstreamNote(wt, branch string) string {
	if !git.HasRemote(wt, "origin") {
		return "no origin remote"
	}
	if !git.HasUpstream(wt) {
		return fmt.Sprintf("branch %s has no upstream (unpushed)", branch)
	}
	ahead, behind, err := git.BranchStatus(wt)
	if err != nil {
		return fmt.Sprintf("branch %s is pushed; `gh pr view` yourself", branch)
	}
	return fmt.Sprintf("branch %s is pushed (%d ahead, %d behind upstream); check `gh pr view %s` yourself", branch, ahead, behind, branch)
}
