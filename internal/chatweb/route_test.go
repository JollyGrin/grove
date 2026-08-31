package chatweb_test

import (
	"testing"

	"github.com/JollyGrin/grove/internal/chatweb"
)

func TestParseRoute(t *testing.T) {
	cases := []struct {
		path   string
		api    bool
		kind   string
		target string
		method string
	}{
		{"/api/chats", true, chatweb.RouteChats, "", "GET"},
		{"/api/chats/grove-chat-unbrewed-1/events", true, chatweb.RouteEvents, "grove-chat-unbrewed-1", "GET"},
		{"/api/chats/grove-chat-unbrewed-1/send", true, chatweb.RouteSend, "grove-chat-unbrewed-1", "POST"},
		{"/api/chats/grove-chat-unbrewed-1/keys", true, chatweb.RouteKeys, "grove-chat-unbrewed-1", "POST"},
		{"/api/chats/eeeb1234/resume", true, chatweb.RouteResume, "eeeb1234", "POST"},
		{"/api/workspaces/unbrewed/new", true, chatweb.RouteNew, "unbrewed", "POST"},
		// grove-225: the one route added to the closed table, and a READ.
		{"/api/profiles", true, chatweb.RouteProfiles, "", "GET"},

		// Not the API: the embedded UI's files.
		{"/", false, "", "", ""},
		{"/index.html", false, "", "", ""},
		{"/marked.min.js", false, "", "", ""},
		{"/sw.js", false, "", "", ""},

		// Inside /api/ but not a route: 404, never a file-server fallthrough.
		{"/api/", true, "", "", ""},
		{"/api/chats/", true, "", "", ""},
		{"/api/chats/x", true, "", "", ""},
		{"/api/chats/x/y/z", true, "", "", ""},
		{"/api/workspaces/unbrewed", true, "", "", ""},
		{"/api/workspaces//new", true, "", "", ""},
		{"/api/profiles/", true, "", "", ""},
		{"/api/profiles/openrouter-glm", true, "", "", ""},
		{"/api/workspaces/unbrewed/profiles", true, "", "", ""},

		// The scope boundary, asserted rather than described: nothing that
		// finishes a task, deletes a worktree or mutates a backend has a
		// route here, and adding one must fail this test first.
		{"/api/done", true, "", "", ""},
		{"/api/tasks", true, "", "", ""},
		{"/api/chats/grove-chat-unbrewed-1/done", true, "", "", ""},
		{"/api/chats/grove-chat-unbrewed-1/untrack", true, "", "", ""},
		{"/api/chats/grove-chat-unbrewed-1/kill", true, "", "", ""},
	}
	for _, c := range cases {
		r, api := chatweb.ParseRoute(c.path)
		if api != c.api {
			t.Errorf("ParseRoute(%q) api = %v, want %v", c.path, api, c.api)
			continue
		}
		if r.Kind != c.kind || r.Target != c.target || r.Method != c.method {
			t.Errorf("ParseRoute(%q) = %+v, want kind %q target %q method %q", c.path, r, c.kind, c.target, c.method)
		}
	}
}
