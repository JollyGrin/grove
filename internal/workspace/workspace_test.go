package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkWorkspace creates dir/.grove/config.yaml — workspace init always
// writes a config, and since grove-100 a bare `.grove/` is not a marker.
func mkWorkspace(t *testing.T, root, config string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".grove"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".grove", "config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
}

func realTemp(t *testing.T) string {
	t.Helper()
	// macOS TempDir lives behind a symlink (/var → /private/var); resolve
	// so Root comparisons are stable.
	r, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestFind(t *testing.T) {
	tmp := realTemp(t)

	outer := filepath.Join(tmp, "outer")
	mkWorkspace(t, outer, "workspace:\n  label: grid\n  scope: parent\n")
	inner := filepath.Join(outer, "inner")
	mkWorkspace(t, inner, "")
	deep := filepath.Join(inner, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(outer, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(tmp, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	// A plain FILE named .grove is not a marker.
	fake := filepath.Join(tmp, "fake")
	if err := os.MkdirAll(fake, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fake, ".grove"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Corrupt config falls back to basename/repo.
	corrupt := filepath.Join(tmp, "corrupt")
	mkWorkspace(t, corrupt, ":: not yaml {{{\n")
	// Config with bogus scope keeps the repo default.
	bogus := filepath.Join(tmp, "bogus")
	mkWorkspace(t, bogus, "workspace:\n  label: bog\n  scope: galaxy\n")
	// grove-100: a `.grove/` holding only the markdown backend's tasks/
	// storage is not a workspace (every gv init-scaffolded repo has one)…
	tasksOnly := filepath.Join(tmp, "tasksonly")
	if err := os.MkdirAll(filepath.Join(tasksOnly, ".grove", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	// …and inside a real workspace the walk-up passes through it.
	legacy := filepath.Join(outer, "legacy")
	if err := os.MkdirAll(filepath.Join(legacy, ".grove", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A bare empty `.grove/` has no workspace substance either.
	empty := filepath.Join(tmp, "empty")
	if err := os.MkdirAll(filepath.Join(empty, ".grove"), 0o755); err != nil {
		t.Fatal(err)
	}
	// state/ or orchestrator/ alone are substance — config.yaml optional.
	stateOnly := filepath.Join(tmp, "stateonly")
	if err := os.MkdirAll(filepath.Join(stateOnly, ".grove", "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	orchOnly := filepath.Join(tmp, "orchonly")
	if err := os.MkdirAll(filepath.Join(orchOnly, ".grove", "orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		cwd  string
		want *Workspace
	}{
		{"root itself", outer, &Workspace{outer, "grid", ScopeParent}},
		{"child dir", sub, &Workspace{outer, "grid", ScopeParent}},
		{"nested: inner wins", deep, &Workspace{inner, "inner", ScopeRepo}},
		{"no marker anywhere", plain, nil},
		{"file marker ignored", fake, nil},
		{"corrupt config falls back", corrupt, &Workspace{corrupt, "corrupt", ScopeRepo}},
		{"bogus scope defaults to repo", bogus, &Workspace{bogus, "bog", ScopeRepo}},
		{"tasks-only is not a workspace", tasksOnly, nil},
		{"tasks-only walks up to real workspace", legacy, &Workspace{outer, "grid", ScopeParent}},
		{"bare empty .grove is not a workspace", empty, nil},
		{"state-only is a workspace", stateOnly, &Workspace{stateOnly, "stateonly", ScopeRepo}},
		{"orchestrator-only is a workspace", orchOnly, &Workspace{orchOnly, "orchonly", ScopeRepo}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Find(tc.cwd)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("Find(%s) = %+v, want nil", tc.cwd, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("Find(%s) = nil, want %+v", tc.cwd, tc.want)
			}
			if *got != *tc.want {
				t.Fatalf("Find(%s) = %+v, want %+v", tc.cwd, got, tc.want)
			}
		})
	}
}

func TestFindThroughSymlink(t *testing.T) {
	tmp := realTemp(t)
	root := filepath.Join(tmp, "real")
	mkWorkspace(t, root, "")
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	got := Find(link)
	if got == nil || got.Root != root {
		t.Fatalf("Find through symlink = %+v, want root %s", got, root)
	}
}

func TestValidateLabel(t *testing.T) {
	cases := []struct {
		label string
		ok    bool
	}{
		{"grid", true},
		{"the-grid", true},
		{"a1_b-2", true},
		{"9lives", true},
		{"grove-x", true}, // reserved words only match exactly
		{"", false},
		{"-lead", false},
		{"_lead", false},
		{"Grid", false},
		{"has space", false},
		{"dots.bad", false},
		{"grove", false},
		{"mobile", false},
		{"dash", false},
	}
	for _, tc := range cases {
		err := ValidateLabel(tc.label)
		if tc.ok && err != nil {
			t.Errorf("ValidateLabel(%q) = %v, want nil", tc.label, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("ValidateLabel(%q) = nil, want error", tc.label)
		}
	}
}

func TestRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Missing file is an empty registry, not an error.
	list, err := LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry missing file: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("LoadRegistry missing file = %v, want empty", list)
	}

	alpha := Workspace{Root: "/w/alpha", Label: "alpha", Scope: ScopeRepo}
	if err := AddToRegistry(alpha); err != nil {
		t.Fatalf("add alpha: %v", err)
	}
	if err := AddToRegistry(Workspace{Root: "/w/beta", Label: "beta", Scope: ScopeParent}); err != nil {
		t.Fatalf("add beta: %v", err)
	}

	// Idempotent re-add of the same root+label (scope refreshes in place).
	alpha.Scope = ScopeParent
	if err := AddToRegistry(alpha); err != nil {
		t.Fatalf("idempotent re-add: %v", err)
	}
	list, err = LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("registry = %v, want 2 entries", list)
	}
	if list[0] != alpha {
		t.Fatalf("re-add did not update in place: %+v", list[0])
	}

	// Duplicate label on a different root is a conflict.
	if err := AddToRegistry(Workspace{Root: "/elsewhere", Label: "alpha", Scope: ScopeRepo}); err == nil {
		t.Fatal("dup label with different root: want error, got nil")
	}

	// Invalid and reserved labels are rejected.
	if err := AddToRegistry(Workspace{Root: "/x", Label: "Bad Label", Scope: ScopeRepo}); err == nil {
		t.Fatal("invalid label: want error")
	}
	if err := AddToRegistry(Workspace{Root: "/x", Label: "grove", Scope: ScopeRepo}); err == nil {
		t.Fatal("reserved label: want error")
	}

	// Remove.
	if err := RemoveFromRegistry("alpha"); err != nil {
		t.Fatalf("remove alpha: %v", err)
	}
	list, _ = LoadRegistry()
	if len(list) != 1 || list[0].Label != "beta" {
		t.Fatalf("after remove: %v, want just beta", list)
	}
	if err := RemoveFromRegistry("ghost"); err == nil {
		t.Fatal("remove unknown label: want error")
	}
}

func TestAlive(t *testing.T) {
	tmp := realTemp(t)
	root := filepath.Join(tmp, "live")
	mkWorkspace(t, root, "")
	if !Alive(Workspace{Root: root}) {
		t.Fatal("Alive(existing marker) = false")
	}
	if Alive(Workspace{Root: filepath.Join(tmp, "gone")}) {
		t.Fatal("Alive(missing root) = true")
	}
	// Root exists but marker was deleted.
	dead := filepath.Join(tmp, "dead")
	mkWorkspace(t, dead, "")
	if err := os.RemoveAll(filepath.Join(dead, ".grove")); err != nil {
		t.Fatal(err)
	}
	if Alive(Workspace{Root: dead}) {
		t.Fatal("Alive(marker removed) = true")
	}
	// grove-100: a root whose `.grove/` holds only tasks/ is not alive…
	tasksOnly := filepath.Join(tmp, "tasksonly")
	if err := os.MkdirAll(filepath.Join(tasksOnly, ".grove", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if Alive(Workspace{Root: tasksOnly}) {
		t.Fatal("Alive(tasks-only marker) = true")
	}
	// …but a still-configured workspace without config.yaml (state/ only)
	// must not be reported dead.
	stateOnly := filepath.Join(tmp, "stateonly")
	if err := os.MkdirAll(filepath.Join(stateOnly, ".grove", "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !Alive(Workspace{Root: stateOnly}) {
		t.Fatal("Alive(state-only marker) = false")
	}
}

const rollupFixture = `[
  {"ticket":"T1","agent":"working","done":false},
  {"ticket":"T2","agent":"setup","done":false},
  {"ticket":"T3","agent":"waiting","sentinel":"question","done":false},
  {"ticket":"T4","agent":"blocked","done":false},
  {"ticket":"T5","agent":"idle","sentinel":"done","done":false},
  {"ticket":"T6","agent":"working","human":"reviewing","done":false},
  {"ticket":"T7","agent":"working","done":true},
  {"ticket":"T8","agent":"dead","done":false}
]`

func writeRollupFixture(t *testing.T, root, content string) Workspace {
	t.Helper()
	stateDir := filepath.Join(root, ".grove", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "tasks.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return Workspace{Root: root, Label: filepath.Base(root), Scope: ScopeRepo}
}

func TestReadRollup(t *testing.T) {
	tmp := realTemp(t)

	ws := writeRollupFixture(t, filepath.Join(tmp, "fleet"), rollupFixture)
	got := ReadRollup(ws)
	// T1+T2 working; T3+T4 waiting; T5 (sentinel done) + T6 (reviewing)
	// review; T7 done and T8 dead uncounted.
	want := Rollup{Working: 2, Waiting: 2, Review: 2}
	if got != want {
		t.Fatalf("ReadRollup = %+v, want %+v", got, want)
	}

	// Map form (ticket → task) parses too.
	wsMap := writeRollupFixture(t, filepath.Join(tmp, "mapfleet"),
		`{"T1":{"agent":"working","done":false},"T2":{"agent":"waiting","done":false}}`)
	if got := ReadRollup(wsMap); got != (Rollup{Working: 1, Waiting: 1}) {
		t.Fatalf("ReadRollup(map form) = %+v", got)
	}

	// Corrupt file → zero rollup, no panic.
	wsBad := writeRollupFixture(t, filepath.Join(tmp, "bad"), "{{{ not json")
	if got := ReadRollup(wsBad); got != (Rollup{}) {
		t.Fatalf("ReadRollup(corrupt) = %+v, want zero", got)
	}

	// Missing state entirely → zero rollup AND nothing gets created
	// (raw mkdir, not mkWorkspace: the emptiness assertion below needs a
	// `.grove/` with no config.yaml, and ReadRollup never checks markers).
	bare := filepath.Join(tmp, "bare")
	if err := os.MkdirAll(filepath.Join(bare, ".grove"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ReadRollup(Workspace{Root: bare}); got != (Rollup{}) {
		t.Fatalf("ReadRollup(missing) = %+v, want zero", got)
	}
	if _, err := os.Stat(filepath.Join(bare, ".grove", "state")); !os.IsNotExist(err) {
		t.Fatal("ReadRollup created the state dir — it must be read-only")
	}
	entries, err := os.ReadDir(filepath.Join(bare, ".grove"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("ReadRollup wrote into .grove/: %s", strings.Join(names, ", "))
	}
}

func TestSortByActionability(t *testing.T) {
	list := []Workspace{
		{Root: "/e", Label: "echo"},
		{Root: "/a", Label: "alpha"},
		{Root: "/b", Label: "beta"},
		{Root: "/c", Label: "casa"},
		{Root: "/d", Label: "delta"},
	}
	rollups := map[string]Rollup{
		"alpha": {Working: 3},             // busy but nothing actionable
		"beta":  {Waiting: 2},             // most actionable
		"casa":  {Review: 1, Working: 1},  // review beats working
		"delta": {Waiting: 2, Working: 1}, // ties beta on waiting → label asc would put beta first, but delta has more working
		"echo":  {},                       // idle sinks; no rollup entry also works
	}
	got := SortByActionability(list, rollups)
	want := []string{"delta", "beta", "casa", "alpha", "echo"}
	for i, w := range want {
		if got[i].Label != w {
			labels := make([]string, len(got))
			for j := range got {
				labels[j] = got[j].Label
			}
			t.Fatalf("order = %v, want %v", labels, want)
		}
	}
	// Pure: input order untouched.
	if list[0].Label != "echo" || list[4].Label != "delta" {
		t.Fatalf("input slice mutated: %v", list)
	}

	// Label asc tiebreak on identical rollups.
	tie := SortByActionability(
		[]Workspace{{Label: "zeta"}, {Label: "ada"}},
		map[string]Rollup{},
	)
	if tie[0].Label != "ada" || tie[1].Label != "zeta" {
		t.Fatalf("tiebreak order = %v", tie)
	}
}
