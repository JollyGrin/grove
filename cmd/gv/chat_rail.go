package main

// grove-486: the focus-mode chat rail. `gv chat rail --session <s>` is the
// viewer that lives in the short pane under the chat; railTick is the
// cockpit-side writer that feeds it through the session option
// @grove_rail_rows. The viewer never polls chats itself.

import (
	"flag"
	"fmt"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/chatrail"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/workspace"
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
	fmt.Fprint(os.Stdout, "\x1b[?25l") // hide the cursor; the pane is a display
	_ = v.Step()
	for range time.Tick(railViewerTick) {
		_ = v.Step()
	}
	return nil
}

var (
	railWriter = chatrail.Writer{Run: tmux.Run}
	railBeat   int
)

// railWriteEvery spaces the cockpit-side rebuild: the refresh tick is 1s,
// the report behind it (list-panes, ps, transcript stat, pane captures) is
// not free, and a minimized chat's state does not need to be that fresh.
const railWriteEvery = 5

// railTick runs inside the dash's existing refresh command (no goroutine or
// timer of its own). Outside the focus layout, or with no rail pane, it does
// nothing past one cheap layout read.
func railTick(label, session string) {
	railBeat++
	if label == "" || railBeat%railWriteEvery != 1 {
		return
	}
	if tmux.CockpitLayout(session) != "focus" {
		return
	}
	railWriteNow(label, session)
}

// railWriteNow rebuilds the chat report and writes the rail's option at
// once (grove-487: the focus moves rewrite it directly rather than waiting
// for the next beat). No-op without a rail pane.
func railWriteNow(label, session string) {
	_, window, ok := tmux.RailPane(session)
	if !ok {
		return
	}
	list, err := workspace.LoadRegistry()
	if err != nil {
		return
	}
	targets, err := chatWorkspaces(list, label)
	if err != nil {
		return
	}
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return
	}
	recs := chatRecords(targets, liveChatLookup(isCockpit))
	markWaiting(recs, tmux.CapturePane)
	off, shown := railRows(recs, window)
	_, _ = railWriter.Write(session, off, shown, time.Now())
}

// railRows splits a report into the chats off screen (one box each) and the
// number of the chat on screen. Off screen = a detached chat session nobody
// is attached to, or an orchestrator pane living outside the cockpit window
// (hidden by hand, or minimized by focus).
func railRows(recs []chatRecord, cockpitWindow string) (off []chat.Row, shown int) {
	for _, r := range recs {
		switch r.Row.Kind {
		case chat.KindChat:
			if r.Row.Attached {
				shown = r.Row.N
			} else {
				off = append(off, r.Row)
			}
		case chat.KindCockpit:
			if r.Window == cockpitWindow {
				shown = r.Row.N
			} else {
				off = append(off, r.Row)
			}
		}
	}
	return off, shown
}
