package chatweb_test

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/chatweb"
)

// sseEvent is one parsed frame; a keep-alive comment is event ":".
type sseEvent struct{ name, data string }

// openList connects to the list stream and returns its frames as they land.
func openList(t *testing.T, srv *httptest.Server) <-chan sseEvent {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/api/chats/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("list stream: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	t.Cleanup(func() { resp.Body.Close() })
	out := make(chan sseEvent, 64)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.name != "" {
					out <- ev
				}
				ev = sseEvent{}
			case line == ":":
				ev.name = ":"
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return out
}

// next is the next frame that is not a keep-alive.
func next(t *testing.T, evs <-chan sseEvent, within time.Duration) (sseEvent, bool) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ev, ok := <-evs:
			if !ok {
				t.Fatal("list stream closed")
			}
			if ev.name != ":" {
				return ev, true
			}
		case <-deadline:
			return sseEvent{}, false
		}
	}
}

func TestListStreamEmitsOnConnectThenOnlyOnChange(t *testing.T) {
	b := &fakeBackend{rows: []chat.Row{liveRow()}}
	h := chatweb.NewServer(b)
	chatweb.SetListFeed(h, 20*time.Millisecond, time.Hour)
	srv := httptest.NewServer(h)
	// Cleanup, not defer: LIFO after openList's body close, or Close
	// waits forever on a stream that is still open.
	t.Cleanup(srv.Close)

	evs := openList(t, srv)
	first, ok := next(t, evs, 2*time.Second)
	if !ok || first.name != "chats" {
		t.Fatalf("first event = %+v, want chats", first)
	}
	// The envelope is GET /api/chats, byte for byte.
	want := strings.TrimSuffix(get(t, h, "/api/chats").Body.String(), "\n")
	if first.data != want {
		t.Fatalf("stream payload differs from /api/chats:\n got %s\nwant %s", first.data, want)
	}
	// Many ticks of an unchanged list: nothing.
	if ev, ok := next(t, evs, 200*time.Millisecond); ok {
		t.Fatalf("unchanged list emitted %+v", ev)
	}
	// A change goes out on the next tick.
	row := liveRow()
	row.Waiting = true
	b.setRows([]chat.Row{row})
	ev, ok := next(t, evs, 2*time.Second)
	if !ok || ev.name != "chats" || !strings.Contains(ev.data, `"waiting":true`) {
		t.Fatalf("change not pushed: %+v", ev)
	}
}

func TestListStreamSharesOneEnumeration(t *testing.T) {
	b := &fakeBackend{rows: []chat.Row{liveRow()}}
	h := chatweb.NewServer(b)
	chatweb.SetListFeed(h, 50*time.Millisecond, time.Hour)
	srv := httptest.NewServer(h)
	// Cleanup, not defer: LIFO after openList's body close, or Close
	// waits forever on a stream that is still open.
	t.Cleanup(srv.Close)

	var streams []<-chan sseEvent
	for range 4 {
		evs := openList(t, srv)
		if _, ok := next(t, evs, 2*time.Second); !ok {
			t.Fatal("no initial payload")
		}
		streams = append(streams, evs)
	}
	b.mu.Lock()
	b.chatsCalls = 0
	b.mu.Unlock()
	time.Sleep(500 * time.Millisecond)
	b.mu.Lock()
	calls := b.chatsCalls
	b.mu.Unlock()
	// ~10 ticks in 500ms for ONE feed; four per-client pollers would be ~40.
	if calls < 3 || calls > 14 {
		t.Fatalf("%d enumerations in 500ms across 4 streams, want one feed's worth (~10)", calls)
	}
	// Every stream sees the same change.
	b.setRows(nil)
	for i, evs := range streams {
		ev, ok := next(t, evs, 2*time.Second)
		if !ok || ev.data != `{"chats":[],"schema_version":`+schemaVersion(t, h)+`}` {
			t.Fatalf("stream %d: %+v", i, ev)
		}
	}
}

func TestListStreamStopsPollingWhenNobodyListens(t *testing.T) {
	b := &fakeBackend{rows: []chat.Row{liveRow()}}
	h := chatweb.NewServer(b)
	chatweb.SetListFeed(h, 20*time.Millisecond, time.Hour)
	srv := httptest.NewServer(h)
	// Cleanup, not defer: LIFO after openList's body close, or Close
	// waits forever on a stream that is still open.
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest("GET", srv.URL+"/api/chats/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	_, _ = resp.Body.Read(buf) // the initial payload
	resp.Body.Close()
	time.Sleep(150 * time.Millisecond) // let the handler see the hang-up
	b.mu.Lock()
	b.chatsCalls = 0
	b.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.chatsCalls != 0 {
		t.Fatalf("feed kept enumerating with no stream open: %d calls", b.chatsCalls)
	}
}

func TestListStreamKeepAlive(t *testing.T) {
	b := &fakeBackend{rows: []chat.Row{liveRow()}}
	h := chatweb.NewServer(b)
	chatweb.SetListFeed(h, time.Hour, 30*time.Millisecond)
	srv := httptest.NewServer(h)
	// Cleanup, not defer: LIFO after openList's body close, or Close
	// waits forever on a stream that is still open.
	t.Cleanup(srv.Close)

	evs := openList(t, srv)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-evs:
			if ev.name == ":" {
				return
			}
		case <-deadline:
			t.Fatal("no keep-alive comment on an idle list stream")
		}
	}
}

func TestListStreamIsGETOnly(t *testing.T) {
	w := post(t, chatweb.NewServer(&fakeBackend{}), "/api/chats/events", `{}`)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/chats/events = %d, want 405", w.Code)
	}
}

// schemaVersion reads the envelope's version off GET /api/chats, so the
// literal above does not pin the schema number.
func schemaVersion(t *testing.T, h http.Handler) string {
	t.Helper()
	body := get(t, h, "/api/chats").Body.String()
	i := strings.Index(body, `"schema_version":`)
	j := strings.LastIndex(body, "}")
	return body[i+len(`"schema_version":`) : j]
}
