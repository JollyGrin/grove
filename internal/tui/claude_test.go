package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/account"
	"github.com/JollyGrin/grove/internal/state"
)

// --- claude-accounts 07: the CLAUDE tab ---

var claudeNow = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) // a Sunday

// claudeScratch points the account store at a scratch state dir with a
// token account "alt" beside login, and counts the tab's reads.
func claudeScratch(t *testing.T) (account.Store, *int) {
	t.Helper()
	t.Setenv("GROVE_STATE_DIR", t.TempDir())
	s := claudeStore()
	if _, err := s.Add("alt", strings.NewReader("sk-ant-oat01-test"), time.Now()); err != nil {
		t.Fatal(err)
	}
	reads := 0
	prev := newClaudeCmd
	newClaudeCmd = func(tasks []*state.Task) tea.Cmd { reads++; return prev(tasks) }
	t.Cleanup(func() { newClaudeCmd = prev })
	return s, &reads
}

func writeUsage(t *testing.T, s account.Store, name, body string) {
	t.Helper()
	if err := os.MkdirAll(s.UsageDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.UsagePath(name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func claudeModel(width int) Model {
	m := accountModel(width)
	m.costsTab = costsTabClaude
	return m
}

func TestCostsTabCyclesThree(t *testing.T) {
	_, reads := claudeScratch(t)
	m := claudeModel(80)
	m.costsTab = costsTabSpend
	tab := tea.KeyMsg{Type: tea.KeyTab}

	next, cmd := m.handleCostsKey(tab)
	m = next.(Model)
	if m.costsTab != costsTabAccount || cmd == nil || *reads != 0 {
		t.Fatalf("tab → tab=%d cmd=%v reads=%d, want ACCOUNT + its fetch", m.costsTab, cmd, *reads)
	}
	next, cmd = m.handleCostsKey(tab)
	m = next.(Model)
	if m.costsTab != costsTabClaude || cmd == nil || *reads != 1 {
		t.Fatalf("tab tab → tab=%d cmd=%v reads=%d, want CLAUDE + one read", m.costsTab, cmd, *reads)
	}
	if !strings.Contains(m.costsTabBar(), "CLAUDE") {
		t.Error("tab bar missing CLAUDE")
	}
	next, cmd = m.handleCostsKey(tab)
	m = next.(Model)
	if m.costsTab != costsTabSpend || cmd != nil || *reads != 1 {
		t.Fatalf("third tab → tab=%d cmd=%v reads=%d, want SPEND, no read", m.costsTab, cmd, *reads)
	}

	// r on CLAUDE rereads, and never touches the ledger recorder.
	m.costsTab = costsTabClaude
	next, cmd = m.handleCostsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = next.(Model)
	if cmd == nil || *reads != 2 || m.costs.recording {
		t.Errorf("r on CLAUDE: cmd=%v reads=%d recording=%v, want one reread", cmd, *reads, m.costs.recording)
	}

	// $ from the fleet list still lands on SPEND.
	m.mode = modeList
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("$")})
	m = next.(Model)
	if m.mode != modeCosts || m.costsTab != costsTabSpend {
		t.Errorf("$ → mode=%d tab=%d, want costs/SPEND", m.mode, m.costsTab)
	}
}

// The cockpit RAM rule: the 1s beat and its refresh never reread usage.
func TestClaudeNoReadOnTick(t *testing.T) {
	_, reads := claudeScratch(t)
	m := claudeModel(80)
	m.claude = claudeMsg{fetched: true}
	for _, msg := range []tea.Msg{tickMsg{}, refreshMsg{}, tickMsg{}} {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	if *reads != 0 {
		t.Errorf("tick fired %d usage reads, want 0", *reads)
	}
}

func TestClaudeNoDataWithoutUsageFile(t *testing.T) {
	claudeScratch(t)
	msg := claudeCmd(nil)().(claudeMsg)
	if !msg.fetched || msg.err != "" || len(msg.rows) != 2 || msg.rows[0].Name != account.Login {
		t.Fatalf("snapshot = %+v, want login then alt", msg)
	}
	m := claudeModel(100)
	m.claude = msg
	out := m.viewClaude()
	if n := strings.Count(out, "no data — "+account.HintNoSession); n != 2 {
		t.Errorf("want no data + hint on both rows, got %d:\n%s", n, out)
	}
	if strings.Contains(out, "0%") {
		t.Errorf("no data must never read as a percentage:\n%s", out)
	}
}

func TestClaudeStaleResetsAtRendersReset(t *testing.T) {
	s, _ := claudeScratch(t)
	now := time.Now()
	past := strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)
	future := strconv.FormatInt(now.Add(3*24*time.Hour).Unix(), 10)
	writeUsage(t, s, "alt", `{"five_hour":{"used_percentage":97,"resets_at":`+past+`},"seven_day":{"used_percentage":40,"resets_at":`+future+`},"seen_at":"`+now.Add(-2*time.Hour).UTC().Format(time.RFC3339)+`"}`)
	m := claudeModel(100)
	m.claude = claudeCmd(nil)().(claudeMsg)
	out := m.viewClaude()
	for _, want := range []string{"100% left · reset since last seen", "60% left · resets", "as of 2h00m"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "3% left") {
		t.Errorf("a window past resets_at must not show its old 97%%:\n%s", out)
	}
}

// twoAccounts is a fixed snapshot: login active with live windows, alt
// with a reset 5h, a stale 7d, and one pinned task.
func twoAccounts() claudeMsg {
	at := func(d time.Duration) *time.Time { t := claudeNow.Add(d); return &t }
	pct := func(v float64) *float64 { return &v }
	return claudeMsg{fetched: true, now: claudeNow, loc: time.UTC, rows: []account.Row{
		{Name: account.Login, Active: true, Email: "me@example.com", Pinned: 2, Usage: &account.Usage{
			FiveHour: &account.UsageWindow{Name: account.FiveHour, State: account.StateLive, UsedPercentage: pct(42), ResetsAt: at(2*time.Hour + 10*time.Minute)},
			SevenDay: &account.UsageWindow{Name: account.SevenDay, State: account.StateLive, UsedPercentage: pct(93), ResetsAt: at(3 * 24 * time.Hour)},
			SeenAt:   at(-4 * time.Minute),
		}},
		{Name: "alt", Pinned: 1, Usage: &account.Usage{
			FiveHour: &account.UsageWindow{Name: account.FiveHour, State: account.StateReset, UsedPercentage: pct(0)},
			SevenDay: &account.UsageWindow{Name: account.SevenDay, State: account.StateLive, UsedPercentage: pct(10), Stale: true},
			SeenAt:   at(-8 * 24 * time.Hour),
		}},
	}}
}

func TestViewClaudeTwoAccountsSnapshot(t *testing.T) {
	m := claudeModel(100)
	m.claude = twoAccounts()
	got := m.viewClaudeRows(96)
	path := filepath.Join("testdata", "claude-tab-two-accounts.golden")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("CLAUDE rows drifted (go test ./internal/tui -run TestViewClaudeTwoAccountsSnapshot -update):\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// Every line clamps to the pane, at any width, in every state.
func TestViewClaudeClampWidth(t *testing.T) {
	for _, c := range []claudeMsg{twoAccounts(), {fetched: true, err: "boom"}, {}} {
		for w := 24; w <= 100; w += 4 {
			m := claudeModel(w)
			m.claude = c
			for i, ln := range strings.Split(m.viewCosts(), "\n") {
				if lw := lipgloss.Width(ln); lw > m.width {
					t.Errorf("w=%d: line %d is %d cells", w, i, lw)
				}
			}
		}
	}
}

func TestClaudeUseConfirm(t *testing.T) {
	s, reads := claudeScratch(t)
	m := claudeModel(100)
	m.claude = claudeCmd(nil)().(claudeMsg)
	key := func(r string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)} }
	press := func(k tea.KeyMsg) tea.Cmd {
		next, cmd := m.handleCostsKey(k)
		m = next.(Model)
		return cmd
	}

	// u on the active row (login) is a no-op with a flash.
	if cmd := press(key("u")); cmd != nil || m.claudeUse != "" {
		t.Fatalf("u on the active account armed=%q cmd=%v", m.claudeUse, cmd)
	}
	// j selects alt; u arms the confirm and the footer asks.
	press(key("j"))
	if cmd := press(key("u")); cmd != nil || m.claudeUse != "alt" {
		t.Fatalf("u on alt: armed=%q cmd=%v", m.claudeUse, cmd)
	}
	if f := m.viewClaudeFooter(); !strings.Contains(f, "make alt the active account?") {
		t.Errorf("footer = %q, want the confirm", f)
	}
	// Anything but y cancels — esc included, without leaving the page.
	if cmd := press(tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil || m.claudeUse != "" || m.mode != modeCosts {
		t.Fatalf("esc: armed=%q mode=%d cmd=%v", m.claudeUse, m.mode, cmd)
	}
	if active, _ := s.Active(); active != "" {
		t.Fatalf("cancel switched the account to %q", active)
	}
	// u, y switches and rereads.
	press(key("u"))
	cmd := press(key("y"))
	if cmd == nil {
		t.Fatal("y fired nothing")
	}
	next, reread := m.Update(cmd())
	m = next.(Model)
	if active, _ := s.Active(); active != "alt" {
		t.Errorf("active = %q, want alt", active)
	}
	if reread == nil || *reads != 1 || !strings.Contains(m.flash, "active account: alt") {
		t.Errorf("after use: reads=%d flash=%q", *reads, m.flash)
	}
}
