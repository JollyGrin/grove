package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/state"
)

// fakeTmux logs every invocation to $TMUX_LOG and answers just enough of
// the snapshot + capture verbs for `gv ls`: three windows, one claude pane
// each.
const fakeTmux = `#!/bin/sh
echo "$*" >> "$TMUX_LOG"
for a in "$@"; do
  case "$a" in
    list-windows) printf '@1\t1\tgv-1\n@2\t0\tgv-2\n@3\t0\tgv-3\n'; exit 0;;
    list-panes) printf '@1\t%%1\t0\t24\tclaude\n@2\t%%2\t0\t24\tclaude\n@3\t%%3\t0\t24\tclaude\n'; exit 0;;
    capture-pane) printf '❯ \n'; exit 0;;
  esac
done
exit 0
`

// grove-539 AC: `gv ls` costs one list-windows + one list-panes per
// session however many tasks there are, and a capture-pane only for the
// worker with no session file; --json keeps every old field and gains
// state / state_detail / state_source.
func TestLsResolverExecsAndJSON(t *testing.T) {
	sb := newAccountSandbox(t)
	if err := os.WriteFile(filepath.Join(sb.bin, "tmux"), []byte(fakeTmux), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(sb.home, "tmux.log")
	for i := 1; i <= 3; i++ {
		tk := fmt.Sprintf("gv-%d", i)
		for _, ev := range []state.Event{
			{Type: state.EvTaskCreated, Ticket: tk, Data: map[string]string{
				"repo": "dummy", "branch": tk, "tmux_session": "grove-x", "tmux_window": tk}},
			{Type: state.EvSessionStarted, Ticket: tk, Data: map[string]string{"session_id": "s-" + tk}},
		} {
			if err := state.Append(sb.state, ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := filepath.Join(sb.home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// gv-1 busy, gv-2 waiting on a permission prompt, gv-3 has no file.
	for i, c := range []string{`"status":"busy"`, `"status":"waiting","waitingFor":"permission prompt"`} {
		body := fmt.Sprintf(`{%s,"sessionId":"s-gv-%d","statusUpdatedAt":1,"tmux":"grove-x:@%d.%%%d"}`, c, i+1, i+1, i+1)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", 100+i)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := sb.cmd("ls", "--json", "--no-pr", "--no-cost")
	cmd.Env = append(cmd.Env, "TMUX_LOG="+log)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, out)
	}

	var env struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Tasks) != 3 {
		t.Fatalf("rows = %d, want 3\n%s", len(env.Tasks), out)
	}
	want := map[string][3]string{
		"gv-1": {"working", "", "claude"},
		"gv-2": {"waiting", "permission prompt", "claude"},
		"gv-3": {"working", "", "hook"}, // no file: the hook fold, via the fallback capture
	}
	for _, r := range env.Tasks {
		tk := r["ticket"].(string)
		for _, old := range []string{"agent", "live", "ticket", "repo"} {
			if _, ok := r[old]; !ok {
				t.Errorf("%s: old field %q missing", tk, old)
			}
		}
		got := [3]string{r["state"].(string), r["state_detail"].(string), r["state_source"].(string)}
		if got != want[tk] {
			t.Errorf("%s: state triple = %v, want %v", tk, got, want[tk])
		}
	}

	raw, _ := os.ReadFile(log)
	verbs := map[string]int{}
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		for _, v := range []string{"list-windows", "list-panes", "capture-pane"} {
			if strings.Contains(l, v) {
				verbs[v]++
			}
		}
	}
	if verbs["list-windows"] != 1 || verbs["list-panes"] != 1 || verbs["capture-pane"] != 1 {
		t.Errorf("tmux execs = %v (all: %q), want 1 list-windows, 1 list-panes, 1 capture-pane (gv-3 only)", verbs, string(raw))
	}
	if n := len(strings.Split(strings.TrimSpace(string(raw)), "\n")); n != 3 {
		t.Errorf("total tmux execs = %d, want 3\n%s", n, raw)
	}
}
