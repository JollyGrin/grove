package claudestate

import (
	"os"
	"path/filepath"

	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
)

// SessionDirs lists the Claude Code sessions/ dirs a worker may be running
// under: the default ~/.claude plus every extra config dir given (the
// workspace's claude_config_dir, a model profile's CLAUDE_CONFIG_DIR).
// Empty and duplicate entries are dropped.
func SessionDirs(extraConfigDirs ...string) []string {
	var dirs []string
	add := func(cfgDir string) {
		if cfgDir == "" {
			return
		}
		d := filepath.Join(cfgDir, "sessions")
		for _, have := range dirs {
			if have == d {
				return
			}
		}
		dirs = append(dirs, d)
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".claude"))
	}
	for _, d := range extraConfigDirs {
		add(d)
	}
	return dirs
}

// Observe builds one beat's per-ticket pane reading and Resolve signals —
// the one shape the cockpit, `gv ls` and `gv supervise` share. snapFor
// answers every tmux question (one snapshot per session, memoized by the
// caller); read is called ONCE with every worker pane id the snapshots show,
// and detectFrom (the capture-pane) runs only for a task whose pane has no
// matching session file — the fallback.
func Observe(active []*state.Task, snapFor func(string) *tmux.SessionSnapshot, detectFrom func(*tmux.SessionSnapshot, string) detect.LiveInfo, read func(map[string]bool) map[string]state.ClaudeStatus) (map[string]detect.LiveInfo, map[string]state.Signals) {
	infos := make(map[string]detect.LiveInfo, len(active))
	signals := make(map[string]state.Signals, len(active))

	want := map[string]bool{}
	for _, t := range active {
		if pane, _, ok := snapFor(t.TmuxSession).ClaudePane(t.TmuxWindow); ok {
			want[pane] = true
		}
	}
	var files map[string]state.ClaudeStatus
	if len(want) > 0 && read != nil {
		files = read(want)
	}

	for _, t := range active {
		snap := snapFor(t.TmuxSession)
		sig := state.Signals{
			WindowExists: snap.WindowExists(t.TmuxWindow),
			PaneCommand:  snap.ClaudePaneCommand(t.TmuxWindow),
		}
		var info detect.LiveInfo
		pane, _, _ := snap.ClaudePane(t.TmuxWindow)
		if c, ok := files[pane]; ok && sig.WindowExists && t.SessionID != "" && c.SessionID == t.SessionID {
			c := c
			sig.Claude = &c
			info = detect.LiveInfo{Exists: true, HasClaude: true, Status: AgentStatus(c.Status)}
		} else {
			info = detectFrom(snap, t.TmuxWindow)
			if reason, _, ok := detect.ErrorMarker(info.PaneContent); ok {
				sig.PaneError = reason
			}
		}
		infos[t.Ticket] = info
		signals[t.Ticket] = sig
	}
	return infos, signals
}

// AgentStatus maps Claude's session-file status onto the pane-scan
// vocabulary. shell (idle with a background shell alive) is not stuck on
// the operator, so it reads idle.
func AgentStatus(s string) detect.AgentStatus {
	switch s {
	case "busy":
		return detect.StatusBusy
	case "waiting":
		return detect.StatusWaiting
	case "idle", "shell":
		return detect.StatusIdle
	}
	return detect.StatusUnknown
}
