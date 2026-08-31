package chat

// grove-216: naming a chat on the command line, and refusing to write to one
// that must not take input.

import (
	"fmt"
	"strings"
)

// MinIDPrefix is the shortest session-id prefix Match will accept. `gv chat
// ls` prints 8 characters, so anything a human copies off the table works;
// three characters of a UUID across a fleet's worth of transcripts is a
// coin toss, and steering the WRONG chat is the failure this guards.
const MinIDPrefix = 4

// Match resolves a `<session>` argument against a chat report and returns the
// index of the row it names. Accepted, in order:
//
//  1. the tmux session name (`grove-chat-<label>-<n>`, or a cockpit session);
//  2. the full Claude session id;
//  3. an unambiguous prefix of a session id, at least MinIDPrefix long.
//
// Ambiguity is an ERROR listing the candidates, never a pick. This verb ends
// in a paste into somebody's agent: the whole grove-116/78 lesson is that a
// target which "resolves to something reasonable" delivers the operator's
// words to the wrong session, silently.
func Match(rows []Row, target string) (int, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return -1, fmt.Errorf("name a chat — `gv chat ls` lists them")
	}
	for i, r := range rows {
		if r.Session != "" && r.Session == target {
			return i, nil
		}
	}
	for i, r := range rows {
		if r.SessionID != nil && *r.SessionID == target {
			return i, nil
		}
	}
	var hits []int
	if len(target) >= MinIDPrefix {
		for i, r := range rows {
			if r.SessionID != nil && strings.HasPrefix(*r.SessionID, target) {
				hits = append(hits, i)
			}
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return -1, fmt.Errorf("no chat matching %q — `gv chat ls` lists them (a tmux session name, a session id, or %d+ characters of one)", target, MinIDPrefix)
	default:
		return -1, fmt.Errorf("%q matches %d chats (%s) — name one exactly", target, len(hits), strings.Join(describe(rows, hits), ", "))
	}
}

// describe names the ambiguous candidates the way the operator can retype
// them: the tmux session where there is one, the session id otherwise.
func describe(rows []Row, hits []int) []string {
	var out []string
	for _, i := range hits {
		r := rows[i]
		if r.Session != "" {
			out = append(out, r.Session)
			continue
		}
		if r.SessionID != nil {
			out = append(out, *r.SessionID)
		}
	}
	return out
}

// WriteRefusal is the one gate on `gv chat send` / `gv chat keys`: "" when
// the row takes input, else the reason it does not, phrased so the operator
// knows what to do instead. Keyed on Writable — the same field the contract
// tells a client to disable its input box off — so the CLI and a phone can
// never disagree about which chats are writable.
func WriteRefusal(r Row) string {
	if Writable(r.Kind) {
		return ""
	}
	switch r.Kind {
	case KindCockpit:
		// Mechanically this pane WOULD take a paste. It is refused because
		// the operator may be typing in it at the desk — a write-through
		// needs an interlock, not a flag (design §risks).
		return fmt.Sprintf("%s is the cockpit's own orchestrator pane (kind cockpit, writable: false) — someone may be typing in it; attach with `tmux attach -t '=%s'`, or start a chat of your own with `gv orchestrator new`", r.Session, r.Session)
	case KindArchived:
		return fmt.Sprintf("%s is an archived transcript (kind archived, writable: false) — it has no live pane; revive it with `gv orchestrator new --resume %s`", idOf(r), idOf(r))
	default:
		return fmt.Sprintf("%s is not writable (kind %s)", idOf(r), r.Kind)
	}
}

// idOf names a row that may have no tmux session (an archived one does not).
func idOf(r Row) string {
	if r.Session != "" {
		return r.Session
	}
	if r.SessionID != nil {
		return *r.SessionID
	}
	return "that chat"
}
