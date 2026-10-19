package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// AC (grove-517): the hit table — row lines map to chats; border, title,
// column header, reply row, `+N more` and the bottom border map to none.
func TestChatLayoutHit(t *testing.T) {
	none := -1
	cases := []struct {
		name string
		lay  chatLayout
		sel  int
		want []int // by dy from the top border, through one past the box
	}{
		{"three rows", chatLayout{height: 7, shown: 3}, 0,
			[]int{none, none, none, 0, 1, 2, none, none}},
		{"scrolled with +N more", chatLayout{height: 10, start: 3, shown: 5, more: 3}, 7,
			[]int{none, none, none, 3, 4, 5, 6, 7, none, none, none}},
		{"truncated to one row", chatLayout{height: 6, shown: 1, more: 4}, 0,
			[]int{none, none, none, 0, none, none, none}},
		{"reply row under the cursor", chatLayout{height: 8, shown: 3, reply: true}, 1,
			[]int{none, none, none, 0, 1, none, 2, none, none}},
	}
	for _, c := range cases {
		for dy, want := range c.want {
			i, ok := c.lay.hit(dy, c.sel)
			got := none
			if ok {
				got = i
			}
			if got != want {
				t.Errorf("%s: dy=%d → %d, want %d", c.name, dy, got, want)
			}
		}
		if _, ok := c.lay.hit(-1, c.sel); ok {
			t.Errorf("%s: a line above the box hit a row", c.name)
		}
	}
}

// AC: hit-testing agrees with the drawing — every chat name the frame shows
// is hit on its own line, and no other line of the frame hits anything,
// with and without FEATURES above, scrolled and truncated.
func TestChatHitMatchesFrame(t *testing.T) {
	var eight []ChatRow
	for i := 1; i <= 8; i++ {
		eight = append(eight, chatHiddenRow(i))
	}
	mixed := []ChatRow{chatShownRow(1), chatRemoteRow(2), chatHiddenRow(1), chatHiddenRow(2)}
	models := map[string]Model{}
	for _, sz := range [][2]int{{120, 40}, {80, 24}, {60, 16}} {
		models[fmt.Sprintf("mixed %dx%d", sz[0], sz[1])] = chatsModel(t, fxOff, sz[0], sz[1], mixed...)
		for _, sel := range []int{0, 7} {
			m := chatsModel(t, fxOff, sz[0], sz[1], eight...)
			m.focus, m.chatSel = focusChats, sel
			models[fmt.Sprintf("eight sel=%d %dx%d", sel, sz[0], sz[1])] = m
		}
		models[fmt.Sprintf("features %dx%d", sz[0], sz[1])] = withChats(t, featureModel(t, 2, sz[0], sz[1]), mixed...)
	}
	for name, m := range models {
		lines := frameLines(m.View())
		hits := 0
		for y, ln := range lines {
			plain := stripANSI(ln)
			i, ok := m.chatHit(2, y)
			if !ok {
				for _, l := range m.chats {
					if strings.HasPrefix(strings.TrimLeft(plain, "│ ▸"), chatGlyphs[l.kind]+" "+l.name+" ") {
						t.Errorf("%s: line %d draws %q but hits nothing", name, y, l.name)
					}
				}
				continue
			}
			hits++
			if want := chatGlyphs[m.chats[i].kind] + " " + m.chats[i].name + " "; !strings.Contains(plain, want) {
				t.Errorf("%s: line %d hit chat %d (%s) but draws %q", name, y, i, m.chats[i].name, plain)
			}
			// The side borders are not the row.
			if _, ok := m.chatHit(0, y); ok {
				t.Errorf("%s: the left border of line %d hit a row", name, y)
			}
			if _, ok := m.chatHit(m.width-1, y); ok {
				t.Errorf("%s: the right border of line %d hit a row", name, y)
			}
		}
		if lay := m.chatLayout(); hits != lay.shown {
			t.Errorf("%s: %d lines hit, the box shows %d rows", name, hits, lay.shown)
		}
	}
}

func click(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
}

// chatRowY is the frame line chat i is drawn on.
func chatRowY(t *testing.T, m Model, i int) int {
	t.Helper()
	for y := range frameLines(m.View()) {
		if j, ok := m.chatHit(2, y); ok && j == i {
			return y
		}
	}
	t.Fatalf("chat %d is not on screen", i)
	return -1
}

// AC: a click on row N dispatches what enter on row N does — the same
// call, the same flash — selects the row and focuses CHATS; a click on a
// chat already on screen selects it and does nothing else. Remote rows too.
func TestChatClickIsEnter(t *testing.T) {
	rows := func() []ChatRow {
		return []ChatRow{chatShownRow(1), chatHiddenRow(1), chatRemoteRow(2), chatHiddenRemoteRow(3), chatWaitingRow(4)}
	}
	for n := range rows() {
		calls := stubChatFuncs(t)
		m := chatsFocused(t, 120, 40, n, rows()...)
		km, kcmd := chatPress(t, m, "enter")
		var keyLog []string
		var keyFlash string
		if kcmd != nil {
			km, _ = acted(t, km, kcmd)
			keyLog, keyFlash = calls.log, km.flash
		}

		calls = stubChatFuncs(t)
		m = chatsModel(t, fxOff, 120, 40, rows()...) // AGENTS focused, cursor on 0
		nm, cmd := m.Update(click(10, chatRowY(t, m, n)))
		m = nm.(Model)
		if m.focus != focusChats || m.chatSel != n {
			t.Errorf("row %d: focus=%d sel=%d, want CHATS and %d", n, m.focus, m.chatSel, n)
		}
		if len(calls.log) != 0 {
			t.Fatalf("row %d: tmux work ran inside Update: %v", n, calls.log)
		}
		if !m.chatRows[n].Hidden {
			if cmd != nil {
				t.Errorf("row %d: a click on a chat already on screen dispatched a command", n)
			}
			continue
		}
		m, _ = acted(t, m, cmd)
		if !reflect.DeepEqual(calls.log, keyLog) || m.flash != keyFlash {
			t.Errorf("row %d: click = %v %q, enter = %v %q", n, calls.log, m.flash, keyLog, keyFlash)
		}
	}
}

// AC: clicks off the rows, or under a modal, change nothing.
func TestChatClickElsewhereIsInert(t *testing.T) {
	calls := stubChatFuncs(t)
	m := chatsModel(t, fxOff, 120, 40, chatShownRow(1), chatHiddenRow(1))
	for y := range frameLines(m.View()) {
		if _, ok := m.chatHit(10, y); ok {
			continue
		}
		nm, cmd := m.Update(click(10, y))
		if got := nm.(Model); cmd != nil || got.focus != m.focus || got.chatSel != m.chatSel || got.sel != m.sel {
			t.Errorf("a click on line %d did something", y)
		}
	}
	m.mode = modeChatClose
	if nm, cmd := m.Update(click(10, chatRowY(t, m, 1))); cmd != nil || nm.(Model).mode != modeChatClose {
		t.Error("a click acted under the close confirm")
	}
	if len(calls.log) != 0 {
		t.Errorf("calls = %v", calls.log)
	}
}

// AC: the filter lets the left press through, turns the wheel into ↑/↓
// and drops everything else before Update.
func TestMouseFilter(t *testing.T) {
	ev := func(b tea.MouseButton, a tea.MouseAction) tea.MouseMsg {
		return tea.MouseMsg{X: 3, Y: 4, Button: b, Action: a}
	}
	cases := []struct {
		in   tea.Msg
		want tea.Msg
	}{
		{ev(tea.MouseButtonLeft, tea.MouseActionPress), ev(tea.MouseButtonLeft, tea.MouseActionPress)},
		{ev(tea.MouseButtonWheelUp, tea.MouseActionPress), tea.KeyMsg{Type: tea.KeyUp}},
		{ev(tea.MouseButtonWheelDown, tea.MouseActionPress), tea.KeyMsg{Type: tea.KeyDown}},
		{ev(tea.MouseButtonLeft, tea.MouseActionRelease), nil},
		{ev(tea.MouseButtonLeft, tea.MouseActionMotion), nil},
		{ev(tea.MouseButtonNone, tea.MouseActionRelease), nil},
		{ev(tea.MouseButtonRight, tea.MouseActionPress), nil},
		{ev(tea.MouseButtonMiddle, tea.MouseActionPress), nil},
		{fkey("j"), fkey("j")},
	}
	for _, c := range cases {
		if got := mouseFilter(nil, c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%#v → %#v, want %#v", c.in, got, c.want)
		}
	}
}
