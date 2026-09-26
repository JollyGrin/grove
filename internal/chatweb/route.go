package chatweb

// grove-218: the API surface, as a pure parse.
//
// SCOPE BOUNDARY, and it is deliberate: the routes below are READ, RELAY
// and SPAWN. There is no route to `gv done`, `gv untrack --rm`, `gv grab`,
// or any task-backend mutation, and none may be added — propose-then-dispose
// applies harder to a phone than to a desk, and a fleet surface (task rows,
// cost charts, audit, sweep) belongs in the TUI or an external plugin
// against `--json`, never here (design §"Scope boundary").
//
// grove-225 added ONE route to that closed table, deliberately and within
// the boundary: /api/profiles is a READ of the host's configured model
// profiles, so the phone can pick the backend a new chat runs on instead
// of always taking the host's default. It reaches nothing the spawn route
// could not already reach.
//
// grove-294 added End chat — POST /api/chats/<s>/close — within the same
// boundary: it ends a live chat's claude PROCESS and nothing else. The
// transcript stays (the row turns archived and revivable), and the gate is
// the CLI's own (chat.CloseRefusal): only a kind-chat row, never the
// cockpit's pane.
//
// grove-293 added one more READ: /api/workspaces/<l>/models, the rows of
// the new-chat sheet — each spawn choice with the model it will actually
// run, resolved against THAT workspace's config.
//
// grove-307 added one more READ: /api/chats/events is /api/chats pushed
// over SSE, for the list screens. grove-286 another: /api/version, the
// running build's stamp.
//
// grove-334 added two more READS: /api/workspaces, the registered
// workspaces (so home offers `+ new chat` where no chat exists yet), and
// /api/chats/<s>/pane, the bottom of a chat's pane as text — the "show
// pane" escape hatch for a modal the picker scrape cannot read. Neither
// writes anything; the pane is already read every second for the picker.
//
// Parsing lives away from net/http so the whole table — including every
// path that must 404 — is testable without a listener.

import "strings"

// The route kinds. One per line of the design's §6 table.
const (
	RouteChats  = "chats"  // GET  /api/chats
	RouteEvents = "events" // GET  /api/chats/<s>/events   (SSE)
	RouteSend   = "send"   // POST /api/chats/<s>/send
	RouteKeys   = "keys"   // POST /api/chats/<s>/keys
	RouteNew    = "new"    // POST /api/workspaces/<l>/new
	RouteResume = "resume" // POST /api/chats/<s>/resume
	RouteClose  = "close"  // POST /api/chats/<s>/close   (grove-294: End chat)
	// grove-225: the profile picker's list. Read-only, no target.
	RouteProfiles = "profiles" // GET  /api/profiles
	// grove-307: the list screens' live feed — /api/chats, pushed.
	RouteChatsEvents = "chats-events" // GET  /api/chats/events   (SSE)
	// grove-286: the running build's version. Read-only, no target.
	RouteVersion = "version" // GET  /api/version
	// grove-293: the new-chat sheet's rows for one workspace. Read-only.
	RouteModels = "models" // GET  /api/workspaces/<l>/models
	// grove-334: the registered workspaces, and a chat's pane snapshot.
	RouteWorkspaces = "workspaces" // GET  /api/workspaces
	RoutePane       = "pane"       // GET  /api/chats/<s>/pane
)

// Route is a parsed API request. Target is the chat address for the chat
// routes and the workspace label for RouteNew.
//
// The chat address is deliberately whatever `gv chat ls` put in the row —
// its tmux SESSION NAME wherever it has one, its Claude session id only for
// an archived row that has no pane. That preference is not cosmetic: a
// session name comes straight from tmux, while an id routes through the
// pane↔transcript join, and a write that lands in the wrong chat is the
// failure this whole subsystem is shaped around (grove-116/78, grove-222).
type Route struct {
	Kind   string
	Target string
	Method string // the ONE method this route accepts
}

// apiPrefix is the only path namespace the API owns; everything outside it
// is a static asset from the embedded UI.
const apiPrefix = "/api/"

// ParseRoute matches an API path. ok is false for anything outside /api/,
// which the caller serves from the embedded FS instead; a path INSIDE /api/
// that matches nothing returns a zero Route with ok true, so the caller
// answers 404 rather than handing an api path to the file server.
func ParseRoute(path string) (r Route, api bool) {
	if !strings.HasPrefix(path, apiPrefix) {
		return Route{}, false
	}
	// No trailing-slash tolerance: on a closed route table, "close enough"
	// is how a path nobody meant to serve ends up served.
	parts := strings.Split(strings.TrimPrefix(path, apiPrefix), "/")
	switch {
	case len(parts) == 1 && parts[0] == "chats":
		return Route{Kind: RouteChats, Method: "GET"}, true
	case len(parts) == 1 && parts[0] == "profiles":
		return Route{Kind: RouteProfiles, Method: "GET"}, true
	case len(parts) == 1 && parts[0] == "version":
		return Route{Kind: RouteVersion, Method: "GET"}, true
	case len(parts) == 1 && parts[0] == "workspaces":
		return Route{Kind: RouteWorkspaces, Method: "GET"}, true
	case len(parts) == 2 && parts[0] == "chats" && parts[1] == "events":
		return Route{Kind: RouteChatsEvents, Method: "GET"}, true
	// A chat literally addressed "events" would read as the list stream
	// one segment up; refuse the address rather than make the parser guess.
	case len(parts) == 3 && parts[0] == "chats" && parts[1] != "" && parts[1] != "events":
		target := parts[1]
		switch parts[2] {
		case "events":
			return Route{Kind: RouteEvents, Target: target, Method: "GET"}, true
		case "send":
			return Route{Kind: RouteSend, Target: target, Method: "POST"}, true
		case "keys":
			return Route{Kind: RouteKeys, Target: target, Method: "POST"}, true
		case "resume":
			return Route{Kind: RouteResume, Target: target, Method: "POST"}, true
		case "close":
			return Route{Kind: RouteClose, Target: target, Method: "POST"}, true
		case "pane":
			return Route{Kind: RoutePane, Target: target, Method: "GET"}, true
		}
	case len(parts) == 3 && parts[0] == "workspaces" && parts[1] != "" && parts[2] == "new":
		return Route{Kind: RouteNew, Target: parts[1], Method: "POST"}, true
	case len(parts) == 3 && parts[0] == "workspaces" && parts[1] != "" && parts[2] == "models":
		return Route{Kind: RouteModels, Target: parts[1], Method: "GET"}, true
	}
	return Route{}, true
}
