package main

// grove-216: the join `tail`/`send`/`keys` ride on — a contract row plus the
// pane id to paste into and the cwd whose project dir holds the transcript.
// `gv chat ls` throws both away, so nothing before this ticket proved they
// survive the report.

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/transcript"
	"github.com/JollyGrin/grove/internal/workspace"
)

func TestChatRecordsCarryPaneAndTranscriptPath(t *testing.T) {
	ws, orch := chatFixture(t)
	profileDir := filepath.Join(orch, "e2e-glm")
	when := time.Now().Add(-time.Hour)
	writeTranscript(t, orch, "1111-brain", "triage the artgen backlog", when)
	writeTranscript(t, profileDir, "2222-glm", "on the cheap lane", when)
	writeTranscript(t, orch, "3333-old", "last tuesday", when.Add(-48*time.Hour))

	panes := []tmux.LivePane{
		{Session: "grove-chat-unbrewed-1", Pane: "%7", Dir: orch, Command: "claude", Created: time.Unix(1700000100, 0)},
		{Session: "grove-chat-unbrewed-2", Pane: "%8", Dir: profileDir, Command: "claude", Created: time.Unix(1700000200, 0)},
	}
	_, stamp := recordStamps()
	recs := chatRecords([]workspace.Workspace{ws}, panes, neverCockpit, stamp)

	byName := map[string]chatRecord{}
	for _, r := range recs {
		byName[chatName(r.Row)] = r
	}
	// A profiled chat reads its OWN project dir — Claude Code keys project
	// dirs by cwd, so borrowing the brain dir's path here would hand the
	// phone the wrong conversation.
	for _, tc := range []struct{ target, pane, dir, id string }{
		{"grove-chat-unbrewed-1", "%7", orch, "1111-brain"},
		{"grove-chat-unbrewed-2", "%8", profileDir, "2222-glm"},
		{"3333-old", "", orch, "3333-old"},
	} {
		rec, ok := byName[tc.target]
		if !ok {
			t.Fatalf("no record for %s in %v", tc.target, byName)
		}
		if rec.Pane != tc.pane {
			t.Errorf("%s pane = %q, want %q", tc.target, rec.Pane, tc.pane)
		}
		if rec.Dir != tc.dir {
			t.Errorf("%s dir = %q, want %q", tc.target, rec.Dir, tc.dir)
		}
		got, err := rec.transcriptPath()
		if err != nil {
			t.Fatalf("%s: %v", tc.target, err)
		}
		if want := filepath.Join(transcript.ProjectDir(tc.dir), tc.id+".jsonl"); got != want {
			t.Errorf("%s transcript = %q, want %q", tc.target, got, want)
		}
	}

	// And the report a caller matches against is still exactly `ls`'s.
	rows := chatRows([]workspace.Workspace{ws}, panes, neverCockpit, stamp)
	if len(rows) != len(recs) {
		t.Fatalf("chatRows dropped rows: %d vs %d records", len(rows), len(recs))
	}
	for i := range rows {
		if !reflect.DeepEqual(rows[i], recs[i].Row) {
			t.Errorf("row %d diverged from its record", i)
		}
	}
}

// A chat that has not written its first transcript line yet reports
// session_id null (grove-215) — so the read verbs must say "still booting",
// never point at a `.jsonl` named after the empty string.
func TestChatRecordUnresolvedHasNoTranscriptPath(t *testing.T) {
	rec := chatRecord{Row: chat.Row{Session: "grove-chat-unbrewed-9", Kind: chat.KindChat}, Pane: "%9", Dir: "/w"}
	_, err := rec.transcriptPath()
	if err == nil {
		t.Fatal("an unresolved chat must not produce a transcript path")
	}
	if !strings.Contains(err.Error(), "still booting") {
		t.Errorf("error = %v, want it to say the chat is still booting", err)
	}
}
