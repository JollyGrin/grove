package main

// grove-486: the focus-mode chat rail. `gv chat rail --session <s>` is the
// viewer that lives in the short pane under the chat; railTick is the
// cockpit-side writer that feeds it through the session option
// @grove_rail_rows. The viewer never polls chats itself.

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/JollyGrin/grove/internal/chatrail"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/tui"
)

// railViewerTick is how often the rail re-reads its option when nothing
// wakes it — the heartbeat; a write wakes it at once (railSignal).
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
	// grove-521: the click resolves inside this rail's workspace only.
	label, _, _ := cockpitWorkspace(*session)
	v.Focus = func(target string) error {
		_, err := focusChat(target, label, true)
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
	// grove-521: a writer wakes the rail with SIGUSR1 right after it wrote;
	// the stamp tells it which pid to signal. The tick stays as heartbeat.
	wake := make(chan os.Signal, 1)
	signal.Notify(wake, syscall.SIGUSR1)
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		_ = tmux.StampRailPID(pane, os.Getpid())
	}
	tick := time.NewTicker(railViewerTick)
	defer tick.Stop()
	v.Loop(tick.C, wake, clicks, nil)
	return nil
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
	_, _, pid, ok := tmux.RailPane(session)
	if !ok {
		return
	}
	ws, ok := labelWorkspace(label)
	if !ok {
		return
	}
	rows := cockpitChats(&ws, true, liveCockpitChatLookup())
	if wrote, err := railWriter.Write(session, railChips(rows), time.Now(), force); wrote && err == nil {
		railSignal(pid)
	}
}

// railSignal wakes the rail viewer so it redraws now rather than on its
// next tick (grove-521). pid 0 — no stamp, or a viewer not running as the
// pane's own process — is left to the tick. Best-effort: a viewer that has
// just gone needs no redraw.
func railSignal(pid int) {
	if pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGUSR1)
	}
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
		return tui.ChatLess(chips[i].Name, chips[j].Name)
	})
	return chips
}
