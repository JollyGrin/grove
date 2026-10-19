// Package chatrail is the focus-mode chat rail (grove-486): the pure
// renderer for the short pane beneath the chat, the wire format the cockpit
// writes into the session option @grove_rail_rows, the throttled writer,
// and the viewer loop behind `gv chat rail`. The viewer is a dumb reader —
// one show-options per tick, no chat ls, no gh, no transcript reads.
package chatrail

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"

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

	emptyText = "no chats"
	version   = "v2"
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

// Chip is one chat on the rail (grove-510): every chat of the workspace has
// one, the on-screen chat included (Shown, drawn highlighted).
type Chip struct {
	Name    string    // the chat's stable name — cockpit·1, chat-3 — as the CHATS box says it
	Title   string    // the chip title (Title picks it)
	Turn    string    // running / waiting / idle / errored / stopped / ""
	At      time.Time // last activity; zero = unknown
	Session string    // the focus target; "" = not clickable (a remote chat)
	Shown   bool      // on screen in the cockpit window
}

// Title picks a chip title: Claude's own session title (the pane title,
// already stripped by PaneTitle) wins; else a ticket id found in the
// transcript label; else that label (the first prompt). Never the last
// message.
func Title(paneTitle, label string) string {
	if t := strings.Join(strings.Fields(paneTitle), " "); t != "" {
		return t
	}
	if t := ticketRe.FindString(label); t != "" {
		return t
	}
	return strings.Join(strings.Fields(label), " ")
}

// PaneTitle reads a Claude session title off a raw pane title: Claude Code
// titles its pane "<status glyph> <title>" (✳ when idle, a spinner while
// working). Only a title led by such a glyph is Claude's — anything else is
// tmux's default (the hostname) or a shell's — and Claude's placeholder
// "Claude Code" is no title yet. "" when there is none.
func PaneTitle(raw string) string {
	f := strings.Fields(raw)
	if len(f) < 2 {
		return ""
	}
	for _, r := range f[0] {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return ""
		}
	}
	t := strings.Join(f[1:], " ")
	if t == "Claude Code" {
		return ""
	}
	return t
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

// layout is the one computation behind both the drawing and the click
// hit-test, so the two can never disagree: the chips that get a slot, how
// many chats the "+N more" slot stands for, and the content cells per box.
// Box i spans columns [i*(cw+1), i*(cw+1)+cw); the │ sits right after it.
// Every chip is a candidate — the shown one included — and when they do not
// all fit, the shown chip is never the one folded into "+N more".
func layout(chips []Chip, width int) (boxes []Chip, more, cw int) {
	if len(chips) == 0 {
		return nil, 0, 0
	}
	boxes = chips
	slots := len(boxes)
	if width/slots < MinBox {
		slots = width / MinBox
		if slots < 1 {
			slots = 1
		}
	}
	if slots < len(boxes) {
		keep := slots - 1
		boxes = append([]Chip(nil), chips[:keep]...)
		if keep > 0 {
			for _, c := range chips[keep:] {
				if c.Shown { // the shown chip takes the last slot rather than hide in "+N more"
					boxes[keep-1] = c
					break
				}
			}
		}
		more = len(chips) - keep
	}
	cw = width/slots - 1 // content cells per box; the │ takes one
	if cw < MinBox-1 {
		cw = MinBox - 1
	}
	return boxes, more, cw
}

// Hit maps a 0-based column on the rail to the chat session whose box spans
// it. Separators, the "+N more" slot, columns past the last box, the shown
// chip (clicking it is a no-op) and chips without a session answer ok=false.
func Hit(chips []Chip, width, x int) (session string, ok bool) {
	boxes, _, cw := layout(chips, width)
	if x < 0 || cw == 0 {
		return "", false
	}
	i, off := x/(cw+1), x%(cw+1)
	if off == cw || i >= len(boxes) || boxes[i].Shown || boxes[i].Session == "" {
		return "", false
	}
	return boxes[i].Session, true
}

// Render draws the rail: two lines, one box per chat, separated by │, no
// outer frame. Line one is the turn glyph and the title, line two the
// chat's name (▣ on screen, ○ off it — the CHATS box's glyphs) and age. The shown chat's box is bold. Pure — now is a parameter so the
// goldens are stable. Boxes are width/len wide, never under MinBox: past
// what fits, the last slot reads "+N more".
func Render(chips []Chip, width int, now time.Time) string {
	return render(chips, width, now, "")
}

// render is Render with one box (by session) inverted: the click feedback.
func render(chips []Chip, width int, now time.Time, invert string) string {
	boxes, more, cw := layout(chips, width)
	if len(boxes) == 0 {
		return emptyText
	}
	var l1, l2 []string
	for _, c := range boxes {
		a := strings.TrimRight(Glyph(c.Turn)+" "+c.Title, " ")
		mark := "○ "
		if c.Shown {
			mark = "▣ "
		}
		b := mark + c.Name
		if age := compactAge(c.At, now); age != "" {
			b += " · " + age
		}
		c1, c2 := fit(a, cw), fit(b, cw)
		switch {
		case invert != "" && c.Session == invert:
			c1, c2 = "\x1b[7m"+c1+"\x1b[27m", "\x1b[7m"+c2+"\x1b[27m"
		case c.Shown:
			c1, c2 = "\x1b[1m"+c1+"\x1b[22m", "\x1b[1m"+c2+"\x1b[22m"
		}
		l1 = append(l1, c1)
		l2 = append(l2, c2)
	}
	if more > 0 {
		l1 = append(l1, fit(fmt.Sprintf("+%d more", more), cw))
		l2 = append(l2, fit("", cw))
	}
	return strings.TrimRight(strings.Join(l1, "│"), " ") + "\n" + strings.TrimRight(strings.Join(l2, "│"), " ")
}

// --- wire format: "v2\t<unix ts>" then "<shown 0|1>\t<turn>\t<last unix>\t<name>\t<session>\t<title>" per chip ---

func clean(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)), " ")
}

// Encode renders the payload for the chips, in rail order.
func Encode(chips []Chip, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\t%d", version, now.Unix())
	for _, c := range chips {
		shown := 0
		if c.Shown {
			shown = 1
		}
		var at int64
		if !c.At.IsZero() {
			at = c.At.Unix()
		}
		fmt.Fprintf(&b, "\n%d\t%s\t%d\t%s\t%s\t%s", shown, clean(c.Turn), at, clean(c.Name), clean(c.Session), clean(c.Title))
	}
	return b.String()
}

// Parse is Encode's inverse. ok=false on anything it does not recognise —
// a payload of another version included (a rail drawn by an older binary).
func Parse(s string) (chips []Chip, wrote time.Time, ok bool) {
	lines := strings.Split(s, "\n")
	head := strings.Split(lines[0], "\t")
	if len(head) < 2 || head[0] != version {
		return nil, time.Time{}, false
	}
	ts, err := strconv.ParseInt(head[1], 10, 64)
	if err != nil {
		return nil, time.Time{}, false
	}
	for _, line := range lines[1:] {
		f := strings.SplitN(line, "\t", 6)
		if len(f) < 5 {
			continue
		}
		at, _ := strconv.ParseInt(f[2], 10, 64)
		c := Chip{Shown: f[0] == "1", Turn: f[1], Name: f[3], Session: f[4]}
		if len(f) > 5 {
			c.Title = f[5]
		}
		if at > 0 {
			c.At = time.Unix(at, 0)
		}
		chips = append(chips, c)
	}
	return chips, time.Unix(ts, 0), true
}

// Runner is the tmux exec seam (tmux.Run in production).
type Runner func(args ...string) (string, error)

// Writer is the cockpit side: on the beat it writes the payload only when
// it changed (plus a slow heartbeat), so identical ticks cost nothing; a
// focus move forces the write (grove-510) — another process may have
// written the option since this Writer last did, so "unchanged since my
// last write" proves nothing then.
type Writer struct {
	Run   Runner
	mu    sync.Mutex // Write is called from overlapping refresh and focus commands
	last  string
	lastT time.Time
}

// Write stores the chips for the session; reports whether it touched tmux.
// force skips the change detection.
func (w *Writer) Write(session string, chips []Chip, now time.Time, force bool) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	body := Encode(chips, time.Time{})
	// Compare without the timestamp: change detection ignores the clock.
	if !force && body == w.last && now.Sub(w.lastT) < Heartbeat {
		return false, nil
	}
	_, err := w.Run("set-option", "-t", tmux.ExactActive(session), tmux.RailRowsOption, Encode(chips, now))
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
	// Focus is what a click on a box runs: the gv binary's own focus helper
	// (injected by cmd/gv; never a child process). Nil = clicks do nothing.
	Focus func(session string) error
	prev  string
	chips []Chip // what the picture on screen was drawn from
}

// read is the single tmux call of a tick (or a click's freshness check).
func (v *Viewer) read() (chips []Chip, wrote time.Time, ok bool, err error) {
	out, err := v.Run("show-options", "-t", tmux.ExactActive(v.Session), "-v", tmux.RailRowsOption)
	chips, wrote, ok = Parse(out)
	return chips, wrote, ok && err == nil, err
}

// Step is one tick: a single show-options, then a redraw if the picture
// changed. A failed read keeps the last picture (the cockpit may be gone).
func (v *Viewer) Step() error {
	chips, wrote, ok, err := v.read()
	now := v.Now()
	var text string
	if ok {
		text = Render(chips, v.Width(), now)
		if now.Sub(wrote) > StaleAfter {
			text = markStale(text)
		}
		v.chips = chips
	} else if v.prev == "" {
		text = emptyText
	} else {
		return err
	}
	return v.draw(text)
}

func (v *Viewer) draw(text string) error {
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

// Click handles a left click at the 0-based column x, line y (0 or 1 are the
// two box lines). A click on a box inverts it for the tick, then runs Focus
// with that chat's session; the next Step redraws. A separator, the "+N more"
// slot, the shown chat's own box or empty space does nothing. A box whose chat has gone since it was
// drawn (the fresh option no longer lists it) flashes "gone" for one tick and
// mutates nothing — the only tmux call made first is a read.
func (v *Viewer) Click(x, y int) error {
	if v.Focus == nil || y < 0 || y > 1 {
		return nil
	}
	session, ok := Hit(v.chips, v.Width(), x)
	if !ok {
		return nil
	}
	fresh, _, ok, _ := v.read()
	live := false
	for _, c := range fresh {
		if c.Session == session && !c.Shown {
			live = true
		}
	}
	if !ok || !live {
		return v.draw(markGone(v.prev))
	}
	plain := v.prev
	_ = v.draw(render(v.chips, v.Width(), v.Now(), session))
	if err := v.Focus(session); err != nil {
		v.prev = ""
		return v.draw(markGone(plain))
	}
	v.prev = "" // the swap changes what the rail should show: force a redraw
	return v.Step()
}

func markGone(text string) string {
	lines := strings.Split(text, "\n")
	last := len(lines) - 1
	lines[last] = strings.TrimRight(lines[last], " ") + " · gone"
	return strings.Join(lines, "\n")
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

// MouseOn / MouseOff switch the terminal's button reporting (SGR encoding, so
// columns past 223 survive). Only press/release are requested — no motion.
const (
	MouseOn  = "\x1b[?1000h\x1b[?1006h"
	MouseOff = "\x1b[?1006l\x1b[?1000l"
)

// ReadClicks parses SGR mouse reports (ESC [ < b ; x ; y M|m) from r and
// calls click(x, y) — 0-based — for every plain left-button RELEASE. Every
// other byte (keys, wheel, other buttons, modified clicks, presses) is
// ignored: the rail takes no keyboard. Returns when r ends.
func ReadClicks(r io.Reader, click func(x, y int)) {
	br := bufio.NewReader(r)
	for {
		c, err := br.ReadByte()
		if err != nil {
			return
		}
		if c != 0x1b {
			continue
		}
		if c, err = br.ReadByte(); err != nil {
			return
		} else if c != '[' {
			_ = br.UnreadByte() // may itself be the ESC of the next report
			continue
		}
		if c, err = br.ReadByte(); err != nil {
			return
		} else if c != '<' {
			_ = br.UnreadByte()
			continue
		}
		var nums [3]int
		i, digits := 0, false
		for {
			c, err = br.ReadByte()
			if err != nil {
				return
			}
			if c >= '0' && c <= '9' {
				nums[i] = nums[i]*10 + int(c-'0')
				digits = true
				continue
			}
			if (c == ';') && digits && i < 2 {
				i++
				digits = false
				continue
			}
			break
		}
		if (c == 'm') && i == 2 && digits && nums[0] == 0 {
			click(nums[1]-1, nums[2]-1)
		}
	}
}
