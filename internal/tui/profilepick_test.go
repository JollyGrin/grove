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

// ) with a single profile STILL opens the picker (grove-105): the choice is
// always shown — no auto-spawn shortcut.
func TestBracketSingleProfileOpensPicker(t *testing.T) {
	cfg := &config.Config{ModelProfiles: map[string]*config.ModelProfile{
		"openrouter-glm": {Opus: "z-ai/glm-5.2"},
	}}
	m := New(cfg, "", "")
	next, cmd := m.handleKey(runeKey(")"))
	nm := next.(Model)
	if nm.mode != modeProfilePick {
		t.Fatalf("single profile: mode = %d, want modeProfilePick (always the picker)", nm.mode)
	}
	if cmd != nil {
		t.Fatal("single profile: ) must not spawn anything")
	}
	if len(nm.pickProfiles) != 1 || nm.pickProfiles[0] != "openrouter-glm" {
		t.Fatalf("pickProfiles = %v, want [openrouter-glm]", nm.pickProfiles)
	}
}

// ) with zero profiles flashes the hint.
func TestBracketZeroProfilesHints(t *testing.T) {
	m := New(&config.Config{}, "", "")
	next, cmd := m.handleKey(runeKey(")"))
	nm := next.(Model)
	if nm.mode != modeList || cmd != nil {
		t.Fatalf("zero profiles: mode = %d, cmd = %v; want modeList and no cmd", nm.mode, cmd)
	}
	if !strings.Contains(nm.flash, "no model_profiles") {
		t.Fatalf("flash = %q, want the no-profiles hint", nm.flash)
	}
}

// ) with ≥2 profiles opens the picker over the sorted names.
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

// stubSaveHotkey swaps the persist hook for an in-memory recorder.
func stubSaveHotkey(t *testing.T) *[][2]string {
	t.Helper()
	orig := SaveHotkeyBinding
	t.Cleanup(func() { SaveHotkeyBinding = orig })
	var saves [][2]string
	SaveHotkeyBinding = func(digit, profile string) error {
		saves = append(saves, [2]string{digit, profile})
		return nil
	}
	return &saves
}

// A digit in the picker binds the highlighted profile, persists it, keeps
// the picker open; the row's own digit again unbinds; a taken digit is
// stolen and a profile holds only one digit.
func TestPickerDigitBindsUnbindsSteals(t *testing.T) {
	saves := stubSaveHotkey(t)
	m := twoProfiles()
	next, _ := m.handleKey(runeKey(")"))
	m = next.(Model)

	// Bind 1 → openrouter-glm (row 0).
	m, cmd := mustModel(m.handleProfilePickKey(runeKey("1")))
	if m.mode != modeProfilePick {
		t.Fatalf("bind: mode = %d, want picker still open", m.mode)
	}
	if cmd != nil {
		t.Fatal("bind: must not spawn")
	}
	if got := m.cfg.Orchestrator.Hotkeys["1"]; got != "openrouter-glm" {
		t.Fatalf("bind: hotkeys[1] = %q, want openrouter-glm", got)
	}
	if !strings.Contains(m.viewProfilePick(), "[1]") {
		t.Error("picker view missing the [1] binding")
	}

	// Moving glm to 2 drops its old digit (one digit per profile).
	m, _ = mustModel(m.handleProfilePickKey(runeKey("2")))
	if hk := m.cfg.Orchestrator.Hotkeys; hk["2"] != "openrouter-glm" || hk["1"] != "" {
		t.Fatalf("move: hotkeys = %v, want only 2→openrouter-glm", hk)
	}

	// Stealing: kimi (row 1) takes digit 2 from glm.
	m, _ = mustModel(m.handleProfilePickKey(runeKey("j")))
	m, _ = mustModel(m.handleProfilePickKey(runeKey("2")))
	if hk := m.cfg.Orchestrator.Hotkeys; hk["2"] != "openrouter-kimi" || len(hk) != 1 {
		t.Fatalf("steal: hotkeys = %v, want exactly 2→openrouter-kimi", hk)
	}

	// The row's own digit again unbinds it.
	m, _ = mustModel(m.handleProfilePickKey(runeKey("2")))
	if hk := m.cfg.Orchestrator.Hotkeys; len(hk) != 0 {
		t.Fatalf("unbind: hotkeys = %v, want empty", hk)
	}
	if !strings.Contains(m.flash, "unbound") {
		t.Fatalf("unbind flash = %q, want 'unbound'", m.flash)
	}

	want := [][2]string{
		{"1", "openrouter-glm"}, {"2", "openrouter-glm"},
		{"2", "openrouter-kimi"}, {"2", ""},
	}
	if len(*saves) != len(want) {
		t.Fatalf("persisted saves = %v, want %v", *saves, want)
	}
	for i, w := range want {
		if (*saves)[i] != w {
			t.Fatalf("save %d = %v, want %v", i, (*saves)[i], w)
		}
	}
}

// In the fleet list a bound digit spawns that profile directly; an unbound
// digit just flashes.
func TestListDigitSpawnsBoundProfile(t *testing.T) {
	orig := SpawnOrchestratorProfile
	defer func() { SpawnOrchestratorProfile = orig }()
	var got string
	SpawnOrchestratorProfile = func(cfg *config.Config, profile string) (string, error) {
		got = profile
		return "", nil
	}

	m := twoProfiles()
	m.cfg.Orchestrator.Hotkeys = map[string]string{"3": "openrouter-kimi"}

	next, cmd := m.handleKey(runeKey("3"))
	if cmd == nil {
		t.Fatal("bound digit: expected a spawn command")
	}
	cmd()
	if got != "openrouter-kimi" {
		t.Fatalf("spawned = %q, want openrouter-kimi", got)
	}

	next, cmd = next.(Model).handleKey(runeKey("4"))
	nm := next.(Model)
	if cmd != nil {
		t.Fatal("unbound digit: must not spawn")
	}
	if want := "4 unbound — bind it in the ) picker"; nm.flash != want {
		t.Fatalf("unbound flash = %q, want %q", nm.flash, want)
	}
}
