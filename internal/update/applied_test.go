package update

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Applied is the gate the post-update brain sweep (grove-236) hangs off:
// it must be true ONLY when the binary was really replaced, so a routine
// `gv update --yes` on an up-to-date box stays silent.
func TestAppliedOnlyWhenTheBinaryWasReplaced(t *testing.T) {
	// A shell stub is enough: Run verifies by exec'ing `<target> version`.
	payload := []byte("#!/bin/sh\necho \"gv v0.9.9 (test)\"\n")

	cases := []struct {
		name    string
		latest  string
		confirm func(string, string) bool
		want    bool
	}{
		{"replaced", "v0.9.9", nil, true},
		{"already current", "v0.1.1", nil, false},
		{"declined at the prompt", "v0.9.9", func(_, _ string) bool { return false }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := releaseServer(t, c.latest, payload)
			target := filepath.Join(t.TempDir(), "gv")
			if err := os.WriteFile(target, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			applied := false
			err := Run(Options{
				APIBase: srv.URL, Repo: "test/repo", Current: "v0.1.1",
				GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
				Target: target, Out: &bytes.Buffer{},
				Confirm: c.confirm, Applied: &applied,
			})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if applied != c.want {
				t.Errorf("Applied = %v, want %v", applied, c.want)
			}
		})
	}
}
