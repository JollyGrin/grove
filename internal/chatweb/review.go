package chatweb

// grove-334: the Submit page's picks.
//
// AskUserQuestion's last page is a two-row menu — Submit answers / Cancel —
// and the phone showed only that: the operator was asked to submit answers
// it could not see. The pane shows them, above the menu:
//
//	Review your answers
//
//	 ● Which colors do you like?
//	   → Red, Blue
//
//	Ready to submit your answers?
//
// Kept out of picker.go's detector on purpose: this is a read of text the
// detector already fired on, not a new way to fire.

import "strings"

// WithReview fills p.Review from the capture p was detected in. A picker
// with no review block, or none at all, comes back unchanged.
func WithReview(p Picker, capture string) Picker {
	if p.Detected {
		p.Review = reviewPicks(capture)
	}
	return p
}

// reviewPicks reads the LAST "Review your answers" block of a capture: each
// `● question` with its `→ answer`, joined. An answer with no question
// line above it stands alone.
func reviewPicks(capture string) []string {
	lines := strings.Split(capture, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "Review your answers" {
			start = i + 1
		}
	}
	if start < 0 {
		return nil
	}
	var out []string
	q := ""
	for _, l := range lines[start:] {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "Ready to submit"), strings.HasPrefix(t, "❯"):
			return out
		case strings.HasPrefix(t, "●"):
			q = strings.TrimSpace(strings.TrimPrefix(t, "●"))
		case strings.HasPrefix(t, "→"):
			a := strings.TrimSpace(strings.TrimPrefix(t, "→"))
			if q != "" {
				a = q + " → " + a
			}
			out = append(out, a)
			q = ""
		}
	}
	return out
}
