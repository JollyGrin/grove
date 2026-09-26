package chatweb_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/chatweb"
)

func capture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The captures are live Claude Code 2.1.283 panes (grove-300). running is
// a spinner frame with NO "thinking/thought" stats and a ✢ glyph — plus
// the prompt and hints below it — which is exactly the frame the plain
// classifier used to read as idle.
func TestClassifyTurn(t *testing.T) {
	for _, c := range []struct {
		name, capture string
		alive         bool
		want          string
	}{
		{"running", capture(t, "cc2.1.283-running.txt"), true, chatweb.TurnRunning},
		{"idle", capture(t, "cc2.1.283-idle.txt"), true, chatweb.TurnIdle},
		// A message pasted but never submitted sits in the input box: the
		// pane is idle, and nothing is going to answer it — case 1.
		{"idle, unsubmitted text in the box", capture(t, "cc2.1.283-idle-typed.txt"), true, chatweb.TurnIdle},
		// Case 2: the turn died on an API error.
		{"errored", capture(t, "cc2.1.283-errored.txt"), true, chatweb.TurnErrored},
		// grove-342: the turn died on an expired Claude login — the pane
		// otherwise reads as quiet idle, and the phone must say errored.
		{"login expired", capture(t, "cc2.1.283-login-expired.txt"), true, chatweb.TurnErrored},
		{"a modal is up", capture(t, "cc2.1.282-perm.txt"), true, chatweb.TurnWaiting},
		// Case 1's other half: the pane or its claude is gone.
		{"no claude in the pane", capture(t, "cc2.1.283-running.txt"), false, chatweb.TurnStopped},
		{"nothing readable", "", true, chatweb.TurnUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := chatweb.ClassifyTurn(c.capture, c.alive); got.State != c.want {
				t.Errorf("state %q, want %q (%+v)", got.State, c.want, got)
			}
		})
	}

	got := chatweb.ClassifyTurn(capture(t, "cc2.1.283-errored.txt"), true)
	if got.Reason != "api_error" || !strings.Contains(got.Line, "API Error: 529") {
		t.Errorf("errored must say what happened: %+v", got)
	}

	// grove-342: an expired login passes its own line through verbatim.
	auth := chatweb.ClassifyTurn(capture(t, "cc2.1.283-login-expired.txt"), true)
	if auth.Reason != "auth" || !strings.Contains(auth.Line, "Login expired · Please run /login") {
		t.Errorf("an expired login must be errored/auth with its line: %+v", auth)
	}
}

// An error line still on screen does not outrank a live spinner: the
// operator retried, and the new turn is running.
func TestClassifyTurnSpinnerOutranksAnOldError(t *testing.T) {
	c := strings.Replace(capture(t, "cc2.1.283-errored.txt"),
		"────", "✶ Crunching… (4s · ↓ 120 tokens)\n\n────", 1)
	if got := chatweb.ClassifyTurn(c, true); got.State != chatweb.TurnRunning {
		t.Errorf("state %q, want running", got.State)
	}
}

// An error that has scrolled well up the pane is a turn the operator has
// moved past, not the current one.
func TestClassifyTurnIgnoresAnErrorInScrollback(t *testing.T) {
	c := "  ⎿  API Error: 500\n" + strings.Repeat("prose\n", 20) + capture(t, "cc2.1.283-idle.txt")
	if got := chatweb.ClassifyTurn(c, true); got.State != chatweb.TurnIdle {
		t.Errorf("state %q, want idle", got.State)
	}
}

// Same for the auth markers (grove-342): a transcript merely quoting
// /login, or an expired-login line from a turn long since past, is not the
// current turn — the bottom-N trim keeps it out, as for the other markers.
func TestClassifyTurnIgnoresAuthQuotingInScrollback(t *testing.T) {
	c := "  ⎿ the fix is to run /login again — Login expired was the old message\n" +
		strings.Repeat("prose\n", 20) + capture(t, "cc2.1.283-idle.txt")
	if got := chatweb.ClassifyTurn(c, true); got.State != chatweb.TurnIdle {
		t.Errorf("state %q, want idle", got.State)
	}
}

// streamTurns opens a live stream and collects `turn` event payloads until
// n arrive or the deadline passes.
func streamTurns(t *testing.T, b *fakeBackend, n int, within time.Duration) []string {
	t.Helper()
	s := chatweb.NewServer(b)
	chatweb.SetPoll(s, 10*time.Millisecond)
	srv := httptest.NewServer(s)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/chats/c/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got []string
	var buf strings.Builder
	chunk := make([]byte, 4096)
	for len(got) < n {
		k, err := resp.Body.Read(chunk)
		buf.Write(chunk[:k])
		for {
			s := buf.String()
			i := strings.Index(s, "event: turn\ndata: ")
			if i < 0 {
				break
			}
			rest := s[i+len("event: turn\ndata: "):]
			j := strings.Index(rest, "\n")
			if j < 0 {
				break
			}
			got = append(got, rest[:j])
			buf.Reset()
			buf.WriteString(rest[j:])
		}
		if err != nil {
			break
		}
	}
	return got
}

// The two lies the heuristic told forever each end in a `turn` event
// within a few polls.
func TestEventsEmitTurnForBothFailureCases(t *testing.T) {
	stopped := streamTurns(t, &fakeBackend{tailHold: true,
		turns: []chatweb.Turn{{State: chatweb.TurnStopped}}}, 1, 3*time.Second)
	if len(stopped) != 1 || stopped[0] != `{"state":"stopped"}` {
		t.Errorf("a dead pane must be reported: %q", stopped)
	}
	errored := streamTurns(t, &fakeBackend{tailHold: true,
		turns: []chatweb.Turn{{State: chatweb.TurnErrored, Reason: "usage_limit", Line: "Usage limit reached"}}}, 1, 3*time.Second)
	if len(errored) != 1 || errored[0] != `{"state":"errored","reason":"usage_limit","line":"Usage limit reached"}` {
		t.Errorf("an errored turn must be reported with its cause: %q", errored)
	}
}

// Enter-to-first-spinner reads idle for a frame or two. That blip must not
// reach the phone as "no reply"; a state that holds must.
func TestEventsDebounceAQuietBlip(t *testing.T) {
	r, i := chatweb.Turn{State: chatweb.TurnRunning}, chatweb.Turn{State: chatweb.TurnIdle}
	got := streamTurns(t, &fakeBackend{tailHold: true,
		turns: []chatweb.Turn{r, i, i, r, r, i, i, i, i}}, 3, 3*time.Second)
	want := []string{`{"state":"running"}`, `{"state":"idle"}`}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("turn events %q, want %q (the two-frame idle blip must be swallowed)", got, want)
	}
}
