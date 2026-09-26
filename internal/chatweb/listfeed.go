package chatweb

// grove-307: the list screens' live feed.
//
// Before this, every phone on a list screen polled GET /api/chats every 5s
// (grove-258), and every poll re-ran the whole `gv chat ls` enumeration —
// tmux panes, a transcript stat per chat, a pane capture per running chat
// for `waiting` (grove-302) — once per tick PER PHONE. The feed moves that
// poll to the server: ONE enumeration per ListPoll, shared by every
// connected client, and a payload goes out only when its bytes changed.
//
// It runs only while somebody is subscribed. The first stream starts it,
// the last one leaving stops it, so a server nobody is looking at costs
// nothing — the same "off unless used" posture as the server itself.

import (
	"bytes"
	"sync"
	"time"
)

// ListPoll is how often the feed re-reads the chat list while at least one
// stream is open — the old client beat, now paid once per server.
const ListPoll = 5 * time.Second

// ListHeartbeat is the list stream's keep-alive. The list can sit unchanged
// for hours, and a stream with nothing on it is what a proxy (tailscale
// serve) or the phone's radio reaps as dead.
const ListHeartbeat = 25 * time.Second

type listFeed struct {
	// load produces the full `chats` envelope — the same bytes GET
	// /api/chats serves, minus its trailing newline.
	load  func() ([]byte, error)
	every time.Duration

	mu   sync.Mutex
	subs map[chan []byte]struct{}
	last []byte        // the latest payload sent; nil until the first read lands
	stop chan struct{} // non-nil while the poller runs
}

// subscribe joins the feed. The channel holds at most one payload and a
// newer one replaces an unread older one — a slow phone only ever needs
// the latest list, never the history of lists. A subscriber that joins a
// running feed gets its current payload at once; the first one waits for
// the enumeration it just started.
func (f *listFeed) subscribe() (<-chan []byte, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan []byte, 1)
	if f.subs == nil {
		f.subs = map[chan []byte]struct{}{}
	}
	f.subs[ch] = struct{}{}
	if f.last != nil {
		ch <- f.last
	}
	if f.stop == nil {
		f.stop = make(chan struct{})
		go f.run(f.stop)
	}
	return ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.subs, ch)
		if len(f.subs) == 0 && f.stop != nil {
			close(f.stop)
			// A restart must not hand its first subscriber a list from
			// whenever the feed last ran.
			f.stop, f.last = nil, nil
		}
	}
}

func (f *listFeed) run(stop chan struct{}) {
	t := time.NewTicker(f.every)
	defer t.Stop()
	for {
		f.tick(stop)
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

// tick is one enumeration. A failed read sends nothing and keeps the last
// payload: the phone keeps the list it has (a blank list is a worse lie
// than a stale one) and the next tick tries again.
func (f *listFeed) tick(stop chan struct{}) {
	payload, err := f.load()
	if err != nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-stop:
		return // this poller was stopped mid-read; a newer one owns the feed
	default:
	}
	if bytes.Equal(payload, f.last) {
		return
	}
	f.last = payload
	for ch := range f.subs {
		select {
		case <-ch:
		default:
		}
		ch <- payload
	}
}
