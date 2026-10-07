package notify

import (
	"testing"
	"unicode/utf8"
)

// TestCapRunesMultibyteBoundary is the grove-131 (item 2) regression check:
// the notification body cap counts runes, so a cut can never split a
// multibyte codepoint into a garbage character on the phone/desktop.
func TestCapRunesMultibyteBoundary(t *testing.T) {
	// 2-byte runes: a byte cut at 5 would land mid-rune.
	s := "ééééééé"
	got := capRunes(s, 5)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
	if want := "ééééé…"; got != want {
		t.Errorf("capRunes(%q, 5) = %q, want %q", s, got, want)
	}
	// Over the byte cap but within the rune cap: untouched.
	if got := capRunes(s, 7); got != s {
		t.Errorf("at-cap string changed: %q", got)
	}
	if got := capRunes("plain", 120); got != "plain" {
		t.Errorf("short ASCII changed: %q", got)
	}
	// The real caps, with a 4-byte emoji straddling each boundary.
	for _, n := range []int{120, 200} {
		long := ""
		for len([]rune(long)) < n+3 {
			long += "a😀"
		}
		got := capRunes(long, n)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) != n+1 {
			t.Errorf("capRunes(…, %d) = %q (%d runes)", n, got, utf8.RuneCountInString(got))
		}
	}
}
