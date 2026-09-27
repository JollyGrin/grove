package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The CHATS box (chat-hide train, grove-402): one read-only row per live
// orchestrator chat of this workspace, between AGENTS and ACTIVITY. It
// exists only while the workspace has a live chat — with none, every frame
// is byte-identical to the pre-chats cockpit (testdata goldens).
//
// Data discipline (cockpit RAM rule): no new goroutine, poll, timer or
// cache. The CHEAP pass (one `list-panes -a`: glyph, model, remote marker,
// busy) rides the 1s beat; the COSTLY pass (`ps` ground truth, transcript
// reads, the waiting pane-capture) rides the 30s PR beat — and the 1s beat
// only while the box is focused. Between costly passes a row keeps its
// last known label/age/last line/turn (mergeChats). Rows are derived in
// assembleChats, in Update — View only styles them.

// focusChats is the third tab stop (FEATURES ⇄ AGENTS ⇄ CHATS).
const focusChats = focusFeatures + 1

// maxChatRows caps the box: more chats than this scroll under the cursor
// behind a `+N more` line.
const maxChatRows = 5

// Turn words, as `gv chat ls` reports them (chatweb.Turn*). Spelled here
// rather than imported: the TUI reads rows, it never classifies a pane.
const (
	chatTurnWaiting = "waiting"
	chatTurnStopped = "stopped"
	chatTurnRunning = "running"
)

// ChatRow is one live chat as CockpitChats reports it. The first block is
// the cheap pass's; the second is filled only by a costly pass (Deep).
type ChatRow struct {
	Pane    string // the pane's immutable %id — the row's identity across beats
	Hidden  bool   // kind `chat` (a detached grove-chat-<label>-<n> session) vs kind `cockpit`
	N       int    // the chat number, or the cockpit pane's index
	Host    string // @grove_remote: the host a remote chat pane attaches to
	Model   string
	Busy    bool
	Created time.Time

	Label      string
	LastActive time.Time
	Last       string // the chat's last line
	Waiting    bool
	Turn       string
}

// CockpitChats is injected by cmd/gv (the tmux/ps/transcript join lives
// there): this workspace's live chats. deep=false is the cheap pass — one
// tmux invocation, nothing else; deep=true adds the costly fields. A pure
// read either way: nothing is stamped, no event is written. Unwired = none.
var CockpitChats = func(label string, deep bool) []ChatRow { return nil }

// chatsMsg carries one pass. Data only — it never re-arms a timer.
type chatsMsg struct {
	rows []ChatRow
	deep bool
}

// chatsCmd runs one pass off the UI thread, on a beat that already exists.
func chatsCmd(label string, deep bool) tea.Cmd {
	return func() tea.Msg {
		return chatsMsg{rows: CockpitChats(label, deep), deep: deep}
	}
}

// Row kinds index the glyph and style tables.
const (
	chatShown = iota
	chatHidden
	chatWaiting
)

// Chat glyphs and styles — package-level tables, never rebuilt per frame.
var (
	chatGlyphs = [...]string{chatShown: "▣", chatHidden: "○", chatWaiting: "◆"}
	chatStyles = map[string]lipgloss.Style{
		chatTurnWaiting: sWaiting,
		chatTurnRunning: sWorking,
		"errored":       sFail,
		"remote":        sDelivery,
	}
)

// Column widths (content + gap). NAME is sized to the longest name.
const (
	chatModelW = 9
	chatStateW = 9
	chatAgeW   = 6
	chatLastW  = 8 // the least LAST worth drawing
)

// mergeChats folds a fresh pass over the last known rows, in place. A deep
// pass stands as read. A cheap pass knows nothing of the costly fields, so
// each row keeps what the last deep pass said about the same pane — except
// what the cheap pass can itself refute: a pane no longer running claude is
// stopped and waits on nobody.
func mergeChats(prev, fresh []ChatRow, deep bool) []ChatRow {
	if !deep {
		for i := range fresh {
			f := &fresh[i]
			for j := range prev {
				if prev[j].Pane != f.Pane {
					continue
				}
				p := prev[j]
				f.Label, f.LastActive, f.Last = p.Label, p.LastActive, p.Last
				f.Waiting, f.Turn = p.Waiting, p.Turn
				break
			}
			if !f.Busy && f.Host == "" {
				f.Waiting, f.Turn = false, chatTurnStopped
			}
		}
	}
	sortChats(fresh)
	return fresh
}

// sortChats orders the box: the chats on screen first, then the hidden
// ones, each by number — positions hold still under the cursor.
func sortChats(rows []ChatRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Hidden != b.Hidden {
			return !a.Hidden
		}
		return a.N < b.N
	})
}

// chatIsWaiting: the agent is blocked on a picker — by either field.
func chatIsWaiting(r ChatRow) bool { return r.Waiting || r.Turn == chatTurnWaiting }

// chatCounter is the box title: `CHATS 3 · 2 hidden · 1 waiting`, zero
// parts dropped.
func chatCounter(rows []ChatRow) string {
	hidden, waiting := 0, 0
	for _, r := range rows {
		if r.Hidden {
			hidden++
		}
		if chatIsWaiting(r) {
			waiting++
		}
	}
	s := fmt.Sprintf("CHATS %d", len(rows))
	if hidden > 0 {
		s += fmt.Sprintf(" · %d hidden", hidden)
	}
	if waiting > 0 {
		s += fmt.Sprintf(" · %d waiting", waiting)
	}
	return s
}

// chatName names a row: `cockpit·2` for a pane on screen, `chat-3` for a
// hidden session, with the `@host` marker on a remote pane.
func chatName(r ChatRow) string {
	name := fmt.Sprintf("cockpit·%d", r.N)
	if r.Hidden {
		name = fmt.Sprintf("chat-%d", r.N)
	}
	if r.Host != "" {
		name += " @" + r.Host
	}
	return name
}

// chatState is the STATE cell: the turn the last costly pass read, else
// what the cheap pass alone can say.
func chatState(r ChatRow) string {
	switch {
	case chatIsWaiting(r):
		return "WAITING"
	case r.Turn != "" && r.Turn != "unknown":
		return r.Turn
	case r.Host != "":
		return "remote"
	case r.Busy:
		return chatTurnRunning
	}
	return chatTurnStopped
}

// chatLine is one row, fully derived in assembleChats: plain cells plus
// the table indexes View styles them with.
type chatLine struct {
	kind    int    // chatShown | chatHidden | chatWaiting
	style   string // chatStyles key
	name    string
	model   string
	state   string
	age     string
	last    string
	waiting bool
}

// buildChatLines derives the rows. Pure: rows in, plain cells out.
func buildChatLines(dst []chatLine, rows []ChatRow) []chatLine {
	dst = dst[:0]
	for _, r := range rows {
		l := chatLine{kind: chatShown, name: chatName(r), model: r.Model, state: chatState(r)}
		if r.Hidden {
			l.kind = chatHidden
		}
		l.style = l.state
		if r.Host != "" && l.state != chatTurnRunning {
			l.style = "remote"
		}
		if chatIsWaiting(r) {
			l.kind, l.waiting, l.style = chatWaiting, true, chatTurnWaiting
		}
		if l.model == "" {
			l.model = "—"
		}
		at := r.LastActive
		if at.IsZero() {
			at = r.Created
		}
		if !at.IsZero() {
			l.age = age(at)
		}
		l.last = strings.Join(strings.Fields(r.Last), " ")
		if l.last == "" {
			l.last = strings.Join(strings.Fields(r.Label), " ")
		}
		dst = append(dst, l)
	}
	return dst
}

// assembleChats rebuilds the rows the box renders. Called from Update on
// each chatsMsg — never per frame — reusing the backing array.
func (m *Model) assembleChats() {
	m.chats = buildChatLines(m.chats, m.chatRows)
	m.chatTitle = chatCounter(m.chatRows)
	m.chatNameW = len("CHAT")
	for _, l := range m.chats {
		if n := utf8.RuneCountInString(l.name); n > m.chatNameW {
			m.chatNameW = n
		}
	}
	if m.chatNameW > 24 {
		m.chatNameW = 24
	}
	m.chatNameW++ // one-cell gap
	if len(m.chats) == 0 {
		m.chatSel = 0
		if m.focus == focusChats {
			m.focus = focusAgents
		}
		return
	}
	if m.chatSel >= len(m.chats) {
		m.chatSel = len(m.chats) - 1
	}
}

// chatCols says which optional columns fit a content width w, dropping
// right-to-left: LAST, then AGE, then MODEL. lastW is LAST's width.
func chatCols(w, nameW int) (model, ageCol bool, lastW int) {
	base := 3 + nameW + chatStateW // cursor, glyph, gap · CHAT · STATE
	if w < base+chatModelW {
		return false, false, 0
	}
	if w < base+chatModelW+chatAgeW {
		return true, false, 0
	}
	if lastW = w - base - chatModelW - chatAgeW; lastW < chatLastW {
		lastW = 0
	}
	return true, true, lastW
}

// spareRows is what the frame has left once the header, AGENTS, ACTIVITY's
// own chrome and the footer are paid for — the rows CHATS, FEATURES,
// the feed and the scene share.
func (m Model) spareRows() int {
	return m.height - (len(m.board) + 4) - 5 - m.footerHeight()
}

// chatLayout is how the CHATS box spends its rows this frame.
type chatLayout struct {
	height int // total box rows incl. border; 0 = no box
	start  int // first shown chat
	shown  int
	more   int // chats behind the `+N more` line
}

// chatLayout budgets the box against the spare rows. It leaves the feed
// one row and never takes a row from AGENTS or the footer: with no room
// it draws nothing. Pure arithmetic — no allocation, safe per frame.
func (m Model) chatLayout() chatLayout {
	n := len(m.chats)
	if n == 0 {
		return chatLayout{}
	}
	room := m.spareRows() - 4 - 1 // border, title, column header · one feed row
	if room < 1 {
		return chatLayout{}
	}
	lay := chatLayout{shown: min(min(n, maxChatRows), room)}
	if lay.shown < n {
		if room == lay.shown && lay.shown > 1 {
			lay.shown-- // make room for the +N more line
		}
		if room > lay.shown {
			lay.more = n - lay.shown
		}
	}
	lay.height = 4 + lay.shown
	if lay.more > 0 {
		lay.height++
	}
	if m.chatSel >= lay.shown {
		lay.start = m.chatSel - lay.shown + 1
	}
	return lay
}

// chatsVisible: the box is on screen, so tab may stop on it.
func (m Model) chatsVisible() bool { return m.chatLayout().height > 0 }

// nextFocus is the tab cycle, top to bottom and around: FEATURES → AGENTS
// → CHATS. An inert panel (no open feature, no chat on screen) is skipped,
// so with neither tab stays on AGENTS.
func nextFocus(cur int, hasFeatures, hasChats bool) int {
	order := [...]int{focusFeatures, focusAgents, focusChats}
	at := 1
	for i, f := range order {
		if f == cur {
			at = i
		}
	}
	for step := 1; step <= len(order); step++ {
		f := order[(at+step)%len(order)]
		if (f == focusFeatures && !hasFeatures) || (f == focusChats && !hasChats) {
			continue
		}
		return f
	}
	return focusAgents
}

// viewChats renders the box for the layout chatLayout chose.
func (m Model) viewChats(lay chatLayout) string {
	w := m.width - 4
	model, ageCol, lastW := chatCols(w, m.chatNameW)
	focused := m.focus == focusChats

	header := "   " + pad("CHAT", m.chatNameW)
	if model {
		header += pad("MODEL", chatModelW)
	}
	header += pad("STATE", chatStateW)
	if ageCol {
		header += pad("AGE", chatAgeW)
	}
	if lastW > 0 {
		header += "LAST"
	}
	rows := make([]string, 0, lay.height)
	rows = append(rows, sHeaderCol.Render(truncPad(header, w)))
	for i := lay.start; i < lay.start+lay.shown; i++ {
		l := m.chats[i]
		st, ok := chatStyles[l.style]
		if !ok {
			st = sIdle
		}
		cursor := " "
		if focused && i == m.chatSel {
			cursor = sSelected.Render("▸")
		}
		name := sChrome.Render(pad(l.name, m.chatNameW))
		if l.waiting {
			name = sWaiting.Render(pad(l.name, m.chatNameW))
		}
		line := cursor + st.Render(chatGlyphs[l.kind]) + " " + name
		if model {
			line += sDim.Render(pad(trunc(l.model, chatModelW-1), chatModelW))
		}
		line += st.Render(pad(trunc(l.state, chatStateW-1), chatStateW))
		if ageCol {
			line += sChrome.Render(pad(trunc(l.age, chatAgeW-1), chatAgeW))
		}
		if lastW > 0 && l.last != "" {
			last := sDim
			if l.waiting {
				last = sQuestion
			}
			line += last.Render(trunc(l.last, lastW))
		}
		rows = append(rows, truncPad(line, w))
	}
	if lay.more > 0 {
		rows = append(rows, truncPad(sDim.Render(fmt.Sprintf("  +%d more", lay.more)), w))
	}
	titleStyle, border := sPanelTitle, sPanel
	if focused {
		titleStyle, border = sPanelTitleFocus, sPanelFocus
	}
	// The counter rides the title line: clamp it, a box title has no
	// width of its own to fall back on.
	body := truncPad(titleStyle.Render(m.chatTitle), w) + "\n" + strings.Join(rows, "\n")
	return border.Width(m.width - 2).Render(body)
}
