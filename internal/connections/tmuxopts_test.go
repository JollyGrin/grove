package connections

import (
	"errors"
	"testing"
)

func tmuxEnv(live map[string]string) Env {
	return Env{
		LookPath:          func(string) (string, error) { return "/usr/bin/tmux", nil },
		TmuxGlobalOptions: func(...string) map[string]string { return live },
	}
}

func TestTmuxOptionConnections(t *testing.T) {
	defaults := map[string]string{"base-index": "0", "pane-base-index": "0", "renumber-windows": "off", "allow-rename": "off"}
	hostile := map[string]string{"base-index": "1", "pane-base-index": "1", "renumber-windows": "on", "allow-rename": "on"}

	t.Run("default config yields no rows", func(t *testing.T) {
		if got := tmuxOptionConnections(tmuxEnv(defaults)); len(got) != 0 {
			t.Fatalf("want no rows, got %d: %+v", len(got), got)
		}
	})

	t.Run("nil seam drops the section", func(t *testing.T) {
		e := tmuxEnv(hostile)
		e.TmuxGlobalOptions = nil
		if got := tmuxOptionConnections(e); len(got) != 0 {
			t.Fatalf("want no rows without a seam, got %d", len(got))
		}
	})

	t.Run("missing tmux binary drops the section", func(t *testing.T) {
		e := tmuxEnv(hostile)
		e.LookPath = func(string) (string, error) { return "", errors.New("not found") }
		if got := tmuxOptionConnections(e); len(got) != 0 {
			t.Fatalf("want no rows without tmux, got %d", len(got))
		}
	})

	t.Run("failed exec yields no rows", func(t *testing.T) {
		if got := tmuxOptionConnections(tmuxEnv(nil)); len(got) != 0 {
			t.Fatalf("want no rows on nil live map, got %d", len(got))
		}
	})

	t.Run("hostile config: info rows for indices, warn for allow-rename", func(t *testing.T) {
		e := tmuxEnv(hostile)
		got := tmuxOptionConnections(e)
		wantIDs := []string{"tmux-option:base-index", "tmux-option:pane-base-index", "tmux-option:renumber-windows", "tmux-option:allow-rename"}
		if len(got) != len(wantIDs) {
			t.Fatalf("want %d rows, got %d", len(wantIDs), len(got))
		}
		for i, c := range got {
			if c.ID != wantIDs[i] {
				t.Errorf("row %d: id %q, want %q", i, c.ID, wantIDs[i])
			}
			if c.Severity != SeverityWarn {
				t.Errorf("%s: severity %q, must never block doctor", c.ID, c.Severity)
			}
			st := c.Check(e)
			switch c.ID {
			case "tmux-option:allow-rename":
				if st.State != StateWarn || c.Fix == "" {
					t.Errorf("allow-rename: state %q fix %q; want warn with a fix", st.State, c.Fix)
				}
				if c.Title != "tmux allow-rename on" {
					t.Errorf("allow-rename title %q", c.Title)
				}
			default:
				if st.State != StateOK || st.Info != "non-default, supported since #168" {
					t.Errorf("%s: state %q info %q; want ok info line", c.ID, st.State, st.Info)
				}
			}
		}
		if got[0].Title != "tmux base-index 1" {
			t.Errorf("base-index title %q", got[0].Title)
		}
	})

	t.Run("only the non-default options get rows", func(t *testing.T) {
		live := map[string]string{"base-index": "1", "pane-base-index": "0", "renumber-windows": "off", "allow-rename": "off"}
		got := tmuxOptionConnections(tmuxEnv(live))
		if len(got) != 1 || got[0].ID != "tmux-option:base-index" {
			t.Fatalf("want just base-index, got %+v", got)
		}
	})
}
