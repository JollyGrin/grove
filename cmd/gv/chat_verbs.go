package main

// grove-216: the read and write halves of a chat — `gv chat tail`, `gv chat
// send`, `gv chat keys`. Train 2/4 of `gv chat` (design §3–4).
//
// Two house rules run through this file, and both were paid for:
//
//	READ THE TRANSCRIPT, NEVER THE PANE. `tail` follows the append-only
//	`.jsonl`; a pane capture is ANSI soup wrapped at pane width whose chrome
//	has changed under us twice. Scraping is liveness garnish; the transcript
//	is truth.
//
//	WRITE THROUGH THE RELAY PATH, NEVER AROUND IT. `send` calls
//	tmux.PasteText — load-buffer into the server-global `gv-relay` buffer,
//	BRACKETED paste-buffer, settle, a SEPARATE Enter, then a scrape that
//	proves the submit landed (grove-144). Delivered is not submitted, and a
//	silent success costs far more than a loud failure. `keys` is that rule's
//	one exception: a single character aimed at an option picker, raw and
//	un-Enter-wrapped.
//
// The target is always the immutable `%N` pane id resolved through the same
// report `gv chat ls` prints — never a stored session or window name, which
// prefix-match and silently resolve to a sibling (grove-116/78).

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/transcript"
	"github.com/JollyGrin/grove/internal/workspace"
)

// findChat resolves a `<session>` argument to the record behind it: the row
// a client sees plus the pane id and project dir it does not.
//
// The lookup deliberately spans EVERY registered workspace rather than the
// ambient one (grove-191's workspace-transparent shape, and grove-215's `ls`
// default): a phone naming `grove-chat-unbrewed-1` has no cwd to be ambient
// about. It also runs the same lazy id resolution `ls` does, so the first
// `gv chat tail` on a fresh chat stamps its pane exactly like the first `ls`
// would.
func findChat(target string) (chatRecord, error) {
	list, err := workspace.LoadRegistry()
	if err != nil {
		return chatRecord{}, err
	}
	targets, err := chatWorkspaces(list, "")
	if err != nil {
		return chatRecord{}, err
	}
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return chatRecord{}, err
	}
	recs := chatRecords(targets, liveChatLookup(isCockpit))
	rows := make([]chat.Row, len(recs))
	for i, r := range recs {
		rows[i] = r.Row
	}
	i, err := chat.Match(rows, target)
	if err != nil {
		return chatRecord{}, err
	}
	return recs[i], nil
}

// transcriptPath is where this chat's conversation lives on disk: the
// session id inside the project dir keyed on the chat's OWN cwd — which is
// why a profiled chat under `<brain>/<profile>` reads its own history and
// not the default pane's (Claude Code keys project dirs by cwd).
func (r chatRecord) transcriptPath() (string, error) {
	if r.Row.SessionID == nil || *r.Row.SessionID == "" {
		return "", fmt.Errorf("%s has no Claude session id yet — the chat is still booting (claude mints the id on boot, and `gv chat ls` resolves it once the first transcript line lands)", chatName(r.Row))
	}
	if r.Dir == "" {
		return "", fmt.Errorf("%s has no known working directory — nothing to read a transcript from", chatName(r.Row))
	}
	return filepath.Join(transcript.ProjectDir(r.Dir), *r.Row.SessionID+".jsonl"), nil
}

// chatName is how an error names a chat: its tmux session where it has one,
// its session id otherwise (an archived chat has no session).
func chatName(r chat.Row) string {
	if r.Session != "" {
		return r.Session
	}
	if r.SessionID != nil {
		return *r.SessionID
	}
	return "that chat"
}

// --- gv chat tail ---

// cmdChatTail streams a chat's transcript as JSONL — one entry per line,
// `{seq, role, kind, text, tool, ts}`. Read-only, so it works on all three
// kinds: a live chat, the cockpit's own pane, and an archived transcript
// nothing is running any more.
func cmdChatTail(args []string) error {
	fs := flag.NewFlagSet("chat tail", flag.ExitOnError)
	follow := fs.Bool("follow", false, "keep the stream open and emit appends as they land")
	fs.BoolVar(follow, "f", false, "shorthand for --follow")
	since := fs.Int("since", 0, "skip entries with seq <= N (resume where a client left off)")
	rest := parseAnywhere(fs, args)
	if len(rest) != 1 {
		return fmt.Errorf("usage: gv chat tail <session> [--follow] [--since <n>]")
	}
	rec, err := findChat(rest[0])
	if err != nil {
		return err
	}
	path, err := rec.transcriptPath()
	if err != nil {
		return err
	}
	// SIGINT/SIGTERM end a --follow cleanly rather than killing it mid-line:
	// the consumer on the other end (grove-218's SSE bridge, or a pipe) sees
	// a closed stream, not a truncated JSON object.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = chat.Tail(ctx, path, chat.TailOptions{Since: *since, Follow: *follow}, os.Stdout)
	if os.IsNotExist(err) {
		return fmt.Errorf("%s has no transcript at %s — nothing has been said in it yet", chatName(rec.Row), path)
	}
	return err
}

// --- gv chat send ---

// cmdChatSend relays prose into a live chat and verifies it was SUBMITTED.
//
// No FlagSet, deliberately: a message that starts with `-` ("--json is
// broken, look at…") must reach the agent, not the flag parser. Everything
// after the session name is the message.
func cmdChatSend(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gv chat send <session> \"<text>\"   (or pipe the text on stdin)")
	}
	text := strings.TrimSpace(strings.Join(args[1:], " "))
	if text == "" {
		in, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading the message from stdin: %w", err)
		}
		text = strings.TrimSpace(string(in))
	}
	if text == "" {
		return fmt.Errorf("empty message — nothing sent")
	}
	pane, row, err := writableChat(args[0])
	if err != nil {
		return err
	}
	// PasteText is the whole grove-144 sequence: bracketed paste, settle,
	// separate Enter, verify, one retry Enter, then a loud error. An error
	// here means the agent never got the text — which is why nothing is
	// printed or recorded on this path.
	warn, err := tmux.PasteText(pane, text)
	if err != nil {
		return fmt.Errorf("gv chat send to %s: %w", row.Session, err)
	}
	// A verified submit with no sign of uptake still succeeded — but the
	// operator hears about it, on stderr so ✓ and any --json stay clean.
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	fmt.Printf("✓ sent to %s\n", row.Session)
	return nil
}

// --- gv chat keys ---

// cmdChatKeys delivers raw characters with NO Enter — the relay rule's own
// exception, for the permission prompts and option pickers a prose box
// cannot drive (they act on the keypress itself). A newline is refused
// rather than translated: submitting text is `send`'s job, and its verified
// submit is the reason that verb exists.
func cmdChatKeys(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: gv chat keys <session> <chars>   (a picker keystroke — sent raw, no Enter)")
	}
	keys := args[1]
	if keys == "" {
		return fmt.Errorf("no characters — nothing sent")
	}
	if strings.ContainsAny(keys, "\r\n") {
		return fmt.Errorf("`gv chat keys` sends raw characters with no Enter — use `gv chat send` for text that has to be submitted")
	}
	pane, row, err := writableChat(args[0])
	if err != nil {
		return err
	}
	if err := tmux.SendRawKey(pane, keys); err != nil {
		return fmt.Errorf("gv chat keys to %s: %w", row.Session, err)
	}
	fmt.Printf("✓ %q → %s (raw, no Enter)\n", keys, row.Session)
	return nil
}

// writableChat resolves a target for the two WRITE verbs: the pane id to
// aim at, or a refusal naming what to do instead.
//
// The gate is chat.WriteRefusal, keyed on the row's `writable` field — the
// same field the contract tells a phone to disable its input box off. One
// answer, one place: the CLI and a client can never disagree about which
// chats take input.
func writableChat(target string) (pane string, row chat.Row, err error) {
	rec, err := findChat(target)
	if err != nil {
		return "", chat.Row{}, err
	}
	if refusal := chat.WriteRefusal(rec.Row); refusal != "" {
		return "", chat.Row{}, fmt.Errorf("%s", refusal)
	}
	if rec.Pane == "" {
		return "", chat.Row{}, fmt.Errorf("%s has no live pane to write to", chatName(rec.Row))
	}
	return rec.Pane, rec.Row, nil
}

// --- gv chat restamp (grove-222) ---

// cmdChatRestamp is the escape hatch for a chat wearing the wrong identity:
// point a live pane at the conversation actually running in it, or clear the
// stamp and let the next `gv chat ls` re-derive one.
//
// It exists because two things cannot be re-derived. A pane mis-stamped by
// the old mtime resolver whose agent was launched without an explicit id has
// no ground truth left to read; and a conversation REPLACED inside a living
// pane (a `/clear` starts a fresh one) changes neither the argv nor the
// stamp. Both are one-line fixes for a human who can see the pane.
//
// A pane whose claude carries an explicit `--session-id`/`--resume` is
// re-corrected from that argv on the next report — the process outranks a
// typed-in answer, deliberately — so restamping such a pane to something
// else does not stick, and says so.
func cmdChatRestamp(args []string) error {
	if len(args) == 0 || len(args) > 2 {
		return fmt.Errorf("usage: gv chat restamp <session> [<session-id>]   (no id clears the stamp; the next `gv chat ls` re-derives it)")
	}
	rec, err := findChat(args[0])
	if err != nil {
		return err
	}
	if rec.Pane == "" {
		return fmt.Errorf("%s has no live pane — only a running chat wears an identity stamp", chatName(rec.Row))
	}
	if len(args) == 1 {
		if err := tmux.SetPaneChatSession(rec.Pane, ""); err != nil {
			return fmt.Errorf("clearing the stamp on %s: %w", rec.Pane, err)
		}
		fmt.Printf("✓ cleared %s's session id — the next `gv chat ls` re-derives it\n", chatName(rec.Row))
		return nil
	}
	id := args[1]
	if !chat.ValidSessionID(id) {
		return fmt.Errorf("%q is not a Claude session id — `gv chat ls` prints the ids this machine has", id)
	}
	// Refuse an id with no transcript in THIS chat's project dir: a stamp
	// that names a conversation the pane's cwd cannot hold sends `tail` at a
	// file that will never exist and `send` at the wrong agent — the exact
	// failure this verb repairs.
	sessions, err := transcript.ListSessions(rec.Dir)
	if err != nil {
		return fmt.Errorf("reading the transcripts under %s: %w", rec.Dir, err)
	}
	if !hasSession(sessions, id) {
		return fmt.Errorf("no transcript %s in %s — a chat's cwd is its project dir, so an id from another dir can never be the one running here (`gv chat ls` lists them)", id, rec.Dir)
	}
	if err := tmux.SetPaneChatSession(rec.Pane, id); err != nil {
		return fmt.Errorf("stamping %s: %w", rec.Pane, err)
	}
	fmt.Printf("✓ %s → %s\n", chatName(rec.Row), id)
	if live := chat.PaneSessionID(scanProcs(), rec.PID); live != "" && live != id {
		fmt.Fprintf(os.Stderr, "warning: the claude running in %s was launched on %s — the next `gv chat ls` corrects the stamp back to it\n", rec.Pane, live)
	}
	return nil
}

// hasSession reports whether id is among these transcripts — the membership
// test transcriptLabel cannot make on its own, because a conversation whose
// first line is not a prompt has a real transcript and an empty label.
func hasSession(sessions []transcript.Session, id string) bool {
	for _, s := range sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}
