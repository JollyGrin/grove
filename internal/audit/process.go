package audit

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// OrphanProcess is a suspect claude/mcp descendant that has reparented to
// launchd (ppid==1) and is not reachable from any live tracked tmux pane.
// Report-only: gv never kills anything here; killing is offered later by
// an interactive sweep.
type OrphanProcess struct {
	PID     int     `json:"pid"`
	CPUPct  float64 `json:"cpu_pct"`
	RSSKB   int64   `json:"rss_kb"`
	Elapsed string  `json:"elapsed"`
	Args    string  `json:"args"`
}

// WorktreeProcess is a process whose argv references a grove-created
// worktree path — a build/test child that daemonized out of the worker's
// pane (grove-156: jest-worker spinning for days after the task shipped).
// Ownership is by construction: grove created the path and it is unique
// per task, so anything referencing it belongs to that task. Report-only
// here; killing is offered by sweep or done at reap.
type WorktreeProcess struct {
	PID      int     `json:"pid"`
	CPUPct   float64 `json:"cpu_pct"`
	RSSKB    int64   `json:"rss_kb"`
	Elapsed  string  `json:"elapsed"`
	Args     string  `json:"args"`
	Ticket   string  `json:"ticket"`
	Worktree string  `json:"worktree"`
}

// process is one parsed row of `ps -Ao pid,ppid,pcpu,rss,etime,args`.
type process struct {
	pid, ppid int
	cpuPct    float64
	rssKB     int64
	elapsed   string
	args      string
}

var (
	claudeOrMCPRe = regexp.MustCompile(`(?i)claude\b|\bmcp\b`)
	mcpPathRe     = regexp.MustCompile(`(?i)\.claude/|mcp[-_.]?config|mcp\.json`)
	// appBundleRe matches a macOS app-bundle executable path (e.g. the
	// Claude desktop app's /Applications/Claude.app/Contents/MacOS/Claude).
	// These are launchd-parented by design — ppid==1 is normal, not a
	// grove worker gone orphan — so they're excluded outright regardless
	// of any claude/mcp text elsewhere in argv.
	appBundleRe = regexp.MustCompile(`(?i)\.app/Contents/`)
	psLineRe    = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+([\d.]+)\s+(\d+)\s+(\S+)\s+(.*)$`)
)

// DetectOrphanProcesses parses raw `ps` and `tmux list-panes` output and
// classifies suspect claude/mcp processes: ppid==1 (reparented to launchd)
// whose args match `claude\b` or `\bmcp\b` (case-insensitive, word-bounded
// so "mcprotocol"/"tmcpipe" don't false-positive), or a `node` process
// whose args reference ".claude/" or a known MCP config path — AND whose
// pid is not in the ancestry (self or descendant) of any live tracked
// pane. App-bundle executables (".app/Contents/...", e.g. the Claude
// desktop app) are excluded outright: they're launchd-parented by design,
// not a grove worker gone orphan.
//
// psOutput: lines of `ps -Ao pid,ppid,pcpu,rss,etime,args` (a non-matching
// header line, if present, is skipped).
// panePIDsOutput: lines of `tmux list-panes -a -F '#{pane_pid}'`.
func DetectOrphanProcesses(psOutput, panePIDsOutput string) []OrphanProcess {
	procs := parsePS(psOutput)
	live := liveAncestry(procs, parsePanePIDs(panePIDsOutput))

	var orphans []OrphanProcess
	for _, p := range procs {
		if p.ppid != 1 || live[p.pid] || !isSuspectArgs(p.args) {
			continue
		}
		orphans = append(orphans, OrphanProcess{PID: p.pid, CPUPct: p.cpuPct, RSSKB: p.rssKB, Elapsed: p.elapsed, Args: p.args})
	}
	return orphans
}

// DetectWorktreeProcesses parses raw `ps` output and returns every process
// whose argv references one of the given worktree paths. The map is
// worktree path → ticket, and the caller decides which paths are fair
// game — the hard scoping rule (grove-156) is that only paths grove
// itself created (the `worktree` field of tasks.json rows) ever go in
// here; never a generic `.worktrees/` pattern. A path match requires a
// boundary after it (end, '/', or a non-filename byte) so the worktree
// of grove-15 never claims a process working in grove-156. Results are
// sorted by pid.
//
// psOutput: lines of `ps -Ao pid,ppid,pcpu,rss,etime,args` (a
// non-matching header line, if present, is skipped).
func DetectWorktreeProcesses(psOutput string, worktrees map[string]string) []WorktreeProcess {
	paths := make([]string, 0, len(worktrees))
	for p := range worktrees {
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)

	var out []WorktreeProcess
	for _, p := range parsePS(psOutput) {
		for _, path := range paths {
			if !argsReferencePath(p.args, path) {
				continue
			}
			out = append(out, WorktreeProcess{
				PID: p.pid, CPUPct: p.cpuPct, RSSKB: p.rssKB, Elapsed: p.elapsed,
				Args: p.args, Ticket: worktrees[path], Worktree: path,
			})
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// argsReferencePath reports whether args contains path as a whole path —
// followed by end-of-string, a '/', or any byte that cannot extend a
// filename, so a path that is a string prefix of a sibling directory's
// path does not match it.
func argsReferencePath(args, path string) bool {
	for i := 0; ; {
		j := strings.Index(args[i:], path)
		if j < 0 {
			return false
		}
		end := i + j + len(path)
		if end == len(args) || !isFilenameByte(args[end]) {
			return true
		}
		i += j + 1
	}
}

// isFilenameByte reports whether c can extend a path component: if the
// byte right after a matched worktree path is one of these, the argv
// actually references a longer sibling path that merely shares the
// prefix (e.g. …/grove-15 vs …/grove-156-fix).
func isFilenameByte(c byte) bool {
	return c == '-' || c == '_' || c == '.' || c == '~' || c == '+' || c == '@' ||
		('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isSuspectArgs(args string) bool {
	if appBundleRe.MatchString(args) {
		return false
	}
	if claudeOrMCPRe.MatchString(args) {
		return true
	}
	return isNodeArgs(args) && mcpPathRe.MatchString(args)
}

// isNodeArgs reports whether the process's argv[0] basename is "node".
func isNodeArgs(args string) bool {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return false
	}
	name := fields[0]
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return strings.EqualFold(name, "node")
}

func parsePS(out string) []process {
	var procs []process
	for line := range strings.SplitSeq(out, "\n") {
		m := psLineRe.FindStringSubmatch(line)
		if m == nil {
			continue // header line or blank
		}
		pid, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		ppid, _ := strconv.Atoi(m[2])
		cpu, _ := strconv.ParseFloat(m[3], 64)
		rss, _ := strconv.ParseInt(m[4], 10, 64)
		procs = append(procs, process{pid: pid, ppid: ppid, cpuPct: cpu, rssKB: rss, elapsed: m[5], args: m[6]})
	}
	return procs
}

func parsePanePIDs(out string) []int {
	var pids []int
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if pid, err := strconv.Atoi(line); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// liveAncestry returns the set of pids reachable from any live pane pid by
// walking the ppid->children tree downward (the pane pid itself included).
func liveAncestry(procs []process, panePIDs []int) map[int]bool {
	children := map[int][]int{}
	for _, p := range procs {
		children[p.ppid] = append(children[p.ppid], p.pid)
	}
	live := map[int]bool{}
	var walk func(pid int)
	walk = func(pid int) {
		if live[pid] {
			return
		}
		live[pid] = true
		for _, c := range children[pid] {
			walk(c)
		}
	}
	for _, pid := range panePIDs {
		walk(pid)
	}
	return live
}
