package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/cost"
	"github.com/JollyGrin/grove/internal/kimi"
	"github.com/JollyGrin/grove/internal/ledger"
	"github.com/JollyGrin/grove/internal/openrouter"
	"github.com/JollyGrin/grove/internal/secrets"
	"github.com/JollyGrin/grove/internal/state"
)

// The costs page's tabs (grove-87): `$` always lands on SPEND; tab cycles.
const (
	costsTabSpend = iota
	costsTabAccount
	costsTabClaude // claude-accounts §Decision 8 (claude.go)
	costsTabCount
)

// accountMsg is the ACCOUNT tab's one-shot snapshot: OpenRouter account
// state next to grove's own ledger/transcript estimates. Fetched on tab
// open and on r — never polled, never re-fired by the 1s tick (cockpit RAM
// rule: the two HTTP calls happen only when the operator asks).
type accountMsg struct {
	fetched   bool
	keyMasked string // OpenRouter key mask; "" = unresolved → connect prompt (zero-profile mode)
	hint      string // dim reason when OpenRouter columns are dashes (offline, non-200)

	// keys is the per-profile key manager (grove-104): one row per distinct
	// auth_token_env across configured model_profiles, sorted by var name.
	// Empty = zero profiles configured → the standalone OpenRouter view.
	keys []keyRow

	creditsOK bool
	credits   openrouter.Credits
	usageOK   bool
	usage     openrouter.KeyUsage

	estDay, estWeek, estMonth, estYear float64
	byModel                            []cost.ModelSpend // 30d per-model grove estimates, biggest first
}

// keyRow is one env var the key manager shows: which profiles reference it
// and what to show for its value when it resolves (process env or the key
// store). shown "" = not set; otherwise it is the opt-in preview or
// `set · <type>` / `set`. The value itself is never carried.
type keyRow struct {
	envVar   string
	profiles []string // sorted profile names sharing this var
	shown    string
	ns       string // namespace tag: "global", a workspace label, or "env"

	// Kimi Code plan fuel (grove-133): fuelBase is the profile base_url
	// when a profile on this row targets the plan endpoint ("" = not a
	// kimi row). fuel holds the fetched quota windows; empty with a set
	// fuelBase renders as a dash gauge (unset key, failed fetch, or an
	// unrecognizable payload), with fuelHint as the dim reason.
	fuelBase string
	fuel     []kimi.Window
	fuelHint string
}

// hasOpenRouter reports whether the OpenRouter extras (balance, runway,
// top-up link) apply: a profile row references OPENROUTER_API_KEY, or zero
// profiles are configured (the grove-87 standalone view is the fallback).
func (a accountMsg) hasOpenRouter() bool {
	if len(a.keys) == 0 {
		return true
	}
	for _, r := range a.keys {
		if r.envVar == openrouter.EnvVar {
			return true
		}
	}
	return false
}

// accountKeyRows builds the key-manager rows: one per distinct
// auth_token_env (several profiles may share one var), sorted by var name.
// lookup maps a var name to its key-store state; only the namespace and
// display text survive into the row, never the value.
func accountKeyRows(profiles map[string]*config.ModelProfile, lookup func(string) keyState) []keyRow {
	byVar := map[string][]string{}
	for name, p := range profiles {
		if p == nil || strings.TrimSpace(p.AuthTokenEnv) == "" {
			continue
		}
		byVar[p.AuthTokenEnv] = append(byVar[p.AuthTokenEnv], name)
	}
	vars := make([]string, 0, len(byVar))
	for v := range byVar {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	rows := make([]keyRow, 0, len(vars))
	for _, v := range vars {
		names := byVar[v]
		sort.Strings(names)
		st := lookup(v)
		// Kimi Code plan rows get fuel gauges (grove-133): first profile
		// (sorted, so deterministic) whose base_url targets the plan wins.
		base := ""
		for _, n := range names {
			if p := profiles[n]; p != nil && kimi.IsPlanBaseURL(p.BaseURL) {
				base = p.BaseURL
				break
			}
		}
		rows = append(rows, keyRow{envVar: v, profiles: names, shown: st.shown, ns: st.ns, fuelBase: base})
	}
	return rows
}

// keyState is what the key manager learns about one var: shown is ""
// when unresolved, else the preview or `set · <type>` / `set`.
type keyState struct{ shown, ns string }

// keyLookup resolves each var through secrets.Resolve for label and pairs
// it with the sidecar preview/type (names only, no decrypt) of the
// namespace that supplied it. A value from the process env has no
// sidecar and renders plain `set`.
func keyLookup(label string) func(string) keyState {
	meta := map[[2]string]secrets.Row{}
	if rows, err := secrets.Rows(label); err == nil {
		for _, r := range rows {
			meta[[2]string{r.Namespace, r.Name}] = r
		}
	}
	return func(v string) keyState {
		val, ns, err := secrets.Lookup(label, v)
		if err != nil || val == "" {
			return keyState{}
		}
		return keyState{shown: setText(meta[[2]string{ns, v}]), ns: ns}
	}
}

// setText is the row's value text: the opt-in preview when stored, else
// `set · <type>`, else `set`.
func setText(r secrets.Row) string {
	switch {
	case r.Preview != "":
		return r.Preview
	case r.Type != "":
		return "set · " + r.Type
	}
	return "set"
}

// keySavedMsg reports the paste-key flow persisted a key; the handler
// flashes the masked form and re-fetches the tab.
type keySavedMsg struct{ varName, shown string }

// accountCmd assembles the ACCOUNT snapshot off the tea loop. The grove
// side (windows + per-model totals) always computes — live transcripts via
// the shared cache plus ledger deltas for pruned tickets, the same pipeline
// costsCmd charts. The OpenRouter side degrades to dashes on a missing key
// or failed fetch; never an error state, never blocks the tab.
func accountCmd(cfg *config.Config, stateDir, label string, tasks []*state.Task, cache *cost.Cache) tea.Cmd {
	return func() tea.Msg {
		msg := accountMsg{fetched: true}
		now := time.Now()
		since30 := now.AddDate(0, 0, -30)

		var points []cost.Point
		live := map[string]bool{}
		byModel := map[string]float64{}
		for _, t := range tasks {
			entries, err := cache.UsageForTask(t.Worktree)
			if err != nil || len(entries) == 0 {
				continue
			}
			live[t.Ticket] = true
			points = append(points, cost.Points(entries)...)
			for model, usd := range cost.SpendByModel(entries, since30) {
				byModel[model] += usd
			}
		}
		all, _ := ledger.Read(stateDir)
		points = append(points, ledger.DeltaPoints(all, live)...)
		for model, usd := range ledger.ModelDeltaSpend(all, live, since30) {
			byModel[model] += usd
		}
		msg.estDay, msg.estWeek, msg.estMonth, msg.estYear = cost.WindowSums(points, now)
		msg.byModel = cost.SortedModelSpend(byModel)

		// Key rows read the key store here — on tab open / r / after save,
		// exactly like the OpenRouter fetch (cockpit RAM rule: no cache).
		var profiles map[string]*config.ModelProfile
		if cfg != nil {
			profiles = cfg.ModelProfiles
		}
		msg.keys = accountKeyRows(profiles, keyLookup(label))

		// Kimi Code plan fuel (grove-133): one-shot quota fetch per kimi
		// row whose key resolves — same cadence as the OpenRouter calls,
		// never polled. Any failure degrades to a dash gauge with a hint.
		for i := range msg.keys {
			r := &msg.keys[i]
			if r.fuelBase == "" || r.shown == "" {
				continue
			}
			ws, err := kimi.Usages(r.fuelBase, secrets.Resolve(label, r.envVar))
			if err != nil {
				r.fuelHint = err.Error()
				continue
			}
			if len(ws) == 0 {
				r.fuelHint = "no usage data"
			}
			r.fuel = ws
		}

		// OpenRouter extras only apply to the OpenRouter row (or the
		// zero-profile standalone view) — no balance API is assumed for
		// other providers.
		if !msg.hasOpenRouter() {
			return msg
		}
		key := secrets.Resolve(label, openrouter.EnvVar)
		if key == "" {
			return msg
		}
		msg.keyMasked = openrouter.Mask(key)
		if c, err := openrouter.FetchCredits(openrouter.DefaultBaseURL, key); err == nil {
			msg.creditsOK, msg.credits = true, c
		} else {
			msg.hint = err.Error()
		}
		if u, err := openrouter.FetchKeyUsage(openrouter.DefaultBaseURL, key); err == nil {
			msg.usageOK, msg.usage = true, u
		} else if msg.hint == "" {
			msg.hint = err.Error()
		}
		return msg
	}
}

// readClipboard is the paste flow's clipboard seam (tests stub it); only
// macOS has pbpaste.
var readClipboard = func() ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, errNoPbpaste
	}
	return exec.Command("pbpaste").Output()
}

var errNoPbpaste = errors.New("no pbpaste")

// pasteKeyCmd is the connect flow (grove-87, generalized grove-104): read
// the clipboard and persist the candidate as varName in the key store's
// global namespace (keys are billing accounts, not per-workspace).
// OpenRouter keys are validated live against /api/v1/key first; other
// providers have no assumed API, so their keys save as pasted. An
// unreadable or rejected key is never saved, and the full key never
// renders — the saved flash carries the stored preview or `set`, nothing
// derived from the value.
func pasteKeyCmd(varName string) tea.Cmd {
	return func() tea.Msg {
		out, err := readClipboard()
		if errors.Is(err, errNoPbpaste) {
			return flashMsg("paste needs pbpaste (macOS) — run `gv keys set " + varName + "` instead")
		}
		if err != nil {
			return flashMsg("clipboard read failed: " + err.Error())
		}
		key := strings.TrimSpace(string(out))
		if key == "" {
			return flashMsg("clipboard is empty — copy your " + varName + " value first")
		}
		if varName == openrouter.EnvVar {
			if err := openrouter.ValidateKey(openrouter.DefaultBaseURL, key); err != nil {
				return flashMsg("key rejected (" + err.Error() + ") — not saved")
			}
		}
		// SetWithMeta (Set + sidecar) so a re-paste drops the previous
		// key's stored preview instead of showing it for the new value.
		if _, err := secrets.SetWithMeta(secrets.Global, varName, []byte(key), "", 0); err != nil {
			return flashMsg("save failed: " + err.Error() + " (gv keys init?)")
		}
		shown := "set"
		if m, err := secrets.ReadMeta(secrets.Global, varName); err == nil {
			shown = setText(secrets.Row{Type: m.Type, Preview: m.Preview})
		}
		return keySavedMsg{varName: varName, shown: shown}
	}
}

// --- runway gauge ---

// Runway severity, colored by time-left rather than dollars. Tank mode and
// ∞ (zero burn) carry no urgency signal, so they render dim/neutral.
const (
	runwayNormal = iota // > 2 weeks
	runwayWarm          // ≤ 2 weeks
	runwayAlert         // ≤ 3 days
	runwayDim           // ∞ at zero burn
)

// runwayHorizonWeeks fixes the gauge scale: full bar = 4 weeks, forever —
// a half bar always means ~2 weeks, regardless of balance size.
const runwayHorizonWeeks = 4.0

type runwayGauge struct {
	ok      bool // false → render "—" with the account hint
	frac    float64
	sev     int
	caption string
}

// runwaySpec computes the RUNWAY line. tankUSD > 0 switches to flat fuel
// mode (bar = remaining ÷ tank); otherwise runway = remaining ÷ weekly
// burn, falling back to usage_daily × 7 when there is no weekly history,
// and to a dim ∞ when there is no burn at all.
func runwaySpec(creditsOK, usageOK bool, remaining float64, u openrouter.KeyUsage, tankUSD float64) runwayGauge {
	if !creditsOK {
		return runwayGauge{}
	}
	if remaining < 0 {
		remaining = 0
	}
	if tankUSD > 0 {
		return runwayGauge{
			ok: true, frac: remaining / tankUSD, sev: runwayNormal,
			caption: fmt.Sprintf("$%.2f of $%.2f tank", remaining, tankUSD),
		}
	}
	if !usageOK {
		return runwayGauge{}
	}
	weekly := u.Weekly
	if weekly <= 0 {
		weekly = u.Daily * 7
	}
	if weekly <= 0 {
		return runwayGauge{ok: true, frac: 1, sev: runwayDim, caption: "∞ at current burn"}
	}
	weeks := remaining / weekly
	sev := runwayNormal
	switch {
	case weeks*7 <= 3:
		sev = runwayAlert
	case weeks <= 2:
		sev = runwayWarm
	}
	return runwayGauge{
		ok: true, frac: weeks / runwayHorizonWeeks, sev: sev,
		caption: fmt.Sprintf("~%.1f wk at current burn ($%.2f/wk)", weeks, weekly),
	}
}

func runwayStyle(sev int) func(...string) string {
	switch sev {
	case runwayWarm:
		return sWaiting.Render
	case runwayAlert:
		return sBlocked.Render
	case runwayDim:
		return sDim.Render
	default:
		return sWorking.Render
	}
}

// gaugeBar renders a fixed-width fill gauge — ▓ for the filled fraction, ░
// for the rest. Unlike cost.Bar, the empty remainder is drawn: a half-full
// tank must read as half of something.
func gaugeBar(frac float64, width int) string {
	if width <= 0 {
		return ""
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	n := int(frac*float64(width) + 0.5)
	if frac > 0 && n == 0 {
		n = 1
	}
	return strings.Repeat("▓", n) + strings.Repeat("░", width-n)
}

// hyperlink wraps text in an OSC 8 terminal hyperlink. Footer-only: reflow's
// ANSI width accounting miscounts the OSC 8 payload as printable, so linked
// text must never pass through lipgloss width handling (panels, truncPad).
func hyperlink(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

func (m Model) tankUSD() float64 {
	if m.cfg == nil {
		return 0
	}
	return m.cfg.Cost.OpenRouter.TankUSD
}

// --- view ---

// costsTabBar is the costs page's growth structure (grove-87): plain header
// + switch, no framework. Short by design — it must fit the narrowest panes
// (viewHeader has no width clamp; grove-56 lesson applies to every header).
func (m Model) costsTabBar() string {
	names := []string{"SPEND", "ACCOUNT", "CLAUDE"}
	for i, n := range names {
		if i == m.costsTab {
			names[i] = sPanelTitleFocus.Render(n)
		} else {
			names[i] = sPanelTitle.Render(n)
		}
	}
	return truncPad(strings.Join(names, sDim.Render(" │ ")), m.width-4)
}

func (m Model) viewAccount() string {
	w := m.width - 4
	a := m.account

	sections := []string{m.costsTabBar()}
	switch {
	case !a.fetched:
		sections = append(sections, sDim.Render("fetching account…"))
	case len(a.keys) > 0:
		// Profiles configured → the per-profile key manager (grove-104);
		// the OpenRouter row keeps its balance/runway extras below.
		sections = append(sections, m.viewKeyRows(w))
		if a.keyMasked != "" {
			sections = append(sections, m.viewBalanceRunway(w))
		}
	case a.keyMasked == "":
		sections = append(sections, viewConnectPrompt(w))
	default:
		sections = append(sections, m.viewBalanceRunway(w))
	}
	sections = append(sections, m.viewAccountWindows(w))
	if s := m.viewAccountByModel(w); s != "" {
		sections = append(sections, s)
	}
	if a.hasOpenRouter() {
		sections = append(sections, truncPad(
			sKey.Render("o")+sFoot.Render(" top-up ↗ ")+
				sDelivery.Render(strings.TrimPrefix(openrouter.CreditsURL, "https://")), w))
	}

	body := sPanelFocus.Width(m.width - 2).Render(strings.Join(sections, "\n\n"))
	return m.viewHeader() + "\n" + body + "\n" + m.viewAccountFooter()
}

// viewKeyRows renders the key manager (grove-104): one selectable row per
// distinct auth_token_env — var name, masked value (or an obvious unset
// state), and the profiles that use it. Stars only: the full key never
// renders, and non-OpenRouter rows assume no balance API. Kimi Code plan
// rows carry fuel gauges underneath (grove-133).
func (m Model) viewKeyRows(w int) string {
	rows := []string{sPanelTitleFocus.Render("KEYS")}
	for i, r := range m.account.keys {
		cur := "  "
		if i == m.accountSel {
			cur = sKey.Render("▸ ")
		}
		val := sWaiting.Render(pad("not set — enter to paste", 26))
		tag := ""
		if r.shown != "" {
			val = sWorking.Render(pad(r.shown, 26))
			tag = sDim.Render(pad(r.ns, 8))
		}
		line := cur + pad(r.envVar, 22) + val + tag + sDim.Render(strings.Join(r.profiles, ", "))
		rows = append(rows, truncPad(line, w))
		if r.fuelBase != "" {
			rows = append(rows, viewFuelRows(r, w)...)
		}
	}
	return strings.Join(rows, "\n")
}

// --- kimi fuel gauges (grove-133) ---

// fuelSpec turns one quota window into gauge terms: fraction of quota
// left, runway-family severity by that fraction (≤10% alert, ≤30% warm),
// and the percent caption. A zero limit carries no ratio — dim dash.
func fuelSpec(fw kimi.Window) (frac float64, sev int, caption string) {
	if fw.Limit <= 0 {
		return 0, runwayDim, "—"
	}
	rem := fw.Limit - fw.Used
	if rem < 0 {
		rem = 0
	}
	frac = float64(rem) / float64(fw.Limit)
	switch {
	case frac <= 0.1:
		sev = runwayAlert
	case frac <= 0.3:
		sev = runwayWarm
	default:
		sev = runwayNormal
	}
	return frac, sev, fmt.Sprintf("%.0f%% left", frac*100)
}

// viewFuelRows renders a kimi row's plan fuel under its KEYS line: one
// gauge per window (`5h  ▓▓▓▓▓░░░  62% left · resets in 2h 20m`), or a
// single dash line when nothing was fetched (unset key, failed fetch).
func viewFuelRows(r keyRow, w int) []string {
	if len(r.fuel) == 0 {
		line := "    " + sChrome.Render(pad("fuel", 8)) + "—"
		if r.fuelHint != "" {
			line += "  " + sDim.Render(r.fuelHint)
		}
		return []string{truncPad(line, w)}
	}
	out := make([]string, 0, len(r.fuel))
	for _, fw := range r.fuel {
		out = append(out, fuelLine(fw, w))
	}
	return out
}

// fuelLine renders one quota window as a fuel gauge; the CLAUDE tab
// reuses it for the 5h / 7d windows.
func fuelLine(fw kimi.Window, w int) string {
	frac, sev, caption := fuelSpec(fw)
	line := "    " + sChrome.Render(pad(trunc(fw.Label, 7), 8)) +
		runwayStyle(sev)(gaugeBar(frac, 16)) + "  " + caption
	if fw.ResetHint != "" {
		line += sDim.Render(" · " + fw.ResetHint)
	}
	return truncPad(line, w)
}

// viewConnectPrompt replaces BALANCE/RUNWAY when no key resolves: explain
// what's missing and offer the paste flow. The grove estimates below still
// render — a missing key never blanks the tab.
func viewConnectPrompt(w int) string {
	lines := []string{
		sPanelTitleFocus.Render("CONNECT OPENROUTER"),
		truncPad(sChrome.Render("no API key found — balance and the OPENROUTER column need one"), w),
		truncPad(sChrome.Render("run ")+sDelivery.Render("gv keys set OPENROUTER_API_KEY")+
			sChrome.Render(", or"), w),
		truncPad(sKey.Render("p")+sChrome.Render(" — paste key from clipboard (validated live, then saved in the key store)"), w),
	}
	return strings.Join(lines, "\n")
}

func (m Model) viewBalanceRunway(w int) string {
	a := m.account

	bal := pad("BALANCE", 10)
	if a.creditsOK {
		bal += sWorking.Render(fmt.Sprintf("$%.2f remaining", a.credits.Remaining())) +
			sDim.Render(fmt.Sprintf("   ($%.2f purchased · $%.2f used, lifetime)",
				a.credits.TotalCredits, a.credits.TotalUsage))
	} else {
		bal += "—  " + sDim.Render(a.hint)
	}

	run := pad("RUNWAY", 10)
	spec := runwaySpec(a.creditsOK, a.usageOK, a.credits.Remaining(), a.usage, m.tankUSD())
	if spec.ok {
		run += runwayStyle(spec.sev)(gaugeBar(spec.frac, 21)) + "  " + sChrome.Render(spec.caption)
	} else {
		run += "—  " + sDim.Render(a.hint)
	}
	return truncPad(bal, w) + "\n" + truncPad(run, w)
}

func (m Model) viewAccountWindows(w int) string {
	a := m.account
	or := func(v float64) string {
		if !a.usageOK {
			return "—"
		}
		return fmt.Sprintf("$%.2f", v)
	}
	rows := []string{sHeaderCol.Render(truncPad(pad("WINDOW", 14)+pad("OPENROUTER", 16)+"GROVE EST (all providers)", w))}
	add := func(label, orV string, est float64) {
		rows = append(rows, truncPad(sChrome.Render(pad(label, 14))+pad(orV, 16)+
			sWorking.Render(fmt.Sprintf("$%.2f", est)), w))
	}
	add("today", or(a.usage.Daily), a.estDay)
	add("this week", or(a.usage.Weekly), a.estWeek)
	add("this month", or(a.usage.Monthly), a.estMonth)
	add("this year", "—", a.estYear) // OpenRouter has no yearly window — ledger-only
	return strings.Join(rows, "\n")
}

func (m Model) viewAccountByModel(w int) string {
	models := m.account.byModel
	if len(models) == 0 || models[0].USD <= 0 {
		return ""
	}
	const shownMax = 6
	shown := models
	if len(shown) > shownMax {
		shown = shown[:shownMax]
	}
	barW := w - 24
	if barW > 20 {
		barW = 20
	}
	if barW < 4 {
		barW = 4
	}
	rows := []string{sPanelTitle.Render("BY MODEL · 30d (grove est)")}
	max := models[0].USD
	for _, ms := range shown {
		rows = append(rows, truncPad(pad(trunc(ms.Model, 12), 13)+
			sDelivery.Render(gaugeBar(ms.USD/max, barW))+
			sChrome.Render(fmt.Sprintf("  $%.2f", ms.USD)), w))
	}
	if n := len(models) - len(shown); n > 0 {
		rows = append(rows, sDim.Render(fmt.Sprintf("  +%d more", n)))
	}
	return strings.Join(rows, "\n")
}

func (m Model) viewAccountFooter() string {
	a := m.account
	keys := []string{sKey.Render("tab") + sFoot.Render(" claude")}
	if len(a.keys) > 0 {
		keys = append(keys, sKey.Render("enter")+sFoot.Render(" paste key"))
	} else if a.fetched && a.keyMasked == "" {
		keys = append(keys, sKey.Render("p")+sFoot.Render(" paste key"))
	}
	keys = append(keys,
		sKey.Render("r")+sFoot.Render(" refetch"),
		sKey.Render("o")+sFoot.Render(" ")+hyperlink(openrouter.CreditsURL, sFoot.Render("top-up ↗")),
		sKey.Render("esc")+sFoot.Render(" back"),
	)
	if a.keyMasked != "" && len(a.keys) == 0 {
		keys = append(keys, sDim.Render("key "+a.keyMasked))
	}
	keys = append(keys, sDim.Render("estimates, not billing"))
	line := " " + strings.Join(keys, sDim.Render(" · "))
	if m.flash != "" {
		line += "   " + sChrome.Render(m.flash)
	}
	return line
}
