package main

// grove-218: `gv chat serve` — the phone UI's listener, and grove's FIRST.
//
// This file is glue and nothing else. The routing, the framing, the two
// safety gates and the page all live in internal/chatweb, table-tested and
// httptest-tested without a tmux server; what is here is the half that
// cannot be: tmux, the workspace registry, the transcript dir. Every method
// below is the same call the matching `gv chat` verb makes, deliberately —
// the verbs are the contract and this server is just their first client, so
// a divergence between what a phone can do and what a terminal can do is a
// bug rather than a feature.
//
// SCOPE BOUNDARY (design §"Scope boundary", and it is load-bearing):
// `gv chat serve` serves ORCHESTRATOR CHATS ONLY. Read, relay, spawn.
// Anything fleet-shaped — task rows, cost charts, `gv audit`, `gv sweep`,
// anything that reaches `done` or `untrack --rm` or a task backend — stays
// in the TUI or goes to an external plugin against `--json`. In-repo
// surfaces accrete, and grove's value is being a lean CLI.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/chatweb"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/tmux"
)

// cmdChatServe runs the server until interrupted. OFF unless invoked: no
// daemon, no autostart, nothing in `gv` or the cockpit starts it.
func cmdChatServe(args []string) error {
	fs := flag.NewFlagSet("chat serve", flag.ExitOnError)
	port := fs.Int("port", 3000, "TCP port to listen on")
	bind := fs.String("bind", "127.0.0.1", "address to bind — LOOPBACK BY DEFAULT; any other value exposes chat spawning and pane input to that network")
	parseAnywhere(fs, args)

	// Bind safety (design §7). The default is loopback because
	// `tailscale serve` in front of it is the sanctioned exposure and the
	// whole auth story — this server has no auth of its own, by design.
	// Another bind is allowed (the flag IS the consent) but never quiet.
	if warn := chatweb.BindWarning(*bind, *port); warn != "" {
		fmt.Fprintln(os.Stderr, warn)
		fmt.Fprintln(os.Stderr)
	}
	addr := net.JoinHostPort(*bind, fmt.Sprint(*port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler: chatweb.NewServer(chatBackend{}),
		// No WriteTimeout: an SSE stream is meant to stay open for hours.
		// ReadHeaderTimeout still bounds a client that connects and stalls.
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Printf("gv chat serve — http://%s\n", addr)
	if chatweb.Loopback(*bind) {
		fmt.Printf("  this machine only. To reach it from a phone: tailscale serve --bg %d\n", *port)
		fmt.Println("  (tailnet-only, identity-gated, real cert. `tailscale funnel` is PUBLIC and never correct here.)")
	}
	fmt.Println("  ^C to stop — nothing keeps running afterwards")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	fmt.Println("\ngv chat serve stopped")
	return nil
}

// chatBackend is chatweb.Backend over this machine. Stateless: every call
// re-reads tmux and the registry, so a chat spawned or reaped from the desk
// shows up on the phone at the next request rather than at a restart.
type chatBackend struct{}

func (chatBackend) Chats() ([]chat.Row, error) {
	recs, err := chatReport()
	if err != nil {
		return nil, err
	}
	rows := make([]chat.Row, 0, len(recs))
	for _, r := range recs {
		rows = append(rows, r.Row)
	}
	return rows, nil
}

func (chatBackend) Tail(ctx context.Context, target string, since int, follow bool, w io.Writer) error {
	rec, err := findChat(target)
	if err != nil {
		return err
	}
	path, err := rec.transcriptPath()
	if err != nil {
		return err
	}
	err = chat.Tail(ctx, path, chat.TailOptions{Since: since, Follow: follow}, w)
	if os.IsNotExist(err) {
		return fmt.Errorf("%s has no transcript yet — nothing has been said in it", chatName(rec.Row))
	}
	return err
}

// Send is `gv chat send`'s body: the whole grove-144 relay sequence via
// tmux.PasteText — bracketed paste, settle, a SEPARATE Enter, then a scrape
// proving it SUBMITTED. Never a shortcut around it, and the refusal a
// non-writable chat earns is chat.WriteRefusal's own words (via
// writableChat), so the phone and the CLI cannot disagree.
func (chatBackend) Send(target, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("empty message — nothing sent")
	}
	pane, row, err := writableChat(target)
	if err != nil {
		return err
	}
	warn, err := tmux.PasteText(pane, text)
	if err != nil {
		return fmt.Errorf("send to %s: %w", row.Session, err)
	}
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	return nil
}

// Keys is the relay rule's one exception — a raw character with no Enter,
// for a picker that acts on the keypress itself. literal is already gated
// by chatweb.ValidKey, so nothing free-form reaches send-keys here.
func (chatBackend) Keys(target, literal string) error {
	pane, row, err := writableChat(target)
	if err != nil {
		return err
	}
	if err := tmux.SendRawKey(pane, literal); err != nil {
		return fmt.Errorf("keys to %s: %w", row.Session, err)
	}
	return nil
}

// Picker is the ONE pane read in this whole subsystem, and it is garnish by
// house rule: an unanswered modal is not in the transcript, so there is
// nothing else to look at. Never an error — an unreadable pane, a chat that
// is not writable, a tmux that has gone away all mean "no picker", because
// a failed scrape must never look like a modal the operator should answer.
func (chatBackend) Picker(target string) chatweb.Picker {
	rec, err := findChat(target)
	if err != nil || rec.Pane == "" || !rec.Row.Writable {
		return chatweb.Picker{}
	}
	capture, err := tmux.CapturePane(rec.Pane)
	if err != nil {
		return chatweb.Picker{}
	}
	return chatweb.DetectPicker(capture)
}

// NewChat is `+ New chat` on the phone. profile is a model-profile name
// ("" = the operator's own Claude), and it goes into the SAME chatSpawnReq
// the desk's `gv orchestrator new --profile` fills — so the phone gets the
// profiled spawn path unchanged, including its refusals: an unknown name
// dies in chatSpawnPlan's ResolveProfile before a dir, a session or an
// event exists (grove-225).
func (chatBackend) NewChat(label, profile string) (string, error) {
	return spawnAndName(label, chatSpawnReq{Label: label, Profile: profile})
}

// Profiles lists the model profiles this machine has configured, on the
// cockpit `)` hotkey's own semantics (ResolveOrchestratorProfile): sorted
// names, and nothing at all when none are configured — the phone then
// shows no picker and `+ New chat` behaves exactly as it did before.
//
// A machine with NO config file has no profiles, which is an empty picker
// and not a fault: `gv chat serve` is runnable from anywhere, and a 500 on
// a garnish route would be a broken button on a working server.
func (chatBackend) Profiles() ([]string, error) {
	cfg, err := loadCfg()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	names, action := cfg.ResolveOrchestratorProfile()
	if action != config.ProfilePick {
		return nil, nil
	}
	return names, nil
}

// Resume revives an archived chat (grove-217). The target names a row; the
// workspace and the Claude session id both come off that row, so a phone
// never has to know either.
func (chatBackend) Resume(target string) (string, error) {
	rec, err := findChat(target)
	if err != nil {
		return "", err
	}
	if rec.Row.SessionID == nil || *rec.Row.SessionID == "" {
		return "", fmt.Errorf("%s has no Claude session id — there is no conversation to revive", chatName(rec.Row))
	}
	if rec.Row.Kind == chat.KindChat {
		return "", fmt.Errorf("%s is already live — open it instead of reviving it", rec.Row.Session)
	}
	return spawnAndName(rec.Row.Workspace, chatSpawnReq{Label: rec.Row.Workspace, Resume: *rec.Row.SessionID})
}

// spawnAndName runs a spawn and answers with the session it created, so the
// phone can navigate straight into the new chat.
//
// The name is read by DIFFING tmux's session list around the spawn rather
// than by having spawnWorkspaceChat hand it back. That keeps the shared
// spawn path — which the cockpit, the relay and `--resume` all go through —
// untouched by this server. The cost is a spawn racing another one being
// reported as "" (the phone then just lands back on the chat list), which
// is the right failure for a single-operator tool.
//
// spawnWorkspaceChat prints its own success line; on a server that is the
// log, which is what you want when a phone spawned it.
func spawnAndName(label string, req chatSpawnReq) (string, error) {
	prefix := "grove-chat-" + label + "-"
	before := map[string]bool{}
	for _, s := range tmux.SessionNames() {
		before[s] = true
	}
	if err := spawnWorkspaceChat(req); err != nil {
		return "", err
	}
	for _, s := range tmux.SessionNames() {
		if !before[s] && strings.HasPrefix(s, prefix) {
			return s, nil
		}
	}
	return "", nil
}
