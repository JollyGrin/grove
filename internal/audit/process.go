package audit

import (
	"regexp"
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
	Elapsed string  `json:"elapsed"`
	Args    string  `json:"args"`
}

// process is one parsed row of `ps -Ao pid,ppid,pcpu,etime,args`.
type process struct {
	pid, ppid int
	cpuPct    float64
	elapsed   string
	args      string
}

var (
	claudeOrMCPRe = regexp.MustCompile(`(?i)claude\b|mcp`)
	mcpPathRe     = regexp.MustCompile(`(?i)\.claude/|mcp[-_.]?config|mcp\.json`)
	psLineRe      = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+([\d.]+)\s+(\S+)\s+(.*)$`)
)

// DetectOrphanProcesses parses raw `ps` and `tmux list-panes` output and
// classifies suspect claude/mcp processes: ppid==1 (reparented to launchd)
// whose args match `claude\b` or `mcp` (case-insensitive), or a `node`
// process whose args reference ".claude/" or a known MCP config path —
// AND whose pid is not in the ancestry (self or descendant) of any live
// tracked pane.
//
// psOutput: lines of `ps -Ao pid,ppid,pcpu,etime,args` (a non-matching
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
		orphans = append(orphans, OrphanProcess{PID: p.pid, CPUPct: p.cpuPct, Elapsed: p.elapsed, Args: p.args})
	}
	return orphans
}

func isSuspectArgs(args string) bool {
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
		procs = append(procs, process{pid: pid, ppid: ppid, cpuPct: cpu, elapsed: m[4], args: m[5]})
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
