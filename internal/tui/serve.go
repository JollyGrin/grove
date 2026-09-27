package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/serve"
	"github.com/JollyGrin/grove/internal/state"
)

// Serve in the cockpit (grove-381, feature trains Decisions 5 and 7): `s`
// on the selected FEATURES row serves that feature through the same path
// as `gv serve` (startServe in cmd/gv). A trusted run.sh starts at once;
// an untrusted or changed one opens the review modal — the script, its
// sha256, `y` trust-and-run / `esc` cancel — and nothing runs until `y`.
// A live serve window turns `s` into a stop confirmation.
//
// The rail's serve state rides the PR-cadence feature pass (featuresCmd)
// plus one pass after each start/stop — no new poll, goroutine or timer.

// Injected by cmd/gv (the serve plumbing lives there); wired at startup to
// avoid an import cycle. Unwired, `s` does nothing but say so.
var (
	// ServeScript reads the workspace's .grove/run.sh: bytes and sha256,
	// or an error wrapping os.ErrNotExist.
	ServeScript = func() ([]byte, string, error) { return nil, "", fmt.Errorf("serve not wired") }
	// ServeRunning reports whether the feature's exact ▶ <slug> window is up.
	ServeRunning = func(slug string) bool { return false }
	// ServeStart runs startServe with the cockpit's refusal as its trust
	// func (the modal records trust first) and returns the READY value.
	ServeStart = func(slug string) (string, error) { return "", fmt.Errorf("serve not wired") }
	// ServeStop kills the ▶ <slug> window; false when none was running.
	ServeStop = func(slug string) (bool, error) { return false, fmt.Errorf("serve not wired") }
	// ServeStatuses derives each feature's serve status (tmux + local git,
	// no network) — called only off the UI thread, in featuresCmd and
	// serveStatusCmd.
	ServeStatuses = func(fs []*state.Feature) map[string]serve.Status { return nil }
)

// serveReview is the modal's snapshot: the bytes shown are the bytes whose
// sha `y` trusts. startServe re-hashes run.sh, so an edit after the modal
// opened is refused rather than run unreviewed.
type serveReview struct {
	slug   string
	sha    string
	lines  []string // sanitized for display, never re-parsed
	scroll int
}

// serveDoneMsg carries a start's outcome; serveStoppedMsg a stop's;
// serveStatusMsg a fresh status pass after either.
type serveDoneMsg struct {
	slug, ready string
	err         error
}

type serveStoppedMsg struct {
	slug    string
	stopped bool
	err     error
}

type serveStatusMsg struct{ serves map[string]serve.Status }

// serveStatusCmd re-derives serve status right after a start or stop, so
// the rail need not wait for the next 30s beat.
func serveStatusCmd(features map[string]*state.Feature) tea.Cmd {
	if len(features) == 0 {
		return nil
	}
	return func() tea.Msg { return serveStatusMsg{serves: ServeStatuses(featureList(features))} }
}

func featureList(features map[string]*state.Feature) []*state.Feature {
	out := make([]*state.Feature, 0, len(features))
	for _, f := range features {
		out = append(out, f)
	}
	return out
}

// serveKey is `s` on a feature.
func (m Model) serveKey(slug string) (tea.Model, tea.Cmd) {
	if ServeRunning(slug) {
		m.serveStop = slug
		m.mode = modeServeStop
		m.flash = ""
		return m, nil
	}
	script, sha, err := ServeScript()
	if errors.Is(err, os.ErrNotExist) {
		m.flash = "no .grove/run.sh — `gv serve init` drafts one"
		return m, nil
	}
	if err != nil {
		m.flash = err.Error()
		return m, nil
	}
	ledger, err := state.LoadServe(m.stateDir)
	if err != nil {
		m.flash = err.Error()
		return m, nil
	}
	if serve.Trusted(ledger, sha) {
		return m.startServe(slug)
	}
	m.review = &serveReview{slug: slug, sha: sha, lines: reviewLines(script)}
	m.mode = modeServeReview
	m.flash = ""
	return m, nil
}

func (m Model) startServe(slug string) (tea.Model, tea.Cmd) {
	m.flash = "▶ starting " + slug + " — waiting for GROVE_READY…"
	return m, func() tea.Msg {
		ready, err := ServeStart(slug)
		return serveDoneMsg{slug: slug, ready: ready, err: err}
	}
}

// handleServeReviewKey: y trusts exactly the reviewed sha (the CLI's
// run_script_trusted event) and starts; esc/n cancel with no event; j/k
// and pages scroll. Every other key is inert — the modal never falls
// through to a list key.
func (m Model) handleServeReviewKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.review
	if r == nil {
		m.mode = m.backMode()
		return m, nil
	}
	page := m.reviewWindow()
	switch k.String() {
	case "y":
		m.mode, m.review = m.backMode(), nil
		if err := state.Append(m.stateDir, state.Event{Type: state.EvRunScriptTrusted, Data: map[string]string{"sha256": r.sha}}); err != nil {
			m.flash = err.Error()
			return m, nil
		}
		return m.startServe(r.slug)
	case "esc", "n":
		m.mode, m.review = m.backMode(), nil
		m.flash = "run.sh not trusted — nothing ran"
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		r.scroll++
	case "k", "up":
		r.scroll--
	case "pgdown", " ", "f":
		r.scroll += max(1, page)
	case "pgup", "b":
		r.scroll -= max(1, page)
	case "g", "home":
		r.scroll = 0
	case "G", "end":
		r.scroll = len(r.lines)
	}
	r.scroll = min(r.scroll, max(0, len(r.lines)-page))
	r.scroll = max(r.scroll, 0)
	return m, nil
}

// handleServeStopKey mirrors the done confirmation: y stops, anything else
// backs out.
func (m Model) handleServeStopKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	slug := m.serveStop
	m.mode, m.serveStop = m.backMode(), ""
	if k.String() != "y" || slug == "" {
		return m, nil
	}
	m.flash = "stopping " + serve.Window(slug) + "…"
	return m, func() tea.Msg {
		ok, err := ServeStop(slug)
		return serveStoppedMsg{slug: slug, stopped: ok, err: err}
	}
}

// reviewChrome is the modal's fixed rows: header, panel border ×2, title,
// sha, rule, footer.
const reviewChrome = 7

// reviewWindow is how many script lines the modal shows at this height.
func (m Model) reviewWindow() int { return max(0, m.height-reviewChrome) }

// viewServeReview is mockup E: the script, scrollable, its sha256, and the
// two answers. Fits m.height: at tiny sizes the script window shrinks to
// nothing before the title/sha rows go, and every line is truncPad-ed.
func (m Model) viewServeReview() string {
	r := m.review
	w := m.width - 4
	n := len(r.lines)
	page := m.reviewWindow()
	end := min(n, r.scroll+page)
	rows := []string{
		truncPad(sPanelTitleFocus.Render("REVIEW .grove/run.sh")+sChrome.Render(" · serve "+r.slug+" — untrusted, nothing has run"), w),
		truncPad(sChrome.Render("sha256 ")+sQuestion.Render(r.sha), w),
		truncPad(sDim.Render(fmt.Sprintf("── lines %d–%d of %d ──", min(n, r.scroll+1), end, n)), w),
	}
	numW := len(fmt.Sprint(n))
	for i := r.scroll; i < end; i++ {
		rows = append(rows, truncPad(sDim.Render(fmt.Sprintf("%*d ", numW, i+1))+r.lines[i], w))
	}
	foot := truncPad(" "+sKey.Render("y")+sFoot.Render(" trust & run")+sDim.Render(" · ")+
		sKey.Render("esc")+sFoot.Render(" cancel")+sDim.Render(" · ")+
		sKey.Render("j/k")+sFoot.Render(" scroll"), m.width)
	if m.height < 5 {
		// No room for a bordered panel: the sha and the answers, bare.
		lines := []string{truncPad(rows[1], m.width), foot}
		return strings.Join(lines[max(0, len(lines)-m.height):], "\n")
	}
	if maxRows := m.height - 4; len(rows) > maxRows {
		rows = rows[:maxRows]
	}
	panel := sPanelFocus.Width(m.width - 2).Render(strings.Join(rows, "\n"))
	return m.viewHeader() + "\n" + panel + "\n" + foot
}

// reviewLines makes the script safe to LOOK at: the operator is deciding
// whether to run these bytes, so nothing in them may restyle, hide or
// reorder the screen. Tabs expand; control bytes (ESC above all) render as
// caret notation; bidi controls (Trojan Source) and invalid UTF-8 render
// as visible codepoints.
func reviewLines(script []byte) []string {
	text := strings.TrimSuffix(string(script), "\n")
	raw := strings.Split(text, "\n")
	out := make([]string, len(raw))
	for i, line := range raw {
		var b strings.Builder
		col := 0
		for len(line) > 0 {
			r, size := utf8.DecodeRuneInString(line)
			line = line[size:]
			switch {
			case r == utf8.RuneError && size == 1:
				b.WriteString("\\x??")
			case r == '\t':
				n := 4 - col%4
				b.WriteString(strings.Repeat(" ", n))
				col += n
				continue
			case r < 0x20:
				b.WriteString("^" + string(rune(r+64)))
			case r == 0x7f:
				b.WriteString("^?")
			case unicode.Is(unicode.Bidi_Control, r) || unicode.IsControl(r) || unicode.In(r, unicode.Cf):
				fmt.Fprintf(&b, "<U+%04X>", r)
			default:
				b.WriteRune(r)
			}
			col++
		}
		out[i] = b.String()
	}
	return out
}

// serveLabel is a feature's serve state as the rail title shows it.
func serveLabel(st serve.Status, ok bool) string {
	if !ok {
		return "serve –"
	}
	switch st.State {
	case serve.StateRunning:
		where := st.URL
		if where == "" && st.Port > 0 {
			where = fmt.Sprintf(":%d", st.Port)
		}
		s := "serve ▶ " + where
		if st.Behind {
			s += " (behind tip)"
		}
		return strings.TrimRight(s, " ")
	case serve.StateStopped:
		return "serve stopped"
	case serve.StateUntrusted:
		return "serve untrusted"
	}
	return "serve –"
}
