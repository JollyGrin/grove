package tui

import (
	"time"

	"github.com/JollyGrin/grove/internal/state"
)

// feedItem is one rendered ACTIVITY row: the objective, complete record of
// what the swarm did — including while you were away (cockpit design §3).
type feedItem struct {
	Time   time.Time
	Ticket string
	Glyph  string
	Text   string
}

// feedItems maps raw events (oldest-first) to curated feed rows,
// newest-first. Poll noise (plain idle stops, session starts) is excluded;
// the allowlist is grabbed · adopted · question · blocked · reports done ·
// answered · needs-input · session died · done · untracked.
func feedItems(events []state.Event) []feedItem {
	var out []feedItem
	for _, ev := range events {
		var it feedItem
		switch ev.Type {
		case state.EvTaskCreated:
			it = feedItem{Glyph: "●", Text: "grabbed — worktree up"}
		case state.EvTaskAdopted:
			it = feedItem{Glyph: "●", Text: "adopted"}
		case state.EvTaskDone:
			it = feedItem{Glyph: "⬢", Text: "done — cleaned up"}
		case state.EvTaskUntracked:
			it = feedItem{Glyph: "○", Text: "untracked"}
		case state.EvAnswered:
			it = feedItem{Glyph: "↩", Text: "answered"}
		case state.EvNotification:
			it = feedItem{Glyph: "◆", Text: "needs input: " + firstLine(ev.Data["message"])}
		case state.EvSessionEnded:
			it = feedItem{Glyph: "✝", Text: "session ended"}
		case state.EvAgentStatus:
			switch ev.Data["sentinel"] {
			case "question":
				it = feedItem{Glyph: "◆", Text: "asked: " + firstLine(ev.Data["question"])}
			case "blocked":
				it = feedItem{Glyph: "⚠", Text: "blocked: " + firstLine(ev.Data["question"])}
			case "done":
				it = feedItem{Glyph: "✓", Text: "reports done"}
			default:
				continue // plain idle stop — poll noise
			}
		default:
			continue
		}
		it.Time, it.Ticket = ev.Time, ev.Ticket
		out = append(out, it)
	}
	// newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
