package statusline

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fixture is the "Full JSON schema" example from
// https://code.claude.com/docs/en/statusline, copied verbatim.
func fixture(t testing.TB) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "docs-full-schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func TestCaptureWritesExpectedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage", "login.json")
	wrote, err := Capture(fixture(t), path, t0)
	if err != nil || !wrote {
		t.Fatalf("Capture = %v, %v; want a write", wrote, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"five_hour":{"used_percentage":23.5,"resets_at":1738425600},"seven_day":{"used_percentage":41.2,"resets_at":1738857600},"seen_at":"2026-10-11T12:00:00Z"}` + "\n"
	if string(raw) != want {
		t.Fatalf("usage file =\n%s\nwant\n%s", raw, want)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("usage file mode %#o, want 0600", fi.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".tmp-*")); len(left) > 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestCaptureAbsentRateLimitsWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "login.json")
	noLimits := strings.Replace(string(fixture(t)), `"rate_limits"`, `"not_rate_limits"`, 1)
	for name, payload := range map[string]string{
		"absent":        noLimits,
		"spend only":    `{"rate_limits":{"spend_limit":{"used_percentage":1,"resets_at":2}}}`,
		"not json":      `garbage`,
		"empty payload": ``,
	} {
		if wrote, _ := Capture([]byte(payload), path, t0); wrote {
			t.Errorf("%s: wrote a usage file", name)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("dir holds %d entries, want none", len(entries))
	}
}

func TestCaptureUnchangedDoesNotRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "login.json")
	if _, err := Capture(fixture(t), path, t0); err != nil {
		t.Fatal(err)
	}
	old := t0.Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	wrote, err := Capture(fixture(t), path, t0.Add(time.Minute))
	if err != nil || wrote {
		t.Fatalf("unchanged Capture = %v, %v; want no write", wrote, err)
	}
	if fi, _ := os.Stat(path); !fi.ModTime().Equal(old) {
		t.Fatalf("mtime moved to %v; unchanged values must not rewrite", fi.ModTime())
	}
	if u, _ := ReadUsage(path); !u.SeenAt.Equal(t0) {
		t.Errorf("seen_at = %v, want the first sighting %v", u.SeenAt, t0)
	}
	// A changed value rewrites; a dropped window (reset passed) is a change.
	changed := strings.Replace(string(fixture(t)), "23.5", "24", 1)
	if wrote, _ := Capture([]byte(changed), path, t0.Add(2*time.Minute)); !wrote {
		t.Fatal("changed used_percentage did not rewrite")
	}
	dropped := `{"rate_limits":{"seven_day":{"used_percentage":41.2,"resets_at":1738857600}}}`
	if wrote, _ := Capture([]byte(dropped), path, t0.Add(3*time.Minute)); !wrote {
		t.Fatal("dropped five_hour window did not rewrite")
	}
	if u, _ := ReadUsage(path); u.FiveHour != nil || u.SevenDay == nil {
		t.Errorf("after drop: %+v, want seven_day only", u)
	}
}

func TestRunNamesFileByGroveAccount(t *testing.T) {
	cases := map[string]string{"": "login.json", "login": "login.json", "alt": "alt.json"}
	for env, want := range cases {
		dir := t.TempDir()
		Run(bytes.NewReader(fixture(t)), &bytes.Buffer{}, &bytes.Buffer{}, "", dir, env, t0)
		if got := names(t, dir); got != want {
			t.Errorf("GROVE_ACCOUNT=%q: files %q, want %q", env, got, want)
		}
	}
	// An unsafe value writes nothing rather than escaping the dir.
	dir := t.TempDir()
	Run(bytes.NewReader(fixture(t)), &bytes.Buffer{}, &bytes.Buffer{}, "", filepath.Join(dir, "usage"), "../evil", t0)
	if got := names(t, dir); got != "" {
		t.Errorf("GROVE_ACCOUNT=../evil wrote %q", got)
	}
}

func names(t *testing.T, dir string) string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestRunPassesThroughOutputAndExitStatus(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "status.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'known line'\necho 'to stderr' >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Run(bytes.NewReader(fixture(t)), &out, &errOut, script, filepath.Join(dir, "usage"), "", t0)
	if code != 3 || out.String() != "known line\n" || errOut.String() != "to stderr\n" {
		t.Fatalf("Run = %d, stdout %q, stderr %q; want 3, %q, %q", code, out.String(), errOut.String(), "known line\n", "to stderr\n")
	}
	if _, err := os.Stat(filepath.Join(dir, "usage", "login.json")); err != nil {
		t.Errorf("capture skipped alongside a failing wrapped command: %v", err)
	}
}

func TestRunWrappedCommandGetsIdenticalStdin(t *testing.T) {
	dir := t.TempDir()
	got := filepath.Join(dir, "stdin.copy")
	in := fixture(t)
	code := Run(bytes.NewReader(in), &bytes.Buffer{}, &bytes.Buffer{}, "cat > "+got, filepath.Join(dir, "usage"), "", t0)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	copied, err := os.ReadFile(got)
	if err != nil || !bytes.Equal(copied, in) {
		t.Fatalf("wrapped stdin differs (%d bytes vs %d, err %v)", len(copied), len(in), err)
	}
}

func TestRunEmptyOrSelfWrapPrintsNothing(t *testing.T) {
	var out bytes.Buffer
	for _, wrap := range []string{"  ", "/tmp/gv-472 statusline"} {
		if code := Run(bytes.NewReader(fixture(t)), &out, &bytes.Buffer{}, wrap, t.TempDir(), "", t0); code != 0 || out.Len() != 0 {
			t.Fatalf("wrap %q: exit %d, stdout %q", wrap, code, out.String())
		}
	}
}

// TestRunOverhead: the shim must add < 20ms over the wrapped command on
// the fixture. Medians of interleaved runs, so machine noise hits both.
func TestRunOverhead(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t)
	wrap := "cat >/dev/null; echo ok"
	const n = 15
	var direct, shim []time.Duration
	for i := 0; i < n; i++ {
		start := time.Now()
		c := exec.Command("sh", "-c", wrap)
		c.Stdin = bytes.NewReader(in)
		if err := c.Run(); err != nil {
			t.Fatal(err)
		}
		direct = append(direct, time.Since(start))
		// Vary a value so every shim run pays for a real write.
		payload := strings.Replace(string(in), "23.5", strconv.Itoa(i), 1)
		start = time.Now()
		Run(strings.NewReader(payload), &bytes.Buffer{}, &bytes.Buffer{}, wrap, dir, "", t0)
		shim = append(shim, time.Since(start))
	}
	med := func(d []time.Duration) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[len(d)/2]
	}
	if over := med(shim) - med(direct); over > 20*time.Millisecond {
		t.Fatalf("shim overhead %v (shim %v, direct %v), want < 20ms", over, med(shim), med(direct))
	} else {
		t.Logf("shim overhead %v (shim median %v, direct median %v)", over, med(shim), med(direct))
	}
}

func BenchmarkCapture(b *testing.B) {
	path := filepath.Join(b.TempDir(), "login.json")
	in := fixture(b)
	for i := 0; i < b.N; i++ {
		_, _ = Capture(in, path, t0)
	}
}
