// Package hooks implements the `gv hook <event>` receivers and the
// settings.json merge-installer. Receivers must be fast, silent, and always
// exit 0 — they run inside every ccwork session, including untracked ones.
package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
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

// Receive handles one hook event from stdin. Errors are swallowed by the
// caller (exit 0 always) — a broken hook must never break a session.
func Receive(stateDir, event string, stdin io.Reader) error {
	var p Payload
	if err := json.NewDecoder(stdin).Decode(&p); err != nil {
		return err
	}
	tasks, err := state.Load(stateDir)
	if err != nil {
		return err
	}
	task := state.FindByCwd(tasks, p.Cwd)
	if task == nil {
		return nil // not a tracked worktree (manual session, orchestrator) — stay silent
	}

	switch event {
	case "session-start":
		return state.Append(stateDir, state.Event{
			Type: state.EvSessionStarted, Ticket: task.Ticket,
			Data: map[string]string{"session_id": p.SessionID},
		})

	case "stop":
		status, sentinel, question, message := classify(p.LastAssistantMessage)
		if err := state.Append(stateDir, state.Event{
			Type: state.EvAgentStatus, Ticket: task.Ticket,
			Data: map[string]string{
				"status": status, "sentinel": sentinel,
				"question": question, "message": message,
			},
		}); err != nil {
			return err
		}
		switch sentinel {
		case "question":
			notify(task.Ticket+" has a question", question)
			pushNtfy(task.Ticket+" has a question", question, "high", "grey_question")
		case "blocked":
			notify(task.Ticket+" is blocked", question)
			pushNtfy(task.Ticket+" is blocked", question, "high", "no_entry")
		case "done":
			notify(task.Ticket+" reports done", firstLine(message))
			pushNtfy(task.Ticket+" reports done", firstLine(message), "default", "white_check_mark")
		default:
			// Plain idle stops are the common case — desktop-only, so an
			// ntfy outage can never tax every turn-end.
			notify(task.Ticket+" went idle", "no STATUS line — check on it")
		}
		return nil

	case "notification":
		_ = state.Append(stateDir, state.Event{
			Type: state.EvNotification, Ticket: task.Ticket,
			Data: map[string]string{"message": p.Message},
		})
		notify(task.Ticket+" needs input", p.Message)
		pushNtfy(task.Ticket+" needs input", p.Message, "high", "bell")
		return nil

	case "session-end":
		return state.Append(stateDir, state.Event{
			Type: state.EvSessionEnded, Ticket: task.Ticket,
		})
	}
	return fmt.Errorf("unknown hook event %q", event)
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

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

// notify pings via terminal-notifier, best-effort. Fired from the hook
// itself so it works with the TUI closed.
func notify(title, body string) {
	if _, err := exec.LookPath("terminal-notifier"); err != nil {
		return
	}
	if len(body) > 120 {
		body = body[:120] + "…"
	}
	_ = exec.Command("terminal-notifier",
		"-title", "gv: "+title, "-message", body,
		"-group", "grove", "-sender", "com.apple.Terminal").Start()
}

// --- ntfy push ---

// ntfySettings resolves the push target; only consulted for push-worthy
// classes, and overridable in tests so they can never reach a real topic.
var ntfySettings = config.NotifySettings

// ntfyClient bounds how long a push may delay a hook: an ntfy outage costs
// at most 1.5s, and only on notification-worthy turn-ends.
var ntfyClient = &http.Client{Timeout: 1500 * time.Millisecond}

// pushNtfy sends a phone push, best-effort. Synchronous by necessity — a
// goroutine can't outlive the hook process — and silent on every failure.
func pushNtfy(title, body, priority, tags string) {
	n := ntfySettings()
	if n.Ntfy == "" {
		return
	}
	if n.NtfyBody == "title-only" {
		body = ""
	}
	if len(body) > 200 {
		body = body[:200] + "…"
	}
	req, err := http.NewRequest(http.MethodPost, n.Ntfy, strings.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Title", "gv: "+title)
	req.Header.Set("Priority", priority)
	req.Header.Set("Tags", tags)
	resp, err := ntfyClient.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// --- installer ---

var hookEvents = map[string]string{
	"SessionStart": "session-start",
	"Notification": "notification",
	"Stop":         "stop",
	"SessionEnd":   "session-end",
}

func settingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cc-work", "settings.json")
}

// Install merges the four gv hooks into ~/.cc-work/settings.json without
// clobbering existing hooks. Idempotent: an existing "gv hook" entry for an
// event is replaced (binary path may have changed), others are preserved.
// During the ovs→grove transition window both tools' hooks coexist in the
// same file — ovs entries must never match isGvEntry (DESIGN.md §12).
func Install() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	return install(settingsPath(), exe)
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

// Installed reports which hook events currently have a gv entry.
func Installed() (map[string]bool, error) {
	return installed(settingsPath())
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
