package connections

import (
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
)

func TestKickoffConnectionOnlyWithOverride(t *testing.T) {
	cfg := &config.Config{Repos: map[string]*config.Repo{
		"a": {},
		"b": {PromptOverlay: "/o.tmpl"},
	}}
	if got := kickoffConnections(Env{Cfg: cfg}); len(got) != 0 {
		t.Fatalf("no override: want no row, got %+v", got)
	}
	cfg.Repos["c"] = &config.Repo{Prompt: "/whole.tmpl"}
	got := kickoffConnections(Env{Cfg: cfg})
	if len(got) != 1 || got[0].Severity != SeverityWarn {
		t.Fatalf("want one warn row, got %+v", got)
	}
	if !strings.HasPrefix(got[0].Title, "1 repo(s) override the kickoff template wholesale") {
		t.Errorf("title = %q", got[0].Title)
	}
	if st := got[0].Check(Env{}); st.State != StateWarn {
		t.Errorf("state = %v, want warn", st.State)
	}
}
