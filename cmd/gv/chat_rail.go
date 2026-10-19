package main

// grove-486: the focus-mode chat rail. `gv chat rail --session <s>` is the
// viewer that lives in the short pane under the chat; railTick is the
// cockpit-side writer that feeds it through the session option
// @grove_rail_rows. The viewer never polls chats itself.

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/JollyGrin/grove/internal/chatrail"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/tui"
)

// railViewerTick is how often the rail re-reads its option.
const railViewerTick = 2 * time.Second

func cmdChatRail(args []string) error {
	fs := flag.NewFlagSet("chat rail", flag.ExitOnError)
	session := fs.String("session", "", "the cockpit tmux session whose rail this draws")
	parseAnywhere(fs, args)
	if *session == "" {
		return fmt.Errorf("usage: gv chat rail --session <cockpit-session>   (drawn by the focus layout; not run by hand)")
	}
	v := &chatrail.Viewer{
		Run: tmux.Run, Session: *session, Out: os.Stdout, Now: time.Now,
		Width: func() int {
			if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
				return w
			}
			return 80
		},
	}
	// A click runs gv's own focus helper in this very process (the rail IS
	// gv). The terminal goes raw so mouse reports arrive unechoed; one
	// reader goroutine feeds the same loop that ticks, so nothing races.
	v.Focus = func(target string) error {
		_, err := focusChat(target, true)
		return err
	}
	clicks := make(chan [2]int, 4)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if old, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
			defer term.Restore(int(os.Stdin.Fd()), old)
			defer fmt.Fprint(os.Stdout, chatrail.MouseOff)
			fmt.Fprint(os.Stdout, chatrail.MouseOn)
			go chatrail.ReadClicks(os.Stdin, func(x, y int) {
				select {
				case clicks <- [2]int{x, y}:
				default:
				}
			})
		}
	}
	fmt.Fprint(os.Stdout, "\x1b[?25l") // hide the cursor; the pane is a display
	_ = v.Step()
	tick := time.NewTicker(railViewerTick)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			_ = v.Step()
		case c := <-clicks:
			_ = v.Click(c[0], c[1])
		}
	}
}

var (
	railWriter = chatrail.Writer{Run: tmux.Run}
	railBeat   int
	railBeatMu sync.Mutex // guards railBeat; TryLock so an overlapping refresh skips its beat
)

// railWriteEvery spaces the cockpit-side rebuild: the refresh tick is 1s,
// the report behind it (list-panes, ps, transcript stat, pane captures) is
// not free, and a minimized chat's state does not need to be that fresh.
const railWriteEvery = 5

// railTick runs inside the dash's existing refresh command (no goroutine or
// timer of its own). Outside the focus layout, or with no rail pane, it does
// nothing past one cheap layout read.
func railTick(label, session string) {
	if !railBeatMu.TryLock() {
		return
	}
	defer railBeatMu.Unlock()
	railBeat++
	if label == "" || railBeat%railWriteEvery != 1 {
		return
	}
	if tmux.CockpitLayout(session) != "focus" {
		return
	}
	if !railWriteMu.TryLock() {
		return // a move is writing the rail right now
	}
	defer railWriteMu.Unlock()
	railWrite(label, session, false)
}

// railWriteMu serializes this process's rail rebuilds: two overlapping
// moves must not let the older report land last (grove-510's churn lag).
var railWriteMu sync.Mutex

// railWriteNow rebuilds the chips and writes the rail's option at once,
// unconditionally — every move calls it (grove-487, grove-510), not only
// the beat. No-op without a rail pane.
func railWriteNow(label, session string) {
	railWriteMu.Lock()
	defer railWriteMu.Unlock()
	railWrite(label, session, true)
}

// railWrite builds the rail from the same rows as the CHATS box (the deep
// pass), so the box and the rail count and name the same chats.
func railWrite(label, session string, force bool) {
	if _, _, ok := tmux.RailPane(session); !ok {
		return
	}
	ws, ok := labelWorkspace(label)
	if !ok {
		return
	}
	rows := cockpitChats(&ws, true, liveCockpitChatLookup())
	_, _ = railWriter.Write(session, railChips(rows), time.Now(), force)
}

// railChips turns the CHATS box's rows into the rail's chips: every chat,
// the on-screen ones marked Shown, in a stable order — by name, cockpit·N
// before chat-N — so a chip holds its place when focus swaps. A remote
// chat has no session to focus here: its chip is not clickable.
func railChips(rows []tui.ChatRow) []chatrail.Chip {
	chips := make([]chatrail.Chip, 0, len(rows))
	for _, r := range rows {
		turn := r.Turn
		if r.Waiting {
			turn = "waiting"
		}
		at := r.LastActive
		if at.IsZero() {
			at = r.Created
		}
		c := chatrail.Chip{Name: tui.ChatName(r), Title: chatrail.Title(r.Title, r.Label), Turn: turn, At: at, Shown: !r.Hidden}
		if r.Host == "" {
			c.Session = r.Session
		}
		chips = append(chips, c)
	}
	sort.SliceStable(chips, func(i, j int) bool {
		ki, ni := chipOrder(chips[i].Name)
		kj, nj := chipOrder(chips[j].Name)
		if ki != kj {
			return ki < kj
		}
		if ni != nj {
			return ni < nj
		}
		return chips[i].Name < chips[j].Name
	})
	return chips
}

// chipOrder ranks a chat name: local cockpit·N, local chat-N, then remote
// (an "@host" suffix), each by number.
func chipOrder(name string) (rank, n int) {
	base, _, remote := strings.Cut(name, " @")
	if num, ok := strings.CutPrefix(base, "cockpit·"); ok {
		n, _ = strconv.Atoi(num)
	} else if num, ok := strings.CutPrefix(base, "chat-"); ok {
		rank = 1
		n, _ = strconv.Atoi(num)
	} else {
		rank = 2
	}
	if remote {
		rank += 3
	}
	return rank, n
}
