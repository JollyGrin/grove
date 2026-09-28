package tmux

import (
	"fmt"
	"net/url"
	"strings"
)

// --- grove-404: hide / show for REMOTE chat panes ---
//
// A remote chat (grove-199) lives on its host; the cockpit pane is only a
// local `ssh -t <host> tmux attach`. So hiding one closes the LOCAL attach
// pane and nothing else — no byte is sent to the host — and showing it opens
// a fresh attach pane. What must survive in between is only "which host
// session was that": one record per hidden chat, kept in a user option on the
// cockpit SESSION. No file, and no read of its own: a session option resolves
// in a pane's format context, so the record rides the one `list-panes -a` the
// CHATS box already runs (paneListFormat). It dies with the cockpit session
// on park, which is right — the chat itself is still on the host.

// hiddenRemoteOption is the cockpit session's record of its hidden remote
// chats (EncodeHiddenRemotes' format).
const hiddenRemoteOption = "@grove_hidden_remote"

// remoteSessionOption stamps a remote chat pane with the HOST session it is
// attached to — what a hide must know, and can learn nowhere else: the pane's
// text is the remote agent's, never evidence.
const remoteSessionOption = "@grove_remote_session"

// HiddenRemote is one hidden remote chat: enough to re-attach and to draw
// its CHATS row without dialing the host.
type HiddenRemote struct {
	Host    string // the configured host name (hosts: in config), not the ssh target
	Session string // the chat's tmux session ON the host
	Profile string // the pane's @grove_profile, "" for the host's own Claude
}

// Record separators. NOT tab and newline: the record is read back inside a
// tab-separated, one-pane-per-line list-panes answer, so either would split
// the line it travels in. Every field is percent-escaped (url.PathEscape,
// which escapes both separators, whitespace and tmux's `#`), so no value can
// forge a boundary.
const (
	hiddenRemoteFieldSep  = "|"
	hiddenRemoteRecordSep = ","
)

// EncodeHiddenRemotes renders the records as the option's value, "" for
// none. Records without a host or a session are dropped.
func EncodeHiddenRemotes(list []HiddenRemote) string {
	var b strings.Builder
	for _, r := range list {
		if r.Host == "" || r.Session == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(hiddenRemoteRecordSep)
		}
		b.WriteString(url.PathEscape(r.Host))
		b.WriteString(hiddenRemoteFieldSep)
		b.WriteString(url.PathEscape(r.Session))
		b.WriteString(hiddenRemoteFieldSep)
		b.WriteString(url.PathEscape(r.Profile))
	}
	return b.String()
}

// ParseHiddenRemotes is EncodeHiddenRemotes' inverse. A malformed record —
// wrong field count, a bad escape, no host, no session — is ignored, never
// repaired: the option is operator-writable, and a guessed record would
// attach to a guessed session. A repeated host+session keeps its first.
func ParseHiddenRemotes(value string) []HiddenRemote {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var list []HiddenRemote
	for _, rec := range strings.Split(value, hiddenRemoteRecordSep) {
		f := strings.Split(rec, hiddenRemoteFieldSep)
		if len(f) != 3 {
			continue
		}
		var r HiddenRemote
		var err [3]error
		r.Host, err[0] = url.PathUnescape(f[0])
		r.Session, err[1] = url.PathUnescape(f[1])
		r.Profile, err[2] = url.PathUnescape(f[2])
		if err[0] != nil || err[1] != nil || err[2] != nil || r.Host == "" || r.Session == "" {
			continue
		}
		if hiddenRemoteIndex(list, r.Host, r.Session) >= 0 {
			continue
		}
		list = append(list, r)
	}
	return list
}

func hiddenRemoteIndex(list []HiddenRemote, host, session string) int {
	for i, r := range list {
		if r.Host == host && r.Session == session {
			return i
		}
	}
	return -1
}

// AddHiddenRemote appends r unless that host+session is already recorded, in
// which case the record in place is updated (a re-hide may carry a profile
// the first one lacked) and keeps its position.
func AddHiddenRemote(list []HiddenRemote, r HiddenRemote) []HiddenRemote {
	if r.Host == "" || r.Session == "" {
		return list
	}
	if i := hiddenRemoteIndex(list, r.Host, r.Session); i >= 0 {
		list[i] = r
		return list
	}
	return append(list, r)
}

// RemoveHiddenRemote drops the record for host+session; ok=false when there
// was none.
func RemoveHiddenRemote(list []HiddenRemote, host, session string) ([]HiddenRemote, bool) {
	i := hiddenRemoteIndex(list, host, session)
	if i < 0 {
		return list, false
	}
	return append(list[:i:i], list[i+1:]...), true
}

// HiddenRemotes reads a cockpit session's hidden remote chats. A session
// that is not running, or has none, is an empty list. For the verbs — the
// cockpit's tick reads the same value off Panes() instead.
func HiddenRemotes(cockpitSession string) []HiddenRemote {
	out, err := run("show-options", "-qv", "-t", ExactActive(cockpitSession), hiddenRemoteOption)
	if err != nil {
		return nil
	}
	return ParseHiddenRemotes(out)
}

// writeHiddenRemotes stores the list; an empty one unsets the option, so a
// cockpit with nothing hidden answers list-panes exactly as it did before.
func writeHiddenRemotes(cockpitSession string, list []HiddenRemote) error {
	value := EncodeHiddenRemotes(list)
	if value == "" {
		_, err := run("set-option", "-t", ExactActive(cockpitSession), "-u", hiddenRemoteOption)
		if err != nil && len(HiddenRemotes(cockpitSession)) == 0 {
			return nil // unsetting an option that was never set
		}
		return err
	}
	_, err := run("set-option", "-t", ExactActive(cockpitSession), hiddenRemoteOption, value)
	return err
}

// ForgetHiddenRemote drops one record — a hidden remote chat that was ended
// on its host. No record is not an error.
func ForgetHiddenRemote(cockpitSession, host, session string) error {
	list, ok := RemoveHiddenRemote(HiddenRemotes(cockpitSession), host, session)
	if !ok {
		return nil
	}
	return writeHiddenRemotes(cockpitSession, list)
}

// SetPaneRemoteSession stamps a remote chat pane with the host session it is
// attached to. An empty session clears the stamp.
func SetPaneRemoteSession(pane, session string) error {
	if session == "" {
		_, err := run("set-option", "-p", "-t", pane, "-u", remoteSessionOption)
		return err
	}
	_, err := run("set-option", "-p", "-t", pane, remoteSessionOption, session)
	return err
}

// TagRemotePane is everything a local attach pane wears (grove-199, plus
// grove-404's session stamp): host, profile, border color, and the window's
// border line that makes them visible. The ONE tail shared by the `@` spawn
// and by show. Every step is cosmetic or recoverable, so nothing here fails
// the caller — it gets the warnings, and decides where they may be written
// (never the terminal, from inside the cockpit's tea loop).
func TagRemotePane(pane, windowTarget, host, profile, remoteSession string) []string {
	var warnings []string
	if err := SetPaneRemote(pane, host); err != nil {
		warnings = append(warnings, fmt.Sprintf("could not tag remote chat pane with host %q: %v", host, err))
	}
	if err := SetPaneRemoteSession(pane, remoteSession); err != nil {
		warnings = append(warnings, fmt.Sprintf("could not stamp remote chat pane with its session %q (it cannot be hidden): %v", remoteSession, err))
	}
	if profile != "" {
		if err := SetPaneProfile(pane, profile); err != nil {
			warnings = append(warnings, fmt.Sprintf("could not tag remote chat pane with profile %q: %v", profile, err))
		}
	}
	if err := SetPaneRemoteBorder(pane); err != nil {
		warnings = append(warnings, fmt.Sprintf("could not color the remote chat pane's border: %v", err))
	}
	if err := ShowPaneBorders(windowTarget); err != nil {
		warnings = append(warnings, fmt.Sprintf("could not show cockpit pane borders: %v", err))
	}
	return warnings
}

// remotePane is the pure guard on top of hidablePane for the verbs that act
// on a remote pane AS a remote pane (hide, close): it must be one, and it
// must wear the stamp.
func remotePane(p PaneFacts, isCockpit CockpitCheck) error {
	if err := hidablePane(p, isCockpit); err != nil {
		return err
	}
	if p.Remote == "" {
		return fmt.Errorf("pane %d is a local chat, not a remote attachment", p.Index)
	}
	return nil
}

// RemotePane reports whether pane is a stamped remote chat pane of a
// cockpit, and the facts it decided from. Read-only.
func RemotePane(pane string, isCockpit CockpitCheck) (PaneFacts, error) {
	p, err := paneFacts(pane)
	if err != nil {
		return PaneFacts{}, err
	}
	return p, remotePane(p, isCockpit)
}

// HideRemotePane closes the LOCAL attach pane of a remote chat, records the
// chat on the cockpit session and re-tiles the cockpit window. Nothing is
// sent to the host: the remote session and its agent are untouched.
//
// Order: every refusal, then announce (the caller's event), then the record,
// then the kill — so a failure at any step leaves a pane on screen or a row
// in the box, never neither.
func HideRemotePane(pane string, isCockpit CockpitCheck, announce func(HiddenRemote) error) (HiddenRemote, error) {
	facts, err := RemotePane(pane, isCockpit)
	if err != nil {
		return HiddenRemote{}, err
	}
	rec := HiddenRemote{Host: facts.Remote, Session: facts.RemoteSession, Profile: facts.Profile}
	if announce != nil {
		if err := announce(rec); err != nil {
			return HiddenRemote{}, err
		}
	}
	before := HiddenRemotes(facts.Session)
	if err := writeHiddenRemotes(facts.Session, AddHiddenRemote(append([]HiddenRemote(nil), before...), rec)); err != nil {
		return HiddenRemote{}, fmt.Errorf("could not record the hidden chat, so nothing was closed: %w", err)
	}
	if _, err := run("kill-pane", "-t", pane); err != nil {
		_ = writeHiddenRemotes(facts.Session, before)
		return HiddenRemote{}, err
	}
	return rec, retileCockpit(facts.Session, facts.WindowID)
}

// SpawnPaneInWindow is SpawnPane aimed at ONE window by its immutable id
// rather than at the session's active window, for a caller that may not be
// sitting in the cockpit (a verb typed in a worker window). focus=false
// leaves the window's active pane where it was.
func SpawnPaneInWindow(session, windowID, dir, cmd string, focus bool) (string, error) {
	split := []string{"split-window"}
	if !focus {
		split = append(split, "-d")
	}
	paneID, err := run(append(split, "-t", windowID, "-c", dir, "-P", "-F", "#{pane_id}")...)
	if err != nil {
		return "", err
	}
	paneID = strings.TrimSpace(paneID)
	if err := retileCockpit(session, windowID); err != nil {
		return paneID, err
	}
	if err := SendKeys(paneID, cmd); err != nil {
		return paneID, err
	}
	if focus {
		if _, err := run("select-pane", "-t", paneID); err != nil {
			return paneID, err
		}
	}
	return paneID, nil
}

// showableRemote is the pure guard for ShowRemotePane.
func showableRemote(cockpitSession, cockpitWindowID string, rec HiddenRemote, recorded bool, isCockpit CockpitCheck) error {
	if !isCockpit.cockpit(cockpitSession) {
		return fmt.Errorf("session %q is not a registered workspace's cockpit — a remote chat is shown in a cockpit (`gv workspaces` lists the workspaces)", cockpitSession)
	}
	if cockpitWindowID == "" {
		return fmt.Errorf("the cockpit %s is not running, so there is no window to show @%s's %s in — open the cockpit with `gv`", cockpitSession, rec.Host, rec.Session)
	}
	if !recorded {
		return fmt.Errorf("no hidden remote chat %s on @%s in %s — only a chat hidden from this cockpit can be shown; the CHATS box lists them", rec.Session, rec.Host, cockpitSession)
	}
	return nil
}

// ShowRemotePane re-attaches a hidden remote chat: a new pane in the cockpit
// window running attachCmd, tagged like a freshly spawned remote chat, and
// the record dropped. Returns the new pane's %id and TagRemotePane's
// warnings.
//
// attachCmd is the caller's (it knows the ssh target); only a RECORDED
// host+session is ever attached to. Whether the chat still lives on the host
// is not checked — that would be a dial; a session that is gone shows as
// ssh's own error in the pane.
func ShowRemotePane(cockpitSession, host, session, attachCmd, dir string, focus bool, isCockpit CockpitCheck) (string, []string, error) {
	window := ""
	if cockpitSession != "" && SessionExists(cockpitSession) {
		window, _ = WindowIDExact(cockpitSession, cockpitWindow)
	}
	list := HiddenRemotes(cockpitSession)
	rec := HiddenRemote{Host: host, Session: session}
	i := hiddenRemoteIndex(list, host, session)
	if i >= 0 {
		rec = list[i]
	}
	if err := showableRemote(cockpitSession, window, rec, i >= 0, isCockpit); err != nil {
		return "", nil, err
	}
	pane, err := SpawnPaneInWindow(cockpitSession, window, dir, attachCmd, focus)
	if pane == "" {
		return "", nil, err
	}
	// The pane exists: from here it is tagged and the record dropped whatever
	// else went wrong, or the chat would be on screen AND in the hidden rows.
	warnings := TagRemotePane(pane, window, rec.Host, rec.Profile, rec.Session)
	list, _ = RemoveHiddenRemote(list, host, session)
	if werr := writeHiddenRemotes(cockpitSession, list); werr != nil && err == nil {
		err = werr
	}
	return pane, warnings, err
}
