package chatweb_test

// Every handler through httptest, with NO tmux server, no registry and no
// transcripts: the backend is a fake, so what these tests pin down is the
// server's own behavior — the route table, the write gate, the SSE framing,
// and the refusals — rather than grove's.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/chatweb"
)

// fakeBackend records what it was asked and answers what it was told to.
type fakeBackend struct {
	mu       sync.Mutex
	rows     []chat.Row
	chatsErr error

	lines    []string // JSONL the tail emits, one per element
	tailErr  error
	tailHold bool // keep --follow open until the request context ends

	sendErr, keysErr, spawnErr error
	picker                     chatweb.Picker
	newSession                 string
	profiles                   []string
	profilesErr                error

	sentTo, sentText string
	keyTo, keyLit    string
	spawned, resumed string
	spawnProfile     string
	tailTarget       string
	tailSince        int
	tailFollow       bool
}

func (f *fakeBackend) Chats() ([]chat.Row, error) { return f.rows, f.chatsErr }

func (f *fakeBackend) Tail(ctx context.Context, target string, since int, follow bool, w io.Writer) error {
	f.mu.Lock()
	f.tailTarget, f.tailSince, f.tailFollow = target, since, follow
	f.mu.Unlock()
	if f.tailErr != nil {
		return f.tailErr
	}
	for _, l := range f.lines {
		if _, err := io.WriteString(w, l+"\n"); err != nil {
			return err
		}
	}
	if follow && f.tailHold {
		<-ctx.Done()
	}
	return nil
}

func (f *fakeBackend) Send(target, text string) error {
	f.sentTo, f.sentText = target, text
	return f.sendErr
}

func (f *fakeBackend) Keys(target, literal string) error {
	f.keyTo, f.keyLit = target, literal
	return f.keysErr
}

func (f *fakeBackend) Picker(string) chatweb.Picker { return f.picker }

func (f *fakeBackend) NewChat(label, profile string) (string, error) {
	f.spawned, f.spawnProfile = label, profile
	return f.newSession, f.spawnErr
}

func (f *fakeBackend) Profiles() ([]string, error) { return f.profiles, f.profilesErr }

func (f *fakeBackend) Resume(target string) (string, error) {
	f.resumed = target
	return f.newSession, f.spawnErr
}

func id(s string) *string { return &s }

func liveRow() chat.Row {
	return chat.Row{
		Session: "grove-chat-unbrewed-1", Workspace: "unbrewed", N: 1,
		Kind: chat.KindChat, SessionID: id("eeeb1234"), Label: "triage the backlog",
		Command: "claude", Busy: true, Writable: true,
	}
}

func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

// --- the index and its assets ---

func TestServesEmbeddedUI(t *testing.T) {
	h := chatweb.NewServer(&fakeBackend{})
	for _, path := range []string{"/", "/index.html", "/app.js", "/marked.min.js", "/sw.js"} {
		w := get(t, h, path)
		if w.Code != 200 {
			t.Errorf("GET %s = %d, want 200", path, w.Code)
		}
		if w.Body.Len() == 0 {
			t.Errorf("GET %s served nothing", path)
		}
	}
	// The page's markdown rendering is safe because of this header, not
	// because marked sanitizes. Losing it is a security regression.
	if csp := get(t, h, "/").Header().Get("Content-Security-Policy"); csp != chatweb.ContentSecurityPolicy {
		t.Errorf("the shell must carry the CSP, got %q", csp)
	}
	// No SPA catch-all: an unknown path must 404 rather than return HTML a
	// client would then try to parse as JSON.
	if w := get(t, h, "/nope"); w.Code != 404 {
		t.Errorf("GET /nope = %d, want 404", w.Code)
	}
}

// The vendored file is pinned, and this is where a silent swap gets caught.
func TestVendoredMarkedIsPinned(t *testing.T) {
	body := get(t, chatweb.NewServer(&fakeBackend{}), "/marked.min.js").Body.String()
	if !strings.Contains(body, "marked v12.0.2") {
		t.Fatalf("marked.min.js must stay pinned at v12.0.2 (bumping it is a deliberate commit that re-records the SHA); got head: %.80q", body)
	}
}

// --- /api/chats ---

func TestChatsIsTheContractPayload(t *testing.T) {
	b := &fakeBackend{rows: []chat.Row{liveRow()}}
	w := get(t, chatweb.NewServer(b), "/api/chats")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var out struct {
		SchemaVersion int        `json:"schema_version"`
		Chats         []chat.Row `json:"chats"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("not the `gv chat ls --json` envelope: %v\n%s", err, w.Body)
	}
	if out.SchemaVersion == 0 || len(out.Chats) != 1 || out.Chats[0].Session != "grove-chat-unbrewed-1" {
		t.Fatalf("payload lost the rows: %s", w.Body)
	}
	if !out.Chats[0].Writable {
		t.Error("writable must survive the round trip — the UI disables its input box off exactly this field")
	}
}

func TestChatsEmptyIsAnArrayNotNull(t *testing.T) {
	w := get(t, chatweb.NewServer(&fakeBackend{}), "/api/chats")
	if !strings.Contains(w.Body.String(), `"chats":[]`) && !strings.Contains(w.Body.String(), `"chats": []`) {
		t.Errorf("no chats must serialize as [], not null (a client does .forEach on it): %s", w.Body)
	}
}

// grove-222 made `session_id: null` a real, permanent state rather than a
// boot transient: grove mints the id at spawn, so a null means it could not
// identify the conversation without guessing — and it refuses to guess. The
// row must still round-trip, and must still be ADDRESSABLE, because its
// tmux session name is untouched by any of that.
func TestNullSessionIDRoundTripsAndStaysAddressable(t *testing.T) {
	row := liveRow()
	row.SessionID, row.Label = nil, ""
	b := &fakeBackend{rows: []chat.Row{row}}
	h := chatweb.NewServer(b)

	body := get(t, h, "/api/chats").Body.String()
	if !strings.Contains(body, `"session_id":null`) {
		t.Fatalf(`an unidentified chat must serialize as "session_id":null, never "" — a client must read it as absent: %s`, body)
	}
	// Addressed by session name, so send still reaches it. Only READING
	// needs an id; the pane does not.
	if w := post(t, h, "/api/chats/grove-chat-unbrewed-1/send", `{"text":"hi"}`); w.Code != 200 {
		t.Fatalf("a chat with no session id must still take input (its pane is real): %d %s", w.Code, w.Body)
	}
	if b.sentTo != "grove-chat-unbrewed-1" {
		t.Fatalf("send reached %q", b.sentTo)
	}
	// (Reviving one is refused too, but by the backend — chatBackend.Resume
	// has no id to name a conversation with — so it is not this layer's
	// assertion to make.)
}

func TestChatsError(t *testing.T) {
	b := &fakeBackend{chatsErr: fmt.Errorf("cannot read the workspace registry")}
	w := get(t, chatweb.NewServer(b), "/api/chats")
	if w.Code != 500 || !strings.Contains(w.Body.String(), "workspace registry") {
		t.Errorf("the CLI's own error must reach the client verbatim: %d %s", w.Code, w.Body)
	}
}

// --- the write gate ---

func TestWritesRequireJSONContentType(t *testing.T) {
	b := &fakeBackend{}
	h := chatweb.NewServer(b)
	// text/plain is one of the three types a cross-origin request can send
	// with no preflight. Refusing it is what stops a page in another tab
	// from typing into the operator's agent.
	r := httptest.NewRequest("POST", "/api/chats/grove-chat-unbrewed-1/send", strings.NewReader(`{"text":"hi"}`))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d, want 415", w.Code)
	}
	if b.sentTo != "" {
		t.Fatalf("the refused request still reached the agent (%q) — the gate must run BEFORE the backend", b.sentTo)
	}
}

func TestMethodIsPinnedPerRoute(t *testing.T) {
	h := chatweb.NewServer(&fakeBackend{})
	// A read route must not take a POST, and a write route must not be
	// reachable by a GET — a <img src> or a link must never spawn a chat.
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/chats"},
		{"GET", "/api/chats/grove-chat-unbrewed-1/send"},
		{"GET", "/api/workspaces/unbrewed/new"},
		{"DELETE", "/api/chats/grove-chat-unbrewed-1/keys"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", c.method, c.path, w.Code)
		}
	}
}

func TestNoCORSHeaders(t *testing.T) {
	// The Content-Type gate leans on this server answering no preflight and
	// granting no origin. If either ever appears, the gate is decorative.
	h := chatweb.NewServer(&fakeBackend{})
	for _, path := range []string{"/", "/api/chats"} {
		if v := get(t, h, path).Header().Get("Access-Control-Allow-Origin"); v != "" {
			t.Errorf("%s sent Access-Control-Allow-Origin: %q", path, v)
		}
	}
}

// --- send ---

func TestSend(t *testing.T) {
	b := &fakeBackend{}
	w := post(t, chatweb.NewServer(b), "/api/chats/grove-chat-unbrewed-1/send", `{"text":"ship the notes"}`)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	// Addressed by tmux SESSION NAME, which is what the UI sends: that name
	// comes straight from tmux, while an id routes through the pane join.
	if b.sentTo != "grove-chat-unbrewed-1" || b.sentText != "ship the notes" {
		t.Fatalf("send reached %q with %q", b.sentTo, b.sentText)
	}
}

func TestSendRefusalIsTheCLIsOwnWords(t *testing.T) {
	// A non-writable chat's refusal (chat.WriteRefusal) must reach the phone
	// verbatim — it names what to do instead, and the phone must never
	// invent its own reason.
	refusal := chat.WriteRefusal(chat.Row{Session: "grove-unbrewed", Kind: chat.KindCockpit})
	b := &fakeBackend{sendErr: fmt.Errorf("%s", refusal)}
	w := post(t, chatweb.NewServer(b), "/api/chats/grove-unbrewed/send", `{"text":"hi"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", w.Code)
	}
	var out struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Error != refusal || !strings.Contains(out.Error, "tmux attach") {
		t.Fatalf("refusal reached the client as %q, want chat.WriteRefusal's own text", out.Error)
	}
}

func TestSendRejectsEmptyAndGarbage(t *testing.T) {
	b := &fakeBackend{}
	h := chatweb.NewServer(b)
	for _, body := range []string{`{"text":""}`, `{}`, `not json`, `{"text":"hi","cmd":"gv done"}`} {
		if w := post(t, h, "/api/chats/c/send", body); w.Code != 400 {
			t.Errorf("POST send %s = %d, want 400", body, w.Code)
		}
	}
	if b.sentTo != "" {
		t.Errorf("a rejected body still reached the agent: %q", b.sentTo)
	}
}

// --- keys ---

func TestKeys(t *testing.T) {
	b := &fakeBackend{}
	w := post(t, chatweb.NewServer(b), "/api/chats/grove-chat-unbrewed-1/keys", `{"key":"2"}`)
	if w.Code != 200 || b.keyLit != "2" {
		t.Fatalf("status %d, literal %q: %s", w.Code, b.keyLit, w.Body)
	}
	b2 := &fakeBackend{}
	post(t, chatweb.NewServer(b2), "/api/chats/c/keys", `{"key":"esc"}`)
	if b2.keyLit != "\x1b" {
		t.Errorf("esc reached the backend as %q, want the escape character", b2.keyLit)
	}
}

func TestKeysRefusesAnythingButAPickerKey(t *testing.T) {
	// A raw-key endpoint that took free text would be a way to type into
	// somebody's agent while skipping the relay's verified submit — and a
	// newline would submit it.
	b := &fakeBackend{}
	h := chatweb.NewServer(b)
	for _, body := range []string{`{"key":"gv done"}`, `{"key":"\n"}`, `{"key":"Enter"}`, `{"key":""}`, `{"key":"0"}`} {
		if w := post(t, h, "/api/chats/c/keys", body); w.Code != 400 {
			t.Errorf("POST keys %s = %d, want 400", body, w.Code)
		}
	}
	if b.keyTo != "" {
		t.Errorf("a refused key still reached the pane: %q/%q", b.keyTo, b.keyLit)
	}
}

// --- spawn + resume ---

func TestNewChatAndResume(t *testing.T) {
	b := &fakeBackend{newSession: "grove-chat-unbrewed-2"}
	h := chatweb.NewServer(b)
	w := post(t, h, "/api/workspaces/unbrewed/new", `{}`)
	if w.Code != 200 || b.spawned != "unbrewed" || !strings.Contains(w.Body.String(), "grove-chat-unbrewed-2") {
		t.Fatalf("new: %d %s (spawned %q)", w.Code, w.Body, b.spawned)
	}
	w = post(t, h, "/api/chats/eeeb1234/resume", `{}`)
	if w.Code != 200 || b.resumed != "eeeb1234" {
		t.Fatalf("resume: %d %s (resumed %q)", w.Code, w.Body, b.resumed)
	}
}

// grove-225: the profile a phone picked must reach the spawn UNCHANGED,
// and "no choice" must stay byte-compatible with what grove-218 sent — an
// absent body, `{}` and an empty profile are all the host's own Claude.
func TestNewChatCarriesTheProfile(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		code    int
		profile string
	}{
		{"no body at all (a grove-218 client)", "", 200, ""},
		{"empty object", `{}`, 200, ""},
		{"explicit default", `{"profile":""}`, 200, ""},
		{"a named profile", `{"profile":"openrouter-glm"}`, 200, "openrouter-glm"},
		// DisallowUnknownFields, same as /send and /keys: a typo'd key on
		// THIS route would otherwise spawn on the wrong backend silently.
		{"unknown field", `{"prof":"openrouter-glm"}`, 400, ""},
		{"not json", `nope`, 400, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &fakeBackend{newSession: "grove-chat-unbrewed-2"}
			w := post(t, chatweb.NewServer(b), "/api/workspaces/unbrewed/new", c.body)
			if w.Code != c.code {
				t.Fatalf("status %d, want %d (%s)", w.Code, c.code, w.Body)
			}
			if b.spawnProfile != c.profile {
				t.Fatalf("spawned on profile %q, want %q", b.spawnProfile, c.profile)
			}
			if c.code != 200 && b.spawned != "" {
				t.Fatalf("a refused body still spawned a chat in %q", b.spawned)
			}
		})
	}
}

// An unknown profile is the CLI's own refusal, verbatim and with a 409 —
// never a quiet fall-back to the default lane.
func TestUnknownProfileIsTheCLIsRefusal(t *testing.T) {
	b := &fakeBackend{spawnErr: fmt.Errorf(`unknown model profile "nope" (configured: kimi, openrouter-glm)`)}
	w := post(t, chatweb.NewServer(b), "/api/workspaces/unbrewed/new", `{"profile":"nope"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `unknown model profile \"nope\" (configured: kimi, openrouter-glm)`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestProfiles(t *testing.T) {
	// N profiles: the sorted names, in the contract envelope.
	b := &fakeBackend{profiles: []string{"kimi", "openrouter-glm"}}
	w := get(t, chatweb.NewServer(b), "/api/profiles")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got struct {
		Version  int      `json:"schema_version"`
		Profiles []string `json:"profiles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal %s: %v", w.Body, err)
	}
	if got.Version != 1 || len(got.Profiles) != 2 || got.Profiles[0] != "kimi" || got.Profiles[1] != "openrouter-glm" {
		t.Fatalf("got %+v", got)
	}
	// Zero profiles is [] and a 200 — the phone renders no picker, and the
	// spawn button keeps behaving exactly as it did before grove-225.
	w = get(t, chatweb.NewServer(&fakeBackend{}), "/api/profiles")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"profiles":[]`) {
		t.Fatalf("empty: %d %s", w.Code, w.Body)
	}
	// Read-only route: a POST cannot reach it.
	if w = post(t, chatweb.NewServer(&fakeBackend{}), "/api/profiles", `{}`); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/profiles = %d, want 405", w.Code)
	}
}

func TestSpawnErrorReachesTheClient(t *testing.T) {
	b := &fakeBackend{spawnErr: fmt.Errorf("no registered workspace \"nope\"")}
	w := post(t, chatweb.NewServer(b), "/api/workspaces/nope/new", `{}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "no registered workspace") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// --- SSE ---

func TestEventsStreamsTailLinesVerbatim(t *testing.T) {
	// The SSE payload is the `gv chat tail` JSONL line, byte for byte: the
	// browser parses exactly what a piped CLI would print.
	b := &fakeBackend{lines: []string{
		`{"seq":1,"role":"user","kind":"text","text":"triage the backlog","tool":"","ts":null}`,
		`{"seq":2,"role":"assistant","kind":"text","text":"On it.","tool":"","ts":null}`,
	}}
	w := get(t, chatweb.NewServer(b), "/api/chats/grove-chat-unbrewed-1/events?since=0&follow=0")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type %q", ct)
	}
	body := w.Body.String()
	for _, line := range b.lines {
		if !strings.Contains(body, "event: entry\ndata: "+line+"\n\n") {
			t.Fatalf("a tail line was reshaped on the way out:\n%s", body)
		}
	}
	if !strings.Contains(body, "event: eof") {
		t.Errorf("a finished one-shot replay must close with eof:\n%s", body)
	}
	if b.tailTarget != "grove-chat-unbrewed-1" || b.tailFollow {
		t.Errorf("tail called with target %q follow %v", b.tailTarget, b.tailFollow)
	}
}

func TestEventsPassesSinceThrough(t *testing.T) {
	// `since` is what makes a phone waking from sleep cheap: it resumes at
	// the last seq it rendered instead of replaying the conversation.
	b := &fakeBackend{}
	get(t, chatweb.NewServer(b), "/api/chats/c/events?since=42&follow=0")
	if b.tailSince != 42 {
		t.Errorf("since reached the backend as %d, want 42", b.tailSince)
	}
	b2 := &fakeBackend{}
	get(t, chatweb.NewServer(b2), "/api/chats/c/events")
	if !b2.tailFollow {
		t.Error("a stream follows by default — a phone that has to poll is not a live chat")
	}
}

func TestEventsReportsATailFailure(t *testing.T) {
	b := &fakeBackend{tailErr: fmt.Errorf("grove-chat-unbrewed-9 has no transcript yet")}
	body := get(t, chatweb.NewServer(b), "/api/chats/grove-chat-unbrewed-9/events?follow=0").Body.String()
	if !strings.Contains(body, "event: fault") || !strings.Contains(body, "no transcript yet") {
		t.Fatalf("a failed tail must say so on the stream rather than look like an empty chat:\n%s", body)
	}
}

// A live stream emits the modal state, so the phone can grow a raw-key row
// for a permission prompt it would otherwise have to ssh in to answer.
func TestEventsEmitsPickerAndKeepAlive(t *testing.T) {
	b := &fakeBackend{
		tailHold: true,
		picker:   chatweb.Picker{Detected: true, Keys: []string{"1", "2", "esc"}, Prompt: "Do you want to proceed?"},
	}
	srv := httptest.NewServer(chatweb.NewServer(b))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/chats/c/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 4096)
	var got strings.Builder
	for !strings.Contains(got.String(), "event: picker") {
		n, err := resp.Body.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			t.Fatalf("stream ended before a picker event:\n%s\n%v", got.String(), err)
		}
	}
	if !strings.Contains(got.String(), `"prompt":"Do you want to proceed?"`) {
		t.Errorf("the picker event must carry the modal's question:\n%s", got.String())
	}
	// The keep-alive comment is what stops an idle chat's stream being
	// reaped as dead by a proxy or a sleeping radio.
	if !strings.Contains(got.String(), ":\n\n") {
		t.Errorf("no SSE keep-alive comment:\n%s", got.String())
	}
	cancel()
}
