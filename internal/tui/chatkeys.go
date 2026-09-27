package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/chat"
)

// Keys on CHATS rows (chat-hide train, grove-403): h / enter / a / x act on
// the selected chat while the box holds focus — and only then. Everywhere
// else those keys keep the meaning they had.
//
// The decision is chatKeyAction, a pure table. The work is the shared
// function behind the matching CLI verb, injected below, run as ONE tea.Cmd
// per key press whose answer triggers ONE costly pass of the box. Nothing
// standing: no goroutine, poll, timer or cache (the cockpit RAM rule).

// The functions behind `gv chat hide|show|send|close` and `gv orchestrator
// close`, injected by cmd/gv. Unwired they refuse — nothing moves.
var (
	// HideChat moves a cockpit chat pane off-screen; returns its new session.
	HideChat = func(pane string) (string, error) { return "", fmt.Errorf("chat hide not wired") }
	// ShowChat joins a hidden chat into the cockpit; focus=false leaves the
	// keyboard on the dashboard. Returns the pane's %id.
	ShowChat = func(session string, focus bool) (string, error) { return "", fmt.Errorf("chat show not wired") }
	// FocusChat makes a shown chat's pane the active one.
	FocusChat = func(pane string) error { return fmt.Errorf("chat focus not wired") }
	// SendChat is `gv chat send`'s relay: gated, pasted, verified submitted.
	SendChat = func(session, text string) (warn string, err error) {
		return "", fmt.Errorf("chat send not wired")
	}
	// CloseChatPane is the guarded pane close `gv orchestrator close` runs.
	CloseChatPane = func(pane string) error { return fmt.Errorf("chat close not wired") }
	// CloseChatSession is `gv chat close`.
	CloseChatSession = func(session string) error { return fmt.Errorf("chat close not wired") }
)

// chatAct is what a key does to a CHATS row.
type chatAct int

const (
	actNone      chatAct = iota // no CHATS meaning: the key keeps its global one
	actRefuse                   // flash only
	actHide                     // h on a shown row
	actShow                     // h on a hidden row: show, stay on the dashboard
	actFocus                    // enter on a shown row
	actShowFocus                // enter on a hidden row
	actReply                    // a on a hidden row: open the inline input
	actClose                    // x: open the y/N confirm
)

const (
	chatsTaskKeysFlash = "CHATS focused — tab to AGENTS for task keys"
	chatsRemoteFlash   = "remote chats: not yet"
	chatsShownFlash    = "shown — enter to focus it"
)

// chatRowKind is the row's kind as `gv chat ls` reports it.
func chatRowKind(r ChatRow) string {
	if r.Hidden {
		return chat.KindChat
	}
	return chat.KindCockpit
}

// chatKeyAction is the key table: what key does on row r, and the flash a
// refusal carries. Pure.
func chatKeyAction(key string, r ChatRow) (chatAct, string) {
	switch key {
	case "h", "enter", "a", "x":
		if r.Host != "" {
			return actRefuse, chatsRemoteFlash
		}
	case "n", "o", "p", "t", "v", "d", "m":
		// Task keys have no chat under them — refused, never passed on to
		// the AGENTS cursor out of sight (grove-402).
		return actRefuse, chatsTaskKeysFlash
	default:
		return actNone, ""
	}
	switch key {
	case "h":
		if r.Hidden {
			return actShow, ""
		}
		return actHide, ""
	case "enter":
		if r.Hidden {
			return actShowFocus, ""
		}
		return actFocus, ""
	case "a":
		// chat.WriteRefusal stays the single gate on what takes input: a
		// cockpit pane is writable:false — someone may be typing in it.
		if chat.WriteRefusal(chat.Row{Kind: chatRowKind(r), Session: r.Session}) != "" {
			return actRefuse, chatsShownFlash
		}
		return actReply, ""
	}
	return actClose, ""
}

// chatActedMsg is one chat action's answer: the flash line. Its handler
// runs the one costly pass that redraws the box.
type chatActedMsg struct{ flash string }

// chatErr is an error as a flash: its first line (a tmux error carries
// stderr on the lines below, and the footer is one line).
func chatErr(err error) chatActedMsg {
	return chatActedMsg{flash: firstNonEmptyLine(err.Error())}
}

func chatHideCmd(r ChatRow) tea.Cmd {
	name := chatName(r)
	return func() tea.Msg {
		session, err := HideChat(r.Pane)
		if err != nil {
			return chatErr(err)
		}
		return chatActedMsg{flash: "○ hid " + name + " as " + session}
	}
}

func chatShowCmd(r ChatRow, focus bool) tea.Cmd {
	name := chatName(r)
	return func() tea.Msg {
		if _, err := ShowChat(r.Session, focus); err != nil {
			return chatErr(err)
		}
		return chatActedMsg{flash: "▣ showed " + name}
	}
}

func chatFocusCmd(r ChatRow) tea.Cmd {
	name := chatName(r)
	return func() tea.Msg {
		if err := FocusChat(r.Pane); err != nil {
			return chatErr(err)
		}
		return chatActedMsg{flash: "→ " + name}
	}
}

func chatSendCmd(r ChatRow, text string) tea.Cmd {
	name := chatName(r)
	return func() tea.Msg {
		warn, err := SendChat(r.Session, text)
		switch {
		case err != nil:
			return chatErr(err)
		case warn != "":
			// Submitted, but unconfirmed — never a bare ✓ (grove-186).
			return chatActedMsg{flash: firstNonEmptyLine(warn)}
		}
		return chatActedMsg{flash: "✓ sent to " + name}
	}
}

func chatCloseCmd(r ChatRow) tea.Cmd {
	name := chatName(r)
	return func() tea.Msg {
		var err error
		if r.Hidden {
			err = CloseChatSession(r.Session)
		} else {
			err = CloseChatPane(r.Pane)
		}
		if err != nil {
			return chatErr(err)
		}
		return chatActedMsg{flash: "✓ closed " + name}
	}
}

// handleChatsKey is the CHATS focus branch of handleKey. handled=false
// leaves the key to the global switch (j/k, tab, spawn keys, q, …).
func (m Model) handleChatsKey(k tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if m.chatSel >= len(m.chatRows) {
		return m, nil, false
	}
	r := m.chatRows[m.chatSel]
	act, flash := chatKeyAction(k.String(), r)
	switch act {
	case actNone:
		return m, nil, false
	case actRefuse:
		m.flash = flash
		return m, nil, true
	case actHide:
		m.flash = "hiding " + chatName(r) + "…"
		return m, chatHideCmd(r), true
	case actShow:
		m.flash = "showing " + chatName(r) + "…"
		return m, chatShowCmd(r, false), true
	case actShowFocus:
		m.flash = "showing " + chatName(r) + "…"
		return m, chatShowCmd(r, true), true
	case actFocus:
		m.flash = ""
		return m, chatFocusCmd(r), true
	case actReply:
		m.mode, m.chatTarget, m.flash = modeChatReply, r, ""
		m.input.SetValue("")
		m.input.Placeholder = "reply to " + chatName(r)
		m.sizeChatInput()
		m.input.Focus()
		return m, nil, true
	}
	m.mode, m.chatTarget, m.flash = modeChatClose, r, ""
	return m, nil, true
}

// chatInputIndent is the reply row's left margin inside the box.
const chatInputIndent = 4

// sizeChatInput fits the inline input to the box, so a long reply scrolls
// inside its one row instead of running off it.
func (m *Model) sizeChatInput() {
	m.input.Width = max(1, m.width-4-chatInputIndent-lipgloss.Width(m.input.Prompt)-1)
}

// closeChatModal leaves either chat modal and hands the input back as the
// detail view expects it.
func (m *Model) closeChatModal() {
	m.mode, m.chatTarget = modeList, ChatRow{}
	m.input.SetValue("")
	m.input.Width = 0
	m.input.Blur()
}

// handleChatReplyKey owns EVERY key while the inline input is open: esc
// cancels, enter submits, the rest is typed. No hotkey leaks through.
func (m Model) handleChatReplyKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		m.closeChatModal()
		m.flash = "reply cancelled — nothing sent"
		return m, nil
	case tea.KeyEnter:
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		r := m.chatTarget
		m.closeChatModal()
		m.flash = "sending to " + chatName(r) + "…"
		return m, chatSendCmd(r, text)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return m, cmd
}

// handleChatCloseKey: y closes, anything else backs out.
func (m Model) handleChatCloseKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.chatTarget
	m.closeChatModal()
	if k.String() != "y" {
		return m, nil
	}
	m.flash = "closing " + chatName(r) + "…"
	return m, chatCloseCmd(r)
}

// chatModal: the inline reply or the close confirm is open.
func (m Model) chatModal() bool { return m.mode == modeChatReply || m.mode == modeChatClose }

// holdChatTarget keeps an open chat modal on its chat across a pass: the
// cursor follows the row, and a chat that went away (or changed kind —
// hidden ⇄ shown from the CLI) cancels the modal rather than letting it act
// on whatever row took its place.
func (m *Model) holdChatTarget() {
	if !m.chatModal() {
		return
	}
	for i, r := range m.chatRows {
		if r.Pane == m.chatTarget.Pane && r.Hidden == m.chatTarget.Hidden {
			m.chatSel, m.chatTarget = i, r
			return
		}
	}
	name := chatName(m.chatTarget)
	m.closeChatModal()
	m.flash = name + " is gone — nothing sent"
}

// --- footer ---

// chatHints is the row group while CHATS holds focus: only the keys that
// act on THIS row. Fixed-size, built on the stack — nothing allocated.
func chatHints(r ChatRow) ([4]hint, int) {
	if r.Host != "" {
		return [4]hint{}, 0
	}
	if r.Hidden {
		return [4]hint{{"h", "show"}, {"enter", "show+focus"}, {"a", "reply"}, {"x", "close"}}, 4
	}
	return [4]hint{{"h", "hide"}, {"enter", "focus"}, {"x", "close"}}, 3
}

// chatLegend is the footer legend under CHATS focus: the row's chat keys,
// then whatever of the usual spawn/global legend fits beside them. The chat
// keys shed their labels before they would push the O/)/? trio out.
func chatLegend(width int, r ChatRow) string {
	hints, n := chatHints(r)
	if n == 0 {
		return footerLegend(width, false, false)
	}
	sep := len([]rune(interSep))
	for _, bare := range [...]bool{false, true} {
		line, w := " ", 1
		for i := 0; i < n; i++ {
			if i > 0 {
				line += sDim.Render(intraSep)
				w += len([]rune(intraSep))
			}
			if bare {
				line += sKey.Render(hints[i].key)
				w += len([]rune(hints[i].key))
			} else {
				line += hints[i].render()
				w += hints[i].width()
			}
		}
		rest := footerLegend(width-w-sep+1, false, false)
		if bare || w+sep+lipgloss.Width(rest)-1 <= width {
			return line + sDim.Render(interSep) + strings.TrimPrefix(rest, " ")
		}
	}
	return ""
}

// legendFor picks the list-mode legend for the current focus.
func (m Model) legendFor(width int) string {
	if m.focus == focusChats && m.chatSel < len(m.chatRows) {
		return chatLegend(width, m.chatRows[m.chatSel])
	}
	return footerLegend(width, len(m.board) > 0, len(m.feats) > 0)
}

// viewChatFooter is the footer while a chat modal is open.
func (m Model) viewChatFooter() string {
	name := chatName(m.chatTarget)
	if m.mode == modeChatReply {
		line := " " + sWaiting.Render("reply → "+name) + "  " +
			sKey.Render("enter") + sFoot.Render(" send") + sDim.Render(intraSep) +
			sKey.Render("esc") + sFoot.Render(" cancel")
		return truncPad(line, m.width)
	}
	detail := "kills its pane and the agent in it "
	if m.chatTarget.Hidden {
		detail = "ends the chat, transcript kept "
	}
	head := " " + sBlocked.Render("close "+name+"? ")
	long := sKey.Render("y") + sFoot.Render(" confirm · any other key cancels")
	// The answer keys outrank the explanation: a narrow pane sheds the
	// detail, then the long wording — never the y.
	for _, line := range [...]string{
		head + sBlocked.Render(detail) + long,
		head + long,
	} {
		if lipgloss.Width(line) <= m.width {
			return truncPad(line, m.width)
		}
	}
	return truncPad(head+sKey.Render("y")+sFoot.Render(" yes · else cancel"), m.width)
}
