package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestCapRunesMultibyteBoundary is the grove-131 regression: body[:120]
// could split a UTF-8 sequence and put a garbage char at the end of a
// desktop/phone notification.
func TestCapRunesMultibyteBoundary(t *testing.T) {
	for _, n := range []int{120, 200} {
		s := strings.Repeat("ü", n+1) // every byte cut at n lands mid-rune
		got := capRunes(s, n)
		if !utf8.ValidString(got) {
			t.Fatalf("capRunes(%d) produced invalid UTF-8: %q", n, got)
		}
		if want := strings.Repeat("ü", n) + "…"; got != want {
			t.Errorf("capRunes(%d) = %q, want %q", n, got, want)
		}
	}
	if got := capRunes("short", 120); got != "short" {
		t.Errorf("short body altered: %q", got)
	}
}
