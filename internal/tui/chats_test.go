package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
)

// Chat fixtures, by scenario.
func chatShownRow(n int) ChatRow {
	return ChatRow{Pane: fmt.Sprintf("%%%d", n), Session: "grove-golden", N: n, Model: "opus", Busy: true, Turn: "idle",
		LastActive: time.Now().Add(-2 * time.Minute), Last: "anything need me? — fleet summary sent"}
}

func chatHiddenRow(n int) ChatRow {
	return ChatRow{Pane: fmt.Sprintf("%%%d", 10+n), Session: fmt.Sprintf("grove-chat-golden-%d", n), Hidden: true, N: n, Model: "sonnet", Busy: true, Turn: "running",
		LastActive: time.Now().Add(-6 * time.Minute), Last: "token-diet spec sweep · 4/7 subagents back"}
}

func chatWaitingRow(n int) ChatRow {
	r := chatHiddenRow(n)
	r.Waiting, r.Turn, r.Last = true, "waiting", "asks: file as one issue or two?"
	return r
}

func chatRemoteRow(n int) ChatRow {
	return ChatRow{Pane: fmt.Sprintf("%%%d", 20+n), Session: "grove-golden", N: n, Host: "groveremote", Model: "glm-4.6",
		Created: time.Now().Add(-41 * time.Minute)}
}

// chatHiddenRemoteRow is a hidden REMOTE chat (grove-404) as the cockpit
// reads it off its record: host, session, profile — no pane, no age, no
// state.
func chatHiddenRemoteRow(n int) ChatRow {
	return ChatRow{Session: fmt.Sprintf("grove-chat-golden-%d", n), Hidden: true, N: n, Host: "groveremote", Model: "glm-4.6"}
}

// withChats delivers rows the way the beat does: a deep chatsMsg.
func withChats(t *testing.T, m Model, rows ...ChatRow) Model {
	t.Helper()
	next, cmd := m.Update(chatsMsg{rows: rows, deep: true})
	if cmd != nil {
		t.Fatal("chatsMsg is data only — it must not return a command")
	}
	return next.(Model)
}

func TestChatCounter(t *testing.T) {
	cases := []struct {
		name string
		rows []ChatRow
		want string
	}{
		{"shown only", []ChatRow{chatShownRow(1)}, "CHATS 1"},
		{"hidden", []ChatRow{chatShownRow(1), chatHiddenRow(1), chatHiddenRow(2)}, "CHATS 3 · 2 hidden"},
		{"waiting", []ChatRow{chatShownRow(1), chatHiddenRow(1), chatWaitingRow(2)}, "CHATS 3 · 2 hidden · 1 waiting"},
		{"waiting by turn alone", []ChatRow{{Pane: "%1", N: 1, Turn: "waiting"}}, "CHATS 1 · 1 waiting"},
		{"none", nil, "CHATS 0"},
	}
	for _, tc := range cases {
		if got := chatCounter(tc.rows); got != tc.want {
			t.Errorf("%s: counter = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBuildChatLines(t *testing.T) {
	stopped := ChatRow{Pane: "%30", Hidden: true, N: 4, Turn: "stopped", Label: "  overnight\nmandate "}
	shownWaiting := chatShownRow(2)
	shownWaiting.Waiting = true
	cases := []struct {
		name  string
		row   ChatRow
		glyph string
		cname string
		state string
		style string
		last  string
	}{
		{"shown", chatShownRow(1), "▣", "cockpit·1", "idle", "idle", "anything need me? — fleet summary sent"},
		{"hidden", chatHiddenRow(3), "○", "chat-3", "running", "running", "token-diet spec sweep · 4/7 subagents back"},
		{"waiting overrides hidden", chatWaitingRow(2), "◆", "chat-2", "WAITING", "waiting", "asks: file as one issue or two?"},
		{"waiting overrides shown", shownWaiting, "◆", "cockpit·2", "WAITING", "waiting", "anything need me? — fleet summary sent"},
		{"remote", chatRemoteRow(3), "▣", "cockpit·3 @groveremote", "remote", "remote", ""},
		{"stopped, titled by its label", stopped, "○", "chat-4", "stopped", "stopped", "overnight mandate"},
		{"cheap pass only: busy", ChatRow{Pane: "%1", N: 1, Busy: true}, "▣", "cockpit·1", "running", "running", ""},
		{"cheap pass only: at a shell", ChatRow{Pane: "%1", N: 1}, "▣", "cockpit·1", "stopped", "stopped", ""},
	}
	for _, tc := range cases {
		lines := buildChatLines(nil, []ChatRow{tc.row})
		if len(lines) != 1 {
			t.Fatalf("%s: %d lines", tc.name, len(lines))
		}
		l := lines[0]
		if chatGlyphs[l.kind] != tc.glyph || l.name != tc.cname || l.state != tc.state || l.style != tc.style || l.last != tc.last {
			t.Errorf("%s: got glyph=%s name=%q state=%q style=%q last=%q", tc.name, chatGlyphs[l.kind], l.name, l.state, l.style, l.last)
		}
	}
	if got := buildChatLines(nil, nil); len(got) != 0 {
		t.Errorf("no chats must build no lines: %+v", got)
	}
	// Age: last_active, falling back to created; neither = blank.
	aged := buildChatLines(nil, []ChatRow{chatShownRow(1), chatRemoteRow(2), {Pane: "%9", N: 9}})
	if aged[0].age != "2m" || aged[1].age != "41m" || aged[2].age != "" {
		t.Errorf("ages = %q %q %q, want 2m 41m and blank", aged[0].age, aged[1].age, aged[2].age)
	}
}

func TestSortChatsShownThenHiddenByNumber(t *testing.T) {
	rows := []ChatRow{chatHiddenRow(2), chatShownRow(3), chatWaitingRow(1), chatShownRow(1)}
	sortChats(rows)
	var got []string
	for _, r := range rows {
		got = append(got, chatName(r))
	}
	if s := strings.Join(got, " "); s != "cockpit·1 cockpit·3 chat-1 chat-2" {
		t.Errorf("order = %s", s)
	}
}

// Between costly passes a row shows the last known value; the cheap pass
// only refutes what it can see for itself.
func TestMergeChatsKeepsLastKnown(t *testing.T) {
	prev := []ChatRow{chatShownRow(1), chatWaitingRow(2)}
	cheap := []ChatRow{
		{Pane: prev[1].Pane, Hidden: true, N: 2, Busy: true, Model: "sonnet"},
		{Pane: prev[0].Pane, N: 1, Busy: true, Model: "opus"},
		{Pane: "%99", Hidden: true, N: 3, Busy: true},
	}
	got := mergeChats(prev, cheap, false)
	if len(got) != 3 || got[0].N != 1 || got[0].Hidden {
		t.Fatalf("merged = %+v", got)
	}
	if got[0].Last != prev[0].Last || got[0].Turn != "idle" || got[0].LastActive.IsZero() {
		t.Errorf("shown row lost its last known fields: %+v", got[0])
	}
	if !got[1].Waiting || got[1].Turn != "waiting" || got[1].Last != prev[1].Last {
		t.Errorf("waiting row lost its last known fields: %+v", got[1])
	}
	if got[2].Turn != "" || got[2].Last != "" || got[2].Waiting {
		t.Errorf("a chat the costly pass never saw has nothing to remember: %+v", got[2])
	}

	// The agent exited: the cheap pass alone knows nobody is waiting.
	gone := mergeChats(prev, []ChatRow{{Pane: prev[1].Pane, Hidden: true, N: 2}}, false)
	if gone[0].Waiting || gone[0].Turn != "stopped" || gone[0].Last != prev[1].Last {
		t.Errorf("a pane at a shell = %+v", gone[0])
	}

	// A deep pass stands as read — a closed chat's row is gone with it.
	deep := mergeChats(prev, []ChatRow{{Pane: prev[0].Pane, N: 1, Busy: true, Turn: "running"}}, true)
	if len(deep) != 1 || deep[0].Last != "" || deep[0].Turn != "running" {
		t.Errorf("deep pass = %+v", deep)
	}
}

func TestChatColsDropRightToLeft(t *testing.T) {
	cases := []struct {
		w, nameW   int
		model, age bool
		last       bool
	}{
		{116, 10, true, true, true},
		{76, 10, true, true, true},
		{44, 10, true, true, false}, // LAST has under 8 cells
		{36, 10, true, false, false},
		{30, 10, false, false, false},
		{36, 24, false, false, false}, // a long @host name
	}
	for _, tc := range cases {
		model, age, lastW := chatCols(tc.w, tc.nameW)
		if model != tc.model || age != tc.age || (lastW > 0) != tc.last {
			t.Errorf("w=%d nameW=%d: model=%v age=%v lastW=%d", tc.w, tc.nameW, model, age, lastW)
		}
	}
}

// chatsModel is the golden board plus chats.
func chatsModel(t *testing.T, fx fxLevel, w, h int, rows ...ChatRow) Model {
	t.Helper()
	return withChats(t, goldenModel(t, fx, w, h), rows...)
}

var chatScenarios = []struct {
	name string
	rows func() []ChatRow
}{
	{"mixed", func() []ChatRow {
		return []ChatRow{chatShownRow(1), chatRemoteRow(2), chatHiddenRow(1), chatHiddenRow(2)}
	}},
	{"waiting", func() []ChatRow {
		return []ChatRow{chatHiddenRow(1), chatWaitingRow(2), chatHiddenRow(3)}
	}},
	// grove-404: a remote chat on screen beside one that is hidden.
	{"remote", func() []ChatRow {
		return []ChatRow{chatShownRow(1), chatRemoteRow(2), chatHiddenRemoteRow(3)}
	}},
}

// AC: goldens for some shown + some hidden, and all hidden with one
// WAITING, at every pinned size. (No chats IS the nofeature golden set.)
func TestChatsFrameGolden(t *testing.T) {
	for _, sc := range chatScenarios {
		for _, fx := range []fxLevel{fxOff, fxCalm, fxFull} {
			for _, sz := range goldenSizes {
				got := chatsModel(t, fx, sz[0], sz[1], sc.rows()...).View()
				path := filepath.Join("testdata", fmt.Sprintf("chats-%s-fx%d-%dx%d.golden", sc.name, fx, sz[0], sz[1]))
				if *updateGolden {
					if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if got != string(want) {
					t.Errorf("%s fx=%d %dx%d: frame drifted from the golden:\n%s\n--- want ---\n%s", sc.name, fx, sz[0], sz[1], got, want)
				}
			}
		}
	}
}

// AC: with zero chats the box is nothing — an empty pass leaves the frame
// byte-identical to the model that never heard of chats.
func TestNoChatsFrameUnchanged(t *testing.T) {
	for _, fx := range []fxLevel{fxOff, fxCalm, fxFull} {
		for _, sz := range goldenSizes {
			plain := goldenModel(t, fx, sz[0], sz[1])
			if got, want := withChats(t, plain).View(), plain.View(); got != want {
				t.Errorf("fx=%d %dx%d: an empty chat pass changed the frame", fx, sz[0], sz[1])
			}
		}
	}
}

// boxSpan finds the [first,last] frame lines of the box whose title line
// starts with title; -1 when absent.
func boxSpan(lines []string, title string) (int, int) {
	for i, ln := range lines {
		if !strings.Contains(ln, "│ "+title) || i == 0 {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "╰") {
				return i - 1, j
			}
		}
	}
	return -1, -1
}

// AC: the box never pushes AGENTS or the footer off screen, no line wraps
// or overflows, at every size and down to panes with no room at all.
func TestChatsFrameFits(t *testing.T) {
	sizes := [][2]int{{120, 59}, {120, 40}, {80, 24}, {40, 20}, {60, 16}, {40, 12}, {30, 9}, {20, 6}}
	for _, sz := range sizes {
		for n := 0; n <= 8; n++ {
			for _, fx := range []fxLevel{fxOff, fxCalm, fxFull} {
				var rows []ChatRow
				for i := 1; i <= n; i++ {
					if i%3 == 0 {
						rows = append(rows, chatWaitingRow(i))
					} else if i%2 == 0 {
						rows = append(rows, chatRemoteRow(i))
					} else {
						rows = append(rows, chatHiddenRow(i))
					}
				}
				plain := goldenModel(t, fx, sz[0], sz[1])
				m := withChats(t, plain, rows...)
				lines, base := frameLines(m.View()), frameLines(plain.View())
				if len(lines) > max(len(base), m.height) {
					t.Errorf("%dx%d n=%d fx=%d: %d lines, the chat-less frame has %d", sz[0], sz[1], n, fx, len(lines), len(base))
				}
				for i, ln := range lines {
					if lw := lipgloss.Width(ln); lw > m.width {
						t.Errorf("%dx%d n=%d fx=%d: line %d is %d cells", sz[0], sz[1], n, fx, i, lw)
					}
				}
				// Header + AGENTS are the frame's head, the footer its last
				// line: both byte-identical to the chat-less frame.
				_, agentsEnd := boxSpan(base, "AGENTS")
				for i := 0; i <= agentsEnd; i++ {
					if lines[i] != base[i] {
						t.Errorf("%dx%d n=%d fx=%d: line %d (header/AGENTS) changed:\n%q\n%q", sz[0], sz[1], n, fx, i, lines[i], base[i])
					}
				}
				if lines[len(lines)-1] != base[len(base)-1] {
					t.Errorf("%dx%d n=%d fx=%d: the footer changed", sz[0], sz[1], n, fx)
				}
			}
		}
	}
}

// The feature rail shares the spare rows with CHATS and still fits.
func TestChatsWithFeaturesFrameFits(t *testing.T) {
	for _, sz := range [][2]int{{120, 59}, {80, 24}, {60, 16}, {40, 12}, {100, 30}} {
		for n := 0; n <= 5; n++ {
			m := withChats(t, featureModel(t, n, sz[0], sz[1]), chatShownRow(1), chatHiddenRow(1), chatWaitingRow(2))
			lines := frameLines(m.View())
			if len(lines) > m.height {
				t.Errorf("%dx%d n=%d: %d lines > height %d", sz[0], sz[1], n, len(lines), m.height)
			}
			for i, ln := range lines {
				if lw := lipgloss.Width(ln); lw > m.width {
					t.Errorf("%dx%d n=%d: line %d is %d cells", sz[0], sz[1], n, i, lw)
				}
			}
		}
	}
}

// At 40x20 the box degrades right-to-left: LAST goes first, then — once a
// long @host name needs the cells — AGE and MODEL. The rows stay whole.
func TestChatsNarrowDropsColumns(t *testing.T) {
	m := chatsModel(t, fxOff, 40, 20, chatScenarios[1].rows()...)
	box := m.viewChats(m.chatLayout())
	for _, gone := range []string{"LAST", "asks:"} {
		if strings.Contains(box, gone) {
			t.Errorf("40 cols must drop %q:\n%s", gone, box)
		}
	}
	for _, kept := range []string{"CHATS 3 · 3 hidden · 1 waiting", "◆ chat-2", "WAITING", "MODEL", "sonnet", "AGE", "6m"} {
		if !strings.Contains(box, kept) {
			t.Errorf("40 cols must keep %q:\n%s", kept, box)
		}
	}
	long := chatsModel(t, fxOff, 40, 20, chatRemoteRow(2), chatWaitingRow(1))
	box = long.viewChats(long.chatLayout())
	for _, gone := range []string{"LAST", "AGE", "MODEL", "sonnet", "41m"} {
		if strings.Contains(box, gone) {
			t.Errorf("40 cols with a long name must drop %q:\n%s", gone, box)
		}
	}
	for _, kept := range []string{"cockpit·2 @groveremote", "remote", "◆ chat-1", "WAITING"} {
		if !strings.Contains(box, kept) {
			t.Errorf("40 cols with a long name must keep %q:\n%s", kept, box)
		}
	}
	wide := chatsModel(t, fxOff, 120, 40, chatScenarios[1].rows()...)
	if box := wide.viewChats(wide.chatLayout()); !strings.Contains(box, "asks: file as one issue or two?") || !strings.Contains(box, "6m") {
		t.Errorf("120 cols must show AGE and LAST:\n%s", box)
	}
}

// A counter longer than the box is clamped, never wrapped.
func TestChatsTitleClampsToWidth(t *testing.T) {
	m := chatsModel(t, fxOff, 24, 20, chatWaitingRow(1), chatWaitingRow(2))
	for i, ln := range frameLines(m.viewChats(m.chatLayout())) {
		if lw := lipgloss.Width(ln); lw > m.width {
			t.Errorf("line %d is %d cells in a %d-col pane: %q", i, lw, m.width, ln)
		}
	}
}

// More chats than rows: the cursor scrolls the window, `+N more` says so.
func TestChatsOverflowScrolls(t *testing.T) {
	var rows []ChatRow
	for i := 1; i <= 8; i++ {
		rows = append(rows, chatHiddenRow(i))
	}
	m := chatsModel(t, fxOff, 120, 40, rows...)
	lay := m.chatLayout()
	if lay.shown != maxChatRows || lay.more != 3 || lay.start != 0 {
		t.Fatalf("layout = %+v", lay)
	}
	if !strings.Contains(m.viewChats(lay), "+3 more") {
		t.Error("the overflow line is missing")
	}
	m.chatSel = 7
	if lay = m.chatLayout(); lay.start != 3 {
		t.Errorf("cursor on the last chat: start = %d, want 3", lay.start)
	}
	if box := m.viewChats(lay); !strings.Contains(box, "chat-8") || strings.Contains(box, "chat-1 ") {
		t.Errorf("the window must follow the cursor:\n%s", box)
	}
}

// AC: tab cycles FEATURES ⇄ AGENTS ⇄ CHATS, skipping inert panels; j/k
// move the chat cursor only; no row key acts from the box.
func TestNextFocus(t *testing.T) {
	cases := []struct {
		cur             int
		features, chats bool
		want            int
	}{
		{focusAgents, false, false, focusAgents},
		{focusAgents, true, false, focusFeatures},
		{focusFeatures, true, false, focusAgents},
		{focusAgents, false, true, focusChats},
		{focusChats, false, true, focusAgents},
		{focusAgents, true, true, focusChats},
		{focusChats, true, true, focusFeatures},
		{focusFeatures, true, true, focusAgents},
		{focusChats, false, false, focusAgents},
	}
	for _, tc := range cases {
		if got := nextFocus(tc.cur, tc.features, tc.chats); got != tc.want {
			t.Errorf("nextFocus(%d, features=%v, chats=%v) = %d, want %d", tc.cur, tc.features, tc.chats, got, tc.want)
		}
	}
}

func TestChatsFocusAndSelection(t *testing.T) {
	m := chatsModel(t, fxOff, 120, 40, chatShownRow(1), chatHiddenRow(1), chatWaitingRow(2))
	if m.focus != focusAgents {
		t.Fatal("AGENTS holds focus by default")
	}
	if strings.Contains(m.viewChats(m.chatLayout()), "▸") {
		t.Error("an unfocused box draws no cursor")
	}
	nm, _ := m.handleKey(fkey("tab"))
	m = nm.(Model)
	if m.focus != focusChats {
		t.Fatal("tab from AGENTS should focus CHATS")
	}
	sel := m.sel
	nm, _ = m.handleKey(fkey("j"))
	m = nm.(Model)
	if m.chatSel != 1 || m.sel != sel {
		t.Errorf("j with CHATS focused moves the chat cursor only (chatSel=%d sel=%d)", m.chatSel, m.sel)
	}
	nm, _ = m.handleKey(fkey("k"))
	nm, _ = nm.(Model).handleKey(fkey("k"))
	m = nm.(Model)
	if m.chatSel != 2 {
		t.Errorf("k wraps: chatSel = %d, want 2", m.chatSel)
	}
	// Task keys have no chat under them (the chat keys: chatkeys_test.go).
	for _, k := range []string{"n", "d", "v", "m", "o", "p", "t"} {
		nm, cmd := m.handleKey(fkey(k))
		got := nm.(Model)
		if cmd != nil || got.mode != modeList || got.detail != nil || !strings.Contains(got.flash, "tab to AGENTS") {
			t.Errorf("%s on CHATS must not act (mode=%d flash=%q)", k, got.mode, got.flash)
		}
	}
	nm, _ = m.handleKey(fkey("tab"))
	m = nm.(Model)
	if m.focus != focusAgents {
		t.Error("tab from CHATS returns to AGENTS with no feature open")
	}

	// The last chat closes while the box is focused: focus falls back.
	m.focus = focusChats
	m = withChats(t, m)
	if m.focus != focusAgents || m.chatSel != 0 {
		t.Errorf("focus=%d chatSel=%d after the chats went away", m.focus, m.chatSel)
	}

	// A closed feature set must not steal focus from CHATS.
	m = chatsModel(t, fxOff, 120, 40, chatShownRow(1))
	m.focus = focusChats
	m.assemble()
	if m.focus != focusChats {
		t.Error("assemble with no feature must leave CHATS focused")
	}
}

// chatPasses runs cmd (recursing into batches) and returns the deep flag
// of every chat pass it made. A command still running after a moment is a
// beat's timer: it is left to expire on its own rather than waited for.
func chatPasses(t *testing.T, cmd tea.Cmd) []bool {
	t.Helper()
	var calls []bool
	prev := CockpitChats
	CockpitChats = func(_ string, deep bool) []ChatRow {
		calls = append(calls, deep)
		return nil
	}
	defer func() { CockpitChats = prev }()
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		select {
		case msg := <-done:
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range batch {
					walk(sub)
				}
			}
		case <-time.After(2 * time.Second / 4):
		}
	}
	walk(cmd)
	return calls
}

// AC: the 1s beat makes exactly ONE chat pass — the cheap one — while the
// box is unfocused; the costly pass runs on the 30s beat, and on the 1s
// beat only while the box is focused. No pass re-arms anything.
func TestChatPassCadence(t *testing.T) {
	m := chatsModel(t, fxOff, 120, 40, chatShownRow(1), chatHiddenRow(1))
	// The beats' other commands run for real: give them a scratch state dir.
	m.stateDir = t.TempDir()
	m.folder = state.NewFolder(m.stateDir, feedTail)

	_, cmd := m.Update(tickMsg{})
	if got := chatPasses(t, cmd); len(got) != 1 || got[0] {
		t.Errorf("unfocused 1s beat made passes %v, want exactly one cheap pass", got)
	}
	_, cmd = m.Update(prTickMsg{})
	if got := chatPasses(t, cmd); len(got) != 1 || !got[0] {
		t.Errorf("30s beat made passes %v, want exactly one costly pass", got)
	}
	m.focus = focusChats
	_, cmd = m.Update(tickMsg{})
	if got := chatPasses(t, cmd); len(got) != 1 || !got[0] {
		t.Errorf("focused 1s beat made passes %v, want exactly one costly pass", got)
	}
	if got := chatPasses(t, m.Init()); len(got) != 1 || !got[0] {
		t.Errorf("launch made passes %v, want exactly one costly pass", got)
	}
	for _, msg := range []tea.Msg{refreshMsg{ok: true}, prsMsg{}, featuresMsg{}, chatsMsg{}} {
		_, cmd := m.Update(msg)
		if got := chatPasses(t, cmd); len(got) != 0 {
			t.Errorf("%T made chat passes %v — only the two beats may", msg, got)
		}
	}
}

// View reads nothing: the rows were derived in Update.
func TestChatsViewMakesNoPass(t *testing.T) {
	m := chatsModel(t, fxFull, 120, 40, chatShownRow(1), chatWaitingRow(1))
	prev := CockpitChats
	defer func() { CockpitChats = prev }()
	CockpitChats = func(string, bool) []ChatRow {
		t.Error("View must never read chats")
		return nil
	}
	_ = m.View()
}

// The train invariant (opt-in, default unchanged): with only shown chats
// the box is the sole visible difference — header, AGENTS and footer are
// byte-identical, and every spawn key does exactly what it did.
func TestShownChatsChangeNothingButTheBox(t *testing.T) {
	for _, fx := range []fxLevel{fxOff, fxCalm, fxFull} {
		for _, sz := range goldenSizes {
			plain := goldenModel(t, fx, sz[0], sz[1])
			m := withChats(t, plain, chatShownRow(1), chatShownRow(2))
			lines, base := frameLines(m.View()), frameLines(plain.View())
			first, last := boxSpan(lines, "CHATS")
			if first < 0 {
				t.Fatalf("fx=%d %dx%d: no CHATS box", fx, sz[0], sz[1])
			}
			if strings.Contains(strings.Join(lines[first:last+1], "\n"), "hidden") {
				t.Errorf("fx=%d %dx%d: nothing is hidden, the counter must not say so", fx, sz[0], sz[1])
			}
			for i := 0; i < first; i++ {
				if lines[i] != base[i] {
					t.Errorf("fx=%d %dx%d: line %d above the box changed", fx, sz[0], sz[1], i)
				}
			}
			if lines[len(lines)-1] != base[len(base)-1] {
				t.Errorf("fx=%d %dx%d: the footer changed", fx, sz[0], sz[1])
			}
			if m.focus != plain.focus || m.sel != plain.sel || m.mode != plain.mode || len(m.board) != len(plain.board) {
				t.Errorf("fx=%d %dx%d: chats moved the board's state", fx, sz[0], sz[1])
			}
		}
	}

	var spawned []string
	prevO, prevP := SpawnOrchestrator, SpawnOrchestratorProfile
	defer func() { SpawnOrchestrator, SpawnOrchestratorProfile = prevO, prevP }()
	plain := armedModel("vps")
	plain.width, plain.height = 120, 40
	m := withChats(t, plain, chatShownRow(1), chatShownRow(2))
	for _, k := range []string{"O", "0", ")", "1", "8", "@"} {
		a, acmd := plain.handleKey(fkey(k))
		b, bcmd := m.handleKey(fkey(k))
		am, bm := a.(Model), b.(Model)
		if am.flash != bm.flash || am.mode != bm.mode || am.armedHost != bm.armedHost || (acmd == nil) != (bcmd == nil) {
			t.Errorf("%s: chats changed the key (flash %q vs %q, mode %d vs %d)", k, am.flash, bm.flash, am.mode, bm.mode)
		}
	}
	SpawnOrchestrator = func(*config.Config) (string, error) { spawned = append(spawned, "pane"); return "ok", nil }
	_, cmd := m.handleKey(fkey("O"))
	if cmd == nil {
		t.Fatal("O must still spawn")
	}
	cmd()
	if len(spawned) != 1 {
		t.Errorf("O spawned %v, want one cockpit pane through SpawnOrchestrator", spawned)
	}
}

// grove-404: a hidden remote row is its record and nothing else — the state
// and last-line cells say so rather than pretend.
func TestHiddenRemoteRowCells(t *testing.T) {
	lines := buildChatLines(nil, []ChatRow{chatRemoteRow(2), chatHiddenRemoteRow(3)})
	shown, hidden := lines[0], lines[1]
	if shown.kind != chatShown || shown.state != "remote" || shown.name != "cockpit·2 @groveremote" {
		t.Errorf("shown remote line = %+v", shown)
	}
	if hidden.kind != chatHidden || hidden.name != "chat-3 @groveremote" || hidden.model != "glm-4.6" {
		t.Errorf("hidden remote line = %+v", hidden)
	}
	if hidden.state != "—" || hidden.last != "—" || hidden.age != "" || hidden.style != "remote" {
		t.Errorf("hidden remote line claims what the cockpit cannot know: %+v", hidden)
	}
	if got := chatGlyphs[hidden.kind]; got != "○" {
		t.Errorf("hidden remote glyph = %q", got)
	}
	if got := chatCounter([]ChatRow{chatRemoteRow(2), chatHiddenRemoteRow(3)}); got != "CHATS 2 · 1 hidden" {
		t.Errorf("counter = %q", got)
	}
}

// Two hidden remote chats have no pane to tell them apart: a cheap pass must
// not hand one the other's last-known fields, and an open modal must stay on
// ITS chat.
func TestHiddenRemoteRowsKeepTheirIdentity(t *testing.T) {
	a, b := chatHiddenRemoteRow(1), chatHiddenRemoteRow(2)
	a.Last = "only a's"
	got := mergeChats([]ChatRow{a, b}, []ChatRow{chatHiddenRemoteRow(1), chatHiddenRemoteRow(2)}, false)
	if got[0].Last != "only a's" || got[1].Last != "" {
		t.Errorf("merge crossed the rows: %q / %q", got[0].Last, got[1].Last)
	}
	if sameChat(a, b) || !sameChat(b, chatHiddenRemoteRow(2)) {
		t.Error("sameChat must tell hidden remote chats apart by host + session")
	}
	other := chatHiddenRemoteRow(2)
	other.Host = "elsewhere"
	if sameChat(b, other) {
		t.Error("the same session name on another host is another chat")
	}
	if !sameChat(chatShownRow(1), chatShownRow(1)) || sameChat(chatShownRow(1), chatShownRow(2)) {
		t.Error("a pane is still its own identity")
	}
}
