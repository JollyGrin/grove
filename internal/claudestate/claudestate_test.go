package claudestate

import (
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseFixtures(t *testing.T) {
	st, pane, err := Parse(fixture(t, "session-2.1.304-waiting.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "waiting" || st.WaitingFor != "permission prompt" || pane != "%0" ||
		st.SessionID != "00000000-0000-4000-8000-000000000001" || st.UpdatedAt.UnixMilli() != 1792489480137 {
		t.Errorf("waiting fixture = %+v pane %q", st, pane)
	}
	st, pane, err = Parse(fixture(t, "session-2.1.304.json"))
	if err != nil || st.Status != "idle" || st.WaitingFor != "" || pane != "%11" {
		t.Errorf("idle fixture = %+v pane %q err %v", st, pane, err)
	}
}

func TestParseRejectsTornAndStatusless(t *testing.T) {
	for _, in := range []string{`{"status":"bus`, ``, `{"pid":1,"sessionId":"x"}`} {
		if _, _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) = nil error, want error", in)
		}
	}
}

func TestParseNoTmuxField(t *testing.T) {
	_, pane, err := Parse([]byte(`{"status":"idle"}`))
	if err != nil || pane != "" {
		t.Errorf("pane %q err %v, want empty pane, no error", pane, err)
	}
}

func TestReadFiltersAndSkips(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("1.json", fixture(t, "session-2.1.304.json"))              // %11
	write("2.json", fixture(t, "session-2.1.304-waiting.json"))      // %0, not wanted
	write("3.json", []byte(`{"status":"busy","tmux":"s:@1.%5"`))     // torn
	write("1.abc.key", []byte(`{"status":"busy","tmux":"s:@1.%7"}`)) // .key ignored
	got := Read([]string{dir, filepath.Join(dir, "missing")}, map[string]bool{"%11": true, "%5": true, "%7": true})
	if len(got) != 1 || got["%11"].Status != "idle" {
		t.Errorf("Read = %+v, want only %%11 idle", got)
	}
}

func TestReadMissingDirIsEmpty(t *testing.T) {
	got := Read([]string{filepath.Join(t.TempDir(), "nope")}, map[string]bool{"%1": true})
	if got == nil || len(got) != 0 {
		t.Errorf("Read = %v, want empty non-nil map", got)
	}
}
