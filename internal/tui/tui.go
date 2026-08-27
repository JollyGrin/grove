// Package tui is the grove dashboard: one screen answering "what can I
// act on right now?" — the AGENTS list (attach/manage launcher) over an
// ACTIVITY feed (newest-first render of events.jsonl), with inline reply.
// Mail/review panels were dropped per grove-cockpit-design §2/§5: their
// signal lives in the header counts + AGENTS columns + the feed.
package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/cost"
	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/fleet"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/remote"
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
	tasks []*state.Task
	// handedOff is the forwarding-tombstone list (grove-178) — only folded
	// into the board while the R remote merge is on.
	handedOff []*state.Task
	live      map[string]string
	events    []state.Event
	mem       resource.Mem
	workers   int
	ok        bool   // state.Load succeeded — a zero msg (load error) stays false
	focused   string // grove-63: ticket at the tmux-focused window, "" if none
}

// tickMsg is the single cockpit beat (grove-24): one per second, re-armed
// ONLY by its own handler. It advances the animation clock AND kicks a data
// refresh. Keeping it the sole re-armer fixes the old multiplication bug
// (refreshMsg re-armed the timer, so every ad-hoc refresh spawned another
// loop) without adding a timer — the RAM constraint. Ad-hoc refreshCmd calls
// now produce only a data-applying refreshMsg, never touching the clock.
type tickMsg struct{}

// prTickMsg is the PR-poll beat (grove-24 pattern, reapplied for grove-118):
// one per 30s, re-armed ONLY by its own handler. Ad-hoc prsCmd calls (manual
// 'r' refresh, post-action refreshes) produce only a data-applying prsMsg,
// never a prTickMsg, so they can't multiply this loop.
type prTickMsg struct{}
type flashMsg string

// remoteMsg is the one-shot answer to an R keypress (grove-178): every
// configured host's `gv ls --json --no-pr` over ssh. Never re-fired by a
// tick — the cockpit RAM rule (no new goroutine/poll); a fresh fetch is
// one more R.
type remoteMsg struct {
	results []fleet.Result
}
type prsMsg map[string]*github.PR
type paneTailMsg string
type actionDoneMsg struct {
	err    error
	ticket string // for the J2 done ritual (grove-22)
}

// relayDoneMsg carries the outcome of an inline reply. Separate from
// actionDoneMsg so a nudge never triggers the done ritual.
type relayDoneMsg struct {
	err    error
	ticket string
}

// remoteDoneMsg carries the outcome of a cockpit relay to a remote host
// (grove-185). Nothing local changed — the remote host records its own
// answered event — so unlike relayDoneMsg no refresh follows it.
type remoteDoneMsg struct {
	err    error
	verb   string // "answer" | "nudge"
	ticket string
	host   string
}

type Model struct {
	cfg      *config.Config
	stateDir string
	// folder is the incremental events.jsonl reader (grove-126) — a pointer,
	// shared across the value-copied Models, so consumed offsets and fold
	// state survive every Update.
	folder *state.Folder
	label  string // ambient workspace label; "" = legacy global fleet
	width  int
	height int

	// board is what AGENTS renders: local rows, then (with R on) remote
	// rows and the trailing tombstones. Rebuilt only in assemble() — every
	// per-row derivation (host tag, tombstone line) is precomputed there so
	// View never allocates for it (grove-167 compute-once). The backing
	// array is reused across refreshes.
	board []boardRow
	// scene is the live-task prefix of the board for the garden: with R
	// off it IS localTasks (no copy); with R on it is sceneBuf, rebuilt in
	// assemble.
	scene    []*state.Task
	sceneBuf []*state.Task
	live     map[string]string
	prs      map[string]*github.PR
	events   []state.Event

	mem     resource.Mem // last memory reading (gauge)
	workers int          // live worker count at that reading

	sel  int
	mode int

	detail   *state.Task
	paneTail string
	input    textinput.Model
	nudging  bool // detail input sends a nudge instead of an answer
	// detailHost names the host a remote-bound detail relays to (grove-185);
	// "" = a local row, whose submit pastes into the agent's pane here.
	detailHost string

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

	// Remote fleet merge (grove-178). remote is the R toggle; localTasks +
	// handedOff are the last refresh's local view, kept so the board can
	// be rebuilt on either a refresh or a remote answer — in Update, never
	// per frame. handedOff is only computed while R is on (refreshCmd
	// gates the scan). remoteResults is the last R answer, held until R
	// toggles off. Everything that meters, polls, or steers (costs, PRs,
	// header counts, detail) feeds on localTasks — the merged board is a
	// VIEW, never a data source.
	remote        bool
	localTasks    []*state.Task
	handedOff     []*state.Task
	remoteResults []fleet.Result

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

// localReplyPlaceholder is the detail input's hint on a LOCAL row; a
// remote-bound detail swaps it for the ssh flavor in openDetail (grove-185).
const localReplyPlaceholder = "type a reply — enter sends straight to the agent's pane"

func New(cfg *config.Config, stateDir, label string) Model {
	in := textinput.New()
	in.Placeholder = localReplyPlaceholder
	in.Prompt = sKey.Render("❯ ")
	in.CharLimit = 0
	fx := fxFull
	if cfg != nil {
		fx = parseFx(cfg.Cockpit.Effects)
	}
	return Model{cfg: cfg, stateDir: stateDir, label: label, folder: state.NewFolder(stateDir, feedTail), live: map[string]string{}, prs: map[string]*github.PR{}, input: in, costCache: cost.NewCache(), fx: fx, celebrations: map[string]int{}}
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
	farewell := farewellLine(m.fx, countWorking(m.localTasks), nowHour())
	if farewell != "" {
		farewell = sChrome.Render(farewell)
	}
	return m.AttachTo, farewell, nil
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(refreshCmd(m.folder, m.stateDir, m.sessionName(), m.remote), prsCmd(m.cfg, m.stateDir, nil), tickEvery(time.Second), prTickEvery(30*time.Second))
}

// --- commands ---

// tickEvery arms the single cockpit beat. It carries no data — the handler
// fans out to refreshCmd — so it re-arms itself (via tickMsg) without the
// old per-refreshMsg re-arm that let the loops multiply (grove-24).
func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

// prTickEvery arms the single PR-poll beat. It carries no data — the handler
// fans out to prsCmd — so it re-arms itself (via prTickMsg) without letting
// ad-hoc prsMsg deliveries add another loop (grove-118, same class as
// grove-24's tickEvery).
func prTickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return prTickMsg{} })
}

// relayCmd delivers an inline reply off the update loop. PasteText settles
// before its Enter and then verifies the submit landed (grove-144), which can
// take ~1s — far too long to block the render loop. EvAnswered is appended
// only on a verified submit, so a swallowed Enter can no longer leave the
// board claiming the agent is working.
func relayCmd(stateDir, ticket, pane, text string) tea.Cmd {
	return func() tea.Msg {
		var err error
		if len([]rune(text)) == 1 {
			err = tmux.SendRawKey(pane, text) // option pickers: raw, no Enter
		} else {
			err = tmux.PasteText(pane, text)
		}
		if err != nil {
			return relayDoneMsg{err: err, ticket: ticket}
		}
		_ = state.Append(stateDir, state.Event{Type: state.EvAnswered, Ticket: ticket})
		return relayDoneMsg{ticket: ticket}
	}
}

// snapshotSessions returns a memoized snapshot getter for ONE refresh
// (grove-149): every worker shares the workspace session since grove-29, so
// a whole tick costs one list-windows + one list-panes instead of 3-4 window
// listings per task. fetch is tmux.SnapshotSession in production; a failed
// fetch memoizes nil, which every snapshot method answers as "session gone"
// — the same shape as the old per-call error fallbacks.
func snapshotSessions(fetch func(string) (*tmux.SessionSnapshot, error)) func(string) *tmux.SessionSnapshot {
	cache := map[string]*tmux.SessionSnapshot{}
	return func(session string) *tmux.SessionSnapshot {
		if snap, ok := cache[session]; ok {
			return snap
		}
		snap, _ := fetch(session)
		cache[session] = snap
		return snap
	}
}

// liveStates computes the per-ticket live map and the tmux-focused ticket
// (grove-63 S2: the queen stands on whichever plot's window is focused) for
// one refresh, reading tmux through one snapshot per session (grove-149).
// The old shape re-ran window/pane queries per task per second — 6N+2
// process spawns/sec, which pegged the CPU on hosts where spawning is
// expensive (EDR scanning each exec, WSL1). snapFor and detectFrom are
// injected so tests can count exactly what a refresh reads.
func liveStates(active []*state.Task, session string, snapFor func(string) *tmux.SessionSnapshot, detectFrom func(*tmux.SessionSnapshot, string) detect.LiveInfo) (map[string]string, string) {
	live := map[string]string{}
	activeWindow := snapFor(session).ActiveWindow()
	focused := ""
	for _, t := range active {
		snap := snapFor(t.TmuxSession)
		info := detectFrom(snap, t.TmuxWindow)
		if !info.Exists {
			live[t.Ticket] = "gone"
		} else {
			live[t.Ticket] = info.Status.String()
		}
		// A P3 status glyph is a display suffix on the stable base; the
		// snapshot resolves the current (possibly glyphed) name exec-free
		// for the focus comparison.
		if activeWindow != "" && snap.ResolveWindowName(t.TmuxWindow) == activeWindow {
			focused = t.Ticket
		}
	}
	return live, focused
}

// feedTail is the activity-feed tail bound — the folder keeps this many
// events resident, never the whole log (the cockpit RAM rule).
const feedTail = 200

// refreshCmd reads one cockpit beat's worth of data. withTombstones gates
// the state.HandedOff scan on the R toggle (grove-178 fix round): with R
// off — the default — the beat does zero tombstone work.
func refreshCmd(folder *state.Folder, stateDir, session string, withTombstones bool) tea.Cmd {
	return func() tea.Msg {
		// grove-126: one incremental pass answers both the task map and the
		// feed tail — O(appended bytes) per tick, not two full-log scans —
		// and tasks.json is rewritten only when the fold actually changed.
		tasks, events, err := folder.Refresh()
		if err != nil {
			return refreshMsg{}
		}
		active := state.Active(tasks)
		var handedOff []*state.Task
		if withTombstones {
			handedOff = state.HandedOff(tasks)
		}
		// grove-149: two session-wide reads answer every window/pane question
		// for the whole board; only capture-pane stays per task (inside
		// DetectLiveFrom).
		live, focused := liveStates(active, session,
			snapshotSessions(tmux.SnapshotSession), detect.DetectLiveFrom)

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

		return refreshMsg{tasks: active, handedOff: handedOff, live: live, events: events, mem: mem, workers: workers, ok: true, focused: focused}
	}
}

// remoteCmd asks every configured host once (fleet.Fetch: parallel, 5s per
// host). Fired ONLY by the R key — never by a tick (grove-178).
func remoteCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		if cfg == nil || len(cfg.Hosts) == 0 {
			return remoteMsg{}
		}
		return remoteMsg{results: fleet.Fetch(context.Background(), cfg, cfg.HostNames(), nil)}
	}
}

// remoteSendCmd runs one relay verb (answer/nudge) on the row's host over
// ssh (grove-185) — the grove-184 passthrough argv, a one-shot keypress cmd
// in the R merge fetch's class, bounded at 10s so a dead host can only cost
// a flash, never a hang. Output is captured, not streamed: on failure the
// first non-empty line becomes the flash. The REMOTE host records its own
// answered event — nothing is appended locally.
func remoteSendCmd(h *config.Host, host, verb, ticket, text string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		argv := remote.Argv(h, verb, []string{ticket, text})
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		if err == nil {
			return remoteDoneMsg{verb: verb, ticket: ticket, host: host}
		}
		if ctx.Err() == context.DeadlineExceeded {
			err = errors.New("timed out after 10s")
		} else if line := firstNonEmptyLine(out.String()); line != "" {
			err = errors.New(line)
		}
		return remoteDoneMsg{
			err:  fmt.Errorf("%s %s @%s: %v", verb, ticket, host, err),
			verb: verb, ticket: ticket, host: host,
		}
	}
}

// remoteDiffCmd suspends the TUI and pages the remote `gv diff` through
// less (grove-185): the grove-184 passthrough argv piped into `less -R` so
// git's own colors survive. One process per keypress; nothing resident,
// and the cockpit repaints when the pager exits.
func remoteDiffCmd(h *config.Host, host, ticket string) tea.Cmd {
	argv := remote.Argv(h, "diff", []string{ticket})
	paged := exec.Command("sh", "-c", strings.Join(argv, " ")+" | less -R")
	return tea.ExecProcess(paged, func(err error) tea.Msg {
		if err != nil {
			return flashMsg(fmt.Sprintf("diff %s @%s: %v", ticket, host, err))
		}
		return nil
	})
}

// remoteAttachCmd suspends the TUI and execs the field-proven #177 follow
// shape — the remote gv resolves the session/window on ITS side and does
// the attach bookkeeping; a raw `tmux attach -t` from here never could
// (the remote names sessions grove-<label> too, so the target may not even
// exist here). Detach — or a dropped ssh — restores the cockpit.
func remoteAttachCmd(h *config.Host, host, ticket string) tea.Cmd {
	attach := exec.Command("ssh", h.SSH, "-t", h.GV, "attach", ticket)
	return tea.ExecProcess(attach, func(err error) tea.Msg {
		if err != nil {
			return flashMsg(fmt.Sprintf("attach %s @%s: %v", ticket, host, err))
		}
		return nil
	})
}

// firstNonEmptyLine returns the first line with visible content, "" when
// the whole output is blank.
func firstNonEmptyLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// openDetail enters the detail/input mode for t (grove-185 lifted the
// enter/n bodies into one helper so the remote keys can bind a host to
// the same flow). host "" is a LOCAL row: the pane tail follows and
// submit pastes into the agent's pane here. A named host makes the
// submit a grove-184 relay — no pane is scraped (there is none here),
// so the view shows the fixed remote hint instead of a tail.
func (m Model) openDetail(t *state.Task, host string, nudging bool) (tea.Model, tea.Cmd) {
	m.detail = t
	m.detailHost = host
	m.mode = modeDetail
	m.nudging = nudging
	m.paneTail = ""
	m.input.SetValue("")
	m.input.Focus()
	if host != "" {
		m.input.Placeholder = "type a reply — enter relays it over ssh to @" + host
		return m, nil
	}
	m.input.Placeholder = localReplyPlaceholder
	return m, paneTailCmd(t)
}

// remoteHostByName resolves the selected row's host for the grove-185
// remote keys. A lookup miss (config dropped the host between the R
// fetch and the keypress) lands in the flash, never a panic.
func (m Model) remoteHostByName(name string) (*config.Host, error) {
	if m.cfg == nil {
		return nil, fmt.Errorf("no config — remote hosts unknown")
	}
	return m.cfg.Host(name)
}

// remoteReadonlyFlash explains the one row key that stays blocked on a
// live remote row: the reviewing flag is LOCAL backend state, so marking
// it from here would set a flag nobody's ping loop reads.
func remoteReadonlyFlash(r boardRow) string {
	return r.Ticket + " runs on " + r.Host +
		" — review state is local; mark it on the host (gv review " + r.Ticket + " there)"
}

// boardRow is one rendered AGENTS row: the fleet row plus everything the
// frame would otherwise re-derive (grove-167 compute-once). Kind is read
// off the row itself — remote via fleet.IsRemote, tombstone via
// HandedOffTo — so the board carries no positional invariant.
type boardRow struct {
	fleet.Row
	// elsewhere is the precomputed tombstone line ("" on live rows);
	// hostTag/tagW the precomputed "@host " hint prefix ("" / 0 on local
	// rows). Built once per assemble, never per frame.
	elsewhere string
	hostTag   string
	tagW      int
}

// assemble rebuilds the board from the last local refresh plus, while R is
// on, the last remote answer and the handed-off tombstones. Called from
// Update on each refreshMsg/remoteMsg — never per frame — and it reuses
// the board/scene backing arrays, so the steady-state beat allocates
// nothing (the cockpit RAM rule). It also clamps the cursor: a board that
// shrank under the selection (R off, a host answer replacing rows) must
// never leave enter/d acting on a row the user can't see selected.
func (m *Model) assemble() {
	m.board = m.board[:0]
	if !m.remote {
		for _, t := range m.localTasks {
			m.board = append(m.board, boardRow{Row: fleet.Row{Task: t, Host: fleet.LocalHost, Live: m.live[t.Ticket]}})
		}
		m.scene = m.localTasks
		m.clampSel()
		return
	}
	local := make([]fleet.Row, 0, len(m.localTasks))
	for _, t := range m.localTasks {
		local = append(local, fleet.Row{Task: t, Live: m.live[t.Ticket]})
	}
	rows, _ := fleet.Merge(local, m.handedOff, m.remoteResults)
	m.sceneBuf = m.sceneBuf[:0]
	for _, r := range rows {
		br := boardRow{Row: r}
		if r.HandedOffTo != "" {
			br.elsewhere = fleet.Elsewhere(r, age)
		} else {
			m.sceneBuf = append(m.sceneBuf, r.Task)
			if fleet.IsRemote(r) {
				br.hostTag = "@" + r.Host + " "
				br.tagW = utf8.RuneCountInString(br.hostTag)
			}
		}
		m.board = append(m.board, br)
	}
	m.scene = m.sceneBuf
	m.clampSel()
}

// clampSel keeps the cursor on a real row after the board shrinks.
func (m *Model) clampSel() {
	if n := len(m.board); m.sel >= n {
		if n == 0 {
			m.sel = 0
		} else {
			m.sel = n - 1
		}
	}
}

// remoteFlash is the footer line after an R answer: host failures verbatim
// (one per host), else the merged count.
func remoteFlash(results []fleet.Result) string {
	rows, hosts := 0, 0
	var warn []string
	for _, r := range results {
		if r.Err != nil {
			warn = append(warn, r.Host+": "+r.Err.Error())
			continue
		}
		hosts++
		rows += len(r.Rows)
	}
	if len(warn) > 0 {
		return "remote: " + strings.Join(warn, " · ")
	}
	return fmt.Sprintf("remote fleet: %d row(s) from %d host(s)", rows, hosts)
}

func prsCmd(cfg *config.Config, stateDir string, tasks []*state.Task) tea.Cmd {
	return func() tea.Msg {
		if cfg == nil {
			return prsMsg{}
		}
		if tasks == nil {
			// Read-only fallback (grove-126): the derived tasks.json is at
			// most one changed tick stale, and reading it avoids re-folding
			// the whole event log (and rewriting the view) on every 30s beat.
			tasks = state.Active(state.ReadTasks(stateDir))
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
	// A handed-off task has no LOCAL pane at all: its window coordinates
	// describe the remote host's session, and every host names sessions
	// grove-<label>, so a target built from them can resolve to a
	// different local worker's pane (grove-116 class). Refuse here, not
	// just at the call sites.
	if t.Paused || t.HandedOffTo != "" {
		return nil
	}
	return func() tea.Msg {
		// Pane resolved by id (grove-116): a name-built target can resolve
		// to a prefix-extending sibling's window, scraping the WRONG
		// agent's screen into the detail view. Also finds claude when the
		// window lost its split (the old target hardcoded pane .1).
		pane, err := tmux.ClaudePaneTarget(t.TmuxSession, t.TmuxWindow)
		if err != nil {
			return paneTailMsg("")
		}
		out, _ := tmux.CapturePaneBottom(pane, 14)
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
		return m, tea.Batch(refreshCmd(m.folder, m.stateDir, m.sessionName(), m.remote), tickEvery(time.Second))

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
				for _, tk := range freshQuestions(m.localTasks, msg.tasks) {
					if len(m.celebrations) >= maxCelebrations {
						break
					}
					m.celebrations[knockKey(tk)] = knockTicks
				}
				// S2 walk-off: a pawn whose task just left AgentWorking lingers
				// a few ticks, walking off its plot (grove-63). Same capped,
				// prefixed-key pattern as the knock, so it can never collide.
				for _, tk := range walkedOff(m.localTasks, msg.tasks) {
					if len(m.celebrations) >= maxCelebrations {
						break
					}
					m.celebrations[walkKey(tk)] = walkTicks
				}
			}
			m.localTasks, m.handedOff = msg.tasks, msg.handedOff
			m.live = msg.live
			m.assemble()
			m.events = msg.events
			m.focused = msg.focused
			if m.detail != nil {
				// Repoint detail at fresh data — over the LOCAL list only,
				// never the merged board: a remote row shares its window
				// name with local workers, and a target built from it would
				// steer the wrong agent's pane (grove-116 class). A ticket
				// that left the local fleet (done, untracked, handed off)
				// drops detail entirely instead of steering a recycled name.
				// A REMOTE-bound detail (grove-185) skips the repoint: its
				// ticket is by construction absent from localTasks, and the
				// task pointer is held by remoteResults until R toggles off.
				if m.detailHost == "" {
					var fresh *state.Task
					for _, t := range m.localTasks {
						if t.Ticket == m.detail.Ticket {
							fresh = t
							break
						}
					}
					m.detail = fresh
					if fresh == nil && (m.mode == modeDetail || m.mode == modeConfirmDone) {
						m.mode = modeList
						m.input.Blur()
					}
				}
			}
		}
		var cmds []tea.Cmd
		// No pane tail for a remote-bound detail (grove-185): scraping the
		// task's window coordinates here could hit a LOCAL pane that merely
		// shares the name (grove-116 class) — the worker has no local pane.
		if m.mode == modeDetail && m.detail != nil && m.detailHost == "" {
			cmds = append(cmds, paneTailCmd(m.detail))
		}
		if m.mode == modeCosts {
			// Live refresh, no snapshot: the parse cache makes this cheap,
			// and only page-open/toggle-on append ledger rows. Local tasks
			// only — a merged board must never feed the cost meter.
			cmds = append(cmds, costsCmd(m.cfg, m.stateDir, m.localTasks, m.prs, m.costCache, false))
		}
		return m, tea.Batch(cmds...)

	case costsMsg:
		m.costs = msg
		return m, nil

	case remoteMsg:
		// Data only (grove-178): no timer, no re-fire. A stale answer after
		// R was toggled off is dropped.
		if !m.remote {
			return m, nil
		}
		m.remoteResults = msg.results
		m.assemble()
		m.flash = remoteFlash(msg.results)
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
		return m, accountCmd(m.cfg, m.stateDir, m.localTasks, m.costCache)

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

	case prTickMsg:
		// The single PR-poll beat (grove-118, grove-24 pattern): kick a poll
		// AND re-arm ONLY this timer. Ad-hoc prsMsg deliveries (below) never
		// re-arm, so 'r' and other ad-hoc refreshes can't multiply the loop.
		return m, tea.Batch(prsCmd(m.cfg, m.stateDir, nil), prTickEvery(30*time.Second))

	case prsMsg:
		// Data only — the poll loop lives on prTickMsg now (grove-118). This
		// handler runs both on the beat and on ad-hoc refreshes ('r', detail
		// entry); it must never re-arm a timer.
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
		return m, nil

	case paneTailMsg:
		m.paneTail = string(msg)
		return m, nil

	case flashMsg:
		m.flash = string(msg)
		return m, nil

	case relayDoneMsg:
		if msg.err != nil {
			m.flash = msg.err.Error()
		} else {
			m.flash = "✓ sent to " + msg.ticket
		}
		return m, refreshCmd(m.folder, m.stateDir, m.sessionName(), m.remote)

	case remoteDoneMsg:
		// grove-185: nothing local changed (the remote host recorded its own
		// event), so unlike relayDoneMsg no refresh follows — just the flash.
		if msg.err != nil {
			m.flash = msg.err.Error()
		} else {
			m.flash = "✓ " + msg.verb + "ed " + msg.ticket + " on " + msg.host
		}
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
		return m, refreshCmd(m.folder, m.stateDir, m.sessionName(), m.remote)

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

	// grove-178 kept every non-local row read-only; grove-185 lifts that
	// for LIVE remote rows — a/n open the relay input bound to the row's
	// host, d pages the remote diff, enter attaches over ssh. Handed-off
	// tombstones stay read-only bookmarks, and v stays blocked on both:
	// the reviewing flag is local backend state this host owns.
	if r, ok := m.selectedRow(); ok && (fleet.IsRemote(r.Row) || r.HandedOffTo != "") {
		switch k.String() {
		case "v":
			if r.HandedOffTo != "" {
				m.flash = elsewhereFlash(r)
			} else {
				m.flash = remoteReadonlyFlash(r)
			}
			return m, nil
		case "enter", "n", "a", "d":
			if r.HandedOffTo != "" {
				m.flash = elsewhereFlash(r)
				return m, nil
			}
			h, err := m.remoteHostByName(r.Host)
			if err != nil {
				m.flash = err.Error()
				return m, nil
			}
			switch k.String() {
			case "a":
				return m.openDetail(r.Task, r.Host, false)
			case "n":
				return m.openDetail(r.Task, r.Host, true)
			case "d":
				return m, remoteDiffCmd(h, r.Host, r.Ticket)
			case "enter":
				return m, remoteAttachCmd(h, r.Host, r.Ticket)
			}
		}
	}

	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "R": // remote fleet merge (grove-178): one fetch per press, never polled
		m.remote = !m.remote
		if !m.remote {
			m.remoteResults = nil
			m.assemble()
			m.flash = "remote fleet: off"
			return m, nil
		}
		if m.cfg == nil || len(m.cfg.Hosts) == 0 {
			m.remote = false
			m.flash = "remote fleet: no hosts configured (hosts: map in config.yaml)"
			return m, nil
		}
		m.assemble()
		m.flash = fmt.Sprintf("asking %d host(s)…", len(m.cfg.Hosts))
		// The ad-hoc refresh brings the tombstones in (their scan is gated
		// on R being on); live remote rows land with the ssh answer.
		return m, tea.Batch(remoteCmd(m.cfg), refreshCmd(m.folder, m.stateDir, m.sessionName(), true))
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
	case ")": // O's profiled sibling: pick a model profile to orchestrate on.
		// Always the picker when any profile exists (grove-105) — no
		// auto-spawn shortcut hiding the choice; direct spawns are the
		// digit hotkeys' job.
		candidates, action := m.cfg.ResolveOrchestratorProfile()
		if action == config.ProfileHint {
			m.flash = "no model_profiles configured — add one to ~/.config/grove/config.yaml"
			return m, nil
		}
		m.pickProfiles = candidates
		m.pickSel = 0
		m.mode = modeProfilePick
		m.flash = ""
		return m, nil
	case "1", "2", "3", "4", "5", "6", "7", "8":
		// Digit hotkeys (grove-105): one keypress → that bound profile's
		// orchestrator pane, no intermediate UI. ("0" stays the O alias.)
		digit := k.String()
		profile := m.cfg.Orchestrator.Hotkeys[digit]
		if profile == "" {
			m.flash = digit + " unbound — bind it in the ) picker"
			return m, nil
		}
		return m.spawnProfile(profile)
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "enter":
		if t := m.selected(); t != nil {
			return m.openDetail(t, "", false)
		}
	case "n":
		if t := m.selected(); t != nil {
			return m.openDetail(t, "", true)
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
			return m, refreshCmd(m.folder, m.stateDir, m.sessionName(), m.remote)
		}
	case "d":
		if t := m.selected(); t != nil {
			m.detail = t
			m.mode = modeConfirmDone
		}
	case "X": // park the whole workspace (grove-33) — workspace-level, no row needed
		m.mode = modeConfirmClose
	case "r":
		// Local tasks only: PRs polled off a merged board would land a
		// tombstone's handoff PR in m.prs and inflate the review count.
		m.flash = "refreshing PRs…"
		return m, prsCmd(m.cfg, m.stateDir, m.localTasks)
	case "?": // the cheat-sheet overlay (grove-60) — esc/? closes
		m.mode = modeHelp
		m.flash = ""
	case "$", "c":
		m.mode = modeCosts
		m.costsTab = costsTabSpend // $ always lands on SPEND (grove-87)
		m.flash = ""
		// snapshot=true: an open records one ledger row per active ticket
		// (when recording is on) — the cheap periodic point between grabs
		// and the final gv done row. Local tasks ONLY: a merged board here
		// would write durable ledger rows for handed-off tasks that the
		// remote host is already metering (double-counted spend).
		return m, costsCmd(m.cfg, m.stateDir, m.localTasks, m.prs, m.costCache, true)
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
		m.detailHost = ""
		m.input.Blur()
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		t := m.detail
		// Same refusal as paneTailCmd: never build a tmux target for a
		// task without a local pane — the coordinates may resolve to a
		// DIFFERENT local worker (grove-116 class). detail only ever binds
		// to local rows, but the relay is where a miss steers an agent, so
		// it re-checks.
		if t == nil || t.HandedOffTo != "" {
			m.mode = modeList
			m.detail = nil
			m.detailHost = ""
			m.input.Blur()
			return m, nil
		}
		// grove-185: a remote-bound detail relays over ssh (gv answer /
		// nudge --host class). The REMOTE host records its own answered
		// event — no local pane to target, no local state to append — so
		// this must never fall through to relayCmd.
		if host := m.detailHost; host != "" {
			h, err := m.remoteHostByName(host)
			if err != nil {
				m.flash = err.Error()
				return m, nil
			}
			verb := "answer"
			if m.nudging {
				verb = "nudge"
			}
			m.flash = "relaying " + verb + " to " + t.Ticket + " on " + host + "…"
			m.mode = modeList
			m.detail = nil
			m.detailHost = ""
			m.input.Blur()
			return m, remoteSendCmd(h, host, verb, t.Ticket, text)
		}
		// Pane resolved by id (grove-116): a name-built target can hit a
		// prefix-extending sibling's window and steer the wrong agent.
		pane, err := tmux.ClaudePaneTarget(t.TmuxSession, t.TmuxWindow)
		if err != nil {
			m.flash = err.Error()
			return m, nil
		}
		ticket := t.Ticket
		m.flash = "sending to " + ticket + "…"
		m.mode = modeList
		m.detail = nil
		m.input.Blur()
		return m, relayCmd(m.stateDir, ticket, pane, text)
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
		m.detailHost = ""
		m.flash = "cleaning up " + t.Ticket + "…"
		return m, func() tea.Msg { return actionDoneMsg{err: FinishTask(cfg, t, false), ticket: t.Ticket} }
	default:
		m.mode = modeList
		m.detail = nil
		m.detailHost = ""
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
// pane, esc (or any exit key) backs out cleanly to the fleet list. Digits
// 1–8 bind the highlighted profile to that hotkey (grove-105) — rebinding a
// taken digit steals it, pressing the row's own digit again unbinds — and
// persist through SaveHotkeyBinding; the picker stays open so several rows
// can be bound in one visit.
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
	case "1", "2", "3", "4", "5", "6", "7", "8":
		if m.pickSel < 0 || m.pickSel >= len(m.pickProfiles) {
			return m, nil
		}
		digit := k.String()
		profile := m.pickProfiles[m.pickSel]
		unbind := m.cfg.Orchestrator.Hotkeys[digit] == profile
		save := profile
		if unbind {
			save = "" // pressing the row's own digit again unbinds it
		}
		if err := SaveHotkeyBinding(digit, save); err != nil {
			m.flash = err.Error()
			return m, nil
		}
		// Mirror the write into the loaded config so rows/digits react
		// without a reload. Same steal/one-digit-per-profile semantics as
		// the persisted map.
		if m.cfg.Orchestrator.Hotkeys == nil {
			m.cfg.Orchestrator.Hotkeys = map[string]string{}
		}
		delete(m.cfg.Orchestrator.Hotkeys, digit)
		if unbind {
			m.flash = digit + " unbound"
			return m, nil
		}
		if old := m.cfg.HotkeyFor(profile); old != "" {
			delete(m.cfg.Orchestrator.Hotkeys, old)
		}
		m.cfg.Orchestrator.Hotkeys[digit] = profile
		m.flash = digit + " → " + profile
		return m, nil
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

// SaveHotkeyBinding persists one digit→profile hotkey binding from the `)`
// picker (grove-105); profile "" unbinds the digit. Injected by cmd/gv,
// which knows the ambient workspace root and so which config.yaml the
// binding belongs to. The config write is the only mutation on this path.
var SaveHotkeyBinding = func(digit, profile string) error {
	return fmt.Errorf("hotkey persist not wired")
}

// AttachTask is injected by cmd/gv (attach bookkeeping — editor inject,
// attached event — lives there); wired at startup to avoid an import
// cycle. Only called when inside tmux, where attach is a switch-client
// and the tea loop survives it.
var AttachTask = func(t *state.Task) error {
	return fmt.Errorf("attach not wired")
}

func (m *Model) move(delta int) {
	rows := len(m.board)
	if rows == 0 {
		return
	}
	m.sel = (m.sel + delta + rows) % rows
}

func (m Model) selectedRow() (boardRow, bool) {
	if len(m.board) == 0 {
		return boardRow{}, false
	}
	return m.board[min(m.sel, len(m.board)-1)], true
}

func (m Model) selected() *state.Task {
	r, ok := m.selectedRow()
	if !ok {
		return nil
	}
	return r.Task
}

// elsewhereFlash names where a non-local row lives.
func elsewhereFlash(r boardRow) string {
	if r.HandedOffTo != "" {
		return r.Ticket + " was handed off to " + r.HandedOffTo + " — take back: gv handoff " + r.Ticket + " --from " + r.HandedOffTo
	}
	return r.Ticket + " runs on " + r.Host + " — steer it there (ssh " + r.Host + ")"
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
// its idle/dead shape is deliberate (grove-90), not a stall. Local tasks
// only: a waiting REMOTE row is not mail — no local key can answer it.
func (m Model) mailRows() []*state.Task {
	var rows []*state.Task
	for _, t := range m.localTasks {
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
// (press d) or when feedback sends the agent back to work. Local tasks
// only — the remote host's review queue is its own cockpit's.
func (m Model) reviewRows() []*state.Task {
	var fresh, marked []*state.Task
	for _, t := range m.localTasks {
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
