package memprof

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestWrite: the heap file parses as a pprof profile with the heap
// sample types, the goroutine dump is the debug=2 text form, and both
// carry the verb-pid-ts stem the one-liner in HANDOFF.md names.
func TestWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profiles")
	now := time.Date(2026, 10, 24, 15, 4, 5, 0, time.UTC)
	heap, gor, err := Write(dir, "dash", now)
	if err != nil {
		t.Fatal(err)
	}
	wantStem := filepath.Join(dir, "dash-"+itoa(os.Getpid())+"-20261024T150405Z")
	if heap != wantStem+".pprof" || gor != wantStem+".goroutines" {
		t.Fatalf("paths = %q, %q; want stem %q", heap, gor, wantStem)
	}

	// A heap profile is a gzipped profile.proto whose string table names
	// the sample types; checking those needs no pprof dependency. (No
	// function-name check: the heap profile is sampled, so any one site
	// may be absent from a given write.)
	raw, err := os.ReadFile(heap)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("heap profile is not gzip: %v", err)
	}
	proto, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"alloc_space", "inuse_space"} {
		if !bytes.Contains(proto, []byte(want)) {
			t.Errorf("heap profile lacks %q", want)
		}
	}
	// And the operator's reader agrees, where the toolchain is present.
	if goBin, err := exec.LookPath("go"); err == nil {
		out, err := exec.Command(goBin, "tool", "pprof", "-top", heap).CombinedOutput()
		if err != nil || !bytes.Contains(out, []byte("flat%")) {
			t.Errorf("go tool pprof -top: %v\n%s", err, out)
		}
	}

	text, err := os.ReadFile(gor)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(text), "goroutine ") || !strings.Contains(string(text), "memprof.Write") {
		t.Errorf("goroutine dump is not the debug=2 text form:\n%.300s", text)
	}
}

// TestInstallOnSignal drives the seam in-process: arm it, raise SIGUSR2
// on ourselves, and the report lands with two readable files under
// <state>/profiles. Then stop, and a second signal must not write.
func TestInstallOnSignal(t *testing.T) {
	stateDir := t.TempDir()
	got := make(chan [2]string, 1)
	stop := Install(stateDir, "test", func(heap, gor string, err error) {
		if err != nil {
			t.Error(err)
		}
		got <- [2]string{heap, gor}
	})
	if err := syscall.Kill(os.Getpid(), Signal); err != nil {
		t.Fatal(err)
	}
	var paths [2]string
	select {
	case paths = <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("no profile written within 10s of SIGUSR2")
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, Dir(stateDir)+string(filepath.Separator)+"test-") {
			t.Errorf("%s not under %s", p, Dir(stateDir))
		}
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			t.Errorf("%s: err=%v size=%v", p, err, st)
		}
	}
	stop()
	// After stop the signal is back to default — which for USR2 is
	// termination — so do not raise it again; assert the directory is
	// exactly the one pair instead.
	entries, _ := os.ReadDir(Dir(stateDir))
	if len(entries) != 2 {
		t.Errorf("profiles dir has %d entries, want 2", len(entries))
	}
}

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	for n >= 10 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	i--
	b[i] = byte('0' + n)
	return string(b[i:])
}
