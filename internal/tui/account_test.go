package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/cost"
	"github.com/JollyGrin/grove/internal/kimi"
	"github.com/JollyGrin/grove/internal/openrouter"
	"github.com/JollyGrin/grove/internal/secrets"
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
		keyRowsModel(0).account,  // grove-104: key-manager rows must clamp too
		kimiFuelModel(0).account, // grove-133: fuel gauges must clamp too
		{fetched: true},
		{},
	}
	for _, a := range snapshots {
		for w := 24; w <= 100; w += 4 {
			for _, tab := range []int{costsTabSpend, costsTabAccount, costsTabClaude} {
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
	// tab again reaches CLAUDE (claude.go), and once more returns to SPEND
	// without a fetch.
	next, _ = m.handleCostsKey(tea.KeyMsg{Type: tea.KeyTab})
	next, cmd = next.(Model).handleCostsKey(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.costsTab != costsTabSpend || cmd != nil {
		t.Fatalf("tab ×3 → tab=%d cmd=%v, want SPEND + no cmd", m.costsTab, cmd)
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

// --- grove-104: per-profile key manager ---

func TestAccountKeyRows(t *testing.T) {
	profiles := map[string]*config.ModelProfile{
		"kimi":           {AuthTokenEnv: "KIMI_API_KEY"},
		"openrouter-glm": {AuthTokenEnv: "OPENROUTER_API_KEY"},
		"openrouter-alt": {AuthTokenEnv: "OPENROUTER_API_KEY"}, // shared var → one row
		"anthropic-ish":  {},                                   // no auth var → no row
		"nil-profile":    nil,
	}
	rows := accountKeyRows(profiles, func(v string) keyState {
		if v == "OPENROUTER_API_KEY" {
			return keyState{shown: "sk-or-v1-f9b...a6a", ns: "global"}
		}
		return keyState{}
	})
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want 2", rows)
	}
	// Sorted by var name: KIMI first.
	if rows[0].envVar != "KIMI_API_KEY" || rows[0].shown != "" {
		t.Errorf("row 0 = %+v, want unset KIMI_API_KEY", rows[0])
	}
	if got := strings.Join(rows[0].profiles, ","); got != "kimi" {
		t.Errorf("row 0 profiles = %q", got)
	}
	if rows[1].envVar != "OPENROUTER_API_KEY" || rows[1].shown != "sk-or-v1-f9b...a6a" || rows[1].ns != "global" {
		t.Errorf("row 1 = %+v, want masked OPENROUTER_API_KEY", rows[1])
	}
	// Shared var: both profile names, sorted.
	if got := strings.Join(rows[1].profiles, ","); got != "openrouter-alt,openrouter-glm" {
		t.Errorf("row 1 profiles = %q", got)
	}

	if got := accountKeyRows(nil, func(string) keyState { return keyState{} }); len(got) != 0 {
		t.Errorf("zero profiles → %+v, want zero rows", got)
	}
}

func TestAccountHasOpenRouter(t *testing.T) {
	if !(accountMsg{}).hasOpenRouter() {
		t.Error("zero rows (zero profiles) must keep the standalone OpenRouter view")
	}
	or := accountMsg{keys: []keyRow{{envVar: openrouter.EnvVar}}}
	if !or.hasOpenRouter() {
		t.Error("an OPENROUTER_API_KEY row must keep the extras")
	}
	kimi := accountMsg{keys: []keyRow{{envVar: "KIMI_API_KEY"}}}
	if kimi.hasOpenRouter() {
		t.Error("non-OpenRouter-only rows must drop the OpenRouter extras")
	}
}

// keyRowsModel is accountModel plus two key-manager rows: an unset kimi var
// and the resolved OpenRouter var.
func keyRowsModel(width int) Model {
	m := accountModel(width)
	m.account.keys = []keyRow{
		{envVar: "KIMI_API_KEY", profiles: []string{"kimi"}},
		{envVar: openrouter.EnvVar, profiles: []string{"openrouter-glm"}, shown: "sk-or-v1-f9b...a6a", ns: "global"},
	}
	return m
}

func TestViewAccountKeyRows(t *testing.T) {
	m := keyRowsModel(100)
	out := m.viewAccount()
	for _, want := range []string{
		"KEYS", "KIMI_API_KEY", "not set — enter to paste", "kimi",
		"OPENROUTER_API_KEY", "sk-or-v1-f9b...a6a", "openrouter-glm",
		// The OpenRouter row keeps its extras.
		"BALANCE", "$12.81 remaining", "RUNWAY",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("key-rows view missing %q", want)
		}
	}
	if strings.Contains(out, "CONNECT OPENROUTER") {
		t.Error("connect prompt must not render when key rows exist")
	}
	if strings.Contains(out, "sk-or-v1-f9b1") {
		t.Error("unmasked key rendered")
	}
	if !strings.Contains(m.viewAccountFooter(), "paste key") {
		t.Error("footer missing the enter paste-key hint")
	}

	// An unset OpenRouter row drops BALANCE/RUNWAY but keeps the rows.
	m.account.keyMasked = ""
	if out := m.viewAccount(); strings.Contains(out, "BALANCE") || !strings.Contains(out, "KEYS") {
		t.Error("unset OpenRouter key should drop BALANCE but keep KEYS")
	}
}

func TestAccountKeyRowSelection(t *testing.T) {
	m := keyRowsModel(100)
	m.mode = modeCosts

	// j moves down, clamped at the last row; k back up, clamped at 0.
	next, _ := m.handleCostsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(Model)
	if m.accountSel != 1 {
		t.Fatalf("j → sel=%d, want 1", m.accountSel)
	}
	next, _ = m.handleCostsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(Model)
	if m.accountSel != 1 {
		t.Fatalf("j at end → sel=%d, want clamp at 1", m.accountSel)
	}
	next, _ = m.handleCostsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = next.(Model)
	if m.accountSel != 0 {
		t.Fatalf("k → sel=%d, want 0", m.accountSel)
	}

	// enter on a row (set or unset) starts the paste flow.
	if _, cmd := m.handleCostsKey(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Error("enter on a key row should start the paste flow")
	}
	// p mirrors enter when rows exist — even with a resolved OpenRouter key.
	if _, cmd := m.handleCostsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")}); cmd == nil {
		t.Error("p with key rows should start the paste flow")
	}

	// A refetch that shrinks the rows clamps the cursor.
	m.accountSel = 5
	model, _ := m.Update(accountMsg{fetched: true, keys: m.account.keys})
	if got := model.(Model).accountSel; got != 0 {
		t.Errorf("accountMsg with stale sel → %d, want reset to 0", got)
	}
}

// --- grove-133: Kimi Code plan fuel gauges ---

func TestAccountKeyRowsKimiBase(t *testing.T) {
	profiles := map[string]*config.ModelProfile{
		"kimi":           {AuthTokenEnv: "KIMI_CODE_API_KEY", BaseURL: "https://api.kimi.com/coding"},
		"kimi-token":     {AuthTokenEnv: "MOONSHOT_API_KEY", BaseURL: "https://api.moonshot.ai/v1"},
		"openrouter-glm": {AuthTokenEnv: "OPENROUTER_API_KEY", BaseURL: "https://openrouter.ai/api"},
	}
	rows := accountKeyRows(profiles, func(string) keyState { return keyState{} })
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want 3", rows)
	}
	// Sorted by var: KIMI_CODE first (plan base), MOONSHOT (per-token, no
	// quota API), OPENROUTER — only the plan row gets a fuelBase.
	if rows[0].fuelBase != "https://api.kimi.com/coding" {
		t.Errorf("kimi row fuelBase = %q", rows[0].fuelBase)
	}
	if rows[1].fuelBase != "" || rows[2].fuelBase != "" {
		t.Errorf("non-plan rows got fuelBase: %+v", rows[1:])
	}
}

func TestFuelSpec(t *testing.T) {
	frac, sev, caption := fuelSpec(kimi.Window{Used: 304, Limit: 800})
	if frac != 0.62 || sev != runwayNormal || caption != "62% left" {
		t.Errorf("normal = %v %d %q", frac, sev, caption)
	}
	if _, sev, _ := fuelSpec(kimi.Window{Used: 75, Limit: 100}); sev != runwayWarm {
		t.Errorf("25%% left sev = %d, want warm", sev)
	}
	if _, sev, _ := fuelSpec(kimi.Window{Used: 95, Limit: 100}); sev != runwayAlert {
		t.Errorf("5%% left sev = %d, want alert", sev)
	}
	// Overdrawn clamps to empty; zero limit is a dim dash, never a ratio.
	if frac, sev, caption := fuelSpec(kimi.Window{Used: 120, Limit: 100}); frac != 0 || sev != runwayAlert || caption != "0% left" {
		t.Errorf("overdrawn = %v %d %q", frac, sev, caption)
	}
	if frac, sev, caption := fuelSpec(kimi.Window{Used: 5}); frac != 0 || sev != runwayDim || caption != "—" {
		t.Errorf("no-limit = %v %d %q", frac, sev, caption)
	}
}

// kimiFuelModel is keyRowsModel with the kimi row carrying a resolved key
// and fetched fuel windows — the full grove-133 rendering surface.
func kimiFuelModel(width int) Model {
	m := keyRowsModel(width)
	m.account.keys[0] = keyRow{
		envVar: "KIMI_CODE_API_KEY", profiles: []string{"kimi"}, shown: "sk-kimi-abc1...f9a", ns: "global",
		fuelBase: "https://api.kimi.com/coding",
		fuel: []kimi.Window{
			{Label: "5h", Used: 304, Limit: 800, ResetHint: "resets in 2h 20m"},
			{Label: "weekly", Used: 250000, Limit: 1000000, ResetHint: "resets in 2d"},
		},
	}
	return m
}

func TestViewAccountKimiFuel(t *testing.T) {
	m := kimiFuelModel(100)
	out := m.viewAccount()
	for _, want := range []string{
		"KIMI_CODE_API_KEY", "5h", "62% left", "resets in 2h 20m",
		"weekly", "75% left", "resets in 2d", "▓", "░",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fuel view missing %q", want)
		}
	}

	// Unset key (or failed fetch) → a single dash fuel line with the hint,
	// never an error state.
	m.account.keys[0].fuel = nil
	m.account.keys[0].fuelHint = "kimi HTTP 401"
	out = m.viewAccount()
	if !strings.Contains(out, "fuel") || !strings.Contains(out, "kimi HTTP 401") {
		t.Errorf("dash fuel line missing:\n%s", out)
	}
	if strings.Contains(out, "% left") {
		t.Error("gauges rendered with no fuel data")
	}

	// Non-kimi rows never grow fuel lines.
	plain := keyRowsModel(100)
	if out := plain.viewAccount(); strings.Contains(out, "fuel") {
		t.Error("non-kimi rows rendered a fuel line")
	}
}

// grove-79 lesson generalized: the account view with fuel rows present
// must render at every small pane size without panicking or exceeding
// the width.
func TestViewAccountKimiFuelHeightSweep(t *testing.T) {
	for _, w := range []int{24, 40, 60} {
		for h := 5; h <= 30; h++ {
			m := kimiFuelModel(w)
			m.height = h
			out := m.View() // must not panic
			for i, ln := range strings.Split(out, "\n") {
				if lw := lipgloss.Width(ln); lw > m.width && !strings.Contains(ln, "\x1b]8;;") {
					t.Errorf("w=%d h=%d: line %d is %d cells", w, h, i, lw)
				}
			}
		}
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

// scratchStore points HOME and GROVE_STATE_DIR at temp dirs and creates an
// identity, so secrets.Set works without touching the real store.
func scratchStore(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GROVE_STATE_DIR", t.TempDir())
	if _, _, err := secrets.InitIdentity(); err != nil {
		t.Fatal(err)
	}
}

// grove-366: a row with a stored preview shows it, one without shows
// `set · <type>`, and the namespace tag renders; the value never does.
func TestViewKeyRowsPreviewAndSetText(t *testing.T) {
	m := accountModel(110)
	m.account.keys = []keyRow{
		{envVar: "STRIPE_KEY", profiles: []string{"a"}, shown: "rk_live_8723***6AE2", ns: "global"},
		{envVar: "KIMI_API_KEY", profiles: []string{"kimi"}, shown: "set · Kimi key", ns: "demo"},
		{envVar: "PLAIN_KEY", profiles: []string{"p"}, shown: "set", ns: "env"},
	}
	out := m.viewAccount()
	for _, want := range []string{"rk_live_8723***6AE2", "global", "set · Kimi key", "demo", "env"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q", want)
		}
	}
	if got := setText(secrets.Row{Type: "T"}); got != "set · T" {
		t.Errorf("setText type = %q", got)
	}
	if got := setText(secrets.Row{}); got != "set" {
		t.Errorf("setText bare = %q", got)
	}
}

// keyLookup resolves through the store and pairs the sidecar text with the
// namespace that supplied the value; the secret itself never leaves it.
func TestKeyLookupFromStore(t *testing.T) {
	scratchStore(t)
	const secret = "sk-supersecretvalue-0123456789abcdef"
	if _, err := secrets.SetWithMeta(secrets.Global, "A_KEY", []byte(secret), "Alpha", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.SetWithMeta(secrets.Global, "B_KEY", []byte(secret), "Beta", 0); err != nil {
		t.Fatal(err)
	}
	look := keyLookup("demo")
	a, b, c := look("A_KEY"), look("B_KEY"), look("MISSING_KEY")
	if !strings.HasPrefix(a.shown, "sk-") || a.ns != "global" || strings.Contains(a.shown, "supersecret") {
		t.Errorf("A_KEY = %+v, want a masked preview in global", a)
	}
	if b.shown != "set · Beta" || b.ns != "global" {
		t.Errorf("B_KEY = %+v, want `set · Beta`", b)
	}
	if c.shown != "" {
		t.Errorf("MISSING_KEY = %+v, want unset", c)
	}
}

// pasteKeyCmd writes secrets/global/<VAR>.age, no .env line, and reports
// a keySavedMsg carrying `set` (no preview was opted into).
func TestPasteKeyCmdWritesStore(t *testing.T) {
	scratchStore(t)
	orig := readClipboard
	t.Cleanup(func() { readClipboard = orig })
	readClipboard = func() ([]byte, error) { return []byte("  pasted-value-xyz-0123456789\n"), nil }

	msg := pasteKeyCmd("KIMI_API_KEY")()
	saved, ok := msg.(keySavedMsg)
	if !ok {
		t.Fatalf("msg = %#v, want keySavedMsg", msg)
	}
	if saved.varName != "KIMI_API_KEY" || saved.shown != "set" {
		t.Errorf("saved = %+v", saved)
	}
	if _, err := os.Stat(filepath.Join(secrets.StoreDir(), "global", "KIMI_API_KEY.age")); err != nil {
		t.Errorf("no global .age file: %v", err)
	}
	if got := secrets.Resolve("", "KIMI_API_KEY"); got != "pasted-value-xyz-0123456789" {
		t.Errorf("resolve = %q", got)
	}
	if _, err := os.Stat(config.SecretsPath()); err == nil {
		t.Error("paste must not write the legacy .env")
	}
}
