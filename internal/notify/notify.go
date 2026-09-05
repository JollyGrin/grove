// Package notify sends outbound pushes: a desktop notification via
// terminal-notifier, and a phone push via ntfy. Moved out of
// internal/hooks (grove-253) so `gv supervise` can push the exact same way
// the Stop/Notification hooks always have, without importing the hook
// receiver — a pure move, byte-identical behavior.
package notify

import (
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/config"
)

// Settings resolves the push target; only consulted for push-worthy
// classes, and overridable in tests so they can never reach a real topic.
var Settings = config.NotifySettings

// client bounds how long a push may delay the caller: an ntfy outage costs
// at most 1.5s, and only on notification-worthy events.
var client = &http.Client{Timeout: 1500 * time.Millisecond}

// Desktop pings via terminal-notifier, best-effort. Fired from the hook
// itself so it works with the TUI closed.
func Desktop(title, body string) {
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

// Push sends a phone push, best-effort. Synchronous by necessity — a
// goroutine can't outlive a short-lived hook process — and silent on
// every failure.
func Push(title, body, priority, tags string) {
	n := Settings()
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
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}
