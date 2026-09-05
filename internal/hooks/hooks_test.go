package hooks

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/notify"
	"github.com/JollyGrin/grove/internal/state"
)

func TestClassifySentinel(t *testing.T) {
	cases := []struct {
		name         string
		msg          string
		wantStatus   string
		wantSentinel string
		wantQuestion string
	}{
		{
			name:         "question em-dash",
			msg:          "I looked at the code.\n\nSTATUS: QUESTION — Should the filter persist in the URL?",
			wantStatus:   state.AgentWaiting,
			wantSentinel: "question",
			wantQuestion: "Should the filter persist in the URL?",
		},
		{
			name:         "blocked hyphen",
			msg:          "STATUS: BLOCKED - migration needs prod credentials",
			wantStatus:   state.AgentBlocked,
			wantSentinel: "blocked",
			wantQuestion: "migration needs prod credentials",
		},
		{
			name:         "done multiline paragraph",
			msg:          "All finished.\n\nSTATUS: DONE — Added URL-persisted filters.\nCheck the /profiles page in the preview.",
			wantStatus:   state.AgentIdle,
			wantSentinel: "done",
		},
		{
			name:         "no sentinel",
			msg:          "I think I finished but forgot the protocol.",
			wantStatus:   state.AgentIdle,
			wantSentinel: "none",
		},
		{
			name:         "indented sentinel from numbered list",
			msg:          "Summary above.\n   STATUS: QUESTION — keep both endpoints?",
			wantStatus:   state.AgentWaiting,
			wantSentinel: "question",
			wantQuestion: "keep both endpoints?",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, sentinel, question, message := classify(c.msg)
			if status != c.wantStatus || sentinel != c.wantSentinel {
				t.Errorf("classify() = (%s, %s), want (%s, %s)", status, sentinel, c.wantStatus, c.wantSentinel)
			}
			if c.wantQuestion != "" && !strings.HasPrefix(question, c.wantQuestion) {
				t.Errorf("question = %q, want prefix %q", question, c.wantQuestion)
			}
			if message == "" {
				t.Error("full message should always be preserved")
			}
		})
	}
}

// single wraps one state dir as the full candidate list — the pre-workspaces
// shape every legacy-path test exercises.
func single(dir string) []Candidate {
	return []Candidate{{Label: "test", StateDir: dir}}
}

func TestReceiveIgnoresUntrackedCwd(t *testing.T) {
	dir := t.TempDir()
	payload := `{"session_id":"s1","cwd":"/nowhere/special","hook_event_name":"Stop","last_assistant_message":"STATUS: DONE — x"}`
	if err := Receive(single(dir), "stop", strings.NewReader(payload)); err != nil {
		t.Fatalf("untracked cwd must be silently ignored, got %v", err)
	}
	tasks, _ := state.Load(dir)
	if len(tasks) != 0 {
		t.Error("no events should be written for untracked cwd")
	}
}

// seedFleet registers a tracked task in stateDir and materializes the
// derived tasks.json (Receive scans read-only via state.ReadTasks, so the
// view must exist up front — in a live state dir gv itself keeps it fresh).
// Returns the realpath'd worktree for hook payloads.
func seedFleet(t *testing.T, stateDir, ticket, wt string) string {
	t.Helper()
	ev := state.Event{Type: state.EvTaskCreated, Ticket: ticket, Data: map[string]string{
		"title": "x", "repo": "r", "branch": "b-" + ticket, "worktree": wt,
		"tmux_session": "pr-r", "tmux_window": "b",
	}}
	if err := state.Append(stateDir, ev); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Load(stateDir); err != nil { // fold → write tasks.json
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(wt)
	return real
}

// snapshot captures (bytes, mtime) of a file; a missing file reads as
// (nil, zero) so "still absent" is assertable too.
func snapshot(t *testing.T, path string) ([]byte, time.Time) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, time.Time{}
		}
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return data, fi.ModTime()
}

func assertUnchanged(t *testing.T, path string, wantData []byte, wantMtime time.Time) {
	t.Helper()
	data, mtime := snapshot(t, path)
	if !bytes.Equal(data, wantData) {
		t.Errorf("%s: content changed (%d bytes → %d bytes)", path, len(wantData), len(data))
	}
	if !mtime.Equal(wantMtime) {
		t.Errorf("%s: mtime changed %v → %v — non-owner was written to", path, wantMtime, mtime)
	}
}

func TestReceiveMultiFleetOwnership(t *testing.T) {
	withNtfy(t, config.Notify{}) // never reach a real push topic

	dirA, dirB, legacy := t.TempDir(), t.TempDir(), t.TempDir()
	wtA, wtB := t.TempDir(), t.TempDir()
	seedFleet(t, dirA, "DEV-A", wtA)
	cwdB := seedFleet(t, dirB, "DEV-B", wtB)
	candidates := []Candidate{
		{Label: "a", StateDir: dirA},
		{Label: "b", StateDir: dirB},
		{Label: "legacy", StateDir: legacy},
	}

	aEvents := filepath.Join(dirA, "events.jsonl")
	aTasks := filepath.Join(dirA, "tasks.json")
	bEvents := filepath.Join(dirB, "events.jsonl")
	bTasks := filepath.Join(dirB, "tasks.json")
	legacyEvents := filepath.Join(legacy, "events.jsonl")

	// Push A's mtimes into the past so even a byte-identical rewrite of a
	// non-owner would be caught.
	past := time.Now().Add(-time.Hour)
	for _, p := range []string{aEvents, aTasks} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}

	lastEvent := func(t *testing.T, path string) state.Event {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		var ev state.Event
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &ev); err != nil {
			t.Fatal(err)
		}
		return ev
	}

	// 1. A Stop for B's worktree lands in B only; A stays byte- and
	// mtime-untouched (read-only non-owner scan); legacy gains nothing.
	aEvData, aEvMtime := snapshot(t, aEvents)
	aTaskData, aTaskMtime := snapshot(t, aTasks)
	if err := Receive(candidates, "stop", strings.NewReader(stopPayload(cwdB, "plain idle stop"))); err != nil {
		t.Fatal(err)
	}
	if ev := lastEvent(t, bEvents); ev.Type != state.EvAgentStatus || ev.Ticket != "DEV-B" {
		t.Errorf("B's last event = %s/%s, want agent_status/DEV-B", ev.Type, ev.Ticket)
	}
	assertUnchanged(t, aEvents, aEvData, aEvMtime)
	assertUnchanged(t, aTasks, aTaskData, aTaskMtime)
	if _, err := os.Stat(legacyEvents); !os.IsNotExist(err) {
		t.Error("legacy fleet must gain no events.jsonl")
	}

	// 2. An unknown cwd appends nowhere across all three candidates.
	bEvData, bEvMtime := snapshot(t, bEvents)
	bTaskData, bTaskMtime := snapshot(t, bTasks)
	if err := Receive(candidates, "stop", strings.NewReader(stopPayload("/nowhere/special", "plain idle stop"))); err != nil {
		t.Fatal(err)
	}
	assertUnchanged(t, aEvents, aEvData, aEvMtime)
	assertUnchanged(t, aTasks, aTaskData, aTaskMtime)
	assertUnchanged(t, bEvents, bEvData, bEvMtime)
	assertUnchanged(t, bTasks, bTaskData, bTaskMtime)
	if _, err := os.Stat(legacyEvents); !os.IsNotExist(err) {
		t.Error("legacy fleet must gain no events.jsonl for unknown cwd")
	}

	// 3. Ordering: a cwd tracked by BOTH A and B goes to A — first match
	// in the given candidate order wins.
	wtShared := t.TempDir()
	seedFleet(t, dirA, "DEV-SHARED", wtShared)
	cwdShared := seedFleet(t, dirB, "DEV-SHARED", wtShared)
	bEvData, bEvMtime = snapshot(t, bEvents)
	bTaskData, bTaskMtime = snapshot(t, bTasks)
	if err := Receive(candidates, "stop", strings.NewReader(stopPayload(cwdShared, "plain idle stop"))); err != nil {
		t.Fatal(err)
	}
	if ev := lastEvent(t, aEvents); ev.Type != state.EvAgentStatus || ev.Ticket != "DEV-SHARED" {
		t.Errorf("A's last event = %s/%s, want agent_status/DEV-SHARED (first match wins)", ev.Type, ev.Ticket)
	}
	assertUnchanged(t, bEvents, bEvData, bEvMtime)
	assertUnchanged(t, bTasks, bTaskData, bTaskMtime)
}

// --- installer / dual-hook coexistence (DESIGN.md §12) ---

// ovsSettings mirrors a real transition-window ~/.cc-work/settings.json:
// live ovs hooks already wired, plus an unrelated user hook that must
// also survive untouched.
const ovsSettings = `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "/Users/x/go/bin/ovs hook session-start"}]}
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "/Users/x/go/bin/ovs hook stop"}]},
      {"hooks": [{"type": "command", "command": "afplay /System/Library/Sounds/Glass.aiff"}]}
    ]
  },
  "permissions": {"defaultMode": "bypassPermissions"}
}`

func TestIsGvEntry(t *testing.T) {
	entry := func(cmd string) any {
		return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}}
	}
	cases := []struct {
		name string
		e    any
		want bool
	}{
		{"gv absolute path", entry("/Users/x/go/bin/gv hook stop"), true},
		{"grove-named binary", entry("/opt/grove/bin/grove-cli hook stop"), true},
		{"ovs entry NEVER matches", entry("/Users/x/go/bin/ovs hook stop"), false},
		{"overstory-named binary never matches", entry("/Users/x/go/bin/overstory hook stop"), false},
		{"gv-ish parent dir but ovs binary", entry("/Users/x/gv-tools/ovs hook stop"), false},
		{"unrelated command", entry("afplay /System/Library/Sounds/Glass.aiff"), false},
		{"non-hook gv command", entry("/Users/x/go/bin/gv ls"), false},
		{"malformed entry", "not a map", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isGvEntry(c.e); got != c.want {
				t.Errorf("isGvEntry = %v, want %v", got, c.want)
			}
		})
	}
}

func TestInstallPreservesOvsEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(ovsSettings), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := install(path, "/Users/x/go/bin/gv"); err != nil {
		t.Fatal(err)
	}
	// Idempotency: a second install (new binary path) must replace its own
	// entry, not stack a duplicate — and still not touch ovs.
	if err := install(path, "/Users/x/go/bin/gv"); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(path)
	var s struct {
		Hooks       map[string][]any `json:"hooks"`
		Permissions map[string]any   `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}

	count := func(entries []any, substr string) int {
		n := 0
		for _, e := range entries {
			b, _ := json.Marshal(e)
			if strings.Contains(string(b), substr) {
				n++
			}
		}
		return n
	}
	for _, ev := range []string{"SessionStart", "Stop"} {
		if got := count(s.Hooks[ev], "/ovs hook"); got != 1 {
			t.Errorf("%s: ovs entries = %d, want 1 (must survive byte-identical)", ev, got)
		}
	}
	for _, ev := range []string{"SessionStart", "Notification", "Stop", "SessionEnd"} {
		if got := count(s.Hooks[ev], "/gv hook"); got != 1 {
			t.Errorf("%s: gv entries = %d, want exactly 1 after double install", ev, got)
		}
	}
	if got := count(s.Hooks["Stop"], "afplay"); got != 1 {
		t.Error("unrelated user hook must survive")
	}
	if s.Permissions["defaultMode"] != "bypassPermissions" {
		t.Error("non-hook settings keys must survive")
	}

	got, err := installed(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"SessionStart", "Notification", "Stop", "SessionEnd"} {
		if !got[ev] {
			t.Errorf("installed() missing %s", ev)
		}
	}
}

func TestInstalledSeesOnlyGvEntries(t *testing.T) {
	// A settings file with ONLY ovs hooks: gv must report nothing installed.
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(ovsSettings), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := installed(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("ovs-only settings must read as not-installed for gv, got %v", got)
	}
}

// --- ntfy push ---

// seedTask registers a tracked task whose worktree is a real temp dir and
// returns (stateDir, realpath'd cwd for hook payloads).
func seedTask(t *testing.T) (string, string) {
	t.Helper()
	stateDir := t.TempDir()
	cwd := seedFleet(t, stateDir, "DEV-77", t.TempDir())
	return stateDir, cwd
}

func withNtfy(t *testing.T, n config.Notify) {
	t.Helper()
	old := notify.Settings
	notify.Settings = func() config.Notify { return n }
	t.Cleanup(func() { notify.Settings = old })
}

func stopPayload(cwd, msg string) string {
	b, _ := json.Marshal(map[string]string{
		"session_id": "s1", "cwd": cwd, "hook_event_name": "Stop",
		"last_assistant_message": msg,
	})
	return string(b)
}

func TestNtfyPushQuestion(t *testing.T) {
	stateDir, cwd := seedTask(t)
	var gotTitle, gotPriority, gotBody string
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
	}))
	defer srv.Close()
	withNtfy(t, config.Notify{Ntfy: srv.URL})

	payload := stopPayload(cwd, "STATUS: QUESTION — Tabs or spaces?")
	if err := Receive(single(stateDir), "stop", strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if posts != 1 {
		t.Fatalf("posts = %d, want 1", posts)
	}
	if !strings.Contains(gotTitle, "DEV-77") {
		t.Errorf("Title = %q, want ticket id", gotTitle)
	}
	if gotPriority != "high" {
		t.Errorf("Priority = %q, want high", gotPriority)
	}
	if !strings.Contains(gotBody, "Tabs or spaces?") {
		t.Errorf("body = %q", gotBody)
	}
}

func TestNtfyNoPushOnIdle(t *testing.T) {
	stateDir, cwd := seedTask(t)
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts++ }))
	defer srv.Close()
	withNtfy(t, config.Notify{Ntfy: srv.URL})

	payload := stopPayload(cwd, "finished but forgot the protocol")
	if err := Receive(single(stateDir), "stop", strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if posts != 0 {
		t.Fatalf("idle stop must not push, got %d posts", posts)
	}
}

func TestNtfyTitleOnly(t *testing.T) {
	stateDir, cwd := seedTask(t)
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
	}))
	defer srv.Close()
	withNtfy(t, config.Notify{Ntfy: srv.URL, NtfyBody: "title-only"})

	payload := stopPayload(cwd, "STATUS: QUESTION — secret details here")
	if err := Receive(single(stateDir), "stop", strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if gotBody != "" {
		t.Errorf("title-only body = %q, want empty", gotBody)
	}
}

func TestNtfyTimeoutSwallowed(t *testing.T) {
	stateDir, cwd := seedTask(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
	}))
	defer srv.Close()
	withNtfy(t, config.Notify{Ntfy: srv.URL})

	start := time.Now()
	payload := stopPayload(cwd, "STATUS: QUESTION — slow server")
	if err := Receive(single(stateDir), "stop", strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2500*time.Millisecond {
		t.Errorf("Receive took %v — timeout not enforced", elapsed)
	}
}

func TestNtfyBrokenRepoConfigStillPushes(t *testing.T) {
	// NotifySettingsFrom must skip repo validation entirely: a config whose
	// repo paths are invalid still yields the notify section.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yamlBody := "repos:\n  broken:\n    path: /does/not/exist\nnotify:\n  ntfy: https://example.test/topic\n  ntfy_body: title-only\n"
	if err := os.WriteFile(cfgPath, []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	n := config.NotifySettingsFrom(cfgPath)
	if n.Ntfy != "https://example.test/topic" || n.NtfyBody != "title-only" {
		t.Errorf("NotifySettingsFrom = %+v", n)
	}
	if got := config.NotifySettingsFrom(filepath.Join(dir, "missing.yaml")); got.Ntfy != "" {
		t.Errorf("missing file should yield zero settings, got %+v", got)
	}
}

// grove-126: a stop's stored message is bounded — events.jsonl is append-only
// and folded every cockpit tick, so unbounded per-turn messages compound the
// read cost forever. Classification still runs on the FULL text: a sentinel
// past the cap must classify, only the stored message is truncated.
func TestStopMessageCapped(t *testing.T) {
	withNtfy(t, config.Notify{})
	stateDir := t.TempDir()
	cwd := seedFleet(t, stateDir, "DEV-CAP", t.TempDir())

	long := strings.Repeat("héllo wörld ", 1000) // 12k runes, multibyte
	msg := long + "\nSTATUS: DONE — wrapped up despite the essay above"
	if err := Receive(single(stateDir), "stop", strings.NewReader(stopPayload(cwd, msg))); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(stateDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var ev state.Event
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Data["sentinel"] != "done" {
		t.Errorf("sentinel past the cap must still classify, got %q", ev.Data["sentinel"])
	}
	got := ev.Data["message"]
	if n := utf8.RuneCountInString(got); n != messageCap+1 { // cap + ellipsis
		t.Errorf("stored message = %d runes, want %d", n, messageCap+1)
	}
	if !strings.HasSuffix(got, "…") || !utf8.ValidString(got) {
		t.Errorf("cap must be rune-safe with an ellipsis marker, got tail %q", got[len(got)-12:])
	}
	if !strings.HasPrefix(got, "héllo") {
		t.Errorf("cap must keep the head, got %q", got[:12])
	}
}

func TestCapRunesShortMessageUntouched(t *testing.T) {
	msg := "STATUS: DONE — short and sweet"
	if got := capRunes(msg, messageCap); got != msg {
		t.Errorf("under-cap message must pass through untouched, got %q", got)
	}
}

// --- session-id gate (grove-250) ---

// payloadFor builds a hook payload for the given Claude hook event name
// with an explicit session id.
func payloadFor(hookEvent, sessionID, cwd, msg string) string {
	b, _ := json.Marshal(map[string]string{
		"session_id": sessionID, "cwd": cwd, "hook_event_name": hookEvent,
		"last_assistant_message": msg,
	})
	return string(b)
}

// countLines reports the number of event records in a state dir; a
// missing events.jsonl counts as zero.
func countLines(t *testing.T, stateDir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(stateDir, "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return len(strings.Split(strings.TrimSpace(string(raw)), "\n"))
}

func lastEventIn(t *testing.T, stateDir string) state.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(stateDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var ev state.Event
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &ev); err != nil {
		t.Fatal(err)
	}
	return ev
}

// refresh folds events.jsonl into the derived tasks.json the receiver
// scans — what every gv command and cockpit tick does in a live state dir.
func refresh(t *testing.T, stateDir string) map[string]*state.Task {
	t.Helper()
	tasks, err := state.Load(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

// A stop from a session that is NOT the task's recorded worker — an
// orchestrator whose Bash tool cd'd into the worktree — must not touch the
// task: no event, no status change, and the derived view keeps the
// worker's last word.
func TestReceiveStopDropsForeignSession(t *testing.T) {
	withNtfy(t, config.Notify{})
	dir := t.TempDir()
	cwd := seedFleet(t, dir, "DEV-1", t.TempDir())
	if err := Receive(single(dir), "session-start", strings.NewReader(payloadFor("SessionStart", "s-worker", cwd, ""))); err != nil {
		t.Fatal(err)
	}
	if got := refresh(t, dir)["DEV-1"].SessionID; got != "s-worker" {
		t.Fatalf("recorded session id = %q, want s-worker", got)
	}
	if err := Receive(single(dir), "stop", strings.NewReader(payloadFor("Stop", "s-worker", cwd, "STATUS: QUESTION — tabs or spaces?"))); err != nil {
		t.Fatal(err)
	}
	refresh(t, dir)

	evData, evMtime := snapshot(t, filepath.Join(dir, "events.jsonl"))
	before := countLines(t, dir)
	err := Receive(single(dir), "stop", strings.NewReader(payloadFor("Stop", "s-intruder", cwd, "here is my chat reply, no sentinel")))
	if err != nil {
		t.Fatalf("foreign stop must be a silent no-op, got %v", err)
	}
	if got := countLines(t, dir); got != before {
		t.Errorf("foreign stop appended: %d → %d events", before, got)
	}
	assertUnchanged(t, filepath.Join(dir, "events.jsonl"), evData, evMtime)
	task := refresh(t, dir)["DEV-1"]
	if task.Agent != state.AgentWaiting || task.Sentinel != "question" || task.Question != "tabs or spaces?" {
		t.Errorf("worker state hijacked: agent=%s sentinel=%s question=%q", task.Agent, task.Sentinel, task.Question)
	}
	if task.SessionID != "s-worker" {
		t.Errorf("session id changed to %q", task.SessionID)
	}
}

// A stop from the recorded worker lands exactly as before, and the record
// now carries the speaking session's id (additive contract field).
func TestReceiveStopSameSessionAppendsWithID(t *testing.T) {
	withNtfy(t, config.Notify{})
	dir := t.TempDir()
	cwd := seedFleet(t, dir, "DEV-1", t.TempDir())
	if err := Receive(single(dir), "session-start", strings.NewReader(payloadFor("SessionStart", "s-worker", cwd, ""))); err != nil {
		t.Fatal(err)
	}
	refresh(t, dir)
	before := countLines(t, dir)
	if err := Receive(single(dir), "stop", strings.NewReader(payloadFor("Stop", "s-worker", cwd, "STATUS: DONE — shipped"))); err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, dir); got != before+1 {
		t.Fatalf("same-session stop: %d → %d events, want +1", before, got)
	}
	ev := lastEventIn(t, dir)
	if ev.Type != state.EvAgentStatus || ev.Ticket != "DEV-1" {
		t.Fatalf("last event = %s/%s, want agent_status/DEV-1", ev.Type, ev.Ticket)
	}
	if ev.Data["session_id"] != "s-worker" {
		t.Errorf("agent_status data.session_id = %q, want s-worker", ev.Data["session_id"])
	}
	if ev.Data["status"] != state.AgentIdle || ev.Data["sentinel"] != "done" {
		t.Errorf("classification changed: %v", ev.Data)
	}
	task := refresh(t, dir)["DEV-1"]
	if task.Sentinel != "done" || task.LastMessage != "STATUS: DONE — shipped" {
		t.Errorf("fold: sentinel=%s last_message=%q", task.Sentinel, task.LastMessage)
	}
}

// A task with NO recorded session id (pre-capture rows, a lost
// SessionStart) keeps cwd-only attribution — an unknown id must never make
// a task unreachable.
func TestReceiveStopNoRecordedIDFallsBackToCwd(t *testing.T) {
	withNtfy(t, config.Notify{})
	dir := t.TempDir()
	cwd := seedFleet(t, dir, "DEV-1", t.TempDir())
	if got := refresh(t, dir)["DEV-1"].SessionID; got != "" {
		t.Fatalf("fixture leaked a session id %q", got)
	}
	before := countLines(t, dir)
	if err := Receive(single(dir), "stop", strings.NewReader(payloadFor("Stop", "s-anyone", cwd, "STATUS: BLOCKED — no creds"))); err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, dir); got != before+1 {
		t.Fatalf("stop with no recorded id: %d → %d events, want +1 (cwd fallback)", before, got)
	}
	ev := lastEventIn(t, dir)
	if ev.Type != state.EvAgentStatus || ev.Data["session_id"] != "s-anyone" {
		t.Errorf("last event = %s data=%v", ev.Type, ev.Data)
	}
	if task := refresh(t, dir)["DEV-1"]; task.Agent != state.AgentBlocked {
		t.Errorf("agent = %s, want blocked", task.Agent)
	}
}

// session-start is exempt from the gate: a NEW id at the same cwd is how
// `gv adopt`'s fresh pickup session registers, and the fold re-points the
// task's SessionID at it.
func TestReceiveSessionStartNewIDRegisters(t *testing.T) {
	withNtfy(t, config.Notify{})
	dir := t.TempDir()
	cwd := seedFleet(t, dir, "DEV-1", t.TempDir())
	if err := Receive(single(dir), "session-start", strings.NewReader(payloadFor("SessionStart", "s-old", cwd, ""))); err != nil {
		t.Fatal(err)
	}
	if got := refresh(t, dir)["DEV-1"].SessionID; got != "s-old" {
		t.Fatalf("recorded id = %q, want s-old", got)
	}
	before := countLines(t, dir)
	if err := Receive(single(dir), "session-start", strings.NewReader(payloadFor("SessionStart", "s-new", cwd, ""))); err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, dir); got != before+1 {
		t.Fatalf("new-id session-start: %d → %d events, want +1", before, got)
	}
	if ev := lastEventIn(t, dir); ev.Type != state.EvSessionStarted || ev.Data["session_id"] != "s-new" {
		t.Errorf("last event = %s data=%v", ev.Type, ev.Data)
	}
	if got := refresh(t, dir)["DEV-1"].SessionID; got != "s-new" {
		t.Errorf("fold left SessionID = %q, want s-new", got)
	}
	// The new session now owns the task: its stops land, the old id's don't.
	before = countLines(t, dir)
	if err := Receive(single(dir), "stop", strings.NewReader(payloadFor("Stop", "s-old", cwd, "late word"))); err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, dir); got != before {
		t.Errorf("old session's stop appended after re-registration")
	}
	if err := Receive(single(dir), "stop", strings.NewReader(payloadFor("Stop", "s-new", cwd, "STATUS: DONE — ok"))); err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, dir); got != before+1 {
		t.Errorf("new session's stop did not land")
	}
}

// The #148 mechanism: a late SessionEnd from a REPLACED process must not
// stamp `dead` over the live successor; the current session's SessionEnd
// still does.
func TestReceiveSessionEndGatedOnCurrentSession(t *testing.T) {
	withNtfy(t, config.Notify{})
	dir := t.TempDir()
	cwd := seedFleet(t, dir, "DEV-1", t.TempDir())
	for _, id := range []string{"s-old", "s-new"} {
		if err := Receive(single(dir), "session-start", strings.NewReader(payloadFor("SessionStart", id, cwd, ""))); err != nil {
			t.Fatal(err)
		}
	}
	if task := refresh(t, dir)["DEV-1"]; task.SessionID != "s-new" || task.Agent != state.AgentWorking {
		t.Fatalf("fixture: session=%q agent=%s", task.SessionID, task.Agent)
	}

	before := countLines(t, dir)
	if err := Receive(single(dir), "session-end", strings.NewReader(payloadFor("SessionEnd", "s-old", cwd, ""))); err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, dir); got != before {
		t.Errorf("late session-end from the replaced process appended: %d → %d", before, got)
	}
	if task := refresh(t, dir)["DEV-1"]; task.Agent != state.AgentWorking {
		t.Errorf("late session-end stamped agent=%s over the live worker", task.Agent)
	}

	// Notification is gated the same way.
	if err := Receive(single(dir), "notification", strings.NewReader(payloadFor("Notification", "s-old", cwd, ""))); err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, dir); got != before {
		t.Errorf("foreign notification appended: %d → %d", before, got)
	}

	if err := Receive(single(dir), "session-end", strings.NewReader(payloadFor("SessionEnd", "s-new", cwd, ""))); err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, dir); got != before+1 {
		t.Fatalf("current session-end: %d → %d events, want +1", before, got)
	}
	if ev := lastEventIn(t, dir); ev.Type != state.EvSessionEnded || ev.Data["session_id"] != "s-new" {
		t.Errorf("last event = %s data=%v", ev.Type, ev.Data)
	}
	if task := refresh(t, dir)["DEV-1"]; task.Agent != state.AgentDead {
		t.Errorf("agent = %s after the current session's end, want dead", task.Agent)
	}
}
