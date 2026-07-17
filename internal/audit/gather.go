package audit

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/cost"
	"github.com/JollyGrin/grove/internal/git"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/worktree"
)

// TaskResult is one audited task row.
type TaskResult struct {
	Ticket     string       `json:"ticket"`
	Repo       string       `json:"repo"`
	Branch     string       `json:"branch"`
	Worktree   string       `json:"worktree"`
	Class      Class        `json:"class"`
	Suggestion string       `json:"suggestion"`
	Facts      Facts        `json:"facts"`
	PR         *github.PR   `json:"pr,omitempty"`
	Cost       *cost.Totals `json:"cost,omitempty"`
	Stuck      bool         `json:"stuck"` // many turns, no PR — agent-stuck suspect
	Updated    time.Time    `json:"updated"`
}

// Orphan is a git-registered worktree no active task tracks. Report-only:
// ovs never deletes what it didn't create.
type Orphan struct {
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Dirty  bool   `json:"dirty"`
}

// Report is the full audit output (the --json contract).
type Report struct {
	Tasks           []TaskResult    `json:"tasks"`
	Orphans         []Orphan        `json:"orphans"`
	OrphanProcesses []OrphanProcess `json:"orphan_processes"`
	StalePrompts    []string        `json:"stale_prompts"`
	EventsSizeBytes int64           `json:"events_size_bytes"`
}

// Gather cross-checks every active task against reality and scans for
// orphans and stale prompt files. Pure read.
func Gather(cfg *config.Config, tasks map[string]*state.Task, stateDir string) Report {
	staleAfter := time.Duration(cfg.Audit.StaleDays) * 24 * time.Hour
	active := state.Active(tasks)
	costCache := cost.NewCache()

	// Derive parked-ness once from the log (grove-33). Read-only map, safe
	// for the concurrent auditTask reads below.
	events, _ := state.ReadEvents(stateDir, 0)
	parked := state.ParkedTickets(events)

	rows := make([]TaskResult, len(active))
	var wg sync.WaitGroup
	for i, t := range active {
		wg.Add(1)
		go func(i int, t *state.Task) {
			defer wg.Done()
			rows[i] = auditTask(cfg, t, staleAfter, costCache, parked[t.Ticket])
		}(i, t)
	}
	wg.Wait()

	rep := Report{Tasks: rows}
	rep.Orphans = scanOrphans(cfg, tasks)
	rep.OrphanProcesses = scanOrphanProcesses()
	rep.StalePrompts = scanStalePrompts(stateDir, tasks)
	if fi, err := os.Stat(filepath.Join(stateDir, "events.jsonl")); err == nil {
		rep.EventsSizeBytes = fi.Size()
	}
	return rep
}

func auditTask(cfg *config.Config, t *state.Task, staleAfter time.Duration, costCache *cost.Cache, parked bool) TaskResult {
	f := Facts{
		HasSessionID: t.SessionID != "",
		Agent:        t.Agent,
		Parked:       parked,
		Paused:       t.Paused,
		Age:          time.Since(t.Updated),
	}
	if _, err := os.Stat(t.Worktree); err == nil {
		f.WorktreeExists = true
	}
	f.WindowAlive = tmux.WindowExists(t.TmuxSession, t.TmuxWindow)

	var pr *github.PR
	if repo, ok := cfg.Repos[t.Repo]; ok {
		f.LocalBranch = git.LocalBranchExists(repo.Path, t.Branch)
		if remotes, err := git.RemoteBranches(repo.Path, t.Branch); err == nil && len(remotes) > 0 {
			f.RemoteBranch = true
		}
		var err error
		pr, err = github.PRForBranch(repo.Path, t.Branch)
		if err == nil {
			f.PRKnown = true
			if pr != nil {
				f.PRState = pr.State
			}
		}
	}

	class := Classify(f, staleAfter)
	res := TaskResult{
		Ticket: t.Ticket, Repo: t.Repo, Branch: t.Branch, Worktree: t.Worktree,
		Class: class, Suggestion: Suggestion(class), Facts: f, PR: pr, Updated: t.Updated,
	}
	if tot, err := costCache.ForTask(t.Worktree); err == nil {
		res.Cost = &tot
		// "Delivery movement" is approximated by PR existence — a task that
		// burned many turns without ever producing a PR is the stuck shape.
		res.Stuck = cost.StuckFlag(tot.Turns, cfg.Cost.StuckTurns, pr != nil)
	}
	return res
}

func scanOrphans(cfg *config.Config, tasks map[string]*state.Task) []Orphan {
	tracked := map[string]bool{}
	for _, t := range tasks {
		if !t.Done {
			tracked[realpath(t.Worktree)] = true
		}
	}
	var orphans []Orphan
	for name, repo := range cfg.Repos {
		wts, err := worktree.List(repo.Path)
		if err != nil {
			continue
		}
		for _, wt := range wts {
			if wt.IsMain || tracked[realpath(wt.Path)] {
				continue
			}
			orphans = append(orphans, Orphan{Repo: name, Path: wt.Path, Branch: wt.Branch, Dirty: wt.Dirty})
		}
	}
	return orphans
}

func scanStalePrompts(stateDir string, tasks map[string]*state.Task) []string {
	entries, err := os.ReadDir(filepath.Join(stateDir, "prompts"))
	if err != nil {
		return nil
	}
	var stale []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		ticket := strings.TrimSuffix(e.Name(), ".txt")
		if t, ok := tasks[ticket]; !ok || t.Done {
			stale = append(stale, e.Name())
		}
	}
	return stale
}

func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
