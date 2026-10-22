package tmux

import (
	"reflect"
	"testing"
)

// Canned `start-server ; show-options -g ; show-options -gw` output
// (tmux 3.4): session options first, then the window table.
const cannedGlobalOptions = `activity-action other
base-index 1
renumber-windows on
set-titles-string "#S:#I:#W - \"#T\" #{session_alerts}"
status-left "[#{session_name}] "
aggressive-resize off
allow-rename on
automatic-rename on
pane-base-index 1
window-status-format "#I:#W#{?window_flags,#{window_flags}, }"`

func TestParseGlobalOptions(t *testing.T) {
	cases := []struct {
		name  string
		out   string
		names []string
		want  map[string]string
	}{
		{
			name:  "hostile config picks every requested option across both tables",
			out:   cannedGlobalOptions,
			names: []string{"base-index", "pane-base-index", "renumber-windows", "allow-rename"},
			want:  map[string]string{"base-index": "1", "pane-base-index": "1", "renumber-windows": "on", "allow-rename": "on"},
		},
		{
			name:  "default config",
			out:   "base-index 0\nrenumber-windows off\nallow-rename off\npane-base-index 0\n",
			names: []string{"base-index", "pane-base-index", "renumber-windows", "allow-rename"},
			want:  map[string]string{"base-index": "0", "pane-base-index": "0", "renumber-windows": "off", "allow-rename": "off"},
		},
		{
			name:  "unrequested options are dropped, requested-but-absent are missing",
			out:   cannedGlobalOptions,
			names: []string{"base-index", "no-such-option"},
			want:  map[string]string{"base-index": "1"},
		},
		{
			name:  "quoted values lose one layer of quotes and keep inner spaces",
			out:   cannedGlobalOptions,
			names: []string{"status-left", "set-titles-string"},
			want:  map[string]string{"status-left": "[#{session_name}] ", "set-titles-string": `#S:#I:#W - \"#T\" #{session_alerts}`},
		},
		{
			name:  "first occurrence wins when both tables list a name",
			out:   "allow-rename off\nallow-rename on\n",
			names: []string{"allow-rename"},
			want:  map[string]string{"allow-rename": "off"},
		},
		{
			name:  "empty output",
			out:   "",
			names: []string{"base-index"},
			want:  map[string]string{},
		},
	}
	for _, tc := range cases {
		if got := parseGlobalOptions(tc.out, tc.names...); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: parseGlobalOptions = %v, want %v", tc.name, got, tc.want)
		}
	}
}
