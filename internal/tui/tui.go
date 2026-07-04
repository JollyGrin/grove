// Package tui is the grove dashboard: one screen answering "what can I
// act on right now?" Three panels — agents, mail, review queue — with inline
// reply, mirroring the jayminwest/overstory cockpit (see DESIGN.md).
package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
)

const (
	panelAgents = iota
	panelMail
	panelReview
	panelCount
)

const (
	modeList = iota
	modeDetail
	modeConfirmDone
)

type refreshMsg struct {
	tasks []*state.Task
	live  map[string]string
}
type prsMsg map[string]*github.PR
type paneTailMsg string
type actionDoneMsg struct{ err error }

type Model struct {
	cfg    *config.Config
	width  int
	height int

	tasks []*state.Task
	live  map[string]string
	prs   map[string]*github.PR

	focus int
	sel   [panelCount]int
	mode  int

	detail   *state.Task
	paneTail string
	input    textinput.Model
	nudging  bool // detail input sends a nudge instead of an answer

	flash string

	// AttachTo is consumed by main after Run returns: tmux attach replaces
	// the process, so it can't happen inside the tea loop.
	AttachTo *state.Task
}

func New(cfg *config.Config) Model {
	in := textinput.New()
	in.Placeholder = "type a reply — enter sends straight to the agent's pane"
	in.Prompt = sKey.Render("❯ ")
	in.CharLimit = 0
	return Model{cfg: cfg, live: map[string]string{}, prs: map[string]*github.PR{}, input: in}
}

func Run(cfg *config.Config) (*state.Task, error) {
	p := tea.NewProgram(New(cfg), tea.WithAltScreen())
	out, err := p.Run()
	if err != nil {
		return nil, err
	}
	m := out.(Model)
	return m.AttachTo, nil
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(refreshCmd(), prsCmd(m.cfg, nil), tickEvery(time.Second), prTickEvery())
}

// --- commands ---

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return refreshCmd()() })
}

func prTickEvery() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg { return nil })
}

func refreshCmd() tea.Cmd {
	return func() tea.Msg {
		tasks, err := state.Load(config.StateDir())
		if err != nil {
			return refreshMsg{}
		}
		active := state.Active(tasks)
		live := map[string]string{}
		for _, t := range active {
			info := detect.DetectLive(t.TmuxSession, t.TmuxWindow)
			if !info.Exists {
				live[t.Ticket] = "gone"
			} else {
				live[t.Ticket] = info.Status.String()
			}
		}
		return refreshMsg{tasks: active, live: live}
	}
}

func prsCmd(cfg *config.Config, tasks []*state.Task) tea.Cmd {
	return func() tea.Msg {
		if cfg == nil {
			return prsMsg{}
		}
		if tasks == nil {
			loaded, err := state.Load(config.StateDir())
			if err != nil {
				return prsMsg{}
			}
			tasks = state.Active(loaded)
		}
		lookups := map[string][2]string{}
		for _, t := range tasks {
			if r, ok := cfg.Repos[t.Repo]; ok {
				lookups[t.Ticket] = [2]string{r.Path, t.Branch}
			}
		}
		return prsMsg(github.FetchAll(lookups))
	}
}

func paneTailCmd(t *state.Task) tea.Cmd {
	return func() tea.Msg {
		out, _ := tmux.CapturePaneBottom(t.TmuxSession+":"+t.TmuxWindow+".1", 14)
		return paneTailMsg(out)
	}
}

// --- update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case refreshMsg:
		if msg.tasks != nil {
			m.tasks = msg.tasks
			m.live = msg.live
			if m.detail != nil { // keep detail pointed at fresh data
				for _, t := range m.tasks {
					if t.Ticket == m.detail.Ticket {
						m.detail = t
					}
				}
			}
		}
		cmds := []tea.Cmd{tickEvery(time.Second)}
		if m.mode == modeDetail && m.detail != nil {
			cmds = append(cmds, paneTailCmd(m.detail))
		}
		return m, tea.Batch(cmds...)

	case prsMsg:
		m.prs = msg
		return m, tea.Tick(30*time.Second, func(time.Time) tea.Msg { return prsCmd(m.cfg, nil)() })

	case paneTailMsg:
		m.paneTail = string(msg)
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.flash = msg.err.Error()
		} else {
			m.flash = "✓ done"
		}
		return m, refreshCmd()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeDetail {
		return m.handleDetailKey(k)
	}
	if m.mode == modeConfirmDone {
		return m.handleConfirmKey(k)
	}

	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.focus = (m.focus + 1) % panelCount
	case "shift+tab":
		m.focus = (m.focus + panelCount - 1) % panelCount
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "enter":
		if t := m.selected(); t != nil {
			m.detail = t
			m.mode = modeDetail
			m.nudging = false
			m.paneTail = ""
			m.input.SetValue("")
			m.input.Focus()
			return m, paneTailCmd(t)
		}
	case "n":
		if t := m.selected(); t != nil {
			m.detail = t
			m.mode = modeDetail
			m.nudging = true
			m.paneTail = ""
			m.input.SetValue("")
			m.input.Focus()
			return m, paneTailCmd(t)
		}
	case "a":
		if t := m.selected(); t != nil {
			m.AttachTo = t
			return m, tea.Quit
		}
	case "o":
		if t := m.selected(); t != nil {
			m.flash = m.openPreview(t)
		}
	case "p":
		if t := m.selected(); t != nil {
			if pr := m.prs[t.Ticket]; pr != nil {
				m.flash = openURL(pr.URL)
			} else {
				m.flash = t.Ticket + " has no PR yet"
			}
		}
	case "t":
		if t := m.selected(); t != nil {
			m.flash = openURL(t.URL)
		}
	case "v":
		if t := m.selected(); t != nil {
			next := state.HumanReviewing
			if t.Human == state.HumanReviewing {
				next = ""
			}
			_ = state.Append(config.StateDir(), state.Event{
				Type: state.EvHumanStatus, Ticket: t.Ticket,
				Data: map[string]string{"status": next},
			})
			if next == "" {
				m.flash = t.Ticket + " back to unreviewed"
			} else {
				m.flash = "reviewing " + t.Ticket
			}
			return m, refreshCmd()
		}
	case "d":
		if t := m.selected(); t != nil {
			m.detail = t
			m.mode = modeConfirmDone
		}
	case "r":
		m.flash = "refreshing PRs…"
		return m, prsCmd(m.cfg, m.tasks)
	}
	return m, nil
}

func (m Model) handleDetailKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeList
		m.detail = nil
		m.input.Blur()
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		t := m.detail
		pane := t.TmuxSession + ":" + t.TmuxWindow + ".1"
		var err error
		if len([]rune(text)) == 1 {
			err = tmux.SendRawKey(pane, text) // option pickers: raw, no Enter
		} else {
			err = tmux.PasteText(pane, text)
		}
		if err != nil {
			m.flash = err.Error()
			return m, nil
		}
		_ = state.Append(config.StateDir(), state.Event{Type: state.EvAnswered, Ticket: t.Ticket})
		m.flash = "✓ sent to " + t.Ticket
		m.mode = modeList
		m.detail = nil
		m.input.Blur()
		return m, refreshCmd()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return m, cmd
}

func (m Model) handleConfirmKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y":
		t := m.detail
		cfg := m.cfg
		m.mode = modeList
		m.detail = nil
		m.flash = "cleaning up " + t.Ticket + "…"
		return m, func() tea.Msg { return actionDoneMsg{err: FinishTask(cfg, t, false)} }
	default:
		m.mode = modeList
		m.detail = nil
	}
	return m, nil
}

// FinishTask is injected by cmd/gv (the done flow lives there); wired at
// startup to avoid an import cycle.
var FinishTask = func(cfg *config.Config, t *state.Task, force bool) error {
	return fmt.Errorf("done flow not wired")
}

func (m *Model) move(delta int) {
	rows := m.panelLen(m.focus)
	if rows == 0 {
		return
	}
	m.sel[m.focus] = (m.sel[m.focus] + delta + rows) % rows
}

func (m Model) panelLen(panel int) int {
	switch panel {
	case panelAgents:
		return len(m.tasks)
	case panelMail:
		return len(m.mailRows())
	default:
		return len(m.reviewRows())
	}
}

func (m Model) selected() *state.Task {
	switch m.focus {
	case panelAgents:
		if len(m.tasks) == 0 {
			return nil
		}
		return m.tasks[min(m.sel[panelAgents], len(m.tasks)-1)]
	case panelMail:
		rows := m.mailRows()
		if len(rows) == 0 {
			return nil
		}
		return rows[min(m.sel[panelMail], len(rows)-1)]
	default:
		rows := m.reviewRows()
		if len(rows) == 0 {
			return nil
		}
		return rows[min(m.sel[panelReview], len(rows)-1)]
	}
}

func (m Model) openPreview(t *state.Task) string {
	pr := m.prs[t.Ticket]
	url := ""
	switch {
	case pr != nil && pr.PreviewURL != "":
		url = pr.PreviewURL
	case pr != nil:
		// Previews live in the Vercel bot comment (one per affected app
		// on a monorepo PR); dig lazily on demand.
		if r, ok := m.cfg.Repos[t.Repo]; ok {
			if u := github.PreviewURL(r.Path, pr.Number); u != "" {
				url = u
				break
			}
		}
		url = pr.URL
	default:
		url = t.URL // no delivery yet — fall back to the Linear ticket
	}
	return openURL(url)
}

func openURL(url string) string {
	if url == "" {
		return "no URL"
	}
	if err := exec.Command("open", url).Start(); err != nil {
		return err.Error()
	}
	return "opened " + url
}

// mailRows: tasks demanding a human decision.
func (m Model) mailRows() []*state.Task {
	var rows []*state.Task
	for _, t := range m.tasks {
		if t.Agent == state.AgentWaiting || t.Agent == state.AgentBlocked || t.Agent == state.AgentDead ||
			(t.Agent == state.AgentIdle && t.Sentinel == "none") {
			rows = append(rows, t)
		}
	}
	return rows
}

// reviewRows: delivery output ready for human eyes — fresh first, then
// rows already marked reviewing. A row leaves the queue when the PR merges
// (press d) or when feedback sends the agent back to work.
func (m Model) reviewRows() []*state.Task {
	var fresh, marked []*state.Task
	for _, t := range m.tasks {
		pr := m.prs[t.Ticket]
		if (t.Sentinel == "done") || (pr != nil && pr.State != "CLOSED") {
			if t.Human == state.HumanReviewing {
				marked = append(marked, t)
			} else {
				fresh = append(fresh, t)
			}
		}
	}
	return append(fresh, marked...)
}

// doneBlurb extracts the agent's DONE paragraph for the review queue.
func doneBlurb(t *state.Task) string {
	if i := strings.Index(t.LastMessage, "STATUS: DONE"); i >= 0 {
		s := t.LastMessage[i:]
		if j := strings.IndexAny(s, "—–-"); j >= 0 {
			return strings.TrimSpace(strings.SplitN(s[j:], "\n", 2)[0])
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
