package hooks

import "testing"

func TestParseSentinel(t *testing.T) {
	cases := []struct {
		name     string
		msg      string
		wantKind string
		wantText string
		wantOK   bool
	}{
		{"plain em dash", "All done.\n\nSTATUS: DONE — Added filters.", SentinelDone, "Added filters.", true},
		{"en dash", "STATUS: BLOCKED – needs prod creds", SentinelBlocked, "needs prod creds", true},
		{"hyphen", "STATUS: QUESTION - tabs or spaces?", SentinelQuestion, "tabs or spaces?", true},
		{"double hyphen", "STATUS: DONE -- shipped", SentinelDone, "shipped", true},
		{"colon separator", "STATUS: DONE: shipped the thing", SentinelDone, "shipped the thing", true},
		{"bare DONE", "Wrapped up.\n\nSTATUS: DONE", SentinelDone, "", true},
		{"bare DONE trailing space", "STATUS: DONE   ", SentinelDone, "", true},
		{"bare BLOCKED", "STATUS: BLOCKED", SentinelBlocked, "", true},
		{"bold wraps sentinel", "**STATUS: DONE** — shipped", SentinelDone, "shipped", true},
		{"bold wraps whole line", "**STATUS: DONE — shipped**", SentinelDone, "shipped", true},
		{"underscore wrap", "__STATUS: BLOCKED__ — waiting on creds", SentinelBlocked, "waiting on creds", true},
		{"bold bare", "**STATUS: DONE**", SentinelDone, "", true},
		{"indented numbered-list echo", "Summary above.\n   STATUS: QUESTION — keep both endpoints?", SentinelQuestion, "keep both endpoints?", true},
		{"CRLF line endings", "done\r\nSTATUS: DONE — ok\r\n", SentinelDone, "ok", true},
		{"last match wins", "I'll end with STATUS: DONE — once the tests pass.\n\nTests failed.\n\nSTATUS: BLOCKED — go test is red", SentinelBlocked, "go test is red", true},
		{"last match wins over earlier question", "STATUS: QUESTION — tabs?\n\nNever mind.\n\nSTATUS: DONE — used tabs", SentinelDone, "used tabs", true},
		{"question empty text takes last paragraph", "I need a decision.\n\nShould the filter persist\nin the URL?\n\nSTATUS: QUESTION", SentinelQuestion, "Should the filter persist in the URL?", true},
		{"question empty text no paragraph", "STATUS: QUESTION", SentinelQuestion, "", true},
		{"no separator with text is prose", "STATUS: DONE lines are mandatory.", "", "", false},
		{"kind must be whole word", "STATUS: DONEish — nope", "", "", false},
		{"no sentinel", "I think I finished but forgot the protocol.", "", "", false},
		{"empty", "", "", "", false},
		{"prompt echo only", "The kickoff asks me to end with one of:\n   STATUS: QUESTION — <the question, one line>\n   STATUS: BLOCKED — <what is blocking you>\n   STATUS: DONE — <one paragraph: what changed and how you verified it>", "", "", false},
		{"prompt echo then real line", "   STATUS: DONE — <one paragraph: what changed and how you verified it>\n\nSTATUS: DONE — shipped it", SentinelDone, "shipped it", true},
		{"real line then prompt echo", "STATUS: BLOCKED — creds\n\nReminder of the contract:\n   STATUS: DONE — <one paragraph>", SentinelBlocked, "creds", true},
		{"mid-line mention does not match", "I set STATUS: DONE — in the body text", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, text, ok := ParseSentinel(c.msg)
			if ok != c.wantOK || kind != c.wantKind || text != c.wantText {
				t.Errorf("ParseSentinel(%q) = (%q, %q, %v), want (%q, %q, %v)", c.msg, kind, text, ok, c.wantKind, c.wantText, c.wantOK)
			}
		})
	}
}

// The variants that used to classify as "no STATUS line" (stalled) now
// classify — the whole point of grove-441's tolerant parser.
func TestClassifyTolerantVariants(t *testing.T) {
	for _, msg := range []string{
		"STATUS: DONE",
		"**STATUS: DONE** — shipped",
		"STATUS: DONE: shipped",
	} {
		status, sentinel, _, _ := classify(msg)
		if sentinel != "done" {
			t.Errorf("classify(%q) sentinel = %q, want done (status %s)", msg, sentinel, status)
		}
	}
	if _, sentinel, _, _ := classify("STATUS: DONE — first\n\nSTATUS: QUESTION — actually?"); sentinel != "question" {
		t.Errorf("last STATUS line must win, got %q", sentinel)
	}
}
