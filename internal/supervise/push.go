package supervise

import (
	"github.com/JollyGrin/grove/internal/notify"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/watch"
)

// Push fans one emitted event out to ntfy/desktop per docs/plugins.md's
// table. Shared by `gv supervise` and the cockpit driver (grove-254) so
// the two emitters push identically — one table, one body format. Body
// is the same trailing detail `gv watch` prints, capped the same way
// (watch.Detail) — the two surfaces read the same tail, never two
// independently-truncated strings.
func Push(ev state.Event) {
	priority, tags, desktop := PushClass(ev.Type)
	if priority == "" && !desktop {
		return
	}
	title := ev.Ticket
	if label := watch.Label(ev); label != "" {
		title += " " + label
	}
	body := watch.Detail(ev)
	if desktop {
		notify.Desktop(title, body)
	}
	if priority != "" {
		notify.Push(title, body, priority, tags)
	}
}

// PushClass is the grove-253 notification table: every one of the eleven
// delivery/liveness types maps to exactly one row here — the default case
// covers pr_opened/pr_updated/pr_closed/worker_recovered, which push
// nothing.
func PushClass(evType string) (priority, tags string, desktop bool) {
	switch evType {
	case state.EvWorkerWaiting, state.EvWorkerVanished, state.EvWorkerErrored:
		return "high", "warning", true
	case state.EvPRCIFailed, state.EvPRConflicting:
		return "high", "x", true
	case state.EvPRReady:
		return "default", "white_check_mark", true
	case state.EvPRMerged:
		return "default", "tada", true
	default: // pr_opened, pr_updated, pr_closed, worker_recovered
		return "", "", false
	}
}
