package tui

// grove-199: the `@`-armed remote spawn. `@` is a TRANSIENT armed state, not
// a mode — it survives exactly one keypress (or one hop into the `)` picker),
// and while it is off every local spawn key means exactly what it always did.

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/config"
)

// armedSpawn is a recorder for the injected remote spawn.
type armedSpawn struct {
	calls   int
	host    string
	profile string
	err     error
}

// stubRemoteSpawn swaps the injected remote spawn for a recorder, restoring
// it when the test ends.
func stubRemoteSpawn(t *testing.T, err error) *armedSpawn {
	t.Helper()
	orig := SpawnRemoteOrchestrator
	t.Cleanup(func() { SpawnRemoteOrchestrator = orig })
	rec := &armedSpawn{err: err}
	SpawnRemoteOrchestrator = func(_ *config.Config, host, profile string) (string, error) {
		rec.calls++
		rec.host, rec.profile = host, profile
		if rec.err != nil {
			return "", rec.err
		}
		return "✓ @" + host + " chat pane", nil
	}
	return rec
}

// armedModel is a cockpit with the given hosts, one digit binding and one
// model profile (so the `)` picker has something to show).
func armedModel(hosts ...string) Model {
	cfg := &config.Config{
		Hosts:         map[string]*config.Host{},
		ModelProfiles: map[string]*config.ModelProfile{"openrouter-glm": {Opus: "z-ai/glm-5.2"}},
	}
	for _, h := range hosts {
		cfg.Hosts[h] = &config.Host{SSH: h, GV: "gv"}
	}
	cfg.Orchestrator.Hotkeys = map[string]string{"1": "openrouter-glm"}
	m := New(cfg, "", "ws")
	m.width, m.height = 120, 40
	return m
}

// arm → digit → spawn → disarmed: the whole life of the state in three
// keypresses, and only the profile NAME travels.
func TestArmedDigitSpawnsRemotelyAndClears(t *testing.T) {
	rec := stubRemoteSpawn(t, nil)
	m := armedModel("groveremote")

	m, cmd := mustModel(m.handleKey(runeKey("@")))
	if m.armedHost != "groveremote" {
		t.Fatalf("@ armedHost = %q, want groveremote", m.armedHost)
	}
	if cmd != nil {
		t.Fatal("@ alone must not spawn anything")
	}

	m, cmd = mustModel(m.handleKey(runeKey("1")))
	if m.armedHost != "" {
		t.Fatalf("armedHost after the spawn = %q, want cleared", m.armedHost)
	}
	if cmd == nil {
		t.Fatal("@ then 1: expected a spawn command")
	}
	cmd()
	if rec.calls != 1 || rec.host != "groveremote" || rec.profile != "openrouter-glm" {
		t.Fatalf("spawn = %d call(s) host %q profile %q, want 1 groveremote openrouter-glm",
			rec.calls, rec.host, rec.profile)
	}
}

// 0 and O both arm the host's DEFAULT chat: no profile name is sent.
func TestArmedZeroSpawnsDefaultProfile(t *testing.T) {
	for _, k := range []string{"0", "O"} {
		rec := stubRemoteSpawn(t, nil)
		m := armedModel("groveremote")
		m, _ = mustModel(m.handleKey(runeKey("@")))
		m, cmd := mustModel(m.handleKey(runeKey(k)))
		if cmd == nil {
			t.Fatalf("@ then %s: expected a spawn command", k)
		}
		cmd()
		if rec.calls != 1 || rec.profile != "" {
			t.Fatalf("@ then %s: %d call(s), profile %q — want one call with no profile",
				k, rec.calls, rec.profile)
		}
		if m.armedHost != "" {
			t.Fatalf("@ then %s: armedHost = %q, want cleared", k, m.armedHost)
		}
	}
}

// esc cancels: nothing spawns, the state is gone, the flash says so.
func TestArmedEscCancels(t *testing.T) {
	rec := stubRemoteSpawn(t, nil)
	m := armedModel("groveremote")
	m, _ = mustModel(m.handleKey(runeKey("@")))
	m, cmd := mustModel(m.handleKey(tea.KeyMsg{Type: tea.KeyEsc}))
	if m.armedHost != "" || cmd != nil || rec.calls != 0 {
		t.Fatalf("esc: armedHost %q, cmd %v, %d spawn(s) — want cleared, none, none",
			m.armedHost, cmd, rec.calls)
	}
	if !strings.Contains(m.flash, "cancelled") {
		t.Fatalf("esc flash = %q, want a cancellation", m.flash)
	}
}

// Any other key cancels with a flash — it does NOT fall through to its local
// meaning, so an armed `X` can never park the workspace.
func TestArmedUnknownKeyCancelsWithoutFallthrough(t *testing.T) {
	rec := stubRemoteSpawn(t, nil)
	for _, k := range []string{"X", "j", "R", "$"} {
		m := armedModel("groveremote")
		m, _ = mustModel(m.handleKey(runeKey("@")))
		m, cmd := mustModel(m.handleKey(runeKey(k)))
		if m.armedHost != "" {
			t.Fatalf("@ then %s: armedHost = %q, want cleared", k, m.armedHost)
		}
		if cmd != nil || rec.calls != 0 {
			t.Fatalf("@ then %s: must not act (cmd %v, %d spawn(s))", k, cmd, rec.calls)
		}
		if m.mode != modeList {
			t.Fatalf("@ then %s: mode = %d, want modeList (no local meaning)", k, m.mode)
		}
		if !strings.Contains(m.flash, "cancelled") {
			t.Fatalf("@ then %s: flash = %q, want a cancellation", k, m.flash)
		}
	}
}

// An unbound digit is a flash, not a spawn — same message the local digit
// gives, because it is the same binding map.
func TestArmedUnboundDigitFlashes(t *testing.T) {
	rec := stubRemoteSpawn(t, nil)
	m := armedModel("groveremote")
	m, _ = mustModel(m.handleKey(runeKey("@")))
	m, cmd := mustModel(m.handleKey(runeKey("5")))
	if cmd != nil || rec.calls != 0 {
		t.Fatalf("unbound digit must not spawn (cmd %v, %d call(s))", cmd, rec.calls)
	}
	if m.armedHost != "" {
		t.Fatalf("armedHost = %q, want cleared", m.armedHost)
	}
	if !strings.Contains(m.flash, "unbound") {
		t.Fatalf("flash = %q, want the unbound hint", m.flash)
	}
}

// Zero hosts: `@` flashes and never arms.
func TestArmZeroHostsFlashes(t *testing.T) {
	m := armedModel()
	m, cmd := mustModel(m.handleKey(runeKey("@")))
	if m.armedHost != "" || cmd != nil {
		t.Fatalf("zero hosts: armedHost %q, cmd %v — want disarmed and no cmd", m.armedHost, cmd)
	}
	if !strings.Contains(m.flash, "no hosts configured") {
		t.Fatalf("flash = %q, want the no-hosts hint", m.flash)
	}
}

// One host arms directly, and a repeated `@` keeps it armed (cycling a
// single-element ring is a no-op — it must never silently disarm).
func TestArmOneHostCyclesToItself(t *testing.T) {
	m := armedModel("groveremote")
	m, _ = mustModel(m.handleKey(runeKey("@")))
	m, _ = mustModel(m.handleKey(runeKey("@")))
	if m.armedHost != "groveremote" {
		t.Fatalf("second @ with one host: armedHost = %q, want groveremote", m.armedHost)
	}
}

// Many hosts: repeated `@` walks the sorted ring and wraps.
func TestArmManyHostsCycle(t *testing.T) {
	m := armedModel("zed", "alpha", "mid")
	want := []string{"alpha", "mid", "zed", "alpha"}
	for i, w := range want {
		m, _ = mustModel(m.handleKey(runeKey("@")))
		if m.armedHost != w {
			t.Fatalf("@ #%d: armedHost = %q, want %q", i+1, m.armedHost, w)
		}
	}
}

// The armed footer replaces the legend with the prompt — the only keys that
// do anything, plus the host they'd act on.
func TestArmedFooterPrompt(t *testing.T) {
	m := armedModel("groveremote")
	m, _ = mustModel(m.handleKey(runeKey("@")))
	foot := m.viewFooter()
	for _, want := range []string{"@groveremote", "0", "1-8", ")", "esc"} {
		if !strings.Contains(foot, want) {
			t.Fatalf("armed footer %q missing %q", foot, want)
		}
	}
	if strings.Contains(foot, "park") {
		t.Fatalf("armed footer must not advertise the ordinary legend: %q", foot)
	}
}

// `)` while armed hands the picker the arming: the banner names the host and
// enter spawns THERE, not here.
func TestArmedPickerSpawnsRemotely(t *testing.T) {
	rec := stubRemoteSpawn(t, nil)
	local := 0
	origLocal := SpawnOrchestratorProfile
	t.Cleanup(func() { SpawnOrchestratorProfile = origLocal })
	SpawnOrchestratorProfile = func(*config.Config, string) (string, error) { local++; return "local", nil }

	m := armedModel("groveremote")
	m, _ = mustModel(m.handleKey(runeKey("@")))
	m, cmd := mustModel(m.handleKey(runeKey(")")))
	if m.mode != modeProfilePick || cmd != nil {
		t.Fatalf("@ then ): mode = %d, cmd = %v; want the picker and no spawn", m.mode, cmd)
	}
	if m.armedHost != "groveremote" {
		t.Fatalf("the picker must keep the arming, got %q", m.armedHost)
	}
	if view := m.viewProfilePick(); !strings.Contains(view, "@groveremote") {
		t.Fatalf("picker view missing the @host banner:\n%s", view)
	}

	m, cmd = mustModel(m.handleProfilePickKey(tea.KeyMsg{Type: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("picker enter: expected a spawn command")
	}
	cmd()
	if rec.calls != 1 || rec.host != "groveremote" || rec.profile != "openrouter-glm" {
		t.Fatalf("picker spawn = %d call(s) host %q profile %q", rec.calls, rec.host, rec.profile)
	}
	if local != 0 {
		t.Fatal("an armed picker must not spawn a LOCAL chat")
	}
	if m.armedHost != "" {
		t.Fatalf("armedHost after the picker spawn = %q, want cleared", m.armedHost)
	}
}

// esc out of an armed picker drops the arming too — the state never outlives
// the interaction that carried it.
func TestArmedPickerEscClearsArming(t *testing.T) {
	m := armedModel("groveremote")
	m, _ = mustModel(m.handleKey(runeKey("@")))
	m, _ = mustModel(m.handleKey(runeKey(")")))
	m, cmd := mustModel(m.handleProfilePickKey(tea.KeyMsg{Type: tea.KeyEsc}))
	if m.armedHost != "" || m.mode != modeList || cmd != nil {
		t.Fatalf("picker esc: armedHost %q, mode %d, cmd %v", m.armedHost, m.mode, cmd)
	}
}

// A failing relay surfaces the remote's own error line as the flash — and
// (in cmd/gv's half) spawns no pane.
func TestArmedSpawnErrorFlashes(t *testing.T) {
	stubRemoteSpawn(t, errors.New("@groveremote: no workspace 'ws' on @groveremote"))
	m := armedModel("groveremote")
	m, _ = mustModel(m.handleKey(runeKey("@")))
	_, cmd := mustModel(m.handleKey(runeKey("1")))
	if cmd == nil {
		t.Fatal("expected a spawn command")
	}
	msg, ok := cmd().(flashMsg)
	if !ok {
		t.Fatalf("spawn msg = %T, want a flashMsg", cmd())
	}
	if !strings.Contains(string(msg), "no workspace") {
		t.Fatalf("flash = %q, want the remote's error line", string(msg))
	}
}

// Disarmed, the local spawn keys are untouched: a bare digit still spawns
// HERE, and nothing reaches the remote.
func TestUnarmedDigitStaysLocal(t *testing.T) {
	rec := stubRemoteSpawn(t, nil)
	var localProfile string
	origLocal := SpawnOrchestratorProfile
	t.Cleanup(func() { SpawnOrchestratorProfile = origLocal })
	SpawnOrchestratorProfile = func(_ *config.Config, p string) (string, error) {
		localProfile = p
		return "local", nil
	}

	m := armedModel("groveremote")
	_, cmd := mustModel(m.handleKey(runeKey("1")))
	if cmd == nil {
		t.Fatal("bare 1: expected the local spawn command")
	}
	cmd()
	if localProfile != "openrouter-glm" {
		t.Fatalf("local spawn profile = %q, want openrouter-glm", localProfile)
	}
	if rec.calls != 0 {
		t.Fatal("a bare digit must never reach a host")
	}
}
