package feature

import (
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/git"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/worktree"
)

func trainFeatures() map[string]*state.Feature {
	return map[string]*state.Feature{
		"keys":   {Slug: "keys", Repo: "grove", Branch: "feature/keys", Base: "main", Label: "keys"},
		"trains": {Slug: "trains", Repo: "grove", Branch: "feature/trains", Base: "main", Label: "feature-trains"},
		"web":    {Slug: "web", Repo: "site", Branch: "feature/web", Base: "main", Label: "web"},
		"old":    {Slug: "old", Repo: "grove", Branch: "feature/old", Base: "main", Label: "old", Closed: true},
	}
}

func TestChooseForGrab(t *testing.T) {
	cases := []struct {
		name     string
		flag     string
		labels   []string
		wantSlug string // "" = off-train
		wantBase string
		wantWhy  string
		wantErr  []string // substrings; nil = no error
	}{
		{name: "no matching label", labels: []string{"bug", "ui"}, wantBase: "main", wantWhy: "repo base"},
		{name: "no labels", wantBase: "main", wantWhy: "repo base"},
		{name: "one match", labels: []string{"bug", "keys"}, wantSlug: "keys", wantBase: "feature/keys", wantWhy: "feature keys, from label keys"},
		{name: "label differs from slug", labels: []string{"feature-trains"}, wantSlug: "trains", wantBase: "feature/trains", wantWhy: "feature trains, from label feature-trains"},
		{name: "closed feature's label ignored", labels: []string{"old"}, wantBase: "main", wantWhy: "repo base"},
		{name: "none opts out of a matching label", flag: "none", labels: []string{"keys"}, wantBase: "main", wantWhy: "--feature none"},
		{name: "two matches refuse, naming both", labels: []string{"keys", "feature-trains"}, wantErr: []string{"keys (label keys)", "trains (label feature-trains)", "--feature"}},
		{name: "explicit wins over label", flag: "trains", labels: []string{"keys"}, wantSlug: "trains", wantBase: "feature/trains", wantWhy: "feature trains, from --feature"},
		{name: "explicit wins over two matches", flag: "keys", labels: []string{"keys", "feature-trains"}, wantSlug: "keys", wantBase: "feature/keys", wantWhy: "feature keys, from --feature"},
		{name: "unknown slug", flag: "nope", wantErr: []string{`"nope"`, "gv feature ls"}},
		{name: "closed slug", flag: "old", wantErr: []string{`"old"`, "gv feature ls"}},
		{name: "explicit on another repo", flag: "web", wantErr: []string{"feature web is on repo site, not grove"}},
		{name: "inferred on another repo", labels: []string{"web"}, wantErr: []string{"feature web on repo site, not grove", "--feature none"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ChooseForGrab(trainFeatures(), tc.flag, "grove", "main", tc.labels)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("got %+v, want an error", c)
				}
				for _, sub := range tc.wantErr {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("error %q lacks %q", err, sub)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			slug := ""
			if c.Feature != nil {
				slug = c.Feature.Slug
			}
			if slug != tc.wantSlug || c.Base != tc.wantBase || c.Why != tc.wantWhy {
				t.Errorf("got feature %q base %q why %q, want %q %q %q", slug, c.Base, c.Why, tc.wantSlug, tc.wantBase, tc.wantWhy)
			}
		})
	}
	c, _ := ChooseForGrab(trainFeatures(), "", "grove", "main", []string{"keys"})
	if got, want := c.Line(), "base: feature/keys (feature keys, from label keys)"; got != want {
		t.Errorf("Line() = %q, want %q", got, want)
	}
}

// TestForkRefFeatureWorktree: a feature grab's worktree starts at
// origin/<feature branch>, not origin/main (local bare origin, no network).
func TestForkRefFeatureWorktree(t *testing.T) {
	root, origin, _ := fixture(t)
	mainSHA := gitIn(t, origin, "rev-parse", "main")
	// The feature branch is one commit ahead of main on origin only.
	gitIn(t, root, "checkout", "-q", "-b", "feature/keys")
	gitIn(t, root, "commit", "-q", "--allow-empty", "-m", "train car")
	gitIn(t, root, "push", "-q", "origin", "feature/keys")
	gitIn(t, root, "checkout", "-q", "main")
	gitIn(t, root, "branch", "-q", "-D", "feature/keys")
	gitIn(t, root, "update-ref", "-d", "refs/remotes/origin/feature/keys")
	featSHA := gitIn(t, origin, "rev-parse", "feature/keys")
	if featSHA == mainSHA {
		t.Fatal("fixture: feature branch should differ from main")
	}

	c, err := ChooseForGrab(trainFeatures(), "keys", "grove", "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Before the fetch the ref is unknown locally: refuse rather than fall
	// back to anything else.
	if _, err := ForkRef(root, c); err == nil {
		t.Fatal("ForkRef without a fetched origin/feature/keys should refuse")
	}
	if err := git.Fetch(root, "origin", c.Base); err != nil {
		t.Fatal(err)
	}
	ref, err := ForkRef(root, c)
	if err != nil {
		t.Fatal(err)
	}
	if ref != "origin/feature/keys" {
		t.Fatalf("ForkRef = %q, want origin/feature/keys", ref)
	}
	wt, err := worktree.Add(root, "grove-9-car", ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := gitIn(t, wt.Path, "rev-parse", "HEAD"); got != featSHA {
		t.Errorf("worktree HEAD = %s, want origin/feature/keys %s (main is %s)", got, featSHA, mainSHA)
	}

	// Off-train: the repo base, as before.
	off, _ := ChooseForGrab(trainFeatures(), "none", "grove", "main", []string{"keys"})
	if ref, err := ForkRef(root, off); err != nil || ref != "origin/main" {
		t.Errorf("off-train ForkRef = %q, %v; want origin/main", ref, err)
	}
}
