package kickoff

import (
	"testing"

	"github.com/JollyGrin/grove/internal/config"
)

func TestReportStates(t *testing.T) {
	rows := Report(map[string]*config.Repo{
		"c": {Prompt: "/p/whole.tmpl", PromptOverlay: "/p/o.tmpl"},
		"a": {},
		"b": {PromptOverlay: "/p/o.tmpl"},
	})
	if len(rows) != 3 || rows[0].Repo != "a" || rows[1].Repo != "b" || rows[2].Repo != "c" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].State != StateDefault || rows[0].Path != "" || rows[0].Note != "" {
		t.Errorf("default row = %+v", rows[0])
	}
	if rows[1].State != StateOverlay || rows[1].Path != "/p/o.tmpl" {
		t.Errorf("overlay row = %+v", rows[1])
	}
	if rows[2].State != StateOverride || rows[2].Path != "/p/whole.tmpl" {
		t.Errorf("override row = %+v", rows[2])
	}
	if Overrides(rows) != 1 {
		t.Errorf("Overrides = %d", Overrides(rows))
	}
}
