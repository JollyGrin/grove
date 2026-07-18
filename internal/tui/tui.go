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
	modeConfirmClose
	modeCosts
	modeProfilePick
	modeHelp
	modeAlmanac
)

type refreshMsg struct {
	tasks   []*state.Task
	live    map[string]string
	events  []state.Event
	mem     resource.Mem
	workers int
	ok      bool   // state.Load succeeded — a zero msg (load error) stays false
	focused string // grove-63: ticket at the tmux-focused window, "" if none
}

// tickMsg is the single cockpit beat (grove-24): one per second, re-armed
// ONLY by its own handler. It advances the animation clock AND kicks a data
// refresh. Keeping it the sole re-armer fixes the old multiplication bug
// (refreshMsg re-armed the timer, so every ad-hoc refresh spawned another
// loop) without adding a timer — the RAM constraint. Ad-hoc refreshCmd calls
// now produce only a data-applying refreshMsg, never touching the clock.
type tickMsg struct{}
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

	// modeProfilePick overlay state (grove-45): the sorted candidate profile
	// names and the cursor into them. Runtime-only — nothing is persisted.
	pickProfiles []string
	pickSel      int

	costs      costsMsg        // costs page data (grove-8)
	bucketUnit cost.BucketUnit // chart granularity toggle
	costCache  *cost.Cache     // transcript parse cache, shared across refreshes
	costsTab   int             // SPEND | ACCOUNT (grove-87); $ always lands on SPEND
	account    accountMsg      // ACCOUNT tab snapshot — one-shot on tab open / r, never polled
	accountSel int             // cursor into account.keys (grove-104 key manager)

	flash string

	// Cockpit joy (grove-22, grove-24). fx is the effects level; tick is the
	// animation clock — advanced once per 1s tickMsg on the single refresh
	// timer, NOT per refreshMsg, so ad-hoc refreshes (answers, reviews, dones)
	// can't accelerate the breath. celebrations maps ticket → shimmer ticks
	// remaining for a just-merged PR, capped and decremented on that same beat.
	fx           fxLevel
	tick         uint64
	celebrations map[string]int

	// focused is the ticket at the tmux-focused window, read once per
	// refresh via the read-only tmux.ActiveWindow (grove-63 S2) — the
	// amber queen stands on its plot. "" means no worker window is focused
	// (Dean's on the cockpit itself, or a resolve failed).
	focused string

	// modeAlmanac state (grove-63 S4): almanac is a snapshot read once on
	// mode entry (almanacCmd), never re-read on the 1s tick; almSel indexes
	// the browsed day.
	almSel  int
	almanac almanacMsg

	// greeted latches after the first data refresh — the A6 first-light
	// flash fires exactly once per cockpit launch (grove-56).
	greeted bool

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

// Run returns the attach target plus the A5 quit farewell — one styled line
// cmd/gv prints after the alt-screen closes (empty at fx off, grove-56).
func Run(cfg *config.Config, stateDir, label string) (*state.Task, string, error) {
	p := tea.NewProgram(New(cfg, stateDir, label), tea.WithAltScreen())
	out, err := p.Run()
	if err != nil {
		return nil, "", err
	}
	m := out.(Model)
	farewell := farewellLine(m.fx, countWorking(m.tasks), nowHour())
	if farewell != "" {
		farewell = sChrome.Render(farewell)
	}
	return m.AttachTo, farewell, nil
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(refreshCmd(m.stateDir, m.sessionName()), prsCmd(m.cfg, m.stateDir, nil), tickEvery(time.Second), prTickEvery())
}

// --- commands ---

// tickEvery arms the single cockpit beat. It carries no data — the handler
// fans out to refreshCmd — so it re-arms itself (via tickMsg) without the
// old per-refreshMsg re-arm that let the loops multiply (grove-24).
func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

func prTickEvery() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg { return nil })
}

func refreshCmd(stateDir, session string) tea.Cmd {
	return func() tea.Msg {
		tasks, err := state.Load(stateDir)
		if err != nil {
			return refreshMsg{}
		}
		active := state.Active(tasks)
		live := map[string]string{}
		// grove-63 S2: one read-only list-windows call per refresh; the queen
		// stands on whichever plot's resolved window name matches it.
		activeWindow := tmux.ActiveWindow(session)
		focused := ""
		for _, t := range active {
			// Resolve the current window name — a P3 status glyph is a display
			// suffix on the stable base, and the detect probe matches exactly.
			resolved := tmux.ResolveWindowName(t.TmuxSession, t.TmuxWindow)
			info := detect.DetectLive(t.TmuxSession, resolved)
			if !info.Exists {
				live[t.Ticket] = "gone"
			} else {
				live[t.Ticket] = info.Status.String()
			}
			if activeWindow != "" && resolved == activeWindow {
				focused = t.Ticket
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

		return refreshMsg{tasks: active, live: live, events: events, mem: mem, workers: workers, ok: true, focused: focused}
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
	// A paused worker has no pane to scrape (grove-90) — the detail view
	// renders the stored last_message plus the gv adopt hint instead.
	if t.Paused {
		return nil
	}
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
		// grove-53 cause (b): a shrink or a SIGWINCH replay on tmux re-attach
		// leaves stale cells from the old geometry. Force one full repaint per
		// resize so we always start from a clean slate — no per-frame cost.
		return m, tea.ClearScreen

	case tickMsg:
		// The single cockpit beat (grove-24): advance the animation clock,
		// decay celebrations, then kick a data refresh AND re-arm ONLY this
		// timer. Because nothing else re-arms it, ad-hoc refreshes can't
		// multiply the loop or speed the breath. No new timer — this replaces
		// the old refreshMsg-driven loop.
		m.tick++
		decayCelebrations(m.celebrations)
		return m, tea.Batch(refreshCmd(m.stateDir, m.sessionName()), tickEvery(time.Second))

	case refreshMsg:
		// Data only — the clock lives on tickMsg now (grove-24). This handler
		// runs both on the beat and on ad-hoc refreshes (answers, reviews,
		// dones); it must never re-arm a timer or touch m.tick.
		// Gauge refreshes every time, independent of msg.ok below. A failed
		// read is a zero Mem (OK()==false) and simply hides the gauge.
		m.mem = msg.mem
		m.workers = msg.workers
		// A6 first light: greet once, at the first SUCCESSFUL refresh after
		// launch — a load-error zero msg must not greet an empty grove that
		// isn't (nor latch; the next good refresh greets). The next real
		// flash overwrites it naturally (grove-56).
		if !m.greeted && msg.ok {
			m.greeted = true
			if m.fx >= fxCalm && m.flash == "" {
				m.flash = firstLight(nowHour(), len(msg.tasks))
			}
		}
		if msg.ok {
			// Gate on ok, not tasks != nil: an empty-but-healthy fleet also
			// returns a nil tasks slice from state.Active, and that must still
			// apply below — only a load-error zero msg should leave state
			// untouched (grove-65).
			// J5 question knock: a label freshly flipped to QUESTION earns a
			// few ticks of glyph pulse. Same diff-in-Update move as J1, same
			// capped celebrations map under a prefixed key (grove-56).
			if m.fx >= fxFull {
				for _, tk := range freshQuestions(m.tasks, msg.tasks) {
					if len(m.celebrations) >= maxCelebrations {
						break
					}
					m.celebrations[knockKey(tk)] = knockTicks
				}
				// S2 walk-off: a pawn whose task just left AgentWorking lingers
				// a few ticks, walking off its plot (grove-63). Same capped,
				// prefixed-key pattern as the knock, so it can never collide.
				for _, tk := range walkedOff(m.tasks, msg.tasks) {
					if len(m.celebrations) >= maxCelebrations {
						break
					}
					m.celebrations[walkKey(tk)] = walkTicks
				}
			}
			m.tasks = msg.tasks
			m.live = msg.live
			m.events = msg.events
			m.focused = msg.focused
			if m.detail != nil { // keep detail pointed at fresh data
				for _, t := range m.tasks {
					if t.Ticket == m.detail.Ticket {
						m.detail = t
					}
				}
			}
		}
		var cmds []tea.Cmd
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

	case accountMsg:
		m.account = msg
		if m.accountSel >= len(msg.keys) {
			m.accountSel = 0
		}
		return m, nil

	case keySavedMsg:
		// Paste flow succeeded (grove-87): the key is saved in
		// ~/.config/grove/.env — refetch so the unset row becomes data.
		m.flash = "✓ " + msg.varName + " saved to ~/.config/grove/.env (" + msg.masked + ")"
		return m, accountCmd(m.cfg, m.stateDir, m.tasks, m.costCache)

	case almanacMsg:
		// A one-shot snapshot (grove-63 S4) — never re-fired by the 1s tick,
		// unlike costs. m.almSel lands on the newest day (today).
		m.almanac = msg
		m.almSel = len(msg.days) - 1
		if m.almSel < 0 {
			m.almSel = 0
		}
		if m.mode == modeAlmanac {
			m.flash = ""
		}
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
			// J2 done ritual, J6 milestone every 10th tree. The just-shipped
			// tree isn't in m.events yet (its refresh is still in flight), so
			// count the loaded EvTaskDone and add this one — precision doesn't
			// matter (design J2).
			m.flash = doneRitualFlash(msg.ticket, countDone(m.events)+1)
		} else {
			m.flash = "✓ done"
		}
		return m, refreshCmd(m.stateDir, m.sessionName())

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
	if m.mode == modeConfirmClose {
		return m.handleCloseKey(k)
	}
	if m.mode == modeCosts {
		return m.handleCostsKey(k)
	}
	if m.mode == modeProfilePick {
		return m.handleProfilePickKey(k)
	}
	if m.mode == modeHelp {
		return m.handleHelpKey(k)
	}
	if m.mode == modeAlmanac {
		return m.handleAlmanacKey(k)
	}

	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "*": // cycle the effects knob (grove-22) — runtime only, not persisted
		m.fx = cycleFx(m.fx)
		m.flash = "effects: " + fxLabel(m.fx)
	case "L": // cycle cockpit pane orientation (grove-52) — persisted in the
		// session option @grove_layout, which is the source of truth (read
		// live: a Model copy would go stale across dash reconnects).
		session := m.sessionName()
		cur := tmux.CockpitLayout(session)
		if cur == "" && m.cfg != nil {
			cur = m.cfg.CockpitLayout()
		}
		next := nextLayout(cur)
		_ = tmux.SetCockpitLayout(session, next)
		_ = tmux.SelectLayout(session, next)
		m.flash = "layout: " + next
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
	case ")": // O's profiled sibling: spawn an orchestrator on a model profile
		cfg := m.cfg
		profile, candidates, action := cfg.ResolveOrchestratorProfile()
		switch action {
		case config.ProfileHint:
			m.flash = "no model_profiles configured — add one to ~/.config/grove/config.yaml"
			return m, nil
		case config.ProfilePick:
			// ≥2 profiles, no default → let the operator choose (grove-45).
			m.pickProfiles = candidates
			m.pickSel = 0
			m.mode = modeProfilePick
			m.flash = ""
			return m, nil
		}
		return m.spawnProfile(profile)
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
			return m, refreshCmd(m.stateDir, m.sessionName())
		}
	case "d":
		if t := m.selected(); t != nil {
			m.detail = t
			m.mode = modeConfirmDone
		}
	case "X": // park the whole workspace (grove-33) — workspace-level, no row needed
		m.mode = modeConfirmClose
	case "r":
		m.flash = "refreshing PRs…"
		return m, prsCmd(m.cfg, m.stateDir, m.tasks)
	case "?": // the cheat-sheet overlay (grove-60) — esc/? closes
		m.mode = modeHelp
		m.flash = ""
	case "$", "c":
		m.mode = modeCosts
		m.costsTab = costsTabSpend // $ always lands on SPEND (grove-87)
		m.flash = ""
		// snapshot=true: an open records one ledger row per active ticket
		// (when recording is on) — the cheap periodic point between grabs
		// and the final gv done row.
		return m, costsCmd(m.cfg, m.stateDir, m.tasks, m.prs, m.costCache, true)
	case "g": // THE ALMANAC (grove-63): one garden per day, browsable history
		m.mode = modeAlmanac
		m.flash = "leafing through the almanac…"
		return m, almanacCmd(m.stateDir)
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
		return m, refreshCmd(m.stateDir, m.sessionName())
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

// handleCloseKey mirrors handleConfirmKey for the park-workspace modal
// (grove-33): y kills the shared grove-<label> session — freeing every
// worker, the orchestrator, and this very dashboard — after the parked
// event is durably logged; any other key backs out. There is no per-task
// selection: parking is workspace-level.
func (m Model) handleCloseKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "y" {
		stateDir, label := m.stateDir, m.label
		m.flash = "parking " + m.sessionName() + "…"
		return m, func() tea.Msg {
			// CloseWorkspace logs the parked event then kills the session,
			// which takes down this process — so on success nothing below
			// runs. A returned error means the kill never happened; surface it.
			if err := CloseWorkspace(stateDir, label); err != nil {
				return flashMsg(err.Error())
			}
			return nil
		}
	}
	m.mode = modeList
	return m, nil
}

// handleProfilePickKey drives the modeProfilePick overlay (grove-45): j/k or
// arrows move the cursor, enter spawns the selected profile's orchestrator
// pane, esc (or any exit key) backs out cleanly to the fleet list. The
// selection is runtime-only — nothing is persisted to config.
func (m Model) handleProfilePickKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q":
		m.mode = modeList
		m.pickProfiles = nil
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		if m.pickSel < len(m.pickProfiles)-1 {
			m.pickSel++
		}
		return m, nil
	case "k", "up":
		if m.pickSel > 0 {
			m.pickSel--
		}
		return m, nil
	case "enter":
		if m.pickSel < 0 || m.pickSel >= len(m.pickProfiles) {
			return m, nil
		}
		profile := m.pickProfiles[m.pickSel]
		m.mode = modeList
		m.pickProfiles = nil
		return m.spawnProfile(profile)
	}
	return m, nil
}

// spawnProfile fires the profiled-orchestrator spawn for a resolved profile
// name — shared by the `)` auto-pick path and the picker's enter. It returns
// the flash-setting model plus the async spawn command.
func (m Model) spawnProfile(profile string) (tea.Model, tea.Cmd) {
	cfg := m.cfg
	m.flash = "spawning orchestrator chat (" + profile + ")…"
	return m, func() tea.Msg {
		out, err := SpawnOrchestratorProfile(cfg, profile)
		if err != nil {
			return flashMsg(err.Error())
		}
		return flashMsg(out)
	}
}

// sessionName is the workspace's shared tmux session (grove-<label>, or the
// legacy global "grove"); the model carries label so it needn't ask cmd/gv.
func (m Model) sessionName() string {
	if m.label == "" {
		return "grove"
	}
	return "grove-" + m.label
}

// FinishTask is injected by cmd/gv (the done flow lives there); wired at
// startup to avoid an import cycle.
var FinishTask = func(cfg *config.Config, t *state.Task, force bool) error {
	return fmt.Errorf("done flow not wired")
}

// CloseWorkspace is injected by cmd/gv (the park flow — parked event then
// tmux.KillSession — lives there); wired at startup to avoid an import
// cycle. It logs the durable EvWorkspaceParked BEFORE the kill so the
// record survives the session death.
var CloseWorkspace = func(stateDir, label string) error {
	return fmt.Errorf("park flow not wired")
}

// SpawnOrchestrator is injected by cmd/gv (the cockpit plumbing lives
// there); wired at startup to avoid an import cycle. Returns a flash line.
var SpawnOrchestrator = func(cfg *config.Config) (string, error) {
	return "", fmt.Errorf("orchestrator spawn not wired")
}

// SpawnOrchestratorProfile is SpawnOrchestrator's profile-aware twin,
// injected by cmd/gv — the `)` keybind's spawn path (grove-41). It opens
// the new pane on the named model profile, exactly like
// `gv orchestrator new --profile <name>`.
var SpawnOrchestratorProfile = func(cfg *config.Config, profile string) (string, error) {
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

// mailRows: tasks demanding a human decision. A paused task never is one —
// its idle/dead shape is deliberate (grove-90), not a stall.
func (m Model) mailRows() []*state.Task {
	var rows []*state.Task
	for _, t := range m.tasks {
		if t.Paused {
			continue
		}
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
