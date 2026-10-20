package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/state"
)

func setupEvents(t *testing.T, dir string) []state.Event {
	t.Helper()
	evs, err := state.ReadEvents(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestRunSetupCmd(t *testing.T) {
	var out, errb bytes.Buffer

	dir := t.TempDir()
	if err := runSetupCmd("echo boom >&2; exit 3", "T-1", dir, strings.NewReader(""), &out, &errb); err == nil {
		t.Fatal("want the original error back")
	}
	evs := setupEvents(t, dir)
	if len(evs) != 1 || evs[0].Type != state.EvSetupFailed || evs[0].Ticket != "T-1" ||
		evs[0].Data["exit"] != "3" || evs[0].Data["tail"] != "boom" {
		t.Fatalf("events = %+v", evs)
	}
	if !strings.Contains(errb.String(), "boom") {
		t.Errorf("stderr not passed through: %q", errb.String())
	}

	for name, c := range map[string]struct{ setup, ticket string }{
		"success":   {"true", "T-1"},
		"no ticket": {"exit 3", ""},
	} {
		d := t.TempDir()
		_ = runSetupCmd(c.setup, c.ticket, d, strings.NewReader(""), &out, &errb)
		if evs := setupEvents(t, d); len(evs) != 0 {
			t.Errorf("%s: appended %+v", name, evs)
		}
	}
}

func TestTailBufferBounded(t *testing.T) {
	var out, errb bytes.Buffer
	dir := t.TempDir()
	// ~10 MB of stderr, last line "final".
	_ = runSetupCmd(`yes xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx | head -c 10000000 >&2; echo; echo final >&2; exit 1`,
		"T-2", dir, strings.NewReader(""), &out, &errb)
	evs := setupEvents(t, dir)
	if len(evs) != 1 || evs[0].Data["tail"] != "final" {
		t.Fatalf("events = %+v", evs)
	}
	tb := &tailBuffer{max: setupTailBytes}
	for i := 0; i < 5000; i++ {
		tb.Write([]byte(strings.Repeat("y", 1000)))
	}
	if len(tb.buf) != setupTailBytes {
		t.Errorf("tail buffer = %d bytes, want %d", len(tb.buf), setupTailBytes)
	}
}
