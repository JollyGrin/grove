// Focus-mode minimizing (grove-487): in the `focus` layout the cockpit
// window holds the dash and ONE chat; every other local chat is hidden with
// chat-hide's primitives (break-pane into its own grove-chat session), and
// its box in the rail (grove-486) is what shows of it. What focus minimized
// is remembered in the session option @grove_focus_hid, so leaving focus
// restores exactly those and never a chat the operator hid by hand.
package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

// FocusHidOption is the SESSION option holding the space-separated chat
// session names focus mode minimized.
const FocusHidOption = "@grove_focus_hid"

// FocusPane is one cockpit-window pane as the focus planner sees it.
type FocusPane struct {
	ID     string
	Index  int
	Active bool
	Last   bool // the window's previously active pane (#{pane_last})
	Rail   bool
	Remote bool
}

// The format ends in a literal "." field: run() trims its output, which would
// otherwise eat the trailing empty @grove_rail of the last line.
func parseFocusPanes(out string) []FocusPane {
	var panes []FocusPane
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) < 7 || f[0] == "" {
			continue
		}
		idx, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		panes = append(panes, FocusPane{
			ID: f[0], Index: idx, Active: f[2] == "1", Last: f[3] == "1",
			Rail: f[5] != "", Remote: f[4] != "",
		})
	}
	return panes
}

// LocalChatPanes are the window's local chat panes: not the dash (the lowest
// live index — never `== 0`), not the rail, not an ssh attachment (remote
// panes stay visible in this car).
func LocalChatPanes(panes []FocusPane) []FocusPane {
	first, ok := -1, false
	for _, p := range panes {
		if !ok || p.Index < first {
			first, ok = p.Index, true
		}
	}
	var chats []FocusPane
	for _, p := range panes {
		if p.Rail || p.Remote || p.Index == first {
			continue
		}
		chats = append(chats, p)
	}
	return chats
}

// PickShown is the chat that stays on screen when focus is entered: the
// active pane if it is a chat; otherwise the pane the operator was in before
// (#{pane_last} — the dash is active whenever `L` is typed there); otherwise
// the last chat. "" when there is no chat.
func PickShown(chats []FocusPane) string {
	for _, p := range chats {
		if p.Active {
			return p.ID
		}
	}
	for _, p := range chats {
		if p.Last {
			return p.ID
		}
	}
	if len(chats) == 0 {
		return ""
	}
	return chats[len(chats)-1].ID
}

// PlanEnter splits a window's chats into the one to keep and those to
// minimize.
func PlanEnter(panes []FocusPane) (shown string, minimize []string) {
	chats := LocalChatPanes(panes)
	shown = PickShown(chats)
	for _, p := range chats {
		if p.ID != shown {
			minimize = append(minimize, p.ID)
		}
	}
	return shown, minimize
}

// ParseFocusHid reads the @grove_focus_hid value.
func ParseFocusHid(s string) []string { return strings.Fields(s) }

// AddFocusHid appends a session once.
func AddFocusHid(list []string, session string) []string {
	for _, s := range list {
		if s == session {
			return list
		}
	}
	return append(list, session)
}

// ForgetFocusHid drops one session from @grove_focus_hid (a minimized chat
// that was just shown again by hand is no longer minimized by focus).
func ForgetFocusHid(session, name string) error {
	hid := FocusHid(session)
	var keep []string
	for _, s := range hid {
		if s != name {
			keep = append(keep, s)
		}
	}
	if len(keep) == len(hid) {
		return nil
	}
	return setFocusHid(session, keep)
}

// FocusMoved is called after a focus move succeeded, so the caller's event
// lands only for a move that happened (#412). kind is "hidden" or "shown".
type FocusMoved func(kind, session, pane string)

func focusWindowPanes(session string) ([]FocusPane, error) {
	out, err := run("list-panes", "-t", cockpitWindowTarget(session), "-F",
		"#{pane_id}\t#{pane_index}\t#{pane_active}\t#{pane_last}\t#{@grove_remote}\t#{@grove_rail}\t.")
	if err != nil {
		return nil, err
	}
	return parseFocusPanes(out), nil
}

// FocusHid reads which chat sessions focus mode minimized.
func FocusHid(session string) []string {
	out, err := run("show-options", "-t", ExactActive(session), "-v", FocusHidOption)
	if err != nil {
		return nil
	}
	return ParseFocusHid(out)
}

func setFocusHid(session string, list []string) error {
	if len(list) == 0 {
		_, err := run("set-option", "-t", ExactActive(session), "-u", FocusHidOption)
		return err
	}
	_, err := run("set-option", "-t", ExactActive(session), FocusHidOption, strings.Join(list, " "))
	return err
}

// minimizePanes hides each pane, recording each session in @grove_focus_hid
// right after its move.
func minimizePanes(session, label string, panes []string, isCockpit CockpitCheck, moved FocusMoved) error {
	hid := FocusHid(session)
	var firstErr error
	for _, pane := range panes {
		name, err := HideChatPane(pane, label, isCockpit, nil)
		if name != "" {
			hid = AddFocusHid(hid, name)
			if serr := setFocusHid(session, hid); serr != nil && err == nil {
				err = serr
			}
			if moved != nil {
				moved("hidden", name, pane)
			}
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("minimize %s: %w", pane, err)
		}
	}
	return firstErr
}

// MinimizeChats keeps one chat on screen and minimizes the other local
// chats of the cockpit window. Used on entering focus.
func MinimizeChats(session, label string, isCockpit CockpitCheck, moved FocusMoved) error {
	panes, err := focusWindowPanes(session)
	if err != nil {
		return err
	}
	_, minimize := PlanEnter(panes)
	return minimizePanes(session, label, minimize, isCockpit, moved)
}

// MinimizeShown minimizes every local chat in the window — the spawn path in
// focus, called just before the new chat opens so it is the one shown.
func MinimizeShown(session, label string, isCockpit CockpitCheck, moved FocusMoved) error {
	panes, err := focusWindowPanes(session)
	if err != nil {
		return err
	}
	var ids []string
	for _, p := range LocalChatPanes(panes) {
		ids = append(ids, p.ID)
	}
	return minimizePanes(session, label, ids, isCockpit, moved)
}

// RestoreMinimized brings back exactly the chats focus minimized (not ones
// hidden by hand), then clears the option. A session that is gone or already
// shown is skipped; the re-tile is the caller's (ApplyLayout).
func RestoreMinimized(session, label string, isCockpit CockpitCheck, moved FocusMoved) error {
	hid := FocusHid(session)
	if len(hid) == 0 {
		return nil
	}
	var firstErr error
	for _, name := range hid {
		pane, err := ShowChatPaneUnfocused(name, session, isCockpit, nil)
		if pane != "" && moved != nil {
			moved("shown", name, pane)
		}
		if err != nil && pane == "" {
			if _, serr := ChatShowable(name, session, isCockpit); serr != nil && !SessionExists(name) {
				continue // closed meanwhile
			}
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("restore %s: %w", name, err)
		}
	}
	if err := setFocusHid(session, nil); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// PaneOption reads one pane user option ("" when unset or no such pane).
func PaneOption(pane, name string) string {
	out, err := run("display-message", "-p", "-t", pane, "#{"+name+"}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
