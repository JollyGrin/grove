package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
)

// grove-381: `gv serve init` hands the fixed contract prompt to the chat
// launcher and records nothing — a drafted script is never trusted.
func TestServeInitSpawnsPromptAndTrustsNothing(t *testing.T) {
	root := t.TempDir()
	sd := t.TempDir()
	oldDir := ambient.stateDir
	ambient.stateDir = sd
	t.Cleanup(func() { ambient.stateDir = oldDir })
	cfg := &config.Config{Repos: map[string]*config.Repo{"app": {Path: filepath.Join(root, "app")}}}

	var brief string
	calls := 0
	spawn := func(_ *config.Config, b, model, effort string) (string, error) {
		calls++
		brief = b
		// The drafted script lands while the chat runs; init must not trust it.
		if err := os.MkdirAll(filepath.Join(root, ".grove"), 0o755); err != nil {
			return "", err
		}
		return "✓ new orchestrator chat pane", os.WriteFile(filepath.Join(root, ".grove", "run.sh"), []byte("#!/bin/sh\necho GROVE_READY /tmp/x\n"), 0o755)
	}
	if _, err := serveInit(cfg, root, spawn); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("spawn called %d times, want 1", calls)
	}
	for _, want := range []string{"GROVE_WORKTREE", "GROVE_BRANCH", "GROVE_PORT", "GROVE_FEATURE", "GROVE_READY", filepath.Join(root, "app")} {
		if !strings.Contains(brief, want) {
			t.Errorf("init brief missing %q", want)
		}
	}
	evs, err := state.ReadEvents(sd, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type == state.EvRunScriptTrusted {
			t.Fatalf("serve init appended a trust event: %+v", e)
		}
	}
	if len(evs) != 0 {
		t.Fatalf("serve init appended %d event(s), want none", len(evs))
	}

	// A second init refuses to draft over the existing script.
	if _, err := serveInit(cfg, root, spawn); err == nil || calls != 1 {
		t.Fatalf("init over an existing run.sh: err=%v calls=%d, want refusal without a spawn", err, calls)
	}
}
