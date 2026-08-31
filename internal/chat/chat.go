// Package chat is the decision half of `gv chat` (grove-215): what a
// workspace's orchestrator chats ARE, in one shape a phone, a plugin and
// the CLI can all read.
//
// The join this package exists for: tmux knows a workspace's live
// `grove-chat-<label>-<n>` sessions, and Claude Code's transcript dir knows
// the session ids that ran in the orchestrator's cwd — and nothing joined
// them, because "the newest .jsonl in the dir" is ambiguous the moment a
// workspace has two chats (they share one project dir). Resolve pairs a
// live pane with a transcript ONCE; the caller stamps the answer on the
// pane (tmux.SetPaneChatSession) and never re-derives it.
package chat

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/transcript"
)

// The three states a chat can be in (design §"The three states"). They are
// not uniform and the report must not pretend they are.
const (
	// KindChat is a live detached `grove-chat-<label>-<n>` session: its own
	// tmux session, one pane, a claude process. The only writable kind.
	KindChat = "chat"
	// KindCockpit is the cockpit's OWN orchestrator pane — mechanically the
	// same agent, but it lives in `grove-<label>` beside the dashboard and
	// the operator may be typing in it. Read-only, deliberately.
	KindCockpit = "cockpit"
	// KindArchived is a transcript with no live pane: every past chat.
	// Read-only until revived (`--resume`, grove-217).
	KindArchived = "archived"
)

// Row is one `gv chat ls` row. Contract shape (docs/plugins.md): additive
// only. SessionID is a pointer so an unresolved chat emits `null` rather
// than an empty string a client could mistake for an id.
type Row struct {
	Session   string    `json:"session"`
	Workspace string    `json:"workspace"`
	N         int       `json:"n"`
	Kind      string    `json:"kind"`
	SessionID *string   `json:"session_id"`
	Label     string    `json:"label"`
	Command   string    `json:"command"`
	Busy      bool      `json:"busy"`
	Attached  bool      `json:"attached"`
	Created   time.Time `json:"created"`
	Writable  bool      `json:"writable"`
}

// Writable answers the one question a client must never guess: may this row
// take input? Only a live type-A chat pane may. A cockpit pane is somebody
// else's keyboard and an archived transcript has no process at all — a UI
// disables its input off THIS field, never off its own reading of `kind`.
func Writable(kind string) bool { return kind == KindChat }

// Live is one live pane the lister found, already classified. Deliberately
// primitives rather than a tmux type: the row shape is a contract and this
// package is table-tested without a tmux server.
type Live struct {
	Session   string // the tmux session the pane lives in
	Workspace string
	N         int    // chat number, or the cockpit pane's index
	Kind      string // KindChat | KindCockpit
	Command   string // pane_current_command
	Attached  bool
	Created   time.Time
	SessionID string // "" = not resolved yet → session_id: null
	Label     string // the transcript's FirstPrompt, "" while unresolved
}

// Row projects a live pane into the contract shape.
func (l Live) Row() Row {
	return Row{
		Session:   l.Session,
		Workspace: l.Workspace,
		N:         l.N,
		Kind:      l.Kind,
		SessionID: idOrNull(l.SessionID),
		Label:     l.Label,
		Command:   l.Command,
		Busy:      busy(l.Command),
		Attached:  l.Attached,
		Created:   l.Created,
		Writable:  Writable(l.Kind),
	}
}

// ArchivedRow projects a transcript with no live pane. Its `created` is the
// transcript's ModTime — the last time the chat was actually spoken to,
// which is what a "pick yesterday's chat back up" list sorts on.
func ArchivedRow(workspace string, s transcript.Session) Row {
	id := s.ID
	return Row{
		Workspace: workspace,
		Kind:      KindArchived,
		SessionID: idOrNull(id),
		Label:     s.FirstPrompt,
		Created:   s.ModTime,
		Writable:  Writable(KindArchived),
	}
}

// idOrNull keeps an unresolved id honest: JSON null, never "".
func idOrNull(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

// busy reads `pane_current_command`: the agent's own process means work is
// happening; a shell prompt means the chat is idle (or dead). Liveness
// garnish by house rule — the transcript is truth — so it is reported, not
// acted on.
func busy(command string) bool {
	return tmux.Claudeish(strings.TrimSpace(command))
}

// Busy is busy's exported twin, for callers projecting rows themselves.
func Busy(command string) bool { return busy(command) }

// Resolve picks the transcript a live chat pane owns: the newest one whose
// id nothing else has claimed. sessions must be newest-first (what
// transcript.ListSessions returns) and already cwd-filtered — a chat's cwd
// IS its project dir, so a profiled chat under <brain>/<profile> resolves
// against its own dir and can never steal the default pane's transcript.
//
// The claim set is what makes two chats in one workspace separable: caller
// seeds it with every id already stamped on a pane, then adds each id as it
// is handed out. Resolving newest pane first pairs the newest chat with the
// newest transcript. Nothing found = "" = `session_id: null`, and the next
// `gv chat ls` tries again — a chat that is still booting has no .jsonl yet.
func Resolve(sessions []transcript.Session, claimed map[string]bool) (transcript.Session, bool) {
	for _, s := range sessions {
		if s.ID == "" || claimed[s.ID] {
			continue
		}
		return s, true
	}
	return transcript.Session{}, false
}

// IsOrchestratorPane reports whether a pane of a COCKPIT session is one of
// its orchestrator chats — by cwd, not by command. The cockpit's brain dir
// (and a profile subdir of it) is where an orchestrator pane runs and
// nothing else does: the dashboard pane sits at the workspace root, and so
// does a grove-199 remote-chat pane, whose agent runs on another machine
// entirely and must not be reported here.
func IsOrchestratorPane(paneDir, orchDir string) bool {
	if paneDir == "" || orchDir == "" {
		return false
	}
	pane, orch := filepath.Clean(paneDir), filepath.Clean(orchDir)
	if pane == orch {
		return true
	}
	return strings.HasPrefix(pane, orch+string(filepath.Separator))
}

// Sort orders the report: workspace, then live chats before cockpit panes
// before archived transcripts, then chat number, then newest first. Stable
// across calls so a client can diff two `ls` runs.
func Sort(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Workspace != b.Workspace {
			return a.Workspace < b.Workspace
		}
		if a.Kind != b.Kind {
			return kindOrder(a.Kind) < kindOrder(b.Kind)
		}
		if a.N != b.N {
			return a.N < b.N
		}
		if !a.Created.Equal(b.Created) {
			return a.Created.After(b.Created)
		}
		return a.Label < b.Label
	})
}

func kindOrder(kind string) int {
	switch kind {
	case KindChat:
		return 0
	case KindCockpit:
		return 1
	default:
		return 2
	}
}
