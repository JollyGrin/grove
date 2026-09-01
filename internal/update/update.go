// Package update implements gv update: replace the running binary with
// the latest GitHub release. Decision logic (version compare, dev
// refusal, asset naming) is pure and table-tested; the network and file
// mechanics take an injectable API base so tests run against httptest.
// cmd/gv stays thin glue (flags + the y/N prompt).
package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultAPIBase is the GitHub REST API root; tests point this at
	// an httptest server.
	DefaultAPIBase = "https://api.github.com"
	// DefaultRepo is the release source.
	DefaultRepo = "JollyGrin/grove"
)

// ErrDevBuild refuses to silently overwrite a source build — but the way
// out is `--force`, not `go install`: a source build is what MAKES this
// refusal fire, so re-installing from source only guarantees it fires
// again (grove-233). --force overrides.
var ErrDevBuild = errors.New("built from source — `gv update --yes --force` replaces it with the latest release (a plain `go install` re-stamps it dev and this refusal returns)")

// Release is the subset of the GitHub release JSON gv update reads.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Decision is the outcome of comparing the built-in version to the
// latest release tag.
type Decision int

const (
	UpToDate Decision = iota
	Update
)

// Decide compares the stamped version against the latest release tag.
// "dev" (a plain `go build`) is refused with ErrDevBuild unless force;
// a current version at or above the latest tag is UpToDate.
func Decide(current, latest string, force bool) (Decision, error) {
	if strings.TrimSpace(latest) == "" {
		return UpToDate, errors.New("latest release has no tag")
	}
	if current == "dev" {
		if force {
			return Update, nil
		}
		return UpToDate, ErrDevBuild
	}
	if cmp, ok := compare(current, latest); ok {
		if cmp >= 0 {
			return UpToDate, nil
		}
		return Update, nil
	}
	// Unparseable tag on either side: fall back to string identity so a
	// weird-but-matching tag still reports up to date.
	if normalize(current) == normalize(latest) {
		return UpToDate, nil
	}
	return Update, nil
}

// AssetName is the release artifact naming scheme from
// .github/workflows/release.yml.
func AssetName(goos, goarch string) string {
	return fmt.Sprintf("gv_%s_%s", goos, goarch)
}

// FindAsset returns the download URL for the named asset.
func FindAsset(rel *Release, name string) (string, error) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("release %s has no asset %q — this platform may not be built", rel.TagName, name)
}

// FetchLatest reads repos/<repo>/releases/latest unauthenticated.
func FetchLatest(client *http.Client, apiBase, repo string) (*Release, error) {
	url := strings.TrimRight(apiBase, "/") + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach GitHub (%v) — check your network and try again", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("GitHub API rate limit hit — try again in a few minutes")
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("no releases found for %s", repo)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("could not parse release JSON: %w", err)
	}
	return &rel, nil
}

// Target resolves the running binary through symlinks so the rename
// replaces the real file, not a symlink to it.
func Target() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// Apply downloads url over target: temp file in the SAME directory as
// target (keeps the rename atomic — one filesystem), chmod 0755, rename
// over the target. Rename-over-running-binary is safe on darwin/linux.
// Any failure leaves the existing binary untouched.
func Apply(client *http.Client, url, target string) (err error) {
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download failed (%v) — the existing binary is untouched", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed (%s) — the existing binary is untouched", resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".gv-update-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err = io.Copy(tmp, resp.Body); err != nil {
		return fmt.Errorf("download failed (%v) — the existing binary is untouched", err)
	}
	if err = tmp.Chmod(0o755); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

// Options parameterizes Run; zero fields fall back to the real world
// (GitHub API, the running binary, os.Stdout).
type Options struct {
	APIBase string // "" → DefaultAPIBase
	Repo    string // "" → DefaultRepo
	Current string // the stamped build version
	GOOS    string
	GOARCH  string
	Force   bool                              // allow replacing a dev build
	Confirm func(current, latest string) bool // nil → no prompt (--yes)
	Target  string                            // "" → resolve the running binary
	Out     io.Writer                         // "" → os.Stdout
	Client  *http.Client                      // nil → sane per-call timeouts
	// Applied, when non-nil, is set to true exactly when the binary was
	// actually replaced — not on an up-to-date no-op and not on an
	// aborted confirm. grove-236 hangs the post-update brain sweep off
	// it so a routine `gv update --yes` on a current box stays quiet.
	Applied *bool
}

// Run is the whole gv update flow: fetch latest → decide → confirm →
// atomic replace → verify by exec'ing `<target> version`.
func Run(o Options) error {
	if o.APIBase == "" {
		o.APIBase = DefaultAPIBase
	}
	if o.Repo == "" {
		o.Repo = DefaultRepo
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	apiClient, dlClient := o.Client, o.Client
	if o.Client == nil {
		apiClient = &http.Client{Timeout: 15 * time.Second}
		dlClient = &http.Client{Timeout: 5 * time.Minute}
	}

	rel, err := FetchLatest(apiClient, o.APIBase, o.Repo)
	if err != nil {
		return err
	}
	dec, err := Decide(o.Current, rel.TagName, o.Force)
	if err != nil {
		return err
	}
	if dec == UpToDate {
		fmt.Fprintf(o.Out, "gv %s is already the latest release (%s)\n", o.Current, rel.TagName)
		return nil
	}
	url, err := FindAsset(rel, AssetName(o.GOOS, o.GOARCH))
	if err != nil {
		return err
	}
	target := o.Target
	if target == "" {
		if target, err = Target(); err != nil {
			return err
		}
	}

	fmt.Fprintf(o.Out, "gv %s → %s\n", o.Current, rel.TagName)
	if o.Confirm != nil && !o.Confirm(o.Current, rel.TagName) {
		fmt.Fprintln(o.Out, "aborted — nothing changed")
		return nil
	}
	if err := Apply(dlClient, url, target); err != nil {
		return err
	}
	out, err := exec.Command(target, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("replaced %s but `version` failed: %v\n%s", target, err, out)
	}
	fmt.Fprintf(o.Out, "updated %s\n%s", target, out)
	if o.Applied != nil {
		*o.Applied = true
	}
	return nil
}

func normalize(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

// compare returns -1/0/1 for a<b / a==b / a>b; ok=false when either
// side is not a plain [v]X.Y.Z.
func compare(a, b string) (int, bool) {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka || !okb {
		return 0, false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(normalize(v), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
