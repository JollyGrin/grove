// Package claudestate reads Claude Code's own per-session status files
// (<config dir>/sessions/<pid>.json, verified on 2.1.304 — grove-534) so a
// beat can learn busy/idle/waiting without capturing a pane.
package claudestate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/state"
)

// file is the decoded subset of a session file; every other field is ignored.
type file struct {
	Status          string `json:"status"`
	WaitingFor      string `json:"waitingFor"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"` // ms epoch
	SessionID       string `json:"sessionId"`
	Tmux            string `json:"tmux"` // "<session>:@<win>.%<pane>"; absent outside tmux
}

// Parse decodes one session file. pane is the "%N" after the last "." of the
// tmux field ("" outside tmux). A file with no status yet (a new file lacks
// it for ~50 ms) or a torn write is an error — the caller keeps no signal.
func Parse(data []byte) (st state.ClaudeStatus, pane string, err error) {
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return state.ClaudeStatus{}, "", err
	}
	if f.Status == "" {
		return state.ClaudeStatus{}, "", errors.New("claudestate: no status")
	}
	if i := strings.LastIndex(f.Tmux, "."); i >= 0 {
		pane = f.Tmux[i+1:]
	}
	st = state.ClaudeStatus{Status: f.Status, WaitingFor: f.WaitingFor, SessionID: f.SessionID}
	if f.StatusUpdatedAt > 0 {
		st.UpdatedAt = time.UnixMilli(f.StatusUpdatedAt)
	}
	return st, pane, nil
}

// Read scans each dir once (one ReadDir apiece) and returns the status of
// every session whose pane id is in want. Only *.json is read (the sibling
// *.key is skipped); an unreadable dir or undecodable file is skipped, never
// an error for the batch. If two files claim one pane, the one updated last
// wins. The result is meant to live for one refresh.
func Read(dirs []string, want map[string]bool) map[string]state.ClaudeStatus {
	out := map[string]state.ClaudeStatus{}
	if len(want) == 0 {
		return out
	}
	for _, dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			st, pane, err := Parse(data)
			if err != nil || !want[pane] {
				continue
			}
			if old, ok := out[pane]; ok && old.UpdatedAt.After(st.UpdatedAt) {
				continue
			}
			out[pane] = st
		}
	}
	return out
}
