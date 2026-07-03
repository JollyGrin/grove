package hooks

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/config"
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

func TestReceiveIgnoresUntrackedCwd(t *testing.T) {
	dir := t.TempDir()
	payload := `{"session_id":"s1","cwd":"/nowhere/special","hook_event_name":"Stop","last_assistant_message":"STATUS: DONE — x"}`
	if err := Receive(dir, "stop", strings.NewReader(payload)); err != nil {
		t.Fatalf("untracked cwd must be silently ignored, got %v", err)
	}
	tasks, _ := state.Load(dir)
	if len(tasks) != 0 {
		t.Error("no events should be written for untracked cwd")
	}
}

// --- ntfy push ---

// seedTask registers a tracked task whose worktree is a real temp dir and
// returns (stateDir, realpath'd cwd for hook payloads).
func seedTask(t *testing.T) (string, string) {
	t.Helper()
	stateDir := t.TempDir()
	wt := t.TempDir()
	ev := state.Event{Type: state.EvTaskCreated, Ticket: "DEV-77", Data: map[string]string{
		"title": "x", "repo": "r", "branch": "b", "worktree": wt,
		"tmux_session": "pr-r", "tmux_window": "b",
	}}
	if err := state.Append(stateDir, ev); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(wt)
	return stateDir, real
}

func withNtfy(t *testing.T, n config.Notify) {
	t.Helper()
	old := ntfySettings
	ntfySettings = func() config.Notify { return n }
	t.Cleanup(func() { ntfySettings = old })
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
	if err := Receive(stateDir, "stop", strings.NewReader(payload)); err != nil {
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
	if err := Receive(stateDir, "stop", strings.NewReader(payload)); err != nil {
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
	if err := Receive(stateDir, "stop", strings.NewReader(payload)); err != nil {
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
	if err := Receive(stateDir, "stop", strings.NewReader(payload)); err != nil {
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
