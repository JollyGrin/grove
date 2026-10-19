package notify

import (
	"testing"
	"unicode/utf8"
)

func TestCapRunesNeverSplitsCodepoint(t *testing.T) {
	// grove-452 item 2: body[:120]/body[:200] byte cuts could land inside
	// a multibyte rune and put a garbage char in the notification.
	body := ""
	for i := 0; i < 150; i++ {
		body += "é" // 2 bytes each: every byte-cut at 120 would be mid-rune
	}
	for _, n := range []int{120, 200} {
		got := capRunes(body, n)
		if !utf8.ValidString(got) {
			t.Fatalf("capRunes(…, %d) produced invalid UTF-8: %q", n, got)
		}
		r := []rune(got)
		if n >= 150 {
			if got != body {
				t.Fatalf("capRunes(…, %d) altered a body under the cap", n)
			}
			continue
		}
		if len(r) != n+1 || r[n] != '…' {
			t.Fatalf("capRunes(…, %d) = %d runes, want %d + ellipsis", n, len(r), n)
		}
	}
	if got := capRunes("short", 120); got != "short" {
		t.Fatalf("short body altered: %q", got)
	}
}
