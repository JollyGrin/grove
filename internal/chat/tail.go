package chat

// grove-216: following a chat's transcript file.
//
// The whole reason `gv chat tail` is cheap: a Claude Code transcript is
// APPEND-ONLY, so "what happened since" is a byte offset, not a diff. No
// tmux polling, no ANSI, no chrome. The loop below re-opens the file each
// pass (rather than holding a handle) so a chat that has not written its
// first line yet is a wait rather than an error.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"time"
)

// TailPoll is how often --follow looks for appended bytes. The acceptance
// bar is "a new entry within ~1s of an append", and 250ms of stat+read on
// one file is nothing next to the claude process writing it.
const TailPoll = 250 * time.Millisecond

// TailOptions configures a tail. The zero value is a one-shot replay of the
// whole file from the beginning.
type TailOptions struct {
	// Since suppresses entries with seq <= Since. The file is still read
	// from byte zero — seq is only stable if every line is projected — so
	// this is a filter, never a seek.
	Since int
	// Follow keeps the tail open, emitting appends as they land.
	Follow bool
	// Poll overrides TailPoll (tests).
	Poll time.Duration
}

// Tail writes a transcript's entries to w as JSONL, one entry per line,
// flushed as each lands so a --follow consumer (the SSE bridge in grove-218,
// or a human) sees them live rather than at buffer boundaries.
//
// A missing file is an error for a one-shot read (the caller named a
// transcript that is not there) and a WAIT under --follow (the chat is
// booting and claude has not minted its first line yet) — the same
// distinction `gv chat ls` draws with `session_id: null`.
//
// Only complete lines are projected: a line without its terminator is the
// writer mid-append, and is left for the next pass. That is what makes the
// tail safe against a 15KB tool_result landing in two writes.
func Tail(ctx context.Context, path string, opts TailOptions, w io.Writer) error {
	poll := opts.Poll
	if poll <= 0 {
		poll = TailPoll
	}
	proj := NewProjector()
	enc := json.NewEncoder(w)
	var offset int64
	for {
		if err := tailPass(path, &offset, proj, opts, enc); err != nil {
			if !opts.Follow || !os.IsNotExist(err) {
				return err
			}
		}
		if !opts.Follow {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(poll):
		}
	}
}

// tailPass reads every complete line appended since offset, projects it, and
// writes the entries past opts.Since. offset advances only over bytes that
// were fully consumed, so a partial trailing line is re-read next pass.
//
// The one exception: a one-shot read treats a trailing line with no
// terminator as final and projects it best-effort — nobody is going to
// append the newline for us, and json.Unmarshal rejects a truncated line
// anyway (Projector.Line's skip rule), so the worst case is a no-op.
func tailPass(path string, offset *int64, proj *Projector, opts TailOptions, enc *json.Encoder) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(*offset, io.SeekStart); err != nil {
		return err
	}
	// bufio.Reader, never bufio.Scanner: a Scanner silently DROPS a line
	// longer than its buffer, and a tool_result routinely runs past 64KB —
	// the same silent-truncation class that has bitten every other reader in
	// this repo.
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if len(line) > 0 && !opts.Follow {
				*offset += int64(len(line))
				if werr := emit(enc, proj.Line([]byte(line)), opts.Since); werr != nil {
					return werr
				}
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
		*offset += int64(len(line))
		if werr := emit(enc, proj.Line([]byte(line)), opts.Since); werr != nil {
			return werr
		}
	}
}

// emit writes the entries a client has not seen yet.
func emit(enc *json.Encoder, entries []Entry, since int) error {
	for _, e := range entries {
		if e.Seq <= since {
			continue
		}
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}
