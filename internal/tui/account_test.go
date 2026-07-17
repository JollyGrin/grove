package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/cost"
	"github.com/JollyGrin/grove/internal/openrouter"
)

func TestRunwaySpecModes(t *testing.T) {
	u := openrouter.KeyUsage{Daily: 0.5, Weekly: 7.94, Monthly: 27.19}

	// Runway mode: remaining ÷ weekly burn, scaled to the fixed 4-week horizon.
	g := runwaySpec(true, true, 12.81, u, 0)
	if !g.ok || g.sev != runwayWarm { // 1.61 wk ≤ 2 wk → warm
		t.Errorf("runway gauge = %+v, want ok/warm", g)
	}
	if g.frac < 0.40 || g.frac > 0.41 { // 1.61 wk / 4 wk
		t.Errorf("frac = %v, want ~0.40", g.frac)
	}
	if !strings.Contains(g.caption, "~1.6 wk at current burn ($7.94/wk)") {
		t.Errorf("caption = %q", g.caption)
	}

	// Normal above 2 weeks; warm at ≤ 2 weeks; alert at ≤ 3 days.
	if g := runwaySpec(true, true, 40, u, 0); g.sev != runwayNormal {
		t.Errorf("5wk sev = %d, want normal", g.sev)
	}
	if g := runwaySpec(true, true, 15, u, 0); g.sev != runwayWarm {
		t.Errorf("1.9wk sev = %d, want warm", g.sev)
	}
	if g := runwaySpec(true, true, 3, u, 0); g.sev != runwayAlert {
		t.Errorf("2.6-day sev = %d, want alert", g.sev)
	}

	// Zero burn → full dim bar, ∞ caption.
	if g := runwaySpec(true, true, 10, openrouter.KeyUsage{}, 0); !g.ok || g.sev != runwayDim || g.frac != 1 || g.caption != "∞ at current burn" {
		t.Errorf("zero-burn gauge = %+v", g)
	}

	// No weekly history → usage_daily × 7 fallback.
	if g := runwaySpec(true, true, 14, openrouter.KeyUsage{Daily: 1}, 0); !strings.Contains(g.caption, "$7.00/wk") {
		t.Errorf("daily-fallback caption = %q", g.caption)
	}

	// Tank override: flat fuel mode, dollars caption, no urgency color.
	if g := runwaySpec(true, true, 12.81, u, 25); !g.ok || g.sev != runwayNormal ||
		g.frac < 0.51 || g.frac > 0.52 || g.caption != "$12.81 of $25.00 tank" {
		t.Errorf("tank gauge = %+v", g)
	}

	// Missing data → not ok (renders as a dash, never an error state).
	if g := runwaySpec(false, false, 0, u, 0); g.ok {
		t.Errorf("no-credits gauge = %+v, want !ok", g)
	}
	if g := runwaySpec(true, false, 10, u, 0); g.ok {
		t.Errorf("no-usage runway gauge = %+v, want !ok", g)
	}
	// …but the tank needs only the balance.
	if g := runwaySpec(true, false, 10, u, 25); !g.ok {
		t.Errorf("no-usage tank gauge = %+v, want ok", g)
	}
}

func TestGaugeBar(t *testing.T) {
	if got := gaugeBar(0.5, 4); got != "▓▓░░" {
		t.Errorf("half bar = %q", got)
	}
	if got := gaugeBar(0, 4); got != "░░░░" {
		t.Errorf("empty bar = %q", got)
	}
	if got := gaugeBar(2, 4); got != "▓▓▓▓" {
		t.Errorf("overfull bar = %q", got)
	}
	if got := gaugeBar(0.01, 4); got != "▓░░░" { // nonzero always shows a cell
		t.Errorf("sliver bar = %q", got)
	}
	if got := gaugeBar(0.5, 0); got != "" {
		t.Errorf("zero-width bar = %q", got)
	}
}

// accountModel builds a costs-page model with a populated ACCOUNT snapshot.
func accountModel(width int) Model {
	m := New(nil, "", "")
	m.width, m.height = width, 30
	m.mode = modeCosts
	m.costsTab = costsTabAccount
	m.account = accountMsg{
		fetched:   true,
		keyMasked: "sk-or-v1-f9b...a6a",
		creditsOK: true,
		credits:   openrouter.Credits{TotalCredits: 40, TotalUsage: 27.19},
		usageOK:   true,
		usage:     openrouter.KeyUsage{Daily: 0, Weekly: 7.94, Monthly: 27.19},
		estDay:    1.23, estWeek: 12.40, estMonth: 43.10, estYear: 118.05,
		byModel: []cost.ModelSpend{{Model: "kimi-k3", USD: 18.20}, {Model: "gpt-5.5", USD: 6.10}},
	}
	return m
}

func TestViewAccountRenders(t *testing.T) {
	m := accountModel(80)
	out := m.viewAccount()
	for _, want := range []string{
		"SPEND", "ACCOUNT", "BALANCE", "$12.81 remaining",
		"($40.00 purchased · $27.19 used, lifetime)", "RUNWAY",
		"~1.6 wk at current burn ($7.94/wk)",
		"WINDOW", "OPENROUTER", "GROVE EST", "this year",
		"BY MODEL · 30d (grove est)", "kimi-k3", "$18.20",
		"top-up", "openrouter.ai/settings/credits",
		"sk-or-v1-f9b...a6a",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("account view missing %q", want)
		}
	}
	// The full key never renders — only the masked form (guarded above).
	if strings.Contains(out, "sk-or-v1-f9b1") {
		t.Error("unmasked key rendered")
	}
}

func TestViewAccountDegradesWithoutKey(t *testing.T) {
	m := accountModel(80)
	m.account = accountMsg{fetched: true, estMonth: 43.10}
	out := m.viewAccount()
	for _, want := range []string{
		"CONNECT OPENROUTER", "OPENROUTER_API_KEY", "paste key from clipboard",
		"—", "GROVE EST", "$43.10",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no-key account view missing %q", want)
		}
	}
	if !strings.Contains(m.viewAccountFooter(), "paste key") {
		t.Error("footer missing the paste-key hint when no key resolves")
	}
}

func TestViewAccountTankMode(t *testing.T) {
	m := accountModel(80)
	cfg := &config.Config{}
	cfg.Cost.OpenRouter.TankUSD = 25
	m.cfg = cfg
	if out := m.viewAccount(); !strings.Contains(out, "$12.81 of $25.00 tank") {
		t.Errorf("tank caption missing:\n%s", out)
	}
	cfg.Cost.OpenRouter.TankUSD = 0 // unset → runway mode resumes
	if out := m.viewAccount(); !strings.Contains(out, "at current burn") {
		t.Error("runway caption missing after tank unset")
	}
}

// Both tabs must fit narrow panes — the grove-56 lesson generalized: no
// header or panel line may exceed the pane width, with data, without a key,
// and mid-fetch. The single-line footer is excluded: costs footers were
// never clamped, and the ACCOUNT one carries an OSC 8 link whose payload
// lipgloss/reflow miscounts as printable.
func TestViewCostsTabsClampWidth(t *testing.T) {
	snapshots := []accountMsg{
		accountModel(0).account,
		{fetched: true},
		{},
	}
	for _, a := range snapshots {
		for w := 24; w <= 100; w += 4 {
			for _, tab := range []int{costsTabSpend, costsTabAccount} {
				m := accountModel(w)
				m.costsTab = tab
				m.account = a
				lines := strings.Split(m.viewCosts(), "\n")
				for i, ln := range lines[:len(lines)-1] {
					if lw := lipgloss.Width(ln); lw > m.width {
						t.Errorf("tab=%d w=%d fetched=%v: line %d is %d cells", tab, w, a.fetched, i, lw)
					}
				}
			}
		}
	}
}

func TestCostsTabKeys(t *testing.T) {
	m := accountModel(80)
	m.costsTab = costsTabSpend

	// tab reaches ACCOUNT and fires the one-shot fetch.
	next, cmd := m.handleCostsKey(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.costsTab != costsTabAccount || cmd == nil {
		t.Fatalf("tab → tab=%d cmd=%v, want ACCOUNT + fetch cmd", m.costsTab, cmd)
	}
	// tab again returns to SPEND without a fetch.
	next, cmd = m.handleCostsKey(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.costsTab != costsTabSpend || cmd != nil {
		t.Fatalf("tab tab → tab=%d cmd=%v, want SPEND + no cmd", m.costsTab, cmd)
	}

	// r on ACCOUNT refetches instead of toggling the ledger recorder.
	m.costsTab = costsTabAccount
	next, cmd = m.handleCostsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = next.(Model)
	if cmd == nil || m.costs.recording {
		t.Error("r on ACCOUNT should refetch, not touch recording")
	}

	// p only enters the paste flow when no key resolved.
	next, cmd = m.handleCostsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if cmd != nil {
		t.Error("p with a key present should be a no-op")
	}
	m = next.(Model)
	m.account.keyMasked = ""
	if _, cmd = m.handleCostsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")}); cmd == nil {
		t.Error("p without a key should start the paste flow")
	}

	// $ from the fleet list always lands on SPEND.
	m.costsTab = costsTabAccount
	m.mode = modeList
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("$")})
	m = next.(Model)
	if m.mode != modeCosts || m.costsTab != costsTabSpend {
		t.Errorf("$ → mode=%d tab=%d, want costs/SPEND", m.mode, m.costsTab)
	}
}

func TestHyperlinkWrapsOSC8(t *testing.T) {
	got := hyperlink("https://x.test", "text")
	if got != "\x1b]8;;https://x.test\x1b\\text\x1b]8;;\x1b\\" {
		t.Errorf("hyperlink = %q", got)
	}
	if !strings.Contains(accountModel(80).viewAccountFooter(), "\x1b]8;;"+openrouter.CreditsURL) {
		t.Error("account footer missing the OSC 8 top-up link")
	}
}
