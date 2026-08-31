// Package chat is the decision half of `gv chat` (grove-215): what a
// workspace's orchestrator chats ARE, in one shape a phone, a plugin and
// the CLI can all read.
//
// The join this package exists for: tmux knows a workspace's live
// `grove-chat-<label>-<n>` sessions, and Claude Code's transcript dir knows
// the session ids that ran in the orchestrator's cwd — and nothing joined
// them, because "the newest .jsonl in the dir" is ambiguous the moment a
// workspace has two chats (they share one project dir).
//
// Since grove-222 the join is answered by CONSTRUCTION rather than by
// inference: grove mints the session id, passes it to `claude --session-id`
// and stamps the pane at spawn (identity.go). Resolve is what is left for a
// pane grove did not spawn — and it answers only when there is exactly one
// candidate, never by ordering rivals on transcript mtime.
package chat

import (
	"path/filepath"
	"regexp"
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

// Resolve pairs a live pane with a transcript, and REFUSES to when the
// answer would be a guess.
//
// rivals is how many unstamped panes are competing for this project dir.
// Exactly one is the only case this can answer: that pane is the sole live
// candidate, so "the newest transcript nothing else has claimed" names it.
// Two or more and the answer is `false` — session_id stays null — because
// ordering rivals by transcript mtime is precisely the grove-222 bug: mtime
// is LAST WRITE, so an older pane still working outranks a younger one gone
// idle, and the pair comes back inverted. A null id costs a client a `tail`
// button; a wrong one pastes the operator's words into the wrong agent.
//
// sessions must be newest-first (what transcript.ListSessions returns) and
// already cwd-filtered — a chat's cwd IS its project dir, so a profiled chat
// under <brain>/<profile> resolves against its own dir and can never steal
// the default pane's transcript. The claim set holds every id already spoken
// for: stamped on a pane, or read off a running process (identity.go).
//
// This is the FALLBACK path. Every chat grove spawns carries a minted
// `--session-id` and is stamped before it boots, so it never reaches here;
// what does is the cockpit's own `--continue` pane and anything started by
// hand.
func Resolve(sessions []transcript.Session, claimed map[string]bool, rivals int) (transcript.Session, bool) {
	if rivals != 1 {
		return transcript.Session{}, false
	}
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
	sort.SliceStable(rows, func(i, j int) bool { return Less(rows[i], rows[j]) })
}

// Less is Sort's comparator, exported so a caller holding rows PLUS the
// impure details behind them (a pane id, a project dir) can order its own
// slice the same way rather than sorting a projection and losing the join.
func Less(a, b Row) bool {
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

// --- grove-217: reviving an archived chat (`gv orchestrator new --resume`) ---

// sessionIDRe is the shape gate for a `--resume` id. Claude Code mints
// UUIDs, but the e2e fixtures (and any future id scheme) are plainer, so
// this is deliberately a CHARACTER-CLASS gate rather than a UUID parser.
// It is also a safety gate: the id is interpolated into the shell command
// tmux runs in the new chat pane, so anything a shell could read as
// syntax — a space, a quote, `$`, `;`, a leading dash — never gets there.
var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidSessionID reports whether id is shaped like a Claude session id and
// is safe to interpolate into the pane's launch command.
func ValidSessionID(id string) bool { return sessionIDRe.MatchString(id) }

// ProjectDir is one directory a workspace's chats have run in — the brain
// dir or a per-profile subdir of it — paired with the transcripts found
// there. The caller does the scanning (filesystem); this package decides.
type ProjectDir struct {
	Dir      string
	Sessions []transcript.Session
}

// FindSession locates the transcript of id among a workspace's project
// dirs. The DIR is the load-bearing half of the answer: `claude --resume`
// looks the id up in the project dir of its cwd, so a conversation that
// ran under <brain>/<profile> can only be revived from that same dir —
// resuming it from the brain dir finds nothing and starts a fresh chat
// wearing the wrong backend.
func FindSession(dirs []ProjectDir, id string) (ProjectDir, transcript.Session, bool) {
	for _, d := range dirs {
		for _, s := range d.Sessions {
			if s.ID == id {
				return d, s, true
			}
		}
	}
	return ProjectDir{}, transcript.Session{}, false
}

// ProfileForDir names the model profile a project dir belongs to: "" for
// the brain dir itself (the operator's own Claude), the subdir name for a
// profiled chat (the grove-36 T4 convention — one cwd per backend). ok is
// false for a dir that is not the brain dir or an immediate child of it,
// which is a caller bug rather than an operator one.
func ProfileForDir(orchDir, dir string) (string, bool) {
	orch, d := filepath.Clean(orchDir), filepath.Clean(dir)
	if orch == d {
		return "", true
	}
	rel, err := filepath.Rel(orch, d)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || strings.ContainsRune(rel, filepath.Separator) {
		return "", false
	}
	return rel, true
}

// LiveHolder returns the tmux session already running chat id, "" when
// nothing does. Reviving a conversation a live pane is already holding
// would put two claude processes on one append-only transcript, so the
// caller refuses it.
//
// Best-effort by construction: a pane carries its id only once `gv chat
// ls` has stamped it (@grove_chat_session), so a chat spawned seconds ago
// and never listed is invisible here. It catches the case that actually
// happens — reviving from a list that just showed the chat as live.
func LiveHolder(panes []tmux.LivePane, id string) string {
	if id == "" {
		return ""
	}
	for _, p := range panes {
		if p.ChatSession == id {
			return p.Session
		}
	}
	return ""
}
