// The focus-mode chat rail (grove-486): a short pane beneath the chat in the
// cockpit window's right column, drawn by `gv chat rail`. It is tmux
// plumbing only — the box rendering lives in internal/chatrail.
package tmux

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// RailHeight is the rail pane's height in rows: one border/title row, two
// content rows, one spare. A constant, not config.
const RailHeight = 4

// RailRowsOption is the SESSION option the cockpit writes the rail's content
// to. Deliberately not "@grove_rail": that name is the PANE tag, and a
// format like #{@grove_rail} expands through pane → window → session, so a
// session-level value of the same name would make every pane read as the
// rail.
const RailRowsOption = "@grove_rail_rows"

// Run exposes the tmux exec for the rail programs (writer and viewer), which
// take it as an injectable runner so their command sets are testable.
func Run(args ...string) (string, error) { return run(args...) }

// cockpitWindowTarget names the cockpit window by its pinned name rather
// than "the active window": the rail writer runs in the background while
// the operator may be looking at a worker window.
func cockpitWindowTarget(session string) string { return Exact(session) + ":cockpit" }

type railPane struct {
	id    string
	index int
	rail  bool
	act   bool
}

func parseRailPanes(out string) []railPane {
	var panes []railPane
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) < 3 || f[0] == "" {
			continue
		}
		idx, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		panes = append(panes, railPane{id: f[0], index: idx, act: f[2] == "1", rail: len(f) > 3 && f[3] != ""})
	}
	sort.Slice(panes, func(i, j int) bool { return panes[i].index < panes[j].index })
	return panes
}

func cockpitPanes(session string) ([]railPane, error) {
	out, err := run("list-panes", "-t", cockpitWindowTarget(session), "-F",
		"#{pane_id}\t#{pane_index}\t#{pane_active}\t#{@grove_rail}")
	if err != nil {
		return nil, err
	}
	return parseRailPanes(out), nil
}

// RailPane finds the cockpit window's rail pane (stamped @grove_rail), and
// the id of that window, which the writer needs to tell on-screen chats from
// off-screen ones.
func RailPane(session string) (pane, window string, ok bool) {
	out, err := run("list-panes", "-t", cockpitWindowTarget(session), "-F",
		"#{pane_id}\t#{window_id}\t#{@grove_rail}")
	if err != nil {
		return "", "", false
	}
	window = ""
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) < 2 {
			continue
		}
		window = f[1]
		if len(f) > 2 && f[2] != "" {
			pane, ok = f[0], true
		}
	}
	return pane, window, ok
}

// IsRailPane reports whether a pane id carries the rail stamp. Fails open
// (false) on any error — it only ever adds a refusal.
func IsRailPane(pane string) bool {
	if !strings.HasPrefix(pane, "%") {
		return false
	}
	out, err := run("display-message", "-p", "-t", pane, "#{@grove_rail}")
	return err == nil && strings.TrimSpace(out) != ""
}

// ErrRail is the refusal every chat verb answers a rail target with.
var ErrRail = fmt.Errorf("that is the rail, not a chat")

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// RailCommand is the pane command: this very binary (absolute path, not
// PATH — tmux-discipline §3), so a stale gv first on PATH never draws it.
func RailCommand(session string) string {
	exe := "gv"
	if p, err := os.Executable(); err == nil {
		exe = p
	}
	return shellQuote(exe) + " chat rail --session " + shellQuote(session)
}

// EnsureRail guarantees exactly one rail pane in the cockpit window: a
// stamped one is reused, otherwise one is split under the last chat (or
// beside the dash when there is no chat) and stamped. Callers follow with
// RetileFocus. dir is the pane's cwd — never the orchestrator dir.
func EnsureRail(session, dir, cmd string) (string, error) {
	panes, err := cockpitPanes(session)
	if err != nil {
		return "", err
	}
	for _, p := range panes {
		if p.rail {
			return p.id, keepRailHeight(session, p.id)
		}
	}
	if len(panes) == 0 {
		return "", fmt.Errorf("no panes in %s's cockpit window", session)
	}
	args := []string{"split-window", "-d", "-P", "-F", "#{pane_id}", "-c", dir}
	if len(panes) > 1 {
		args = append(args, "-v", "-l", strconv.Itoa(RailHeight), "-t", panes[len(panes)-1].id)
	} else {
		args = append(args, "-h", "-t", panes[0].id)
	}
	id, err := run(append(args, cmd)...)
	if err != nil {
		return "", err
	}
	id = strings.TrimSpace(id)
	if _, err := run("set-option", "-p", "-t", id, "@grove_rail", "1"); err != nil {
		_, _ = run("kill-pane", "-t", id)
		return "", err
	}
	if err := ShowPaneBorders(cockpitWindowTarget(session)); err != nil {
		return id, err
	}
	return id, keepRailHeight(session, id)
}

// railHook is the window hook that holds the rail at RailHeight when the
// window is resized (a client attaching or the terminal being dragged):
// tmux rescales every pane proportionally on resize, so a rail sized at
// build time on a detached 80x24 session would balloon once a real client
// attaches. A tmux hook, not a watcher — it is one command string tmux
// runs, no process or goroutine of ours.
const railHook = "window-resized"

func keepRailHeight(session, pane string) error {
	_, err := run("set-hook", "-w", "-t", cockpitWindowTarget(session), railHook,
		fmt.Sprintf("resize-pane -t %s -y %d", pane, RailHeight))
	return err
}

// KillRail removes the rail pane and only it. Not finding one is success.
func KillRail(session string) error {
	panes, err := cockpitPanes(session)
	if err != nil {
		return err
	}
	for _, p := range panes {
		if p.rail {
			_, _ = run("set-hook", "-wu", "-t", cockpitWindowTarget(session), railHook)
			if _, err := run("kill-pane", "-t", p.id); err != nil {
				return err
			}
		}
	}
	return nil
}

// RetileFocus is THE focus re-tile (grove-486), used by every focus-mode
// path: select-layout main-vertical, then resize the rail back to
// RailHeight (tmux's layouts give stacked panes equal height, so the
// resize after every re-tile is the whole trick), then focus the chat — or
// the dash when there is none. The rail is kept the LAST pane so it is the
// bottom of the right column.
func RetileFocus(session string) error {
	win := cockpitWindowTarget(session)
	panes, err := cockpitPanes(session)
	if err != nil {
		return err
	}
	var rail *railPane
	var chats []railPane
	for i, p := range panes {
		switch {
		case p.rail:
			rail = &panes[i]
		case i > 0:
			chats = append(chats, p)
		}
	}
	if rail != nil && len(chats) > 0 && rail.index < chats[len(chats)-1].index {
		if _, err := run("swap-pane", "-d", "-s", rail.id, "-t", chats[len(chats)-1].id); err != nil {
			return err
		}
	}
	if _, err := run("set-option", "-t", win, "-w", "main-pane-width", fmt.Sprintf("%d%%", cockpitMainWidth)); err != nil {
		return err
	}
	if _, err := run("select-layout", "-t", win, "main-vertical"); err != nil {
		return err
	}
	if rail != nil && len(panes) > 2 {
		if _, err := run("resize-pane", "-t", rail.id, "-y", strconv.Itoa(RailHeight)); err != nil {
			return err
		}
	}
	focus := ""
	if len(panes) > 0 {
		focus = panes[0].id
	}
	if len(chats) > 0 {
		focus = chats[len(chats)-1].id
	}
	for _, p := range chats { // keep the operator's own choice
		if p.act {
			focus = p.id
		}
	}
	if focus == "" {
		return nil
	}
	_, err = run("select-pane", "-t", focus)
	return err
}

// ApplyLayout switches the cockpit to the named layout, owning the rail's
// lifecycle: focus ensures the rail and re-tiles; anything else removes it.
func ApplyLayout(session, name string) error {
	if name != "focus" {
		if err := KillRail(session); err != nil {
			return err
		}
		return SelectLayout(session, name)
	}
	dir, _ := os.Getwd()
	if _, err := EnsureRail(session, dir, RailCommand(session)); err != nil {
		return err
	}
	return RetileFocus(session)
}
