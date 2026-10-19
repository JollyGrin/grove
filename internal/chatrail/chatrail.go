// Package chatrail is the focus-mode chat rail (grove-486): the pure
// renderer for the short pane beneath the chat, the wire format the cockpit
// writes into the session option @grove_rail_rows, the throttled writer,
// and the viewer loop behind `gv chat rail`. The viewer is a dumb reader —
// one show-options per tick, no chat ls, no gh, no transcript reads.
package chatrail

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/tmux"
)

const (
	// MinBox is the narrowest a box may be, in cells.
	MinBox = 16
	// StaleAfter is how old the cockpit's last write may be before the rail
	// marks its content stale.
	StaleAfter = 30 * time.Second
	// Heartbeat is how often the writer refreshes an unchanged payload, so
	// the viewer can tell a quiet cockpit from a dead one.
	Heartbeat = 10 * time.Second

	emptyText = "no minimized chats"
	version   = "v1"
)

// Glyph maps a row's Turn to its state glyph.
func Glyph(turn string) string {
	switch turn {
	case "running":
		return "●"
	case "waiting":
		return "?"
	case "idle":
		return "✓"
	case "errored":
		return "!"
	}
	return "·"
}

var ticketRe = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9]*-[0-9]+\b`)

// Name is the box title: a ticket id found in the label wins over the label.
func Name(r chat.Row) string {
	if t := ticketRe.FindString(r.Label); t != "" {
		return t
	}
	return strings.Join(strings.Fields(r.Label), " ")
}

func cells(s string) int { return lipgloss.Width(s) }

// fit truncates s to w cells (rune-safe, ellipsis) and pads it to exactly w.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if cells(s) > w {
		var b strings.Builder
		used := 0
		for _, r := range s {
			rw := cells(string(r))
			if used+rw > w-1 {
				break
			}
			b.WriteRune(r)
			used += rw
		}
		b.WriteString("…")
		s = b.String()
	}
	if pad := w - cells(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

func compactAge(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	d := now.Sub(at)
	switch {
	case d < 90*time.Second:
		return "now"
	case d < 90*time.Minute:
		return fmt.Sprintf("%dm", int(d.Round(time.Minute)/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Round(time.Hour)/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d.Round(24*time.Hour)/(24*time.Hour)))
}

// Render draws the rail: two lines, one box per chat, separated by │, no
// outer frame. Pure — now is a parameter so the goldens are stable. Rows are
// the chats NOT on screen; shown (the on-screen chat's number) is skipped if
// it slipped in. Boxes are width/len wide, never under MinBox: past what
// fits, the last slot reads "+N more".
func Render(rows []chat.Row, shown, width int, now time.Time) string {
	var boxes []chat.Row
	for _, r := range rows {
		if shown == 0 || r.N != shown {
			boxes = append(boxes, r)
		}
	}
	if len(boxes) == 0 {
		return emptyText
	}
	slots := len(boxes)
	if width/slots < MinBox {
		slots = width / MinBox
		if slots < 1 {
			slots = 1
		}
	}
	more := 0
	if slots < len(boxes) {
		more = len(boxes) - (slots - 1)
		boxes = boxes[:slots-1]
	}
	cw := width/slots - 1 // content cells per box; the │ takes one
	if cw < MinBox-1 {
		cw = MinBox - 1
	}
	var l1, l2 []string
	for _, r := range boxes {
		a := fmt.Sprintf("%s %d %s", Glyph(r.Turn), r.N, Name(r))
		turn := r.Turn
		if turn == "" {
			turn = "unknown"
		}
		b := turn
		if age := compactAge(r.Activity(), now); age != "" {
			b += " · " + age
		}
		l1 = append(l1, fit(strings.TrimRight(a, " "), cw))
		l2 = append(l2, fit(b, cw))
	}
	if more > 0 {
		l1 = append(l1, fit(fmt.Sprintf("+%d more", more), cw))
		l2 = append(l2, fit("", cw))
	}
	return strings.TrimRight(strings.Join(l1, "│"), " ") + "\n" + strings.TrimRight(strings.Join(l2, "│"), " ")
}

// --- wire format: "v1\t<unix ts>\t<shown>" then "<n>\t<turn>\t<last unix>\t<name>" per box ---

func clean(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)), " ")
}

// Encode renders the payload for the given off-screen rows (name = Name(row)
// is stored, so the viewer never needs the label/ticket distinction).
func Encode(rows []chat.Row, shown int, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\t%d\t%d", version, now.Unix(), shown)
	for _, r := range rows {
		b.WriteString("\n")
		fmt.Fprintf(&b, "%d\t%s\t%d\t%s", r.N, clean(r.Turn), r.Activity().Unix(), clean(Name(r)))
	}
	return b.String()
}

// Parse is Encode's inverse. ok=false on anything it does not recognise.
func Parse(s string) (rows []chat.Row, shown int, wrote time.Time, ok bool) {
	lines := strings.Split(s, "\n")
	head := strings.Split(lines[0], "\t")
	if len(head) < 3 || head[0] != version {
		return nil, 0, time.Time{}, false
	}
	ts, err := strconv.ParseInt(head[1], 10, 64)
	if err != nil {
		return nil, 0, time.Time{}, false
	}
	shown, _ = strconv.Atoi(head[2])
	for _, line := range lines[1:] {
		f := strings.SplitN(line, "\t", 4)
		if len(f) < 4 {
			continue
		}
		n, _ := strconv.Atoi(f[0])
		at, _ := strconv.ParseInt(f[2], 10, 64)
		r := chat.Row{N: n, Turn: f[1], Label: f[3]}
		if at > 0 {
			r.LastActive = time.Unix(at, 0)
		}
		rows = append(rows, r)
	}
	return rows, shown, time.Unix(ts, 0), true
}

// Runner is the tmux exec seam (tmux.Run in production).
type Runner func(args ...string) (string, error)

// Writer is the cockpit side: it writes the payload only when it changed
// (plus a slow heartbeat), so identical ticks cost nothing.
type Writer struct {
	Run   Runner
	last  string
	lastT time.Time
}

// Write stores rows for the session; reports whether it touched tmux.
func (w *Writer) Write(session string, rows []chat.Row, shown int, now time.Time) (bool, error) {
	body := Encode(rows, shown, time.Time{})
	// Compare without the timestamp: change detection ignores the clock.
	if body == w.last && now.Sub(w.lastT) < Heartbeat {
		return false, nil
	}
	_, err := w.Run("set-option", "-t", tmux.ExactActive(session), tmux.RailRowsOption, Encode(rows, shown, now))
	if err != nil {
		return false, err
	}
	w.last, w.lastT = body, now
	return true, nil
}

// Viewer is the rail process: read, render, redraw only on change.
type Viewer struct {
	Run     Runner
	Session string
	Out     io.Writer
	Width   func() int
	Now     func() time.Time
	prev    string
}

// Step is one tick: a single show-options, then a redraw if the picture
// changed. A failed read keeps the last picture (the cockpit may be gone).
func (v *Viewer) Step() error {
	out, err := v.Run("show-options", "-t", tmux.ExactActive(v.Session), "-v", tmux.RailRowsOption)
	now := v.Now()
	var text string
	if rows, shown, wrote, ok := Parse(out); err == nil && ok {
		text = Render(rows, shown, v.Width(), now)
		if now.Sub(wrote) > StaleAfter {
			text = markStale(text)
		}
	} else if v.prev == "" {
		text = emptyText
	} else {
		return err
	}
	if text == v.prev {
		return nil
	}
	v.prev = text
	var b strings.Builder
	b.WriteString("\x1b[H")
	for _, l := range strings.Split(text, "\n") {
		b.WriteString(l + "\x1b[K\r\n")
	}
	b.WriteString("\x1b[J")
	_, werr := io.WriteString(v.Out, b.String())
	return werr
}

func markStale(text string) string {
	lines := strings.Split(text, "\n")
	last := len(lines) - 1
	if last == 0 {
		return text + " · stale"
	}
	lines[last] = strings.TrimRight(lines[last], " ") + " · stale"
	return strings.Join(lines, "\n")
}
