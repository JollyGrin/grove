package update

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMain doubles as the self-update helper: the integration test
// copies this test binary to a temp dir and re-execs it with
// GV_UPDATE_TEST_MODE=self, which runs the real Run() flow against the
// httptest server — a stamped binary updating itself in place.
func TestMain(m *testing.M) {
	if os.Getenv("GV_UPDATE_TEST_MODE") == "self" {
		err := Run(Options{
			APIBase: os.Getenv("GV_UPDATE_TEST_API"),
			Repo:    "test/repo",
			Current: os.Getenv("GV_UPDATE_TEST_VERSION"),
			GOOS:    runtime.GOOS,
			GOARCH:  runtime.GOARCH,
			Out:     os.Stdout,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name            string
		current, latest string
		force           bool
		want            Decision
		wantErr         error
	}{
		{"up to date", "v0.1.4", "v0.1.4", false, UpToDate, nil},
		{"newer available", "v0.1.1", "v0.1.4", false, Update, nil},
		{"minor bump", "v0.1.9", "v0.2.0", false, Update, nil},
		{"local ahead of latest", "v0.2.0", "v0.1.4", false, UpToDate, nil},
		{"no v prefix on current", "0.1.1", "v0.1.4", false, Update, nil},
		{"dev refused", "dev", "v0.1.4", false, UpToDate, ErrDevBuild},
		{"dev forced", "dev", "v0.1.4", true, Update, nil},
		{"unparseable but equal", "nightly-1", "nightly-1", false, UpToDate, nil},
		{"unparseable and different", "nightly-1", "nightly-2", false, Update, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Decide(c.current, c.latest, c.force)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("Decide(%q,%q,%v) err = %v, want %v", c.current, c.latest, c.force, err, c.wantErr)
			}
			if err == nil && got != c.want {
				t.Fatalf("Decide(%q,%q,%v) = %v, want %v", c.current, c.latest, c.force, got, c.want)
			}
		})
	}
}

func TestAssetName(t *testing.T) {
	if got := AssetName("darwin", "arm64"); got != "gv_darwin_arm64" {
		t.Fatalf("AssetName = %q", got)
	}
}

func TestFindAsset(t *testing.T) {
	rel := &Release{TagName: "v1.0.0", Assets: []Asset{{Name: "gv_linux_amd64", BrowserDownloadURL: "http://x/dl"}}}
	if url, err := FindAsset(rel, "gv_linux_amd64"); err != nil || url != "http://x/dl" {
		t.Fatalf("FindAsset = %q, %v", url, err)
	}
	if _, err := FindAsset(rel, "gv_plan9_386"); err == nil {
		t.Fatal("FindAsset should fail for a missing asset")
	}
}

func TestFetchLatestErrors(t *testing.T) {
	for status, wantSub := range map[int]string{
		http.StatusForbidden: "rate limit",
		http.StatusNotFound:  "no releases",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		_, err := FetchLatest(srv.Client(), srv.URL, "test/repo")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), wantSub) {
			t.Fatalf("status %d: err = %v, want substring %q", status, err, wantSub)
		}
	}
}

// releaseServer serves a fake latest-release JSON whose single asset
// matches this platform; assetBody == nil makes the download 404.
func releaseServer(t *testing.T, tag string, assetBody []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/test/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":%q}]}`,
			tag, AssetName(runtime.GOOS, runtime.GOARCH), srv.URL+"/dl/gv")
	})
	mux.HandleFunc("/dl/gv", func(w http.ResponseWriter, r *http.Request) {
		if assetBody == nil {
			http.NotFound(w, r)
			return
		}
		w.Write(assetBody)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestSelfUpdate is the integration path: a stamped test binary (this
// test binary re-exec'd in helper mode) replaces itself with the served
// asset, and the replaced file reports the new version.
func TestSelfUpdate(t *testing.T) {
	newBinary := []byte("#!/bin/sh\necho \"gv v0.9.9 (test)\"\n")
	srv := releaseServer(t, "v0.9.9", newBinary)

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "gv")
	if err := os.WriteFile(target, src, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(target)
	cmd.Env = append(os.Environ(),
		"GV_UPDATE_TEST_MODE=self",
		"GV_UPDATE_TEST_API="+srv.URL,
		"GV_UPDATE_TEST_VERSION=v0.1.1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("self-update run failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "gv v0.1.1 → v0.9.9") {
		t.Errorf("missing old → new line in output:\n%s", out)
	}

	got, err := exec.Command(target, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("replaced binary failed to run: %v\n%s", err, got)
	}
	if !strings.Contains(string(got), "v0.9.9") {
		t.Fatalf("replaced binary reports %q, want v0.9.9", got)
	}
}

// TestApply404LeavesBinaryUntouched is the failure injection: the asset
// download 404s and the original file stays byte-identical with no temp
// litter left beside it.
func TestApply404LeavesBinaryUntouched(t *testing.T) {
	srv := releaseServer(t, "v0.9.9", nil)

	dir := t.TempDir()
	target := filepath.Join(dir, "gv")
	original := []byte("#!/bin/sh\necho \"gv v0.1.1\"\n")
	if err := os.WriteFile(target, original, 0o755); err != nil {
		t.Fatal(err)
	}

	err := Run(Options{
		APIBase: srv.URL, Repo: "test/repo", Current: "v0.1.1",
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Target: target, Out: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "untouched") {
		t.Fatalf("err = %v, want download failure mentioning the binary is untouched", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("original binary was modified after a failed download")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temp file litter left in target dir: %v", entries)
	}
}

func TestRunUpToDateTouchesNothing(t *testing.T) {
	srv := releaseServer(t, "v0.1.1", []byte("should never be downloaded"))

	target := filepath.Join(t.TempDir(), "gv")
	original := []byte("original")
	if err := os.WriteFile(target, original, 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := Run(Options{
		APIBase: srv.URL, Repo: "test/repo", Current: "v0.1.1",
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Target: target, Out: &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already the latest") {
		t.Errorf("output = %q, want an already-latest message", out.String())
	}
	if got, _ := os.ReadFile(target); !bytes.Equal(got, original) {
		t.Fatal("up-to-date run modified the binary")
	}
}

func TestRunDeclinedConfirmTouchesNothing(t *testing.T) {
	srv := releaseServer(t, "v0.9.9", []byte("should never be downloaded"))

	target := filepath.Join(t.TempDir(), "gv")
	original := []byte("original")
	if err := os.WriteFile(target, original, 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := Run(Options{
		APIBase: srv.URL, Repo: "test/repo", Current: "v0.1.1",
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Target: target, Out: &out,
		Confirm: func(_, _ string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "aborted") {
		t.Errorf("output = %q, want an aborted message", out.String())
	}
	if got, _ := os.ReadFile(target); !bytes.Equal(got, original) {
		t.Fatal("declined confirm still modified the binary")
	}
}

func TestRunDevRefusal(t *testing.T) {
	srv := releaseServer(t, "v0.9.9", []byte("payload"))
	err := Run(Options{
		APIBase: srv.URL, Repo: "test/repo", Current: "dev",
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Target: filepath.Join(t.TempDir(), "gv"), Out: &bytes.Buffer{},
	})
	if !errors.Is(err, ErrDevBuild) {
		t.Fatalf("err = %v, want ErrDevBuild", err)
	}
}
