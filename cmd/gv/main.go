// gv — grove: turns Linear tickets into autonomous Claude Code sessions
// in detached tmux and answers "what can I act on right now?"
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/JollyGrin/grove/internal/audit"
	"github.com/JollyGrin/grove/internal/bootstrap"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/cost"
	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/doctor"
	"github.com/JollyGrin/grove/internal/git"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/hooks"
	"github.com/JollyGrin/grove/internal/kickoff"
	"github.com/JollyGrin/grove/internal/linear"
	"github.com/JollyGrin/grove/internal/provider"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/tui"
	"github.com/JollyGrin/grove/internal/worktree"
	"github.com/JollyGrin/grove/orchestrator"
)

const usage = `gv — grove

  gv init                                     register this repo + scaffold .grove/tasks/
  gv grab [<task>] [--repo name] [--manual]   task → worktree → agent (no arg: list backlog)
  gv ls [--json]                              fleet table
  gv audit [--json]                           cross-check tasks vs reality (pure read)
  gv cost [--json] [--analyze]                per-ticket token/cost estimates (pure read)
  gv answer <ticket> [text]                   reply to a waiting agent
  gv nudge <ticket> [text]                    follow-up prompt to a session
  gv attach <ticket>                          jump into the tmux window
  gv diff <ticket> [--stat]                   branch diff vs base — review without attach
  gv adopt <ticket> [--branch b] [--manual]   revive a disconnected task / adopt a branch
  gv done <ticket> [--force]                  verify merged → clean up everything
  gv untrack <ticket> [--rm] [--rm-remote]    stop tracking (git untouched unless --rm)
  gv sweep                                    clean up all merged tasks
  gv ui                                       cockpit: dashboard + orchestrator chat
  gv mobile                                   phone-sized dashboard session (for SSH/Termius)
  gv doctor                                   preflight checks
  gv hooks install|status                     wire ~/.cc-work/settings.json
  gv hook <event>                             (internal) hook receiver
  gv run-setup <repo>                         (internal) serialized worktree setup
`

func main() {
	if len(os.Args) < 2 {
		if err := cmdDashboard(); err != nil {
			fmt.Fprintln(os.Stderr, "gv:", err)
			os.Exit(1)
		}
		return
	}
	cmd, args := os.Args[1], os.Args[2:]

	// Hook receiver: always exit 0, never break a session.
	if cmd == "hook" {
		if len(args) == 1 {
			if err := hooks.Receive(config.StateDir(), args[0], os.Stdin); err != nil {
				fmt.Fprintln(os.Stderr, "gv hook:", err)
			}
		}
		return
	}

	var err error
	switch cmd {
	case "init":
		err = cmdInit()
	case "grab":
		err = cmdGrab(args)
	case "ls":
		err = cmdLs(args)
	case "audit":
		err = cmdAudit(args)
	case "cost":
		err = cmdCost(args)
	case "answer":
		err = cmdRelay(args, true)
	case "nudge":
		err = cmdRelay(args, false)
	case "attach":
		err = cmdAttach(args)
	case "diff":
		err = cmdDiff(args)
	case "adopt":
		err = cmdAdopt(args)
	case "done":
		err = cmdDone(args)
	case "untrack":
		err = cmdUntrack(args)
	case "sweep":
		err = cmdSweep(args)
	case "ui", "orchestrator":
		err = cmdUI()
	case "mobile":
		err = cmdMobile()
	case "doctor":
		err = cmdDoctor()
	case "hooks":
		err = cmdHooks(args)
	case "run-setup":
		err = cmdRunSetup(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gv:", err)
		os.Exit(1)
	}
}

// cmdDashboard runs the TUI. Attach is handled after the tea loop exits
// because tmux attach replaces the process (syscall.Exec).
func cmdDashboard() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	tui.FinishTask = finishTask
	attachTo, err := tui.Run(cfg)
	if err != nil {
		return err
	}
	if attachTo != nil {
		if !attachTo.Attached {
			maybeInjectEditor(attachTo.TmuxSession, attachTo.TmuxWindow)
			_ = state.Append(config.StateDir(), state.Event{Type: state.EvAttached, Ticket: attachTo.Ticket})
		}
		return tmux.AttachWindow(attachTo.TmuxSession, attachTo.TmuxWindow)
	}
	return nil
}

// cmdUI builds the cockpit (jayminwest-style): tmux session `gv`, pane 0 =
// dashboard TUI, pane 1 = orchestrator Claude chat in its own directory.
// The orchestrator cwd is untracked, so worker hooks ignore it; --continue
// resumes the same conversation across cockpit launches.
func cmdUI() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	dir := cfg.Orchestrator.Dir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	claudeMd := filepath.Join(dir, "CLAUDE.md")
	if _, err := os.Stat(claudeMd); os.IsNotExist(err) {
		if err := os.WriteFile(claudeMd, []byte(orchestrator.ClaudeMd), 0o644); err != nil {
			return err
		}
		fmt.Println("→ installed orchestrator CLAUDE.md at", claudeMd)
	}

	const session = "grove"
	if !tmux.SessionExists(session) {
		if err := tmux.CreateSession(session, dir); err != nil {
			return err
		}
		if err := tmux.SplitVerticalWindow(session, dir); err != nil {
			return err
		}
		if err := tmux.SendKeys(session+".0", "gv"); err != nil {
			return err
		}
		orchCmd := fmt.Sprintf("%s --continue 2>/dev/null || %s", cfg.Orchestrator.Claude, cfg.Orchestrator.Claude)
		if err := tmux.SendKeys(session+".1", orchCmd); err != nil {
			return err
		}
	}
	return tmux.AttachSession(session)
}

// cmdMobile is the phone cockpit. tmux sizes a session to its SMALLEST
// attached client, so attaching a phone to the desktop `gv` session would
// shrink every desk pane — mobile gets its own single-pane session running
// the dashboard, sized independently. Termius flow: `ssh <mac> -t 'gv
// mobile'`.
func cmdMobile() error {
	const session = "grove-mobile"
	if !tmux.SessionExists(session) {
		home, _ := os.UserHomeDir()
		if err := tmux.CreateSession(session, home); err != nil {
			return err
		}
		if err := tmux.SendKeys(session+".0", "gv"); err != nil {
			return err
		}
	}
	return tmux.AttachSession(session)
}

// parseAnywhere lets flags appear after positionals (`gv grab <url> --repo
// monorepo`) — stdlib flag stops at the first non-flag arg otherwise.
func parseAnywhere(fs *flag.FlagSet, args []string) []string {
	var positionals []string
	for {
		_ = fs.Parse(args) // ExitOnError: bad flags abort with usage
		args = fs.Args()
		if len(args) == 0 {
			return positionals
		}
		positionals = append(positionals, args[0])
		args = args[1:]
	}
}

// --- grab ---

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(title string) string {
	s := slugStrip.ReplaceAllString(strings.ToLower(title), "-")
	s = strings.Trim(s, "-")
	if len(s) > 32 {
		s = s[:32]
		if i := strings.LastIndexByte(s, '-'); i > 12 {
			s = s[:i]
		}
	}
	return s
}

func cmdGrab(args []string) error {
	fs := flag.NewFlagSet("grab", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo name from config (overrides label inference)")
	manual := fs.Bool("manual", false, "hand-driven session: task context only, no autonomous kickoff")
	positionals := parseAnywhere(fs, args)
	if len(positionals) > 1 {
		return fmt.Errorf("usage: gv grab [<task-id-or-url>] [--repo name] [--manual]")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	tasks, err := state.Load(config.StateDir())
	if err != nil {
		return err
	}

	// Provider/repo resolution order differs per kind: linear infers the
	// repo from ticket labels (fetch first), markdown roots task files in
	// the repo (resolve repo first).
	var (
		repoName string
		repo     *config.Repo
		prov     provider.Provider
		task     *provider.Task
	)
	if cfg.Provider.Kind == "linear" {
		if len(positionals) != 1 {
			return fmt.Errorf("usage: gv grab <ticket-id-or-url> [--repo name] [--manual]")
		}
		if prov, err = provider.FromConfig(cfg, ""); err != nil {
			return err
		}
		fmt.Println("→ fetching ticket from Linear…")
		id, err := prov.ParseID(positionals[0])
		if err != nil {
			return err
		}
		if task, err = prov.Get(id); err != nil {
			return err
		}
		if repoName, repo, err = cfg.ResolveRepo(*repoFlag, task.Labels); err != nil {
			return err
		}
	} else {
		if repoName, repo, err = cfg.ResolveRepo(*repoFlag, nil); err != nil {
			return err
		}
		if prov, err = provider.FromConfig(cfg, repo.Path); err != nil {
			return err
		}
		if len(positionals) == 0 {
			return printBacklog(prov, repoName, tasks)
		}
		id, err := prov.ParseID(positionals[0])
		if err != nil {
			return err
		}
		if task, err = prov.Get(id); err != nil {
			return err
		}
	}

	if t, ok := tasks[task.ID]; ok && !t.Done {
		return fmt.Errorf("%s is already tracked (worktree %s) — `gv attach %s`, `gv done %s`, or `gv adopt %s` if its window died",
			task.ID, t.Worktree, task.ID, task.ID, task.ID)
	}

	name := task.ID + "-" + slugify(task.Title)
	fmt.Printf("→ %s on %s (branch %s)\n", task.ID, repoName, name)

	if git.HasRemote(repo.Path, "origin") {
		if err := git.Fetch(repo.Path, "origin", repo.Base); err != nil {
			fmt.Fprintf(os.Stderr, "warning: git fetch failed (%v) — branching from local %s\n", err, repo.Base)
		}
	}
	baseRef, err := git.BaseRef(repo.Path, repo.Base)
	if err != nil {
		return err
	}
	wt, err := worktree.Add(repo.Path, name, baseRef)
	if err != nil {
		return err
	}
	fmt.Printf("→ worktree %s\n", wt.Path)

	for _, envFile := range []string{".env", ".envrc", ".env.local"} {
		src := filepath.Join(repo.Path, envFile)
		if data, err := os.ReadFile(src); err == nil {
			_ = os.WriteFile(filepath.Join(wt.Path, envFile), data, 0o600)
			fmt.Printf("→ copied %s\n", envFile)
		}
	}

	promptMode := kickoff.ModeDefault
	if *manual {
		promptMode = kickoff.ModeManual
	}
	prompt, err := kickoff.Render(task, prov.Verbs(), prov.Kind(), repo.Prompt, promptMode)
	if err != nil {
		return err
	}
	promptDir := filepath.Join(config.StateDir(), "prompts")
	_ = os.MkdirAll(promptDir, 0o755)
	promptPath := filepath.Join(promptDir, task.ID+".txt")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return err
	}

	sessionName := tmux.SessionName(repoName)
	if _, err := tmux.EnsureSession(repoName, repo.Path); err != nil {
		return err
	}
	windowName := tmux.WindowName(name)
	if err := tmux.CreateWindow(sessionName, windowName, wt.Path); err != nil {
		return err
	}
	windowTarget := sessionName + ":" + windowName
	if err := tmux.SplitVerticalWindow(windowTarget, wt.Path); err != nil {
		return err
	}

	// Pane 1: (serialized setup) && claude with the prompt as argv via
	// command substitution — single line, no send-keys mangling, and the
	// pane returns to a shell if claude exits.
	claudeCmd := fmt.Sprintf(`%s "$(cat %q)"`, repo.Claude, promptPath)
	if repo.Setup != "" {
		exe, _ := os.Executable()
		claudeCmd = fmt.Sprintf("%s run-setup %s && %s", exe, repoName, claudeCmd)
	}
	if err := tmux.SendKeys(windowTarget+".1", claudeCmd); err != nil {
		return err
	}

	if err := state.Append(config.StateDir(), state.Event{
		Type: state.EvTaskCreated, Ticket: task.ID,
		Data: map[string]string{
			"title": task.Title, "url": task.URL, "repo": repoName,
			"branch": name, "worktree": wt.Path,
			"tmux_session": sessionName, "tmux_window": windowName,
		},
	}); err != nil {
		return err
	}

	mode := "autonomous"
	if *manual {
		mode = "manual — attach to drive it"
	}
	fmt.Printf("✓ %s grabbed (%s)\n  watch:  gv ls\n  attach: gv attach %s\n", task.ID, mode, task.ID)
	return nil
}

// printBacklog renders the provider's grabbable backlog (gv grab with no
// args), excluding tasks grove already has in flight — the event state is
// authoritative for those (DESIGN.md §5.2).
func printBacklog(prov provider.Provider, repoName string, tracked map[string]*state.Task) error {
	if !prov.Capabilities().CanList {
		return fmt.Errorf("usage: gv grab <task-id> [--repo name] [--manual] (the %s provider cannot list)", prov.Kind())
	}
	backlog, err := prov.List()
	if err != nil {
		return err
	}
	var rows []*provider.Task
	for _, task := range backlog {
		if t, ok := tracked[task.ID]; ok && !t.Done {
			continue // in flight — grove's live state wins over frontmatter
		}
		rows = append(rows, task)
	}
	if len(rows) == 0 {
		fmt.Printf("no grabbable tasks for %s — add a file under the task dir (see `gv init`)\n", repoName)
		return nil
	}
	fmt.Printf("grabbable tasks (%s):\n", repoName)
	for _, task := range rows {
		status := task.Status
		if status == "" {
			status = "todo"
		}
		fmt.Printf("  %-12s %-8s %s\n", task.ID, status, task.Title)
	}
	fmt.Println("\ngv grab <task-id> to start one")
	return nil
}

// cmdInit is the P0 deterministic scaffold: register the cwd repo in
// ~/.config/grove/config.yaml and create .grove/tasks/ with a sample task.
// The probe/wizard/AGENTS.md bootstrap is Phase 1.
func cmdInit() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(config.Dir(), "config.yaml")
	res, err := bootstrap.Run(cwd, cfgPath, time.Now().Format("2006-01-02"))
	if err != nil {
		return err
	}
	if res.WroteConfig {
		fmt.Printf("✓ registered repo %q (base %s) in %s\n", res.RepoName, res.Base, res.ConfigPath)
	} else {
		fmt.Printf("• repo %q already registered in %s\n", res.RepoName, res.ConfigPath)
	}
	if res.WroteSample {
		fmt.Printf("✓ scaffolded %s with a sample task\n", res.TasksDir)
	} else {
		fmt.Printf("• task dir %s already exists\n", res.TasksDir)
	}
	fmt.Printf(`
next steps:
  1. edit %s (title, description, acceptance criteria)
  2. gv doctor                  # preflight
  3. gv grab                    # list grabbable tasks
  4. gv grab task-001           # worktree + tmux + autonomous worker
  5. gv ls                      # watch the fleet
`, filepath.Join(res.TasksDir, "task-001.md"))
	return nil
}

// run-setup serializes per-repo setup commands behind a lockfile so three
// simultaneous grabs don't run three pnpm installs at once.
func cmdRunSetup(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gv run-setup <repo>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	repo, ok := cfg.Repos[args[0]]
	if !ok {
		return fmt.Errorf("unknown repo %q", args[0])
	}
	if repo.Setup == "" {
		return nil
	}

	lockPath := filepath.Join(config.StateDir(), "setup-"+args[0]+".lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	fmt.Printf("gv: waiting for setup lock (%s)…\n", args[0])
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	fmt.Printf("gv: running setup: %s\n", repo.Setup)
	cmd := exec.Command("sh", "-c", repo.Setup)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

// --- ls ---

type lsRow struct {
	*state.Task
	Live string       `json:"live"`
	PR   *github.PR   `json:"pr,omitempty"`
	Cost *cost.Totals `json:"cost,omitempty"`
}

func cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	noPR := fs.Bool("no-pr", false, "skip gh PR lookups (faster)")
	noCost := fs.Bool("no-cost", false, "skip transcript scanning for the COST column (faster)")
	parseAnywhere(fs, args)

	cfg, cfgErr := config.Load()
	tasks, err := state.Load(config.StateDir())
	if err != nil {
		return err
	}
	active := state.Active(tasks)

	prs := map[string]*github.PR{}
	if !*noPR && cfgErr == nil {
		lookups := map[string][2]string{}
		for _, t := range active {
			if r, ok := cfg.Repos[t.Repo]; ok {
				lookups[t.Ticket] = [2]string{r.Path, t.Branch}
			}
		}
		prs = github.FetchAll(lookups)
	}

	costCache := cost.NewCache()
	rows := make([]lsRow, 0, len(active))
	for _, t := range active {
		live := detect.DetectLive(t.TmuxSession, t.TmuxWindow)
		liveStr := "gone"
		if live.Exists {
			liveStr = live.Status.String()
		}
		row := lsRow{Task: t, Live: liveStr, PR: prs[t.Ticket]}
		if !*noCost {
			if tot, err := costCache.ForTask(t.Worktree); err == nil {
				row.Cost = &tot
			}
		}
		rows = append(rows, row)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}

	if len(rows) == 0 {
		fmt.Println("no active tasks — `gv grab <ticket>` to start one")
		return nil
	}
	fmt.Printf("%-11s %-11s %-10s %-8s %-9s %-5s %-9s %-8s %s\n",
		"TICKET", "REPO", "STATUS", "LIVE", "PR", "CI", "PREVIEW", "COST", "AGE")
	for _, r := range rows {
		pr, ci, preview := "—", "—", "—"
		if r.PR != nil {
			pr = fmt.Sprintf("#%d", r.PR.Number)
			if r.PR.State == "MERGED" {
				pr += " ⬢"
			}
			switch r.PR.CI {
			case "pass":
				ci = "✓"
			case "fail":
				ci = "✗"
			case "pending":
				ci = "◌"
			}
			if r.PR.PreviewURL != "" {
				preview = "⬡ up"
			}
		}
		fmt.Printf("%-11s %-11s %-10s %-8s %-9s %-5s %-9s %-8s %s\n",
			r.Ticket, r.Repo, r.Label(), r.Live, pr, ci, preview, fmtUSD(r.Cost), age(r.Created))
		if r.Agent == state.AgentWaiting && r.Question != "" {
			fmt.Printf("  ◆ %s\n", truncateLine(r.Question, 90))
		}
		if r.Agent == state.AgentBlocked && r.Question != "" {
			fmt.Printf("  ⚠ %s\n", truncateLine(r.Question, 90))
		}
	}
	return nil
}

// fmtUSD renders an estimate compactly: "$4.20", "$123" — "~" prefix when
// some entries had no pricing (partial estimate), "—" when nothing billed.
func fmtUSD(t *cost.Totals) string {
	if t == nil || t.Turns == 0 {
		return "—"
	}
	prefix := "$"
	if !t.CostKnown {
		prefix = "~$"
	}
	if t.USD >= 100 {
		return fmt.Sprintf("%s%.0f", prefix, t.USD)
	}
	return fmt.Sprintf("%s%.2f", prefix, t.USD)
}

func age(t time.Time) string {
	d := time.Since(t).Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func truncateLine(s string, n int) string {
	s = strings.SplitN(s, "\n", 2)[0]
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// --- audit ---

func cmdAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	parseAnywhere(fs, args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	tasks, err := state.Load(config.StateDir())
	if err != nil {
		return err
	}
	rep := audit.Gather(cfg, tasks, config.StateDir())

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}

	if len(rep.Tasks) == 0 {
		fmt.Println("no active tasks")
	} else {
		fmt.Printf("%-11s %-11s %-13s %-4s %-4s %-7s %-7s %s\n",
			"TICKET", "REPO", "CLASS", "WT", "WIN", "PR", "AGE", "SUGGESTED")
		for _, r := range rep.Tasks {
			pr := "—"
			if !r.Facts.PRKnown {
				pr = "?"
			} else if r.Facts.PRState != "" {
				pr = strings.ToLower(r.Facts.PRState)
			}
			fmt.Printf("%-11s %-11s %-13s %-4s %-4s %-7s %-7s %s\n",
				r.Ticket, r.Repo, r.Class, mark(r.Facts.WorktreeExists), mark(r.Facts.WindowAlive),
				pr, age(r.Updated), r.Suggestion)
		}
	}

	if len(rep.Orphans) > 0 {
		fmt.Printf("\nORPHAN WORKTREES (not tracked by gv — report only, never deleted by gv):\n")
		for _, o := range rep.Orphans {
			dirty := ""
			if o.Dirty {
				dirty = "  (dirty)"
			}
			fmt.Printf("  %-11s %s%s\n", o.Repo, o.Path, dirty)
		}
	}
	if len(rep.StalePrompts) > 0 {
		fmt.Printf("\n%d stale prompt file(s) for done tasks (gv sweep prunes them)\n", len(rep.StalePrompts))
	}
	fmt.Printf("\nevents.jsonl: %.1f KB\n", float64(rep.EventsSizeBytes)/1024)
	return nil
}

func mark(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

// --- cost ---

type costRow struct {
	Ticket string      `json:"ticket"`
	Repo   string      `json:"repo"`
	Done   bool        `json:"done"`
	Cost   cost.Totals `json:"cost"`
}

// cmdCost reports per-ticket token/cost estimates (active table + done
// rollup). Pure read; numbers are estimates — on a Max plan, $ is a
// relative-effort signal, not a bill.
func cmdCost(args []string) error {
	fs := flag.NewFlagSet("cost", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	analyze := fs.Bool("analyze", false, "outcome-priced ledger with analysis flags")
	parseAnywhere(fs, args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	tasks, err := state.Load(config.StateDir())
	if err != nil {
		return err
	}
	if *analyze {
		return costAnalyze(cfg, tasks, *asJSON)
	}

	cache := cost.NewCache()
	var rows []costRow
	var doneUSD float64
	var doneCount, doneTurns int
	for _, t := range tasks {
		tot, err := cache.ForTask(t.Worktree)
		if err != nil || tot.Turns == 0 {
			continue
		}
		if t.Done {
			doneCount++
			doneUSD += tot.USD
			doneTurns += tot.Turns
			if *asJSON {
				rows = append(rows, costRow{Ticket: t.Ticket, Repo: t.Repo, Done: true, Cost: tot})
			}
			continue
		}
		rows = append(rows, costRow{Ticket: t.Ticket, Repo: t.Repo, Cost: tot})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Cost.USD > rows[j].Cost.USD })

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}

	if len(rows) == 0 {
		fmt.Println("no active tasks with transcripts")
	} else {
		fmt.Printf("%-11s %-11s %-8s %-6s %-8s %-8s %-7s\n",
			"TICKET", "REPO", "EST $", "TURNS", "IN", "OUT", "CACHE%")
		for _, r := range rows {
			fmt.Printf("%-11s %-11s %-8s %-6d %-8s %-8s %-7s\n",
				r.Ticket, r.Repo, fmtUSD(&r.Cost), r.Cost.Turns,
				fmtTok(r.Cost.Input), fmtTok(r.Cost.Output),
				fmt.Sprintf("%.0f%%", 100*r.Cost.CacheReadShare()))
		}
	}
	fmt.Printf("\ndone tasks: %d · est $%.2f total · %d turns  (estimates, not billing)\n",
		doneCount, doneUSD, doneTurns)
	return nil
}

type analyzeRow struct {
	Ticket  string      `json:"ticket"`
	Repo    string      `json:"repo"`
	Done    bool        `json:"done"`
	Outcome string      `json:"outcome"` // merged | closed | open | none | unknown
	Steers  int         `json:"steers"`
	Flags   []string    `json:"flags,omitempty"`
	Cost    cost.Totals `json:"cost"`
}

type analyzeReport struct {
	Rows           []analyzeRow       `json:"rows"`
	TotalUSD       float64            `json:"total_est_usd"`
	MergedCount    int                `json:"merged_count"`
	USDPerMergedPR float64            `json:"est_usd_per_merged_pr"`
	AbandonedUSD   float64            `json:"est_usd_on_abandoned"` // closed-PR tickets: pure waste
	ByRepoUSD      map[string]float64 `json:"by_repo_est_usd"`
}

// costAnalyze assembles the outcome-priced ledger: per ticket → cost,
// tokens, steering, PR outcome, deterministic flags — the judgment layer
// (which ticket shapes burn tokens, what to change) belongs to the
// orchestrator reading the --json form. Pure read.
func costAnalyze(cfg *config.Config, tasks map[string]*state.Task, asJSON bool) error {
	cache := cost.NewCache()
	steers, err := state.EventCounts(config.StateDir(), state.EvAnswered)
	if err != nil {
		return err
	}

	// PR outcomes concurrently — every ticket ever tracked (done included).
	type prRes struct {
		ticket  string
		outcome string
	}
	outcomes := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range tasks {
		repo, ok := cfg.Repos[t.Repo]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(ticket, path, branch string) {
			defer wg.Done()
			out := "unknown"
			if pr, err := github.PRForBranch(path, branch); err == nil {
				switch {
				case pr == nil:
					out = "none"
				case pr.State == "MERGED":
					out = "merged"
				case pr.State == "CLOSED":
					out = "closed"
				default:
					out = "open"
				}
			}
			mu.Lock()
			outcomes[ticket] = out
			mu.Unlock()
		}(t.Ticket, repo.Path, t.Branch)
	}
	wg.Wait()

	rep := analyzeReport{ByRepoUSD: map[string]float64{}}
	var mergedUSDs []float64
	for _, t := range tasks {
		tot, err := cache.ForTask(t.Worktree)
		if err != nil || tot.Turns == 0 {
			continue
		}
		row := analyzeRow{
			Ticket: t.Ticket, Repo: t.Repo, Done: t.Done,
			Outcome: outcomes[t.Ticket], Steers: steers[t.Ticket], Cost: tot,
		}
		rep.Rows = append(rep.Rows, row)
		rep.TotalUSD += tot.USD
		rep.ByRepoUSD[t.Repo] += tot.USD
		switch row.Outcome {
		case "merged":
			rep.MergedCount++
			mergedUSDs = append(mergedUSDs, tot.USD)
		case "closed":
			rep.AbandonedUSD += tot.USD
		}
	}
	medianMerged := median(mergedUSDs)
	if rep.MergedCount > 0 {
		var mergedTotal float64
		for _, v := range mergedUSDs {
			mergedTotal += v
		}
		rep.USDPerMergedPR = mergedTotal / float64(rep.MergedCount)
	}
	for i := range rep.Rows {
		r := &rep.Rows[i]
		if cost.StuckFlag(r.Cost.Turns, cfg.Cost.StuckTurns, r.Outcome != "none" && r.Outcome != "unknown") {
			r.Flags = append(r.Flags, "stuck: many turns, no PR")
		}
		if cost.SteeringAnomaly(r.Steers, r.Cost.Turns) {
			r.Flags = append(r.Flags, "steering: >25% of turns needed a human answer")
		}
		if cost.CostOutlier(r.Cost.USD, medianMerged) {
			r.Flags = append(r.Flags, "cost: ≥2× median of merged tickets")
		}
	}
	sort.Slice(rep.Rows, func(i, j int) bool { return rep.Rows[i].Cost.USD > rep.Rows[j].Cost.USD })

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}

	fmt.Printf("%-11s %-11s %-9s %-8s %-6s %-6s %-7s %s\n",
		"TICKET", "REPO", "OUTCOME", "EST $", "TURNS", "STEER", "CACHE%", "FLAGS")
	for _, r := range rep.Rows {
		fmt.Printf("%-11s %-11s %-9s %-8s %-6d %-6d %-7s %s\n",
			r.Ticket, r.Repo, r.Outcome, fmtUSD(&r.Cost), r.Cost.Turns, r.Steers,
			fmt.Sprintf("%.0f%%", 100*r.Cost.CacheReadShare()), strings.Join(r.Flags, "; "))
	}
	fmt.Printf("\ntotal est $%.2f · %d merged (est $%.2f per merged PR) · est $%.2f on abandoned tickets\n",
		rep.TotalUSD, rep.MergedCount, rep.USDPerMergedPR, rep.AbandonedUSD)
	for repo, usd := range rep.ByRepoUSD {
		fmt.Printf("  %-11s est $%.2f\n", repo, usd)
	}
	fmt.Println("(estimates from transcript token counts — a relative-effort signal, not billing)")
	return nil
}

func median(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vs...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

// fmtTok renders token counts compactly: 950, 9.9k, 1.2M.
func fmtTok(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// --- answer / nudge ---

func cmdRelay(args []string, isAnswer bool) error {
	verb := "nudge"
	if isAnswer {
		verb = "answer"
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: gv %s <ticket> [text]", verb)
	}
	t, err := findTask(args[0])
	if err != nil {
		return err
	}

	if isAnswer && t.Question != "" {
		fmt.Printf("◆ %s asked:\n  %s\n\n", t.Ticket, t.Question)
	}
	text := strings.TrimSpace(strings.Join(args[1:], " "))
	if text == "" {
		fmt.Printf("%s> ", verb)
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		if sc.Scan() {
			text = strings.TrimSpace(sc.Text())
		}
	}
	if text == "" {
		return fmt.Errorf("empty %s — nothing sent", verb)
	}

	// Resolve the claude pane — usually .1, but a window that lost its
	// split runs claude in its only pane (typing into .1 would miss).
	pane := fmt.Sprintf("%s:%s.%d", t.TmuxSession, t.TmuxWindow, tmux.ClaudePane(t.TmuxSession, t.TmuxWindow))
	// Single character → raw key without Enter (option pickers / plan
	// approval). Anything longer → bracketed paste + Enter.
	if len([]rune(text)) == 1 {
		err = tmux.SendRawKey(pane, text)
	} else {
		err = tmux.PasteText(pane, text)
	}
	if err != nil {
		return err
	}
	if err := state.Append(config.StateDir(), state.Event{Type: state.EvAnswered, Ticket: t.Ticket}); err != nil {
		return err
	}
	fmt.Printf("✓ sent to %s\n", t.Ticket)
	return nil
}

// --- attach ---

func cmdAttach(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gv attach <ticket>")
	}
	t, err := findTask(args[0])
	if err != nil {
		return err
	}
	if !t.Attached {
		maybeInjectEditor(t.TmuxSession, t.TmuxWindow)
		_ = state.Append(config.StateDir(), state.Event{Type: state.EvAttached, Ticket: t.Ticket})
	}
	return tmux.AttachWindow(t.TmuxSession, t.TmuxWindow)
}

// maybeInjectEditor lazily starts nvim in pane 0 on first attach (10
// headless worktrees × tsserver is real RAM) — but only when pane 0 is
// not where claude lives: a window that lost its split would otherwise
// get "nvim ." typed INTO the agent session.
func maybeInjectEditor(session, window string) {
	if tmux.ClaudePane(session, window) == 0 {
		return
	}
	_ = tmux.SendKeys(session+":"+window+".0", "nvim .")
}

// --- diff ---

// cmdDiff shows the branch's work-since-fork without attaching: git's own
// color and pager apply (stdio inherited).
func cmdDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	stat := fs.Bool("stat", false, "summary form (files + line counts)")
	positionals := parseAnywhere(fs, args)
	if len(positionals) != 1 {
		return fmt.Errorf("usage: gv diff <ticket> [--stat]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	t, err := findTask(positionals[0])
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(t.Worktree); statErr != nil {
		return fmt.Errorf("%s: worktree %s is gone — `gv adopt %s` to re-create it", t.Ticket, t.Worktree, t.Ticket)
	}
	base := "main"
	if repo, ok := cfg.Repos[t.Repo]; ok {
		base = repo.Base
	}
	gitArgs := []string{"diff", git.DiffBase(t.Worktree, base) + "...HEAD"}
	if *stat {
		gitArgs = append(gitArgs, "--stat")
	}
	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = t.Worktree
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

// --- adopt ---

// cmdAdopt revives a task from whatever survives a disconnect: an intact
// worktree, a local or remote-only branch, a stored session id — or, for
// tickets gv never tracked, just a branch on origin. Fallback chain:
// missing worktree → AddExisting; stored session id → resume with a
// pickup-prompt fallback; no id → pickup prompt.
func cmdAdopt(args []string) error {
	fs := flag.NewFlagSet("adopt", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo name from config (cold adopt: overrides label inference)")
	branchFlag := fs.String("branch", "", "branch to adopt (default: from state, or origin/<ticket>-* inference)")
	manual := fs.Bool("manual", false, "hand-driven session: ticket context only, no autonomous pickup")
	positionals := parseAnywhere(fs, args)
	if len(positionals) != 1 {
		return fmt.Errorf("usage: gv adopt <ticket> [--repo name] [--branch b] [--manual]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	id, err := parseAnyID(cfg, positionals[0])
	if err != nil {
		return err
	}
	tasks, err := state.Load(config.StateDir())
	if err != nil {
		return err
	}

	// Resolve repo, branch, and prior session — from state if gv has ever
	// seen this task (active, done, or untracked), else cold via provider.
	var repoName, branch, sessionID string
	var task *provider.Task
	if t, ok := tasks[id]; ok {
		repoName, branch, sessionID = t.Repo, t.Branch, t.SessionID
		task = &provider.Task{ID: t.Ticket, Title: t.Title, URL: t.URL}
		if !t.Done && tmux.WindowExists(t.TmuxSession, t.TmuxWindow) {
			return fmt.Errorf("%s already has a live window — `gv attach %s`", id, id)
		}
	}
	if *branchFlag != "" {
		branch = *branchFlag
	}

	// A repo is needed before the provider exists (markdown roots task
	// files in the repo). From state, else --repo/label inference — cold
	// markdown adopts can't infer from labels, so --repo or sole-repo.
	if repoName == "" {
		var labels []string
		if task != nil {
			labels = task.Labels
		}
		repoName, _, err = cfg.ResolveRepo(*repoFlag, labels)
		if err != nil {
			return err
		}
	}
	repo, ok := cfg.Repos[repoName]
	if !ok {
		return fmt.Errorf("repo %q no longer in config", repoName)
	}

	// Fresh task fetch enriches the pickup prompt (description + new
	// comments). Non-fatal for tracked tasks — offline adopt still works
	// with the fields state carries.
	prov, provErr := provider.FromConfig(cfg, repo.Path)
	if provErr == nil {
		if fetched, fetchErr := prov.Get(id); fetchErr == nil {
			task = fetched
		} else if task == nil {
			return fmt.Errorf("%s is not tracked and the %s fetch failed: %w", id, cfg.Provider.Kind, fetchErr)
		} else {
			fmt.Fprintf(os.Stderr, "warning: %s fetch failed (%v) — pickup prompt uses stored task fields\n", cfg.Provider.Kind, fetchErr)
		}
	} else if task == nil {
		return fmt.Errorf("%s is not tracked and %v", id, provErr)
	}

	if branch == "" {
		candidates, err := git.RemoteBranches(repo.Path, id+"-*")
		if err != nil {
			return fmt.Errorf("branch inference (origin/%s-*): %w", id, err)
		}
		switch len(candidates) {
		case 0:
			return fmt.Errorf("no branch matching origin/%s-* in %s — pass --branch", id, repoName)
		case 1:
			branch = candidates[0]
		default:
			return fmt.Errorf("multiple branches match origin/%s-*: %s — pass --branch", id, strings.Join(candidates, ", "))
		}
	}

	fmt.Printf("→ adopting %s on %s (branch %s)\n", id, repoName, branch)

	// Worktree: reuse as-is when present (never touch dirty files), else
	// re-create from the existing branch.
	wtPath := worktree.DefaultPath(repo.Path, branch)
	freshWorktree := false
	if _, statErr := os.Stat(wtPath); statErr != nil {
		wt, err := worktree.AddExisting(repo.Path, branch)
		if err != nil {
			return err
		}
		wtPath = wt.Path
		freshWorktree = true
		for _, envFile := range []string{".env", ".envrc", ".env.local"} {
			src := filepath.Join(repo.Path, envFile)
			if data, err := os.ReadFile(src); err == nil {
				_ = os.WriteFile(filepath.Join(wtPath, envFile), data, 0o600)
				fmt.Printf("→ copied %s\n", envFile)
			}
		}
		fmt.Printf("→ worktree %s\n", wtPath)
	} else {
		fmt.Printf("→ reusing worktree %s\n", wtPath)
	}

	// Pickup (or manual) prompt — also the fallback when resume fails.
	promptMode := kickoff.ModePickup
	if *manual {
		promptMode = kickoff.ModeManual
	}
	var verbs provider.Verbs
	if provErr == nil {
		verbs = prov.Verbs()
	}
	prompt, err := kickoff.Render(task, verbs, cfg.Provider.Kind, "", promptMode)
	if err != nil {
		return err
	}
	promptDir := filepath.Join(config.StateDir(), "prompts")
	_ = os.MkdirAll(promptDir, 0o755)
	promptPath := filepath.Join(promptDir, id+".txt")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return err
	}

	sessionName := tmux.SessionName(repoName)
	if _, err := tmux.EnsureSession(repoName, repo.Path); err != nil {
		return err
	}
	windowName := tmux.WindowName(branch)
	if tmux.WindowExists(sessionName, windowName) {
		return fmt.Errorf("window %s:%s already exists — `gv attach %s`", sessionName, windowName, id)
	}
	if err := tmux.CreateWindow(sessionName, windowName, wtPath); err != nil {
		return err
	}
	windowTarget := sessionName + ":" + windowName
	if err := tmux.SplitVerticalWindow(windowTarget, wtPath); err != nil {
		return err
	}

	// Event BEFORE the pane command: FindByCwd skips Done tasks, so the
	// revived session's SessionStart hook only matches (and captures the
	// new session id) once the fold has flipped Done=false.
	if err := state.Append(config.StateDir(), state.Event{
		Type: state.EvTaskAdopted, Ticket: id,
		Data: map[string]string{
			"title": task.Title, "url": task.URL, "repo": repoName,
			"branch": branch, "worktree": wtPath,
			"tmux_session": sessionName, "tmux_window": windowName,
		},
	}); err != nil {
		return err
	}

	claudeCmd := fmt.Sprintf(`%s "$(cat %q)"`, repo.Claude, promptPath)
	if sessionID != "" && !*manual {
		claudeCmd = fmt.Sprintf("%s --resume %s || %s", repo.Claude, sessionID, claudeCmd)
	}
	if freshWorktree && repo.Setup != "" {
		exe, _ := os.Executable()
		claudeCmd = fmt.Sprintf("%s run-setup %s && %s", exe, repoName, claudeCmd)
	}
	if err := tmux.SendKeys(windowTarget+".1", claudeCmd); err != nil {
		return err
	}

	how := "pickup prompt"
	if sessionID != "" && !*manual {
		how = "resume " + sessionID + " (pickup prompt fallback)"
	}
	if *manual {
		how = "manual — attach to drive it"
	}
	fmt.Printf("✓ %s adopted (%s)\n  watch:  gv ls\n  attach: gv attach %s\n", id, how, id)
	return nil
}

// --- done / sweep ---

func cmdDone(args []string) error {
	fs := flag.NewFlagSet("done", flag.ExitOnError)
	force := fs.Bool("force", false, "clean up even if the PR is not merged (or none exists)")
	positionals := parseAnywhere(fs, args)
	if len(positionals) != 1 {
		return fmt.Errorf("usage: gv done <ticket> [--force]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	t, err := findTask(positionals[0])
	if err != nil {
		return err
	}
	return finishTask(cfg, t, *force)
}

func finishTask(cfg *config.Config, t *state.Task, force bool) error {
	repo, ok := cfg.Repos[t.Repo]
	if !ok {
		return fmt.Errorf("repo %q no longer in config", t.Repo)
	}

	// Degraded no-remote path (DESIGN.md §5.2): with no remote there is no
	// PR to verify, so --force IS the human confirmation.
	hasRemote := git.HasRemote(repo.Path, "origin")
	if !hasRemote {
		if !force {
			return fmt.Errorf("%s: repo %s has no remote — grove cannot verify the work merged; confirm cleanup with --force (the local branch is deleted)", t.Ticket, t.Repo)
		}
		fmt.Println("→ no remote: skipping merge check (--force is the confirmation)")
	} else {
		merged, pr, err := github.Merged(repo.Path, t.Branch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: merge check failed: %v\n", err)
		}
		if !merged && !force {
			prState := "no PR found"
			if pr != nil {
				prState = fmt.Sprintf("PR #%d is %s", pr.Number, pr.State)
			}
			return fmt.Errorf("%s: %s — not cleaning up (use --force to override)", t.Ticket, prState)
		}
	}

	if tmux.WindowExists(t.TmuxSession, t.TmuxWindow) {
		_ = tmux.KillWindow(t.TmuxSession, t.TmuxWindow)
		fmt.Println("→ killed tmux window")
	}
	if err := worktree.RemoveSafe(repo.Path, t.Worktree); err != nil {
		if !force {
			return fmt.Errorf("worktree remove: %w (dirty? retry with --force)", err)
		}
		if err := git.RemoveWorktreeForce(repo.Path, t.Worktree); err != nil {
			return fmt.Errorf("worktree remove --force: %w", err)
		}
	}
	fmt.Println("→ removed worktree")
	// -D, not -d: squash-merged branches are never ancestry-merged.
	if err := git.ForceDeleteBranch(repo.Path, t.Branch); err != nil {
		fmt.Fprintf(os.Stderr, "warning: local branch delete: %v\n", err)
	}
	if hasRemote {
		if err := git.DeleteRemoteBranch(repo.Path, t.Branch); err != nil {
			fmt.Fprintf(os.Stderr, "warning: remote branch delete (may already be gone): %v\n", err)
		}
		fmt.Println("→ deleted branch (local + remote)")
	} else {
		fmt.Println("→ deleted local branch")
	}

	if err := state.Append(config.StateDir(), state.Event{Type: state.EvTaskDone, Ticket: t.Ticket}); err != nil {
		return err
	}
	fmt.Printf("✓ %s cleaned up (the task's terminal status in your tracker is yours to move)\n", t.Ticket)
	return nil
}

// cmdUntrack drops a task from tracking. Without --rm nothing but state
// changes — worktree, branches, and window all survive (for "I'm taking
// this over by hand"). --rm is the routine abandon path: window, worktree,
// and local branch go; the remote branch survives unless --rm-remote,
// because an abandoned branch may hold the only copy of unmerged work.
func cmdUntrack(args []string) error {
	fs := flag.NewFlagSet("untrack", flag.ExitOnError)
	rm := fs.Bool("rm", false, "also remove window, worktree, and local branch")
	rmRemote := fs.Bool("rm-remote", false, "with --rm: delete the remote branch too")
	force := fs.Bool("force", false, "with --rm: remove even dirty/unpushed worktrees")
	positionals := parseAnywhere(fs, args)
	if len(positionals) != 1 {
		return fmt.Errorf("usage: gv untrack <ticket> [--rm] [--rm-remote] [--force]")
	}
	t, err := findTask(positionals[0])
	if err != nil {
		return err
	}

	if *rm {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := removeTaskArtifacts(cfg, t, *rmRemote, *force); err != nil {
			return err
		}
	}

	if err := state.Append(config.StateDir(), state.Event{Type: state.EvTaskUntracked, Ticket: t.Ticket}); err != nil {
		return err
	}
	if *rm {
		fmt.Printf("✓ %s untracked and cleaned up\n", t.Ticket)
	} else {
		fmt.Printf("✓ %s untracked — worktree, branches, and window untouched\n", t.Ticket)
	}
	return nil
}

// removeTaskArtifacts is the shared --rm teardown (untrack --rm and
// sweep's abandoned path): kill window, remove worktree, delete the
// local branch. The remote branch survives unless rmRemote — an
// abandoned branch may hold the only copy of unmerged work. Guarded by
// SafeToRemove unless force; a missing worktree falls back to checking
// the surviving branch against origin/<base>.
func removeTaskArtifacts(cfg *config.Config, t *state.Task, rmRemote, force bool) error {
	repo, ok := cfg.Repos[t.Repo]
	if !ok {
		return fmt.Errorf("repo %q no longer in config", t.Repo)
	}
	baseRef := "origin/" + repo.Base

	if !force {
		if _, statErr := os.Stat(t.Worktree); statErr == nil {
			ok, reason, err := git.SafeToRemove(t.Worktree, baseRef)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%s: %s — not removing (use --force to override)", t.Ticket, reason)
			}
		} else if git.LocalBranchExists(repo.Path, t.Branch) {
			// Worktree dir is gone but the branch survives — make sure
			// deleting it can't lose the only copy of unmerged commits.
			n, err := git.CommitsNotOn(repo.Path, baseRef, t.Branch)
			if err != nil || n > 0 {
				return fmt.Errorf("%s: branch %s has %d commit(s) not on %s — not deleting (use --force to override)",
					t.Ticket, t.Branch, n, baseRef)
			}
		}
	}

	if tmux.WindowExists(t.TmuxSession, t.TmuxWindow) {
		_ = tmux.KillWindow(t.TmuxSession, t.TmuxWindow)
		fmt.Println("→ killed tmux window")
	}
	if err := worktree.RemoveSafe(repo.Path, t.Worktree); err != nil {
		if !force {
			return fmt.Errorf("worktree remove: %w (retry with --force)", err)
		}
		if err := git.RemoveWorktreeForce(repo.Path, t.Worktree); err != nil {
			return fmt.Errorf("worktree remove --force: %w", err)
		}
	}
	fmt.Println("→ removed worktree")
	if git.LocalBranchExists(repo.Path, t.Branch) {
		if err := git.ForceDeleteBranch(repo.Path, t.Branch); err != nil {
			fmt.Fprintf(os.Stderr, "warning: local branch delete: %v\n", err)
		} else {
			fmt.Println("→ deleted local branch")
		}
	}
	if rmRemote {
		if err := git.DeleteRemoteBranch(repo.Path, t.Branch); err != nil {
			fmt.Fprintf(os.Stderr, "warning: remote branch delete (may already be gone): %v\n", err)
		} else {
			fmt.Println("→ deleted remote branch")
		}
	} else {
		fmt.Println("→ remote branch kept (delete with --rm-remote)")
	}
	return nil
}

// sweepItem is one proposed action (the --json / --dry-run contract).
type sweepItem struct {
	Ticket string      `json:"ticket"`
	Class  audit.Class `json:"class"`
	Action string      `json:"action"`
	Detail string      `json:"detail,omitempty"`
}

// cmdSweep consumes the audit classification: merged tasks get the full
// done cleanup, abandoned tasks (closed PR / stale with no PR) get
// untrack --rm — each per-item confirmed, never forced. Stale prompt
// files of done tasks are pruned automatically at the end.
func cmdSweep(args []string) error {
	fs := flag.NewFlagSet("sweep", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "print what would be offered, mutate nothing")
	asJSON := fs.Bool("json", false, "machine-readable dry-run output (implies --dry-run)")
	parseAnywhere(fs, args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	tasks, err := state.Load(config.StateDir())
	if err != nil {
		return err
	}
	rep := audit.Gather(cfg, tasks, config.StateDir())

	var items []sweepItem
	for _, r := range rep.Tasks {
		switch r.Class {
		case audit.Merged:
			detail := ""
			if r.PR != nil {
				detail = fmt.Sprintf("PR #%d merged", r.PR.Number)
			}
			items = append(items, sweepItem{Ticket: r.Ticket, Class: r.Class, Action: "done (full cleanup incl. remote branch)", Detail: detail})
		case audit.Abandoned:
			detail := "remote branch kept"
			if _, statErr := os.Stat(r.Worktree); statErr == nil {
				if repo, ok := cfg.Repos[r.Repo]; ok {
					if ok, reason, err := git.SafeToRemove(r.Worktree, "origin/"+repo.Base); err == nil && !ok {
						detail = "guard would refuse: " + reason
					}
				}
			}
			items = append(items, sweepItem{Ticket: r.Ticket, Class: r.Class, Action: "untrack --rm (worktree + local branch)", Detail: detail})
		}
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Items        []sweepItem `json:"items"`
			StalePrompts []string    `json:"stale_prompts"`
		}{items, rep.StalePrompts})
	}

	if len(items) == 0 && len(rep.StalePrompts) == 0 {
		fmt.Println("nothing to sweep")
		return nil
	}

	if *dryRun {
		for _, it := range items {
			fmt.Printf("%s [%s] → %s", it.Ticket, it.Class, it.Action)
			if it.Detail != "" {
				fmt.Printf("  (%s)", it.Detail)
			}
			fmt.Println()
		}
		fmt.Printf("%d stale prompt file(s) would be pruned\n", len(rep.StalePrompts))
		return nil
	}

	sc := bufio.NewScanner(os.Stdin)
	swept := 0
	for _, it := range items {
		t := tasks[it.Ticket]
		if t == nil {
			continue
		}
		prompt := fmt.Sprintf("%s [%s]: %s", it.Ticket, it.Class, it.Action)
		if it.Detail != "" {
			prompt += " (" + it.Detail + ")"
		}
		fmt.Printf("%s — proceed? [y/N] ", prompt)
		if !sc.Scan() || strings.ToLower(strings.TrimSpace(sc.Text())) != "y" {
			continue
		}
		var actErr error
		switch it.Class {
		case audit.Merged:
			actErr = finishTask(cfg, t, false)
		case audit.Abandoned:
			if actErr = removeTaskArtifacts(cfg, t, false, false); actErr == nil {
				actErr = state.Append(config.StateDir(), state.Event{Type: state.EvTaskUntracked, Ticket: t.Ticket})
			}
		}
		if actErr != nil {
			fmt.Fprintf(os.Stderr, "  %v\n", actErr)
			continue
		}
		swept++
	}

	pruned := 0
	for _, name := range rep.StalePrompts {
		if err := os.Remove(filepath.Join(config.StateDir(), "prompts", name)); err == nil {
			pruned++
		}
	}
	fmt.Printf("swept %d task(s), pruned %d stale prompt file(s)\n", swept, pruned)
	return nil
}

// --- doctor / hooks ---

func cmdDoctor() error {
	cfg, cfgErr := config.Load()
	fmt.Println("GROVE DOCTOR")
	if doctor.Print(doctor.Run(cfg, cfgErr)) {
		fmt.Println("all clear 🌳")
		return nil
	}
	os.Exit(1)
	return nil
}

func cmdHooks(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gv hooks install|status")
	}
	switch args[0] {
	case "install":
		if err := hooks.Install(); err != nil {
			return err
		}
		fmt.Println("✓ hooks wired into ~/.cc-work/settings.json (SessionStart, Notification, Stop, SessionEnd)")
		return nil
	case "status":
		installed, err := hooks.Installed()
		if err != nil {
			return err
		}
		for _, ev := range []string{"SessionStart", "Notification", "Stop", "SessionEnd"} {
			mark := "✗"
			if installed[ev] {
				mark = "✓"
			}
			fmt.Printf(" %s %s\n", mark, ev)
		}
		return nil
	}
	return fmt.Errorf("usage: gv hooks install|status")
}

// --- helpers ---

func findTask(idOrURL string) (*state.Task, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	id, err := parseAnyID(cfg, idOrURL)
	if err != nil {
		return nil, err
	}
	tasks, err := state.Load(config.StateDir())
	if err != nil {
		return nil, err
	}
	t, ok := tasks[id]
	if !ok || t.Done {
		return nil, fmt.Errorf("no active task %s — see `gv ls`", id)
	}
	return t, nil
}

// parseAnyID normalizes a task id for the configured provider kind without
// needing a fully-constructed provider (markdown ids are repo-independent,
// linear ids are DEV-1234-shaped).
func parseAnyID(cfg *config.Config, raw string) (string, error) {
	if cfg.Provider.Kind == "linear" {
		return linear.ParseIdentifier(strings.ToUpper(raw))
	}
	return provider.NewMarkdown("").ParseID(raw)
}
