// Package tmux provides wrappers around the tmux CLI for session lifecycle.
package tmux

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// run executes tmux with the given args, returning trimmed stdout.
func run(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tmux %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// SessionName builds a sanitized tmux session name: pr-<repo>.
// Replaces characters that tmux forbids in session names (. and :).
func SessionName(repo string) string {
	name := fmt.Sprintf("pr-%s", repo)
	r := strings.NewReplacer(".", "-", ":", "-")
	return r.Replace(name)
}

// WindowName sanitizes a worktree name for use as a tmux window name.
func WindowName(worktree string) string {
	r := strings.NewReplacer(".", "-", ":", "-")
	return r.Replace(worktree)
}

// WindowTarget returns a tmux target for a window: "pr-<repo>:<worktree>".
func WindowTarget(repo, worktree string) string {
	return SessionName(repo) + ":" + WindowName(worktree)
}

// PaneTarget returns a tmux target for a specific pane: "pr-<repo>:<worktree>.<pane>".
func PaneTarget(repo, worktree string, pane int) string {
	return fmt.Sprintf("%s.%d", WindowTarget(repo, worktree), pane)
}

// SessionExists returns true if the named tmux session exists.
// Returns false (not an error) if the tmux server is not running.
func SessionExists(name string) bool {
	err := exec.Command("tmux", "has-session", "-t", name).Run()
	return err == nil
}

// CreateSession creates a detached tmux session in the given directory.
func CreateSession(name, workDir string) error {
	_, err := run("new-session", "-d", "-s", name, "-c", workDir)
	return err
}

// SplitVertical splits the session's current window horizontally (side-by-side).
func SplitVertical(session, workDir string) error {
	_, err := run("split-window", "-h", "-t", session, "-c", workDir)
	return err
}

// SendKeys sends keystrokes to the given tmux target followed by Enter.
func SendKeys(target, keys string) error {
	_, err := run("send-keys", "-t", target, keys, "Enter")
	return err
}

// AttachSession attaches to (or switches to) the named session.
// Inside tmux: uses switch-client. Outside: replaces the process with tmux attach via syscall.Exec.
func AttachSession(name string) error {
	if IsInsideTmux() {
		_, err := run("switch-client", "-t", name)
		return err
	}

	// Find the tmux binary
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}

	// syscall.Exec replaces this process entirely — no Go code runs after this
	return syscall.Exec(tmuxPath, []string{"tmux", "attach-session", "-t", name}, os.Environ())
}

// CapturePane captures the visible content of a tmux pane.
// Returns "" (not an error) if the pane or session doesn't exist.
func CapturePane(target string) (string, error) {
	out, err := run("capture-pane", "-p", "-J", "-t", target)
	if err != nil {
		// Pane doesn't exist — not an error for our purposes
		return "", nil
	}
	return out, nil
}

// CapturePaneBottom captures only the bottom N visible lines of a tmux pane.
// Queries the pane height and uses -S to skip lines above the bottom region.
// This ensures we always read the most recent activity regardless of pane size.
// Returns "" (not an error) if the pane or session doesn't exist.
func CapturePaneBottom(target string, lines int) (string, error) {
	heightStr, err := run("display-message", "-p", "-t", target, "#{pane_height}")
	if err != nil {
		return "", nil // pane doesn't exist
	}
	height, _ := strconv.Atoi(strings.TrimSpace(heightStr))
	if height <= 0 {
		return "", nil
	}

	// Calculate start offset so we only capture the bottom `lines` rows.
	// tmux capture-pane -S uses 0-indexed rows from the top of the visible pane.
	start := 0
	if height > lines {
		start = height - lines
	}

	out, err := run("capture-pane", "-p", "-J", "-S", strconv.Itoa(start), "-t", target)
	if err != nil {
		return "", nil
	}
	return out, nil
}

// KillSession destroys the named tmux session.
func KillSession(name string) error {
	_, err := run("kill-session", "-t", name)
	return err
}

// EnsureSession creates the repo-level tmux session with a "dashboard" window
// if it doesn't already exist. Returns true if a new session was created.
func EnsureSession(repo, repoRootDir string) (bool, error) {
	name := SessionName(repo)
	if SessionExists(name) {
		return false, nil
	}
	if _, err := run("new-session", "-d", "-s", name, "-n", "dashboard", "-c", repoRootDir); err != nil {
		return false, err
	}
	return true, nil
}

// WindowExists returns true if the named window exists in the given session.
func WindowExists(session, window string) bool {
	out, err := run("list-windows", "-t", session, "-F", "#{window_name}")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == window {
			return true
		}
	}
	return false
}

// CreateWindow creates a new named window in the given session.
func CreateWindow(session, name, workDir string) error {
	_, err := run("new-window", "-t", session, "-n", name, "-c", workDir)
	return err
}

// KillWindow destroys a named window in the given session.
func KillWindow(session, windowName string) error {
	_, err := run("kill-window", "-t", session+":"+windowName)
	return err
}

// AttachWindow attaches to (or switches to) a specific window in a session.
// select-window first so the target window is active, then switch-client/attach.
func AttachWindow(session, windowName string) error {
	target := session + ":" + windowName

	// Select the window first — switch-client and attach-session only take
	// a target-session, so they ignore the :window suffix.
	if _, err := run("select-window", "-t", target); err != nil {
		return fmt.Errorf("select-window %s: %w", target, err)
	}

	if IsInsideTmux() {
		_, err := run("switch-client", "-t", session)
		return err
	}

	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}

	return syscall.Exec(tmuxPath, []string{"tmux", "attach-session", "-t", session}, os.Environ())
}

// IsInsideTmux returns true if the current process is running inside tmux.
func IsInsideTmux() bool {
	return os.Getenv("TMUX") != ""
}
