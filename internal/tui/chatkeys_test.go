package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
)

// AC: the key → action decision per row kind.
func TestChatKeyAction(t *testing.T) {
	shown, hidden, waiting, remote := chatShownRow(1), chatHiddenRow(1), chatWaitingRow(2), chatRemoteRow(2)
	remoteHidden := chatHiddenRemoteRow(3)
	cases := []struct {
		key   string
		row   ChatRow
		want  chatAct
		flash string
	}{
		{"h", shown, actHide, ""},
		{"h", hidden, actShow, ""},
		{"h", waiting, actShow, ""},
		{"enter", shown, actFocus, ""},
		{"enter", hidden, actShowFocus, ""},
		{"enter", waiting, actShowFocus, ""},
		{"a", shown, actRefuse, "shown — enter to focus it"},
		{"a", hidden, actReply, ""},
		{"a", waiting, actReply, ""},
		{"x", shown, actClose, ""},
		{"x", hidden, actClose, ""},
		// Remote rows (grove-404): h / enter / x as on a local row; a is
		// refused either way — no relay carries a reply to a host's chat.
		{"h", remote, actHide, ""},
		{"enter", remote, actFocus, ""},
		{"a", remote, actRefuse, chatsRemoteFlash},
		{"x", remote, actClose, ""},
		{"h", remoteHidden, actShow, ""},
		{"enter", remoteHidden, actShowFocus, ""},
		{"a", remoteHidden, actRefuse, chatsRemoteFlash},
		{"x", remoteHidden, actClose, ""},
		// Task keys are refused, never passed to the AGENTS cursor.
		{"n", shown, actRefuse, chatsTaskKeysFlash},
		{"d", hidden, actRefuse, chatsTaskKeysFlash},
		{"v", remote, actRefuse, chatsTaskKeysFlash},
		{"o", shown, actRefuse, chatsTaskKeysFlash},
		{"p", shown, actRefuse, chatsTaskKeysFlash},
		{"t", shown, actRefuse, chatsTaskKeysFlash},
		{"m", shown, actRefuse, chatsTaskKeysFlash},
	}
	for _, c := range cases {
		got, flash := chatKeyAction(c.key, c.row)
		if got != c.want || flash != c.flash {
			t.Errorf("%s on %s = (%d, %q), want (%d, %q)", c.key, chatName(c.row), got, flash, c.want, c.flash)
		}
	}
	// Globals are untouched under CHATS focus.
	for _, k := range []string{"X", "O", "0", ")", "1", "8", "@", "L", "R", "g", "$", "c", "*", "?", "q", "r", "s", "j", "k", "tab", "H", "A"} {
		for _, r := range []ChatRow{shown, hidden, remote} {
			if got, flash := chatKeyAction(k, r); got != actNone || flash != "" {
				t.Errorf("%s on %s = (%d, %q), want the key left alone", k, chatName(r), got, flash)
			}
		}
	}
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiSeq.ReplaceAllString(s, "") }

// chatCalls records what the injected chat functions were asked to do.
type chatCalls struct {
	log      []string
	sendWarn string
	err      error
}

func stubChatFuncs(t *testing.T) *chatCalls {
	t.Helper()
	c := &chatCalls{}
	ph, ps, pf, pn, pp, pc := HideChat, ShowChat, FocusChat, SendChat, CloseChatPane, CloseChatSession
	prs, prc := ShowRemoteChat, CloseRemoteChat
	t.Cleanup(func() {
		HideChat, ShowChat, FocusChat, SendChat, CloseChatPane, CloseChatSession = ph, ps, pf, pn, pp, pc
		ShowRemoteChat, CloseRemoteChat = prs, prc
	})
	ShowRemoteChat = func(host, session string, focus bool) (string, error) {
		c.log = append(c.log, fmt.Sprintf("show-remote %s %s focus=%v", host, session, focus))
		return "%31", c.err
	}
	CloseRemoteChat = func(host, session, pane string) error {
		c.log = append(c.log, fmt.Sprintf("close-remote %s session=%q pane=%q", host, session, pane))
		return c.err
	}
	HideChat = func(pane string) (string, error) {
		c.log = append(c.log, "hide "+pane)
		return "grove-chat-golden-9", c.err
	}
	ShowChat = func(session string, focus bool) (string, error) {
		c.log = append(c.log, fmt.Sprintf("show %s focus=%v", session, focus))
		return "%11", c.err
	}
	FocusChat = func(pane string) error {
		c.log = append(c.log, "focus "+pane)
		return c.err
	}
	SendChat = func(session, text string) (string, error) {
		c.log = append(c.log, "send "+session+" "+text)
		return c.sendWarn, c.err
	}
	CloseChatPane = func(pane string) error {
		c.log = append(c.log, "close-pane "+pane)
		return c.err
	}
	CloseChatSession = func(session string) error {
		c.log = append(c.log, "close-session "+session)
		return c.err
	}
	return c
}

// chatsFocused is a model with CHATS focused and the cursor on row sel.
func chatsFocused(t *testing.T, w, h, sel int, rows ...ChatRow) Model {
	t.Helper()
	m := chatsModel(t, fxOff, w, h, rows...)
	m.focus, m.chatSel = focusChats, sel
	return m
}

func chatPress(t *testing.T, m Model, k string) (Model, tea.Cmd) {
	t.Helper()
	msg := fkey(k)
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	}
	nm, cmd := m.Update(msg)
	return nm.(Model), cmd
}

// acted runs a chat command and feeds its answer back through Update.
func acted(t *testing.T, m Model, cmd tea.Cmd) (Model, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("the key emitted no command")
	}
	msg, ok := cmd().(chatActedMsg)
	if !ok {
		t.Fatalf("the command answered %T, want chatActedMsg", msg)
	}
	nm, next := m.Update(msg)
	return nm.(Model), next
}

// AC: h on shown emits the hide cmd; h on hidden emits show (no focus);
// enter on hidden emits show+focus; enter on shown focuses.
func TestChatKeysEmitCommands(t *testing.T) {
	cases := []struct {
		key  string
		sel  int
		want string
		said string
	}{
		{"h", 0, "hide %1", "○ hid cockpit·1 as grove-chat-golden-9"},
		{"h", 2, "show grove-chat-golden-1 focus=false", "▣ showed chat-1"},
		{"enter", 2, "show grove-chat-golden-1 focus=true", "▣ showed chat-1"},
		{"enter", 0, "focus %1", "→ cockpit·1"},
		// grove-404: a remote row runs the same keys. Hide goes through the
		// one HideChat (the pane's stamps say it is remote); show names the
		// host, because a hidden remote chat has no pane and no local session.
		{"h", 1, "hide %22", "○ hid cockpit·2 @groveremote as grove-chat-golden-9"},
		{"enter", 1, "focus %22", "→ cockpit·2 @groveremote"},
		{"h", 3, "show-remote groveremote grove-chat-golden-3 focus=false", "▣ showed chat-3 @groveremote"},
		{"enter", 3, "show-remote groveremote grove-chat-golden-3 focus=true", "▣ showed chat-3 @groveremote"},
	}
	for _, c := range cases {
		calls := stubChatFuncs(t)
		// The box sorts them: shown (local 1, remote 2), then hidden (local 1,
		// remote 3) — rows 0 to 3.
		m := chatsFocused(t, 120, 40, c.sel, chatShownRow(1), chatHiddenRow(1), chatRemoteRow(2), chatHiddenRemoteRow(3))
		m, cmd := chatPress(t, m, c.key)
		if len(calls.log) != 0 {
			t.Fatalf("%s: tmux work ran inside Update: %v", c.key, calls.log)
		}
		if m.mode != modeList {
			t.Errorf("%s: mode = %d, want the list", c.key, m.mode)
		}
		m, next := acted(t, m, cmd)
		if !reflect.DeepEqual(calls.log, []string{c.want}) {
			t.Errorf("%s: calls = %v, want [%s]", c.key, calls.log, c.want)
		}
		if m.flash != c.said {
			t.Errorf("%s: flash = %q, want %q", c.key, m.flash, c.said)
		}
		// One key press, ONE refresh of the box — the costly pass.
		if got := chatPasses(t, next); len(got) != 1 || !got[0] {
			t.Errorf("%s: the answer made passes %v, want exactly one costly pass", c.key, got)
		}
	}
}

// A failed action lands in the flash, first line only (the footer is one
// line and a tmux error carries stderr below it).
func TestChatKeyErrorIsOneFlashLine(t *testing.T) {
	calls := stubChatFuncs(t)
	calls.err = errors.New("tmux break-pane: exit status 1\ncan't find pane %1")
	m := chatsFocused(t, 120, 40, 0, chatShownRow(1))
	m, cmd := chatPress(t, m, "h")
	m, _ = acted(t, m, cmd)
	if m.flash != "tmux break-pane: exit status 1" {
		t.Errorf("flash = %q", m.flash)
	}
	for i, ln := range frameLines(m.View()) {
		if lipgloss.Width(ln) > 120 {
			t.Errorf("line %d overflows after an error flash: %q", i, ln)
		}
	}
}

func TestChatKeysRefusals(t *testing.T) {
	calls := stubChatFuncs(t)
	m := chatsFocused(t, 120, 40, 0, chatShownRow(1), chatRemoteRow(2), chatHiddenRow(1))
	got, cmd := chatPress(t, m, "a")
	if cmd != nil || got.mode != modeList || got.flash != "shown — enter to focus it" {
		t.Errorf("a on a shown row: mode=%d flash=%q cmd=%v", got.mode, got.flash, cmd != nil)
	}
	// a on a remote row, shown or hidden: no relay carries a reply to a
	// host's chat, so it is refused and nothing opens (grove-404).
	m = chatsFocused(t, 120, 40, 0, chatShownRow(1), chatRemoteRow(2), chatHiddenRow(1), chatHiddenRemoteRow(3))
	for _, sel := range []int{1, 3} {
		m.chatSel = sel
		got, cmd := chatPress(t, m, "a")
		if cmd != nil || got.mode != modeList || got.flash != chatsRemoteFlash {
			t.Errorf("a on remote row %d: mode=%d flash=%q cmd=%v", sel, got.mode, got.flash, cmd != nil)
		}
	}
	if len(calls.log) != 0 {
		t.Errorf("a refusal called %v", calls.log)
	}
}

// AC: x needs y — anything else cancels.
func TestChatCloseNeedsY(t *testing.T) {
	for _, c := range []struct {
		sel  int
		want string
	}{
		{0, "close-pane %1"},
		{2, "close-session grove-chat-golden-1"},
		// grove-404: a shown remote row names its pane (host and session are
		// read off the pane's stamps); a hidden one names host + session.
		{1, `close-remote groveremote session="" pane="%22"`},
		{3, `close-remote groveremote session="grove-chat-golden-3" pane=""`},
	} {
		for _, answer := range []string{"y", "n", "Y", "enter", "esc", "x", "q", "1"} {
			calls := stubChatFuncs(t)
			m := chatsFocused(t, 120, 40, c.sel, chatShownRow(1), chatHiddenRow(1), chatRemoteRow(2), chatHiddenRemoteRow(3))
			m, cmd := chatPress(t, m, "x")
			if cmd != nil || m.mode != modeChatClose {
				t.Fatalf("x: mode=%d cmd=%v, want the confirm and nothing run", m.mode, cmd != nil)
			}
			m, cmd = chatPress(t, m, answer)
			if m.mode != modeList {
				t.Errorf("%q: mode = %d, want the list", answer, m.mode)
			}
			if answer != "y" {
				if cmd != nil || len(calls.log) != 0 {
					t.Errorf("%q closed the chat (%v)", answer, calls.log)
				}
				continue
			}
			m, _ = acted(t, m, cmd)
			if !reflect.DeepEqual(calls.log, []string{c.want}) {
				t.Errorf("y: calls = %v, want [%s]", calls.log, c.want)
			}
			if !strings.HasPrefix(m.flash, "✓ closed ") {
				t.Errorf("flash = %q", m.flash)
			}
		}
	}
}

// AC: while the inline input is open every key is typed — no hotkey leaks
// through, q / X / digits / spawn keys included.
func TestChatReplySwallowsHotkeys(t *testing.T) {
	calls := stubChatFuncs(t)
	spawned := 0
	prevO, prevP, prevL := SpawnOrchestrator, SpawnOrchestratorProfile, LiveChats
	defer func() { SpawnOrchestrator, SpawnOrchestratorProfile, LiveChats = prevO, prevP, prevL }()
	SpawnOrchestrator = func(*config.Config) (string, error) { spawned++; return "", nil }
	SpawnOrchestratorProfile = func(*config.Config, string) (string, error) { spawned++; return "", nil }
	LiveChats = func(string) []string { t.Error("X reached the park modal"); return nil }

	m := chatsFocused(t, 120, 40, 0, chatHiddenRow(1), chatHiddenRow(2))
	m, cmd := chatPress(t, m, "a")
	if cmd != nil || m.mode != modeChatReply {
		t.Fatalf("a on a hidden row: mode=%d, want the inline reply", m.mode)
	}
	typed := ""
	for _, k := range []string{"q", "X", "1", "8", "0", "O", ")", "@", "h", "x", "a", "?", "$", "L", "R", "g", "*", "d", "j", "k", "y", " "} {
		var c tea.Cmd
		m, c = chatPress(t, m, k)
		if c != nil {
			if msg := c(); msg != nil {
				if _, quit := msg.(tea.QuitMsg); quit {
					t.Fatalf("%q quit the cockpit from inside the reply", k)
				}
			}
		}
		typed += k
		if m.mode != modeChatReply || m.input.Value() != typed {
			t.Fatalf("%q leaked: mode=%d input=%q want %q", k, m.mode, m.input.Value(), typed)
		}
	}
	nm, c := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	if c != nil || m.focus != focusChats || m.chatSel != 0 || m.mode != modeChatReply {
		t.Error("tab moved the focus from inside the reply")
	}
	nm, c = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if c != nil {
		if _, quit := c().(tea.QuitMsg); quit {
			t.Error("ctrl+c quit the cockpit from inside the reply")
		}
	}
	m = nm.(Model)
	if spawned != 0 || len(calls.log) != 0 || m.armedHost != "" {
		t.Errorf("hotkeys acted from inside the reply: spawned=%d calls=%v armed=%q", spawned, calls.log, m.armedHost)
	}

	// enter submits through SendChat — the text as typed, trimmed.
	m, cmd = chatPress(t, m, "enter")
	if m.mode != modeList || m.input.Value() != "" {
		t.Errorf("after submit: mode=%d input=%q", m.mode, m.input.Value())
	}
	m, _ = acted(t, m, cmd)
	want := "send grove-chat-golden-1 " + strings.TrimSpace(typed)
	if !reflect.DeepEqual(calls.log, []string{want}) {
		t.Errorf("calls = %q, want [%q]", calls.log, want)
	}
	if m.flash != "✓ sent to chat-1" {
		t.Errorf("flash = %q", m.flash)
	}
}

// AC: esc cancels without sending; an empty submit sends nothing.
func TestChatReplyCancelAndEmpty(t *testing.T) {
	calls := stubChatFuncs(t)
	m := chatsFocused(t, 120, 40, 0, chatHiddenRow(1))
	m, _ = chatPress(t, m, "a")
	for _, k := range []string{"h", "i"} {
		m, _ = chatPress(t, m, k)
	}
	m, cmd := chatPress(t, m, "esc")
	if cmd != nil || m.mode != modeList || m.input.Value() != "" || len(calls.log) != 0 {
		t.Errorf("esc: mode=%d input=%q calls=%v cmd=%v", m.mode, m.input.Value(), calls.log, cmd != nil)
	}
	if m.input.Width != 0 {
		t.Error("the input must go back to the detail view unsized")
	}

	m, _ = chatPress(t, m, "a")
	if m.input.Value() != "" {
		t.Errorf("a cancelled reply left %q in the input", m.input.Value())
	}
	for _, k := range []string{"enter", " ", " ", "enter"} {
		m, cmd = chatPress(t, m, k)
		if cmd != nil && k == "enter" {
			t.Error("an empty submit emitted a command")
		}
	}
	if m.mode != modeChatReply || len(calls.log) != 0 {
		t.Errorf("an empty submit: mode=%d calls=%v, want the input still open and nothing sent", m.mode, calls.log)
	}
}

// The flash reports the refusal or the warn text, never a bare ✓.
func TestChatReplyReportsWarnAndRefusal(t *testing.T) {
	calls := stubChatFuncs(t)
	calls.sendWarn = "submitted, but the agent shows no sign of taking it up"
	m := chatsFocused(t, 120, 40, 0, chatHiddenRow(1))
	m, _ = chatPress(t, m, "a")
	m, _ = chatPress(t, m, "k")
	m, cmd := chatPress(t, m, "enter")
	m, _ = acted(t, m, cmd)
	if m.flash != calls.sendWarn {
		t.Errorf("flash = %q, want the warn text", m.flash)
	}
	calls.sendWarn, calls.err = "", errors.New("grove-chat-golden-1: a permission dialog is open — answer it first")
	m, _ = chatPress(t, m, "a")
	m, _ = chatPress(t, m, "k")
	m, cmd = chatPress(t, m, "enter")
	m, _ = acted(t, m, cmd)
	if !strings.Contains(m.flash, "permission dialog") {
		t.Errorf("flash = %q, want the refusal", m.flash)
	}
}

// An open modal stays on ITS chat across a pass, and a chat that went away
// or changed kind cancels it.
func TestChatModalHoldsItsTarget(t *testing.T) {
	stubChatFuncs(t)
	m := chatsFocused(t, 120, 40, 1, chatHiddenRow(1), chatHiddenRow(2))
	m, _ = chatPress(t, m, "a")
	m, _ = chatPress(t, m, "k")
	// A shown chat appears: it sorts first, the target moves down a row.
	m = withChats(t, m, chatShownRow(1), chatHiddenRow(1), chatHiddenRow(2))
	if m.mode != modeChatReply || m.chatSel != 2 || m.input.Value() != "k" {
		t.Fatalf("mode=%d chatSel=%d input=%q, want the reply still on chat-2", m.mode, m.chatSel, m.input.Value())
	}
	shown := chatHiddenRow(2)
	shown.Hidden = false
	m = withChats(t, m, chatHiddenRow(1), shown)
	if m.mode != modeList || !strings.Contains(m.flash, "chat-2 is gone") {
		t.Errorf("mode=%d flash=%q, want the reply cancelled", m.mode, m.flash)
	}
	m.chatSel = 0
	m, _ = chatPress(t, m, "x")
	m = withChats(t, m)
	if m.mode != modeList || m.focus != focusAgents {
		t.Errorf("mode=%d focus=%d after every chat went away", m.mode, m.focus)
	}
}

// AC (regression): with AGENTS or FEATURES focused a / enter / h / x do
// exactly what they did before there were chat keys — the same as on a
// cockpit with no chat at all.
func TestChatKeysOnlyUnderChatsFocus(t *testing.T) {
	calls := stubChatFuncs(t)
	prevA := AttachTask
	defer func() { AttachTask = prevA }()
	AttachTask = func(*state.Task) error { return nil }

	same := func(name, k string, plain, with Model) {
		t.Helper()
		a, acmd := chatPress(t, plain, k)
		b, bcmd := chatPress(t, with, k)
		if a.mode != b.mode || a.flash != b.flash || a.detail != b.detail || a.AttachTo != b.AttachTo ||
			a.lensSlug != b.lensSlug || a.sel != b.sel || a.featSel != b.featSel || a.focus != b.focus ||
			(acmd == nil) != (bcmd == nil) {
			t.Errorf("%s: %s changed (mode %d vs %d, flash %q vs %q, cmd %v vs %v)",
				name, k, a.mode, b.mode, a.flash, b.flash, acmd != nil, bcmd != nil)
		}
		if b.mode == modeChatReply || b.mode == modeChatClose {
			t.Errorf("%s: %s opened a chat modal", name, k)
		}
	}
	rows := []ChatRow{chatShownRow(1), chatHiddenRow(1)}
	for _, k := range []string{"a", "enter", "h", "x"} {
		plain := goldenModel(t, fxOff, 120, 40)
		same("AGENTS", k, plain, withChats(t, plain, rows...))

		feat := featureModel(t, 2, 120, 40)
		feat.focus = focusFeatures
		same("FEATURES", k, feat, withChats(t, feat, rows...))
	}
	if len(calls.log) != 0 {
		t.Errorf("a chat function ran without CHATS focus: %v", calls.log)
	}
}

// The train invariant (opt-in, default unchanged): no key press ⇒ nothing
// hides — no beat, launch or data message reaches a chat function — and the
// spawn keys still open side-by-side panes, CHATS focused or not.
func TestNothingHidesByItself(t *testing.T) {
	calls := stubChatFuncs(t)
	m := chatsFocused(t, 120, 40, 0, chatShownRow(1), chatShownRow(2))
	m.stateDir = t.TempDir()
	m.folder = state.NewFolder(m.stateDir, feedTail)
	for _, msg := range []tea.Msg{tickMsg{}, prTickMsg{}, refreshMsg{ok: true}, prsMsg{}, featuresMsg{},
		chatsMsg{rows: []ChatRow{chatShownRow(1), chatShownRow(2)}, deep: true}, flashMsg("x"),
		tea.WindowSizeMsg{Width: 120, Height: 40}} {
		nm, cmd := m.Update(msg)
		chatPasses(t, cmd)
		if got := nm.(Model); got.mode != modeList {
			t.Errorf("%T opened mode %d", msg, got.mode)
		}
	}
	chatPasses(t, m.Init())
	if len(calls.log) != 0 {
		t.Fatalf("a chat function ran with no key pressed: %v", calls.log)
	}

	var spawned []string
	prevO, prevP, prevR := SpawnOrchestrator, SpawnOrchestratorProfile, SpawnRemoteOrchestrator
	defer func() { SpawnOrchestrator, SpawnOrchestratorProfile, SpawnRemoteOrchestrator = prevO, prevP, prevR }()
	SpawnOrchestrator = func(*config.Config) (string, error) { spawned = append(spawned, "O"); return "", nil }
	SpawnOrchestratorProfile = func(_ *config.Config, p string) (string, error) {
		spawned = append(spawned, "profile "+p)
		return "", nil
	}
	plain := armedModel("vps")
	plain.width, plain.height = 120, 40
	plain.cfg.Orchestrator.Hotkeys = map[string]string{"1": "glm"}
	focused := withChats(t, plain, chatShownRow(1), chatHiddenRow(1))
	focused.focus = focusChats
	for _, k := range []string{"O", "0", ")", "1", "8", "@"} {
		a, acmd := chatPress(t, plain, k)
		b, bcmd := chatPress(t, focused, k)
		if a.flash != b.flash || a.mode != b.mode || a.armedHost != b.armedHost || (acmd == nil) != (bcmd == nil) {
			t.Errorf("%s: CHATS focus changed the spawn key (flash %q vs %q, mode %d vs %d)", k, a.flash, b.flash, a.mode, b.mode)
		}
		if bcmd != nil {
			bcmd()
		}
	}
	if !reflect.DeepEqual(spawned, []string{"O", "O", "profile glm"}) {
		t.Errorf("spawn keys under CHATS focus spawned %v, want O, 0 and the bound digit", spawned)
	}
	if len(calls.log) != 0 {
		t.Errorf("a spawn key reached a chat function: %v", calls.log)
	}
}

// --- frames ---

var chatKeyFrames = []struct {
	name string
	open func(t *testing.T, m Model) Model
}{
	{"focus", func(t *testing.T, m Model) Model { return m }},
	{"reply", func(t *testing.T, m Model) Model {
		m, _ = chatPress(t, m, "a")
		for _, r := range "two issues" {
			m, _ = chatPress(t, m, string(r))
		}
		return m
	}},
	{"confirm", func(t *testing.T, m Model) Model {
		m, _ = chatPress(t, m, "x")
		return m
	}},
}

func chatKeyFrame(t *testing.T, name string, w, h int) Model {
	t.Helper()
	m := chatsFocused(t, w, h, 1, chatShownRow(1), chatWaitingRow(2), chatHiddenRow(3))
	for _, f := range chatKeyFrames {
		if f.name == name {
			return f.open(t, m)
		}
	}
	t.Fatalf("no frame %q", name)
	return m
}

// AC: goldens at 80x24 and 40x20 — CHATS focused with footer hints, the
// inline reply open, the close confirm open.
func TestChatKeysFrameGolden(t *testing.T) {
	for _, f := range chatKeyFrames {
		for _, sz := range [][2]int{{80, 24}, {40, 20}} {
			got := chatKeyFrame(t, f.name, sz[0], sz[1]).View()
			path := filepath.Join("testdata", fmt.Sprintf("chatkeys-%s-%dx%d.golden", f.name, sz[0], sz[1]))
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
				t.Errorf("%s %dx%d: frame drifted from the golden:\n%s\n--- want ---\n%s", f.name, sz[0], sz[1], got, want)
			}
		}
	}
}

func TestHelpFrameGolden(t *testing.T) {
	for _, sz := range [][2]int{{120, 59}, {80, 24}} {
		m := goldenModel(t, fxOff, sz[0], sz[1])
		m.mode = modeHelp
		got := m.View()
		path := filepath.Join("testdata", fmt.Sprintf("help-%dx%d.golden", sz[0], sz[1]))
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
			t.Errorf("help %dx%d drifted from the golden:\n%s", sz[0], sz[1], got)
		}
	}
}

// Every chat-key frame fits the pane: at most m.height lines, none wider
// than m.width — swept over small panes, where the reply row has the least
// room to borrow.
func TestChatKeysFrameFits(t *testing.T) {
	for _, f := range chatKeyFrames {
		for w := 30; w <= 130; w += 10 {
			for h := 12; h <= 44; h++ {
				m := chatKeyFrame(t, f.name, w, h)
				if !m.chatsVisible() {
					continue
				}
				lines := frameLines(m.View())
				if len(lines) > h {
					t.Fatalf("%s %dx%d: frame is %d lines", f.name, w, h, len(lines))
				}
				for i, ln := range lines {
					if got := lipgloss.Width(ln); got > w {
						t.Fatalf("%s %dx%d: line %d is %d wide: %q", f.name, w, h, i, got, ln)
					}
				}
				if f.name == "reply" && !strings.Contains(strings.Join(lines, "\n"), "two issues") && w >= 40 {
					t.Fatalf("%s %dx%d: the reply row is not on screen", f.name, w, h)
				}
			}
		}
	}
}

// AC: the chat hints appear only under CHATS focus and fit at 80 columns,
// labels and all; the O/)/? trio never leaves.
func TestChatFooterHints(t *testing.T) {
	m := chatsFocused(t, 80, 24, 0, chatShownRow(1), chatHiddenRow(1), chatRemoteRow(2))
	for sel, want := range map[int][]string{
		0: {"h hide", "enter focus", "x close"},
		2: {"h show", "enter show+focus", "a reply", "x close"},
	} {
		m.chatSel = sel
		foot := stripANSI(m.viewFooter())
		for _, w := range append(want, "O", ")", "?") {
			if !strings.Contains(foot, w) {
				t.Errorf("row %d at 80 columns: footer %q is missing %q", sel, foot, w)
			}
		}
		if got := lipgloss.Width(m.viewFooter()); got > 80 {
			t.Errorf("row %d: footer is %d wide, want at most 80", sel, got)
		}
	}
	m.chatSel = 0
	if foot := stripANSI(m.viewFooter()); strings.Contains(foot, "a reply") {
		t.Errorf("a shown row refuses a — the footer must not offer it: %q", foot)
	}
	m.chatSel = 1 // the remote row: every chat key but the reply
	if foot := stripANSI(m.viewFooter()); !strings.Contains(foot, "h hide") || !strings.Contains(foot, "x close") || strings.Contains(foot, "a reply") {
		t.Errorf("a remote row's hints: %q", foot)
	}
	if hints, n := chatHints(chatHiddenRemoteRow(3)); n != 3 || hints[0].label != "show" || hints[2].key != "x" {
		t.Errorf("a hidden remote row's hints = %v", hints[:n])
	}
	m.focus = focusAgents
	plain := goldenModel(t, fxOff, 80, 24)
	if m.viewFooter() != plain.viewFooter() {
		t.Errorf("without CHATS focus the footer must be the one it always was:\n%q\n%q", m.viewFooter(), plain.viewFooter())
	}

	// One line at every width, with or without a flash; the keys survive
	// as bare keys when the labels do not fit.
	m.focus = focusChats
	for _, sel := range []int{0, 2} {
		m.chatSel = sel
		hints, n := chatHints(m.chatRows[sel])
		for _, flash := range []string{"", "○ hid cockpit·1 as grove-chat-golden-1"} {
			m.flash = flash
			for w := 12; w <= 200; w++ {
				m.width = w
				foot := m.viewFooter()
				if strings.Contains(foot, "\n") || lipgloss.Width(foot) > w {
					t.Fatalf("row %d width %d: footer is %d wide: %q", sel, w, lipgloss.Width(foot), foot)
				}
				if w < 40 {
					continue
				}
				plainFoot := stripANSI(foot)
				for i := 0; i < n; i++ {
					if !strings.Contains(plainFoot, hints[i].key) {
						t.Fatalf("row %d width %d: footer lost the %q key: %q", sel, w, hints[i].key, plainFoot)
					}
				}
			}
		}
	}
}

func TestHelpCoversChatKeys(t *testing.T) {
	documented := map[string]bool{}
	for _, e := range helpChats {
		documented[e.key] = true
	}
	for _, r := range []ChatRow{chatShownRow(1), chatHiddenRow(1)} {
		hints, n := chatHints(r)
		for i := 0; i < n; i++ {
			if !documented[hints[i].key] {
				t.Errorf("chat key %q has no help entry", hints[i].key)
			}
		}
	}
	m := New(nil, "", "")
	m.width, m.height, m.mode = 120, 59, modeHelp
	if view := m.View(); !strings.Contains(view, "CHATS — with the chat box focused") || !strings.Contains(view, "reply to a HIDDEN chat") {
		t.Error("the help overlay is missing the CHATS section")
	}
}
