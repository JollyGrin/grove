package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/config"
)

func runeKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// twoProfiles is a cockpit with two model profiles and no default — the case
// that opens the picker.
func twoProfiles() Model {
	cfg := &config.Config{ModelProfiles: map[string]*config.ModelProfile{
		"openrouter-kimi": {Opus: "moonshotai/kimi-k2"},
		"openrouter-glm":  {Opus: "z-ai/glm-5.2"},
	}}
	m := New(cfg, "", "")
	m.width, m.height = 120, 40
	return m
}

// ) with a single profile spawns immediately — no picker flicker.
func TestBracketSingleProfileNoPicker(t *testing.T) {
	cfg := &config.Config{ModelProfiles: map[string]*config.ModelProfile{
		"openrouter-glm": {Opus: "z-ai/glm-5.2"},
	}}
	m := New(cfg, "", "")
	next, cmd := m.handleKey(runeKey(")"))
	nm := next.(Model)
	if nm.mode != modeList {
		t.Fatalf("single profile: mode = %d, want modeList (spawn immediately, no picker)", nm.mode)
	}
	if cmd == nil {
		t.Fatal("single profile: expected a spawn command")
	}
}

// ) with ≥2 profiles and no default opens the picker over the sorted names.
func TestBracketManyProfilesOpensPicker(t *testing.T) {
	m := twoProfiles()
	next, cmd := m.handleKey(runeKey(")"))
	nm := next.(Model)
	if nm.mode != modeProfilePick {
		t.Fatalf("many profiles: mode = %d, want modeProfilePick", nm.mode)
	}
	if cmd != nil {
		t.Fatal("opening the picker should not spawn anything yet")
	}
	if want := []string{"openrouter-glm", "openrouter-kimi"}; strings.Join(nm.pickProfiles, ",") != strings.Join(want, ",") {
		t.Fatalf("pickProfiles = %v, want sorted %v", nm.pickProfiles, want)
	}
	if nm.pickSel != 0 {
		t.Fatalf("pickSel = %d, want 0", nm.pickSel)
	}

	// The overlay lists each profile with its opus model hint.
	view := nm.viewProfilePick()
	for _, want := range []string{"openrouter-glm", "z-ai/glm-5.2", "openrouter-kimi", "moonshotai/kimi-k2"} {
		if !strings.Contains(view, want) {
			t.Errorf("picker view missing %q, got:\n%s", want, view)
		}
	}
}

// j/k move and clamp at the ends; esc backs out to the fleet list.
func TestPickerNavAndCancel(t *testing.T) {
	m := twoProfiles()
	next, _ := m.handleKey(runeKey(")"))
	m = next.(Model)

	// k at the top stays put.
	m, _ = mustModel(m.handleProfilePickKey(runeKey("k")))
	if m.pickSel != 0 {
		t.Fatalf("k at top: pickSel = %d, want 0 (clamped)", m.pickSel)
	}
	// j moves down.
	m, _ = mustModel(m.handleProfilePickKey(runeKey("j")))
	if m.pickSel != 1 {
		t.Fatalf("j: pickSel = %d, want 1", m.pickSel)
	}
	// j at the bottom stays put.
	m, _ = mustModel(m.handleProfilePickKey(runeKey("j")))
	if m.pickSel != 1 {
		t.Fatalf("j at bottom: pickSel = %d, want 1 (clamped)", m.pickSel)
	}
	// esc cancels cleanly back to the list.
	m, cmd := mustModel(m.handleProfilePickKey(tea.KeyMsg{Type: tea.KeyEsc}))
	if m.mode != modeList {
		t.Fatalf("esc: mode = %d, want modeList", m.mode)
	}
	if m.pickProfiles != nil {
		t.Fatalf("esc: pickProfiles = %v, want nil (cleared)", m.pickProfiles)
	}
	if cmd != nil {
		t.Fatal("esc should not spawn anything")
	}
}

// enter spawns the highlighted profile and closes the overlay.
func TestPickerEnterSpawns(t *testing.T) {
	orig := SpawnOrchestratorProfile
	defer func() { SpawnOrchestratorProfile = orig }()
	var got string
	SpawnOrchestratorProfile = func(cfg *config.Config, profile string) (string, error) {
		got = profile
		return "spawned " + profile, nil
	}

	m := twoProfiles()
	next, _ := m.handleKey(runeKey(")"))
	m = next.(Model)
	m, _ = mustModel(m.handleProfilePickKey(runeKey("j"))) // select openrouter-kimi

	m, cmd := mustModel(m.handleProfilePickKey(tea.KeyMsg{Type: tea.KeyEnter}))
	if m.mode != modeList {
		t.Fatalf("enter: mode = %d, want modeList", m.mode)
	}
	if cmd == nil {
		t.Fatal("enter: expected a spawn command")
	}
	cmd() // fire the async spawn
	if got != "openrouter-kimi" {
		t.Fatalf("spawned profile = %q, want openrouter-kimi", got)
	}
}

func mustModel(md tea.Model, cmd tea.Cmd) (Model, tea.Cmd) { return md.(Model), cmd }
