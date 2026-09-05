// Package chatweb is `gv chat serve` (grove-218): one net/http listener and
// one embedded page, so the operator can read, continue and start
// orchestrator chats from a phone over Tailscale with no terminal.
//
// grove uses net/http as a CLIENT everywhere else (update, openrouter,
// linear, kimi, hooks). This is its first LISTENER, and it is the
// highest-consequence one it will ever have: the routes below paste into
// live agent panes and spawn Claude sessions. Three rules hold that down,
// and each is enforced in code rather than documented and hoped for:
//
//	BIND LOOPBACK BY DEFAULT. `tailscale serve` in front of 127.0.0.1 is
//	the sanctioned exposure and the entire auth story; any other bind is an
//	explicit flag plus BindWarning's paragraph (guard.go).
//
//	READ + RELAY + SPAWN, NOTHING ELSE. The route table is closed (route.go).
//	No `done`, no `untrack --rm`, no backend mutation, ever.
//
//	THE VERBS ARE THE CONTRACT; THIS IS JUST THE FIRST CLIENT. Every handler
//	is a thin shell over the same `gv chat` verb a terminal would run, so an
//	external client built against the JSON stays possible and this package
//	can never grow its own semantics.
//
// The server is OFF unless invoked — no daemon, no autostart, not wired
// into `gv` or the cockpit.
package chatweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/schema"
)

// Backend is everything the server needs from grove, as an interface, so
// every handler is exercised through httptest with NO tmux server, no
// registry and no transcripts (the ticket's "handlers tested via httptest"
// bar). cmd/gv implements it over the same helpers the CLI verbs use.
//
// `target` is always a chat ADDRESS as `gv chat ls` reports it: the tmux
// session name where the row has one, the Claude session id only for an
// archived row that does not. See Route's doc for why that order matters.
type Backend interface {
	// Chats is `gv chat ls --json`'s payload — every registered workspace.
	Chats() ([]chat.Row, error)
	// Tail writes the chat's transcript to w as JSONL, one chat.Entry per
	// line — byte-identical to `gv chat tail`, because the SSE stream
	// forwards those lines verbatim rather than reshaping them.
	Tail(ctx context.Context, target string, since int, follow bool, w io.Writer) error
	// Send relays prose and verifies it SUBMITTED (`gv chat send`).
	Send(target, text string) error
	// Keys delivers literal characters with no Enter (`gv chat keys`).
	// literal is already validated and mapped by the server.
	Keys(target, literal string) error
	// Picker reads the chat's pane for a modal prompt. Best-effort
	// garnish: an unreadable pane is the zero Picker, never an error.
	Picker(target string) Picker
	// NewChat spawns a fresh chat in a registered workspace and returns
	// the `grove-chat-<label>-<n>` it created. profile is a model-profile
	// name, "" for the host's own Claude — the same axis `gv orchestrator
	// new --profile` moves on, and an unknown one is the CLI's own refusal
	// rather than a fallback to the default (grove-225).
	NewChat(label, profile string) (string, error)
	// Profiles is the host's configured model profile names, sorted, on
	// ResolveOrchestratorProfile's semantics: none configured is an empty
	// list, which the phone renders as no picker at all.
	Profiles() ([]string, error)
	// Resume revives an archived chat and returns the session it landed in.
	Resume(target string) (string, error)
}

// Server is the http.Handler. Zero configuration beyond its backend: the
// bind address and port belong to the caller, so this type has nothing to
// say about exposure and cannot accidentally soften it.
type Server struct {
	backend Backend
	ui      fs.FS
	assets  http.Handler
	// poll is how often a live stream re-reads the pane for a modal and
	// emits its keep-alive. Injected for tests.
	poll time.Duration
}

// StreamPoll is the modal-detection and keep-alive cadence on an SSE
// stream. Transcript entries do NOT wait for it (chat.Tail pushes them at
// its own 250ms), so this only bounds how long a picker stays unnoticed.
const StreamPoll = time.Second

// NewServer builds the handler over a backend.
func NewServer(b Backend) *Server {
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		// Impossible: the directory is embedded at compile time.
		panic("chatweb: embedded ui missing: " + err.Error())
	}
	return &Server{backend: b, ui: sub, assets: http.FileServer(http.FS(sub)), poll: StreamPoll}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// No CORS headers, deliberately and permanently: a browser on another
	// origin must not be able to read a chat or steer one, and the write
	// gate (GuardWrite) leans on this server answering no preflight.
	route, api := ParseRoute(r.URL.Path)
	if !api {
		s.serveAsset(w, r)
		return
	}
	if route.Kind == "" {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no such endpoint: %s", r.URL.Path))
		return
	}
	if r.Method != route.Method {
		w.Header().Set("Allow", route.Method)
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("%s takes %s", r.URL.Path, route.Method))
		return
	}
	if route.Method == "POST" {
		if err := GuardWrite(r.Header.Get("Content-Type")); err != nil {
			writeErr(w, http.StatusUnsupportedMediaType, err)
			return
		}
	}
	switch route.Kind {
	case RouteChats:
		s.handleChats(w)
	case RouteProfiles:
		s.handleProfiles(w)
	case RouteEvents:
		s.handleEvents(w, r, route.Target)
	case RouteSend:
		s.handleSend(w, r, route.Target)
	case RouteKeys:
		s.handleKeys(w, r, route.Target)
	case RouteNew:
		s.handleNew(w, r, route.Target)
	case RouteResume:
		s.handleSpawn(w, route.Target, s.backend.Resume)
	}
}

// serveAsset hands out the embedded UI. `/` is index.html; everything else
// must be a real embedded file — there is no SPA catch-all, so a typo'd
// path 404s instead of returning HTML that a client would try to parse as
// JSON. The page routes on the hash, so it never needs one.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET")
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("%s takes GET", r.URL.Path))
		return
	}
	// The shell must not be cached by an intermediary: the service worker
	// owns offline behavior, and a stale index.html against a newer binary
	// is a debugging afternoon.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", ContentSecurityPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The page has no business in anybody's frame, and a clickjacked frame
	// around a spawn button is the exact shape of the risk here.
	w.Header().Set("Referrer-Policy", "no-referrer")
	// The shell is served at "/" under BOTH names, with the request path
	// normalised to "/" first. Go's file helpers redirect a request whose
	// path ends in /index.html to ./ — so serving it any other way is
	// either a 301 the service worker then caches, or a redirect loop.
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		r = r.Clone(r.Context())
		r.URL.Path = "/"
		http.ServeFileFS(w, r, s.ui, "index.html")
		return
	}
	s.assets.ServeHTTP(w, r)
}

// handleChats is `gv chat ls --json`, verbatim: the same rows in the same
// contract envelope. A client that can read this can read the CLI's output
// and vice versa — that equality is the reason the phone is not a fork.
func (s *Server) handleChats(w http.ResponseWriter) {
	rows, err := s.backend.Chats()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if rows == nil {
		rows = []chat.Row{}
	}
	writeJSON(w, http.StatusOK, schema.Envelope("chats", rows))
}

// handleProfiles lists the model profiles a new chat can be spawned on
// (grove-225). Sorted names in the contract envelope, and an EMPTY LIST
// where none are configured rather than a 404 or an error — a host with
// one lane is the common case, and the phone renders zero profiles as no
// picker at all, so `+ New chat` behaves exactly as it did before.
//
// The list is names only. A profile's base_url, its auth env var and its
// model map stay on the host: the phone never needs them to pick one, and
// a route that served them would put an operator's backend config on a
// screen that leaves the house.
func (s *Server) handleProfiles(w http.ResponseWriter) {
	names, err := s.backend.Profiles()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, schema.Envelope("profiles", names))
}

// --- SSE ---

// handleEvents streams a chat over Server-Sent Events. Two named events:
//
//	entry  — one `gv chat tail` JSONL line, forwarded BYTE FOR BYTE. The
//	         browser parses exactly what a piped CLI would.
//	picker — the modal state from a pane scrape, sent on change only.
//
// `?since=N` resumes where a client left off, which is what makes a phone
// waking from sleep cheap: it reconnects with the last seq it rendered
// instead of replaying the whole conversation. The browser does that on its
// own: every `entry` event is stamped `id: <seq>`, so EventSource's automatic
// reconnect carries `Last-Event-ID` and resumes without the page touching the
// URL. A first connection sends neither, and still replays from seq 1.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, target string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("this server cannot stream"))
		return
	}
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	// A reconnect's Last-Event-ID is a floor, not a replacement: whichever
	// of the two is further along is the one the client has already seen.
	if n, err := strconv.Atoi(strings.TrimSpace(r.Header.Get("Last-Event-ID"))); err == nil && n > since {
		since = n
	}
	follow := r.URL.Query().Get("follow") != "0"

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Nothing between the phone and here should buffer a stream whose whole
	// value is arriving as it happens.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	lines := make(chan []byte, 64)
	errc := make(chan error, 1)
	go func() {
		defer close(lines)
		errc <- s.backend.Tail(ctx, target, since, follow, &lineWriter{ctx: ctx, out: lines})
	}()

	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	var last Picker
	first := true
	// EVERY write to w happens on this goroutine — the tail runs on its
	// own and hands bytes over a channel — because an http.ResponseWriter
	// is not safe for concurrent use.
	for {
		select {
		case <-ctx.Done():
			return
		case line, open := <-lines:
			if !open {
				// The tail is done: a one-shot replay finished, a --follow
				// lost its context, or the read failed. Either way there is
				// nothing more to forward, so say which and close — leaving
				// the stream open on a dead tail would show the phone a
				// live-looking chat that never updates again.
				if err := <-errc; err != nil && ctx.Err() == nil {
					sse(w, "fault", []byte(jsonString(err.Error())))
				} else {
					sse(w, "eof", []byte("{}"))
				}
				flusher.Flush()
				return
			}
			sseEntry(w, line)
			flusher.Flush()
		case <-ticker.C:
			if p := s.backend.Picker(target); first || !p.same(last) {
				last, first = p, false
				raw, _ := json.Marshal(p)
				sse(w, "picker", raw)
			}
			// A comment line is the SSE keep-alive: it costs three bytes
			// and stops an idle chat's stream from being reaped by a proxy
			// (or by the phone's radio) as dead.
			fmt.Fprint(w, ":\n\n")
			flusher.Flush()
		}
	}
}

// same is Picker's change test, so an unchanged modal is not re-sent every
// second (the phone re-renders its key row on every picker event).
func (p Picker) same(o Picker) bool {
	if p.Detected != o.Detected || p.Prompt != o.Prompt || len(p.Keys) != len(o.Keys) {
		return false
	}
	for i := range p.Keys {
		if p.Keys[i] != o.Keys[i] {
			return false
		}
	}
	return true
}

// sse writes one framed event. data must be a single line with no embedded
// newline — every producer here is compact JSON, which cannot contain a raw
// one.
func sse(w io.Writer, event string, data []byte) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, bytes.TrimRight(data, "\r\n"))
}

// sseEntry writes an `entry` event stamped with its seq as the SSE id, so a
// browser reconnect resumes past it via Last-Event-ID. Only entries carry a
// stable seq — picker and the keep-alive stay id-less, because an id on
// either would make the browser ask to resume from a number that names no
// entry. A line without a usable seq is forwarded unstamped rather than
// dropped: the data is still the contract, the id is only an optimisation.
func sseEntry(w io.Writer, line []byte) {
	var head struct {
		Seq int `json:"seq"`
	}
	if err := json.Unmarshal(line, &head); err == nil && head.Seq > 0 {
		fmt.Fprintf(w, "id: %d\n", head.Seq)
	}
	sse(w, "entry", line)
}

// lineWriter turns chat.Tail's JSONL into whole lines on a channel. It
// buffers a partial write rather than forwarding it, so a tool_result that
// crosses a write boundary reaches the browser as one JSON object instead
// of two halves it cannot parse.
type lineWriter struct {
	ctx context.Context
	out chan<- []byte
	buf bytes.Buffer
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.buf.Write(p)
	for {
		i := bytes.IndexByte(l.buf.Bytes(), '\n')
		if i < 0 {
			return len(p), nil
		}
		line := make([]byte, i)
		copy(line, l.buf.Next(i + 1)[:i])
		select {
		case l.out <- line:
		case <-l.ctx.Done():
			return len(p), l.ctx.Err()
		}
	}
}

// --- writes ---

// sendBody is `POST /send`'s payload.
type sendBody struct {
	Text string `json:"text"`
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request, target string) {
	var body sendBody
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if body.Text == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("empty message — nothing sent"))
		return
	}
	// The refusal a non-writable chat earns is the CLI's own words
	// (chat.WriteRefusal), routed straight through: the phone must never
	// invent its own reason for why a chat will not take input.
	if err := s.backend.Send(target, body.Text); err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}

// keysBody is `POST /keys`'s payload: ONE key, named, from a closed set.
type keysBody struct {
	Key string `json:"key"`
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request, target string) {
	var body keysBody
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// ValidKey, not "anything without a newline": a raw-key endpoint that
	// takes free text is a way to type into somebody's agent while skipping
	// the relay's verified submit. A picker needs 1–9, y/n and Esc; that is
	// the whole list, and everything else is `send`'s job.
	if !ValidKey(body.Key) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%q is not a picker key — one of 1-9, y, n, esc (prose goes through /send, which verifies the submit)", body.Key))
		return
	}
	if err := s.backend.Keys(target, KeyLiteral(body.Key)); err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": body.Key})
}

// newBody is `POST /workspaces/<l>/new`'s payload, and every field of it
// is OPTIONAL — an absent body, `{}` and a `{"profile":""}` all mean the
// same spawn the route did before grove-225, on the host's own Claude.
// That byte-compatibility is the point: a client written against grove-218
// keeps working, and "no choice made" is spelled the same way as "the
// default was chosen".
type newBody struct {
	Profile string `json:"profile"`
}

// handleNew is `+ New chat`. The profile travels straight into the same
// chatSpawnReq the desk's `gv orchestrator new --profile` fills, so an
// unknown name comes back as the CLI's own refusal ("unknown model profile
// %q (configured: …)") with a 409 — never a quiet fallback to the default
// lane, which is the one failure a phone spawn must not have: an operator
// would find out which backend they were billed for a day later.
func (s *Server) handleNew(w http.ResponseWriter, r *http.Request, label string) {
	var body newBody
	if err := decodeOptional(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	session, err := s.backend.NewChat(label, body.Profile)
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

// handleSpawn serves the Resume route: it takes no body, because an
// archived conversation already CARRIES its backend (the cwd it ran in
// decides it — see chatResumeConflict), so there is nothing to choose.
func (s *Server) handleSpawn(w http.ResponseWriter, target string, spawn func(string) (string, error)) {
	session, err := spawn(target)
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

// --- plumbing ---

// maxBody caps a request body. Every payload here is one short message; a
// megabyte is already absurd and the limit exists so a stuck client cannot
// make the server hold memory for it.
const maxBody = 1 << 20

func decode(r *http.Request, into any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("bad request body: %w", err)
	}
	return nil
}

// decodeOptional is decode for a route whose body is entirely optional:
// an EMPTY body leaves every field at its zero instead of failing. Same
// cap, same DisallowUnknownFields — a typo'd key is still a 400 rather
// than a silently ignored choice, which on the profile route would mean
// spawning on the wrong backend and saying nothing.
func decodeOptional(r *http.Request, into any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("bad request body: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeErr sends the error the CLI would have printed. These strings are
// operator-facing and often say what to do instead ("attach with tmux
// attach…", "revive it with gv orchestrator new --resume…"), so the phone
// renders them verbatim rather than mapping status codes to its own copy.
func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
