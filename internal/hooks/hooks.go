// Package hooks implements the `gv hook <event>` receivers and the
// settings.json merge-installer. Receivers must be fast, silent, and always
// exit 0 — they run inside every ccwork session, including untracked ones.
package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/notify"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
)

// Payload is the hook stdin JSON. Verified live 2026-06-10 (LEARNINGS.md):
// Stop carries last_assistant_message, so no transcript parsing is needed
// for sentinel classification.
type Payload struct {
	SessionID            string `json:"session_id"`
	TranscriptPath       string `json:"transcript_path"`
	Cwd                  string `json:"cwd"`
	HookEventName        string `json:"hook_event_name"`
	Message              string `json:"message"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

// Matches "STATUS: QUESTION — text" with any dash flavor (—, –, -).
var sentinelRe = regexp.MustCompile(`(?ms)^\s*STATUS:\s*(QUESTION|BLOCKED|DONE)\s*[—–-]+\s*(.+?)\s*$`)

// Candidate is one fleet the receiver may attribute an event to: a
// registered workspace (Label = workspace label) or the legacy global
// state dir. The caller supplies candidates in ownership-scan order —
// workspaces sorted by label, legacy last.
type Candidate struct {
	Label    string
	StateDir string
}

// Receive handles one hook event from stdin. Errors are swallowed by the
// caller (exit 0 always) — a broken hook must never break a session.
//
// Ownership is decided by task membership, not workspace resolution
// (DESIGN.md §12): each candidate's tasks.json is checked READ-ONLY via
// state.ReadTasks (never state.Load, which folds and rewrites the derived
// view — this receiver fires on every live ovs turn too). The first
// candidate whose fleet tracks the payload cwd owns the event; the append
// goes to that fleet's state dir only. No candidate matching is the
// ovs-coexistence no-op contract: return nil, zero writes.
func Receive(candidates []Candidate, event string, stdin io.Reader) error {
	var p Payload
	if err := json.NewDecoder(stdin).Decode(&p); err != nil {
		return err
	}
	var stateDir string
	var task *state.Task
	for _, c := range candidates {
		if t := state.FindByCwd(state.ReadTasks(c.StateDir), p.Cwd); t != nil {
			stateDir, task = c.StateDir, t
			break
		}
	}
	if task == nil {
		return nil // no fleet tracks this cwd (manual session, orchestrator, ovs worktree) — stay silent
	}
	if event != "session-start" && foreignSession(task, p.SessionID) {
		return nil // another session at the worker's cwd — not the worker; stay silent (grove-250)
	}

	switch event {
	case "session-start":
		glyphWorker(task, state.Glyph(state.AgentWorking, ""))
		return state.Append(stateDir, state.Event{
			Type: state.EvSessionStarted, Ticket: task.Ticket,
			Data: map[string]string{"session_id": p.SessionID},
		})

	case "stop":
		status, sentinel, question, message := classify(p.LastAssistantMessage)
		// grove-126: events.jsonl is append-only and folded every cockpit
		// tick, so an unbounded stored message compounds the read cost
		// forever. Classification above ran on the full text; the head is
		// enough for every display surface (the parsed sentinel/question
		// fields carry the actionable tail). Reader-side truncation bugs
		// are #123's territory — this cap guarantees new records stay far
		// inside every reader's line buffer.
		message = capRunes(message, messageCap)
		glyphWorker(task, state.Glyph(status, sentinel))
		if err := state.Append(stateDir, state.Event{
			Type: state.EvAgentStatus, Ticket: task.Ticket,
			Data: map[string]string{
				"status": status, "sentinel": sentinel,
				"question": question, "message": message,
				"session_id": p.SessionID,
			},
		}); err != nil {
			return err
		}
		switch sentinel {
		case "question":
			notify.Desktop(task.Ticket+" has a question", question)
			notify.Push(task.Ticket+" has a question", question, "high", "grey_question")
		case "blocked":
			notify.Desktop(task.Ticket+" is blocked", question)
			notify.Push(task.Ticket+" is blocked", question, "high", "no_entry")
		case "done":
			notify.Desktop(task.Ticket+" reports done", firstLine(message))
			notify.Push(task.Ticket+" reports done", firstLine(message), "default", "white_check_mark")
		default:
			// Plain idle stops are the common case — desktop-only, so an
			// ntfy outage can never tax every turn-end.
			notify.Desktop(task.Ticket+" went idle", "no STATUS line — check on it")
		}
		return nil

	case "notification":
		glyphWorker(task, state.Glyph(state.AgentWaiting, ""))
		_ = state.Append(stateDir, state.Event{
			Type: state.EvNotification, Ticket: task.Ticket,
			Data: map[string]string{"message": p.Message, "session_id": p.SessionID},
		})
		notify.Desktop(task.Ticket+" needs input", p.Message)
		notify.Push(task.Ticket+" needs input", p.Message, "high", "bell")
		return nil

	case "session-end":
		glyphWorker(task, state.Glyph(state.AgentDead, ""))
		return state.Append(stateDir, state.Event{
			Type: state.EvSessionEnded, Ticket: task.Ticket,
			Data: map[string]string{"session_id": p.SessionID},
		})
	}
	return fmt.Errorf("unknown hook event %q", event)
}

// foreignSession reports whether a payload at a tracked worktree's cwd
// came from a session OTHER than the task's recorded worker (grove-250).
//
// Hooks fire from every Claude session in the profile and Claude Code's
// hook cwd follows the Bash tool's persistent shell cwd, so one
// `cd <worktree> && …` in an orchestrator makes its next Stop look like
// the worker's — verified live 2026-09-02: a worker 30 minutes into a
// busy turn was stamped `idle` with the orchestrator's chat reply as its
// last_message, and a stall monitor fired on it. The same mechanism lets
// a late SessionEnd from a REPLACED process stamp `dead` over the live
// successor (#148). The cwd match stays the ownership key; the recorded
// session id is the second lock on the door.
//
// Fallback: a task with no recorded id (pre-capture rows, or a worker
// whose SessionStart was lost) keeps cwd-only attribution — an unknown
// id must never make a task unreachable. A payload with no id is treated
// the same way. `session-start` is exempt by the caller: it is how a
// worker registers (adopt's fresh pickup session carries a NEW id).
func foreignSession(task *state.Task, sessionID string) bool {
	return task.SessionID != "" && sessionID != "" && task.SessionID != sessionID
}

// glyphWorker pushes the worker's live status glyph into its tmux window
// name so `Ctrl-b w` reads as a fleet board (grove-29 P3). Event-driven off
// this existing hook path — no new goroutine, poll, or cache (the cockpit-ram
// guardrail) — and best-effort silent: a rename failure (window gone, tmux
// down) must never break a hook. The task's stored tmux_window is used as the
// stable base and is never rewritten; the glyph is a display-only suffix.
func glyphWorker(task *state.Task, glyph string) {
	_ = tmux.RenameWorker(task.TmuxSession, task.TmuxWindow, glyph)
}

// classify maps the final assistant message to agent state via the STATUS
// sentinel the kickoff prompt mandates.
func classify(msg string) (status, sentinel, question, message string) {
	m := sentinelRe.FindStringSubmatch(msg)
	if m == nil {
		return state.AgentIdle, "none", "", msg
	}
	switch m[1] {
	case "QUESTION":
		return state.AgentWaiting, "question", m[2], msg
	case "BLOCKED":
		return state.AgentBlocked, "blocked", m[2], msg
	default: // DONE
		return state.AgentIdle, "done", "", msg
	}
}

// messageCap bounds what a stop event stores as data["message"], in runes —
// roughly a detail-view screenful with margin. JSON-encoded that is at most
// a few KB per turn instead of an entire unbounded assistant message.
const messageCap = 2000

// capRunes truncates to n runes with an ellipsis marker — rune-safe, never
// mid-codepoint (grove-131 class).
func capRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

// --- installer ---

var hookEvents = map[string]string{
	"SessionStart": "session-start",
	"Notification": "notification",
	"Stop":         "stop",
	"SessionEnd":   "session-end",
}

// SettingsPaths derives every Claude settings.json the fleet's workers
// live under — hooks only fire for sessions of the profile whose settings
// they're installed in, so a ccwork (Grid) worker and a plain-claude
// (personal) worker need entries in DIFFERENT files. Recognized shapes:
// `ccwork …` → ~/.cc-work; `CLAUDE_CONFIG_DIR=<dir> …` → <dir>; anything
// else (plain `claude …`) → ~/.claude, the default profile. Deduped,
// sorted; empty input still yields the default profile.
func SettingsPaths(workerCmds []string) []string {
	home, _ := os.UserHomeDir()
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		p := filepath.Join(dir, "settings.json")
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(workerCmds) == 0 {
		add(filepath.Join(home, ".claude"))
	}
	for _, cmd := range workerCmds {
		fields := strings.Fields(cmd)
		switch {
		case len(fields) == 0:
			add(filepath.Join(home, ".claude"))
		case strings.HasPrefix(fields[0], "CLAUDE_CONFIG_DIR="):
			dir := strings.TrimPrefix(fields[0], "CLAUDE_CONFIG_DIR=")
			if strings.HasPrefix(dir, "~/") {
				dir = filepath.Join(home, dir[2:])
			}
			add(dir)
		case fields[0] == "ccwork":
			add(filepath.Join(home, ".cc-work"))
		default:
			add(filepath.Join(home, ".claude"))
		}
	}
	sort.Strings(out)
	return out
}

// WorkerCommands extracts the per-repo worker commands for SettingsPaths.
func WorkerCommands(cfg *config.Config) []string {
	var out []string
	for _, r := range cfg.Repos {
		out = append(out, r.Claude)
	}
	return out
}

// Install merges the four gv hooks into every derived settings.json
// without clobbering existing hooks. Idempotent: an existing "gv hook"
// entry for an event is replaced (binary path may have changed), others
// are preserved. During the ovs→grove transition window both tools' hooks
// coexist in the shared ~/.cc-work file — ovs entries must never match
// isGvEntry (DESIGN.md §12).
func Install(paths []string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	var done []string
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return done, err
		}
		if err := install(path, exe); err != nil {
			return done, fmt.Errorf("%s: %w", path, err)
		}
		done = append(done, path)
	}
	return done, nil
}

// install is Install with the file path and binary path injectable (tests).
func install(path, exe string) error {
	settings := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("parse %s: %w (fix it manually first)", path, err)
		}
	}

	hooksVal, _ := settings["hooks"].(map[string]any)
	if hooksVal == nil {
		hooksVal = map[string]any{}
	}
	for event, sub := range hookEvents {
		entries, _ := hooksVal[event].([]any)
		var kept []any
		for _, e := range entries {
			if !isGvEntry(e) {
				kept = append(kept, e)
			}
		}
		kept = append(kept, map[string]any{
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": fmt.Sprintf("%s hook %s", exe, sub),
			}},
		})
		hooksVal[event] = kept
	}
	settings["hooks"] = hooksVal

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// Installed reports, per settings path, which hook events currently have
// a gv entry. A missing file reads as nothing-installed, not an error.
func Installed(paths []string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, path := range paths {
		got, err := installed(path)
		if err != nil {
			got = map[string]bool{}
		}
		out[path] = got
	}
	return out
}

// AllInstalled reports whether every event is wired in every path.
func AllInstalled(paths []string) bool {
	for _, events := range Installed(paths) {
		if len(events) != len(hookEvents) {
			return false
		}
	}
	return len(paths) > 0
}

func installed(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var settings struct {
		Hooks map[string][]any `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for event := range hookEvents {
		for _, e := range settings.Hooks[event] {
			if isGvEntry(e) {
				out[event] = true
			}
		}
	}
	return out, nil
}

// isGvEntry reports whether a settings.json hook entry is one of OURS.
// Entries are written as "<abs-binary-path> hook <event>", so match on the
// binary's basename — substring checks would risk claiming (and replacing)
// live ovs entries in the shared settings file, which must survive
// byte-identical through every `gv hooks install` (dual-hook contract).
func isGvEntry(e any) bool {
	m, ok := e.(map[string]any)
	if !ok {
		return false
	}
	inner, _ := m["hooks"].([]any)
	for _, h := range inner {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := hm["command"].(string)
		bin, _, found := strings.Cut(cmd, " hook ")
		if !found {
			continue
		}
		base := filepath.Base(strings.TrimSpace(bin))
		if base == "gv" || strings.Contains(base, "grove") {
			return true
		}
	}
	return false
}
