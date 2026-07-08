// Package tui is the grove dashboard: one screen answering "what can I
// act on right now?" — the AGENTS list (attach/manage launcher) over an
// ACTIVITY feed (newest-first render of events.jsonl), with inline reply.
// Mail/review panels were dropped per grove-cockpit-design §2/§5: their
// signal lives in the header counts + AGENTS columns + the feed.
package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/cost"
	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/resource"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
)

const (
	modeList = iota
	modeDetail
	modeConfirmDone
	modeCosts
)

type refreshMsg struct {
	tasks   []*state.Task
	live    map[string]string
	events  []state.Event
	mem     resource.Mem
	workers int
}
type flashMsg string
type prsMsg map[string]*github.PR
type paneTailMsg string
type actionDoneMsg struct {
	err    error
	ticket string // for the J2 done ritual (grove-22)
}

type Model struct {
	cfg      *config.Config
	stateDir string
	label    string // ambient workspace label; "" = legacy global fleet
	width    int
	height   int

	tasks  []*state.Task
	live   map[string]string
	prs    map[string]*github.PR
	events []state.Event

	mem     resource.Mem // last memory reading (gauge)
	workers int          // live worker count at that reading

	sel  int
	mode int

	detail   *state.Task
	paneTail string
	input    textinput.Model
	nudging  bool // detail input sends a nudge instead of an answer

	costs      costsMsg        // costs page data (grove-8)
	bucketUnit cost.BucketUnit // chart granularity toggle
	costCache  *cost.Cache     // transcript parse cache, shared across refreshes

	flash string

	// Cockpit joy (grove-22). fx is the effects level; tick is the animation
	// clock (incremented per refreshMsg — the existing 1s loop, no new timer);
	// celebrations maps ticket → shimmer ticks remaining for a just-merged PR,
	// capped and decremented each tick.
	fx           fxLevel
	tick         uint64
	celebrations map[string]int

	// AttachTo is consumed by main after Run returns — only used when gv
	// runs OUTSIDE tmux, where attach replaces the process (syscall.Exec)
	// and so can't happen inside the tea loop. Inside tmux, attach is a
	// switch-client done live via AttachTask and the dashboard keeps
	// running in its pane.
	AttachTo *state.Task
}

func New(cfg *config.Config, stateDir, label string) Model {
	in := textinput.New()
	in.Placeholder = "type a reply — enter sends straight to the agent's pane"
	in.Prompt = sKey.Render("❯ ")
	in.CharLimit = 0
	fx := fxFull
	if cfg != nil {
		fx = parseFx(cfg.Cockpit.Effects)
	}
	return Model{cfg: cfg, stateDir: stateDir, label: label, live: map[string]string{}, prs: map[string]*github.PR{}, input: in, costCache: cost.NewCache(), fx: fx, celebrations: map[string]int{}}
}

func Run(cfg *config.Config, stateDir, label string) (*state.Task, error) {
	p := tea.NewProgram(New(cfg, stateDir, label), tea.WithAltScreen())
	out, err := p.Run()
	if err != nil {
		return nil, err
	}
	m := out.(Model)
	return m.AttachTo, nil
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(refreshCmd(m.stateDir), prsCmd(m.cfg, m.stateDir, nil), tickEvery(m.stateDir, time.Second), prTickEvery())
}

// --- commands ---

func tickEvery(stateDir string, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return refreshCmd(stateDir)() })
}

func prTickEvery() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg { return nil })
}

func refreshCmd(stateDir string) tea.Cmd {
	return func() tea.Msg {
		tasks, err := state.Load(stateDir)
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
		events, _ := state.ReadEvents(stateDir, 200)

		// Piggyback the resource gauge on the existing 1s tick — no new poll
		// loop or goroutine (grove-3). The read is a handful of sysctls; the
		// sample lands in resource.jsonl (its own capped file, never folded by
		// state.Load), giving the trajectory into a jetsam crash.
		mem, _ := resource.Read()
		workers := resource.LiveWorkers()
		_ = resource.Log(stateDir, resource.Sample{
			Avail: mem.AvailBytes, Total: mem.TotalBytes,
			Workers: workers, Kind: resource.KindSample,
		})

		return refreshMsg{tasks: active, live: live, events: events, mem: mem, workers: workers}
	}
}

func prsCmd(cfg *config.Config, stateDir string, tasks []*state.Task) tea.Cmd {
	return func() tea.Msg {
		if cfg == nil {
			return prsMsg{}
		}
		if tasks == nil {
			loaded, err := state.Load(stateDir)
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
		// The refresh loop IS the animation clock — one tick per second, no new
		// timer (grove-22). Celebrations decay on the same beat.
		m.tick++
		decayCelebrations(m.celebrations)
		// Gauge refreshes every tick — even at zero active tasks, where
		// msg.tasks is nil and the block below is skipped. A failed read is a
		// zero Mem (OK()==false) and simply hides the gauge.
		m.mem = msg.mem
		m.workers = msg.workers
		if msg.tasks != nil {
			m.tasks = msg.tasks
			m.live = msg.live
			m.events = msg.events
			if m.detail != nil { // keep detail pointed at fresh data
				for _, t := range m.tasks {
					if t.Ticket == m.detail.Ticket {
						m.detail = t
					}
				}
			}
		}
		cmds := []tea.Cmd{tickEvery(m.stateDir, time.Second)}
		if m.mode == modeDetail && m.detail != nil {
			cmds = append(cmds, paneTailCmd(m.detail))
		}
		if m.mode == modeCosts {
			// Live refresh, no snapshot: the parse cache makes this cheap,
			// and only page-open/toggle-on append ledger rows.
			cmds = append(cmds, costsCmd(m.cfg, m.stateDir, m.tasks, m.prs, m.costCache, false))
		}
		return m, tea.Batch(cmds...)

	case costsMsg:
		m.costs = msg
		return m, nil

	case prsMsg:
		// J1 merge sparkle: a PR that flipped to MERGED between polls earns a
		// short shimmer + footer flash. Detected by diffing old vs new — no new
		// I/O. Gated to fxFull; the celebrations map is capped so a burst of
		// merges can't grow it without bound.
		if m.fx >= fxFull {
			for ticket, pr := range msg {
				if pr == nil || pr.State != "MERGED" {
					continue
				}
				if old := m.prs[ticket]; old != nil && old.State == "MERGED" {
					continue // already merged last poll — not fresh
				}
				if len(m.celebrations) >= maxCelebrations {
					break
				}
				m.celebrations[ticket] = celebrationTicks
				m.flash = "⬢ " + ticket + " merged — the canopy grows"
			}
		}
		m.prs = msg
		return m, tea.Tick(30*time.Second, func(time.Time) tea.Msg { return prsCmd(m.cfg, m.stateDir, nil)() })

	case paneTailMsg:
		m.paneTail = string(msg)
		return m, nil

	case flashMsg:
		m.flash = string(msg)
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.flash = msg.err.Error()
		} else if m.fx >= fxFull && msg.ticket != "" {
			// J2 done ritual. The just-shipped tree isn't in m.events yet (its
			// refresh is still in flight), so count the loaded EvTaskDone and
			// add this one — precision doesn't matter (design J2).
			m.flash = fmt.Sprintf("✓ %s shipped — tree #%d this season", msg.ticket, countDone(m.events)+1)
		} else {
			m.flash = "✓ done"
		}
		return m, refreshCmd(m.stateDir)

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
	if m.mode == modeCosts {
		return m.handleCostsKey(k)
	}

	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "*": // cycle the effects knob (grove-22) — runtime only, not persisted
		m.fx = cycleFx(m.fx)
		m.flash = "effects: " + fxLabel(m.fx)
	case "O", "0": // "0" alias: the footer's O glyph reads as zero in many fonts
		cfg := m.cfg
		m.flash = "spawning orchestrator chat…"
		return m, func() tea.Msg {
			out, err := SpawnOrchestrator(cfg)
			if err != nil {
				return flashMsg(err.Error())
			}
			return flashMsg(out)
		}
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
			if tmux.IsInsideTmux() {
				// switch-client moves the tmux client; this process
				// stays alive, so the dashboard is still here when the
				// user switches back to the cockpit.
				task := t
				m.flash = "→ " + t.Ticket
				return m, func() tea.Msg {
					if err := AttachTask(task); err != nil {
						return flashMsg(err.Error())
					}
					return flashMsg("→ " + task.Ticket)
				}
			}
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
			_ = state.Append(m.stateDir, state.Event{
				Type: state.EvHumanStatus, Ticket: t.Ticket,
				Data: map[string]string{"status": next},
			})
			if next == "" {
				m.flash = t.Ticket + " back to unreviewed"
			} else {
				m.flash = "reviewing " + t.Ticket
			}
			return m, refreshCmd(m.stateDir)
		}
	case "d":
		if t := m.selected(); t != nil {
			m.detail = t
			m.mode = modeConfirmDone
		}
	case "r":
		m.flash = "refreshing PRs…"
		return m, prsCmd(m.cfg, m.stateDir, m.tasks)
	case "$", "c":
		m.mode = modeCosts
		m.flash = ""
		// snapshot=true: an open records one ledger row per active ticket
		// (when recording is on) — the cheap periodic point between grabs
		// and the final gv done row.
		return m, costsCmd(m.cfg, m.stateDir, m.tasks, m.prs, m.costCache, true)
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
		_ = state.Append(m.stateDir, state.Event{Type: state.EvAnswered, Ticket: t.Ticket})
		m.flash = "✓ sent to " + t.Ticket
		m.mode = modeList
		m.detail = nil
		m.input.Blur()
		return m, refreshCmd(m.stateDir)
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
		return m, func() tea.Msg { return actionDoneMsg{err: FinishTask(cfg, t, false), ticket: t.Ticket} }
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

// SpawnOrchestrator is injected by cmd/gv (the cockpit plumbing lives
// there); wired at startup to avoid an import cycle. Returns a flash line.
var SpawnOrchestrator = func(cfg *config.Config) (string, error) {
	return "", fmt.Errorf("orchestrator spawn not wired")
}

// AttachTask is injected by cmd/gv (attach bookkeeping — editor inject,
// attached event — lives there); wired at startup to avoid an import
// cycle. Only called when inside tmux, where attach is a switch-client
// and the tea loop survives it.
var AttachTask = func(t *state.Task) error {
	return fmt.Errorf("attach not wired")
}

func (m *Model) move(delta int) {
	rows := len(m.tasks)
	if rows == 0 {
		return
	}
	m.sel = (m.sel + delta + rows) % rows
}

func (m Model) selected() *state.Task {
	if len(m.tasks) == 0 {
		return nil
	}
	return m.tasks[min(m.sel, len(m.tasks)-1)]
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
