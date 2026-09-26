package detect

import "testing"

func TestSpinning(t *testing.T) {
	for _, c := range []struct {
		line string
		want bool
	}{
		// Every frame of one live turn: the glyph cycles, the stats come
		// and go — classifyPaneOutput alone reads the bare frames as idle.
		{"✶ Crunching… (2m 41s · ↓ 14.1k tokens)", true},
		{"· Crunching… (2m 40s · ↓ 14.1k tokens)", true},
		{"✻ Crunching… (2m 43s · ↓ 14.1k tokens)", true},
		{"✢ Crunching… (2m 38s · ↓ 14.0k tokens · thought for 7s)", true},
		{"✶ Deciphering… (34s · ↓ 1.3k tokens · still thinking)", true},
		// The finished forms never match.
		{"✻ Baked for 55s · done 12:13 AM", false},
		{"✻ Worked for 4m 47s · done 11:51 PM", false},
		// Prose with an ellipsis is not a spinner.
		{"Thinking about it… (later)", false},
		{"  - step one… (2 of 3)", false},
	} {
		if got := Spinning("above\n" + c.line + "\n❯ \n"); got != c.want {
			t.Errorf("Spinning(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestErrorMarker(t *testing.T) {
	reason, line, ok := ErrorMarker("x\n  ⎿  API Error: 529 overloaded\n❯ ")
	if !ok || reason != "api_error" || line != "⎿  API Error: 529 overloaded" {
		t.Fatalf("got %q %q %v", reason, line, ok)
	}
	if _, _, ok := ErrorMarker("all quiet\n❯ "); ok {
		t.Fatal("a clean pane matched an error marker")
	}
}

// grove-342: an expired Claude login prints one of these (captured live on
// groveremote, 2.1.283) and the pane otherwise reads as quiet idle — the
// phone showed a calm chat and supervise never woke. Matching is
// case-insensitive: the auth wording is not stable across versions.
func TestErrorMarkerAuthExpired(t *testing.T) {
	for _, l := range []string{
		"Login expired · Please run /login",
		"Failed to authenticate: OAuth session expired and could not be refreshed",
		"login expired — please run /login to continue",
	} {
		reason, line, ok := ErrorMarker("x\n" + l + "\n❯ ")
		if !ok || reason != "auth" || line != l {
			t.Errorf("auth expiry %q: got %q %q %v", l, reason, line, ok)
		}
	}
	// A transcript merely quoting /login is not a dead login (and in the
	// turn classifier the bottom-N trim keeps scrollback quoting out too).
	if _, _, ok := ErrorMarker("  ⎿ the fix suggested in the transcript: run /login again\n❯ "); ok {
		t.Fatal("a transcript quoting /login matched an auth marker")
	}
}
