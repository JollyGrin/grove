package audit

import (
	"os"
	"os/exec"

	"github.com/JollyGrin/grove/internal/state"
)

// PSFormat is the column list every gv `ps` scan uses — parsePS and the
// reap-time kill in cmd/gv must see the same shape.
const PSFormat = "pid,ppid,pcpu,rss,etime,args"

// scanProcesses shells out to `ps` (once) and `tmux list-panes` and hands
// their raw output to the pure detectors. Pure read: gv never kills
// anything here. A tmux/ps failure (e.g. no tmux server running) degrades
// to an empty pane list rather than an error — everything ppid==1 and
// claude/mcp-shaped is then reported as a suspect, which is the safe
// direction for an audit to err in.
//
// Worktree-process scoping (grove-156 hard rule): only worktree paths
// grove itself created — the `worktree` field of tasks.json rows — are
// ever matched, and only when the task is done or its directory is gone
// (an active task's worktree legitimately hosts build children).
func scanProcesses(tasks map[string]*state.Task) ([]OrphanProcess, []WorktreeProcess) {
	psOut, err := exec.Command("ps", "-Ao", PSFormat).Output()
	if err != nil {
		return nil, nil
	}
	paneOut, _ := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid}").Output()
	orphans := DetectOrphanProcesses(string(psOut), string(paneOut))

	reapable := map[string]string{}
	for _, t := range tasks {
		if t.Worktree == "" {
			continue
		}
		if t.Done {
			reapable[t.Worktree] = t.Ticket
			continue
		}
		if _, statErr := os.Stat(t.Worktree); statErr != nil {
			reapable[t.Worktree] = t.Ticket
		}
	}
	return orphans, DetectWorktreeProcesses(string(psOut), reapable)
}
