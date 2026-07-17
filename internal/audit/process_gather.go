package audit

import (
	"os/exec"
)

// scanOrphanProcesses shells out to `ps` and `tmux list-panes` and hands
// their raw output to the pure DetectOrphanProcesses. Pure read: gv never
// kills anything here. A tmux/ps failure (e.g. no tmux server running)
// degrades to an empty pane list rather than an error — everything ppid==1
// and claude/mcp-shaped is then reported as a suspect, which is the safe
// direction for an audit to err in.
func scanOrphanProcesses() []OrphanProcess {
	psOut, err := exec.Command("ps", "-Ao", "pid,ppid,pcpu,etime,args").Output()
	if err != nil {
		return nil
	}
	paneOut, _ := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid}").Output()
	return DetectOrphanProcesses(string(psOut), string(paneOut))
}
