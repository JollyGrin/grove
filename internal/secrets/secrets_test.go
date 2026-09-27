package secrets

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"filippo.io/age"
)

// scratch points the config dir (HOME) and the identity (GROVE_STATE_DIR)
// at fresh temp dirs and returns the state dir. Every identity in these
// tests is generated here — no fixture keys.
func scratch(t *testing.T) (home, state string) {
	t.Helper()
	home, state = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROVE_STATE_DIR", state)
	return home, state
}

// host switches the active identity to a new state dir (a different
// "machine" sharing the same store) and returns its recipient.
func host(t *testing.T) (state, recipient string) {
	t.Helper()
	state = t.TempDir()
	t.Setenv("GROVE_STATE_DIR", state)
	r, created, err := InitIdentity()
	if err != nil || !created {
		t.Fatalf("InitIdentity: %v created=%v", err, created)
	}
	return state, r
}

func use(t *testing.T, state string) { t.Helper(); t.Setenv("GROVE_STATE_DIR", state) }

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestRoundTrip(t *testing.T) {
	home, _ := scratch(t)
	if _, _, err := InitIdentity(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ in, want string }{
		{"sk-or-v1-abc\n", "sk-or-v1-abc"}, // one trailing newline stripped
		{"two\n\n", "two\n"},               // only one
		{"no-newline", "no-newline"},
		{"ünï cødé 🔑", "ünï cødé 🔑"},
	} {
		if err := Set(Global, "GV_TEST_RT", []byte(tc.in)); err != nil {
			t.Fatal(err)
		}
		if got := Resolve("", "GV_TEST_RT"); got != tc.want {
			t.Errorf("Set(%q) → Resolve = %q, want %q", tc.in, got, tc.want)
		}
	}
	p := filepath.Join(home, ".config", "grove", "secrets", "global", "GV_TEST_RT.age")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte("-----BEGIN AGE ENCRYPTED FILE-----")) {
		t.Errorf("not armored: %q", raw[:40])
	}
	if bytes.Contains(raw, []byte("cødé")) {
		t.Error("plaintext on disk")
	}
	if m := mode(t, p); m != 0o600 {
		t.Errorf("secret mode %o, want 600", m)
	}
	if m := mode(t, filepath.Dir(p)); m != 0o700 {
		t.Errorf("namespace dir mode %o, want 700", m)
	}
	if err := Set(Global, "GV_TEST_RT", []byte("\n")); err == nil {
		t.Error("empty value accepted")
	}
}

func TestSetBeforeInitFails(t *testing.T) {
	scratch(t)
	if err := Set(Global, "GV_TEST_X", []byte("v")); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("err = %v, want ErrNoIdentity", err)
	}
}

func TestTwoRecipientsThirdRefused(t *testing.T) {
	scratch(t)
	a, _ := host(t)
	b, rb := host(t)
	c, _ := host(t)

	use(t, a)
	if err := Set(Global, "GV_TEST_TWO", []byte("shared")); err != nil {
		t.Fatal(err)
	}
	if _, err := AddRecipient(Global, "b", rb); err != nil {
		t.Fatal(err)
	}
	if _, err := Reseal(Global); err != nil {
		t.Fatal(err)
	}
	for name, st := range map[string]string{"a": a, "b": b} {
		use(t, st)
		if got := Resolve("", "GV_TEST_TWO"); got != "shared" {
			t.Errorf("host %s: %q", name, got)
		}
	}
	use(t, c)
	if got := Resolve("", "GV_TEST_TWO"); got != "" {
		t.Errorf("third host read %q", got)
	}
	_, _, err := Lookup("", "GV_TEST_TWO")
	if !errors.Is(err, ErrNotRecipient) {
		t.Fatalf("err = %v, want ErrNotRecipient", err)
	}
	var nim *age.NoIdentityMatchError
	if !errors.As(err, &nim) {
		t.Errorf("does not unwrap to *age.NoIdentityMatchError: %T", err)
	}
	if !strings.Contains(err.Error(), "not a recipient of `GV_TEST_TWO`") || !strings.Contains(err.Error(), "gv keys push --host") {
		t.Errorf("unfriendly message: %v", err)
	}
}

func TestValidation(t *testing.T) {
	scratch(t)
	if _, _, err := InitIdentity(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../x", "FOO-BAR", "", "1ABC", "A B", "a/b", "X.age"} {
		if err := Set(Global, name, []byte("v")); err == nil {
			t.Errorf("Set name %q accepted", name)
		}
		if err := Remove(Global, name); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Remove name %q: %v", name, err)
		}
		if _, _, err := Lookup("", name); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Lookup name %q: %v", name, err)
		}
	}
	for _, ns := range []string{"", "../x", "..", ".", "Global", "FOO", "-a", "_a", "a/b", "a b", "thé"} {
		if err := Set(ns, "OK_NAME", []byte("v")); err == nil {
			t.Errorf("Set namespace %q accepted", ns)
		}
		if _, err := AddRecipient(ns, "h", mustRecipient(t)); err == nil {
			t.Errorf("AddRecipient namespace %q accepted", ns)
		}
	}
	for _, ns := range []string{"global", "thegrid", "my-ws_2", "0"} {
		if err := Set(ns, "OK_NAME", []byte("v")); err != nil {
			t.Errorf("Set namespace %q: %v", ns, err)
		}
	}
	if _, err := AddRecipient(Global, "h", "age1notakey"); err == nil {
		t.Error("garbage recipient accepted")
	}
	if _, err := AddRecipient(Global, "two\nlines", mustRecipient(t)); err == nil {
		t.Error("multi-line label accepted")
	}
}

func mustRecipient(t *testing.T) string {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id.Recipient().String()
}

func TestPrecedence(t *testing.T) {
	scratch(t)
	if _, _, err := InitIdentity(); err != nil {
		t.Fatal(err)
	}
	const name = "GV_TEST_PRECEDENCE"
	os.Unsetenv(name)
	if err := Set(Global, name, []byte("from-global")); err != nil {
		t.Fatal(err)
	}
	if err := Set("thegrid", name, []byte("from-workspace")); err != nil {
		t.Fatal(err)
	}
	if err := Set("thegrid", "GV_TEST_WS_ONLY", []byte("ws")); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ label, name, want, ns string }{
		{"thegrid", name, "from-workspace", "thegrid"},
		{"", name, "from-global", Global},
		{Global, name, "from-global", Global},
		{"other", name, "from-global", Global},
		{"", "GV_TEST_WS_ONLY", "", ""}, // workspace names don't leak to global
		{"other", "GV_TEST_WS_ONLY", "", ""},
	}
	for _, c := range cases {
		v, ns, _ := Lookup(c.label, c.name)
		if v != c.want || ns != c.ns {
			t.Errorf("Lookup(%q,%q) = %q,%q want %q,%q", c.label, c.name, v, ns, c.want, c.ns)
		}
	}
	if _, _, err := Lookup("", "GV_TEST_WS_ONLY"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing name err = %v", err)
	}
	t.Setenv(name, "from-env")
	if v, ns, _ := Lookup("thegrid", name); v != "from-env" || ns != "env" {
		t.Errorf("env did not win: %q %q", v, ns)
	}
}

func TestWorkspaceUnreadableDoesNotFallThrough(t *testing.T) {
	scratch(t)
	a, _ := host(t)
	if err := Set(Global, "GV_TEST_FT", []byte("global")); err != nil {
		t.Fatal(err)
	}
	host(t) // b becomes active; thegrid/ gets seeded with b alone
	if err := Set("thegrid", "GV_TEST_FT", []byte("workspace")); err != nil {
		t.Fatal(err)
	}
	use(t, a)
	if _, _, err := Lookup("thegrid", "GV_TEST_FT"); !errors.Is(err, ErrNotRecipient) {
		t.Fatalf("err = %v, want ErrNotRecipient (never a silent global fallback)", err)
	}
}

func TestResealAfterAddRecipient(t *testing.T) {
	scratch(t)
	a, _ := host(t)
	b, rb := host(t)
	use(t, a)
	for _, n := range []string{"GV_TEST_R1", "GV_TEST_R2"} {
		if err := Set("thegrid", n, []byte("v-"+n)); err != nil {
			t.Fatal(err)
		}
	}
	use(t, b)
	if Resolve("thegrid", "GV_TEST_R1") != "" {
		t.Fatal("b read before being added")
	}
	use(t, a)
	if added, err := AddRecipient("thegrid", "b", rb); err != nil || !added {
		t.Fatalf("AddRecipient: %v %v", added, err)
	}
	if added, err := AddRecipient("thegrid", "b-again", " "+rb+" "); err != nil || added {
		t.Fatalf("duplicate AddRecipient: %v %v", added, err)
	}
	rs, err := Recipients("thegrid")
	if err != nil || len(rs) != 2 || rs[1] != rb {
		t.Fatalf("Recipients = %v, %v", rs, err)
	}
	raw, _ := os.ReadFile(filepath.Join(StoreDir(), "thegrid", "recipients.txt"))
	if !strings.Contains(string(raw), "# host: b\n"+rb+"\n") {
		t.Errorf("recipients.txt missing label:\n%s", raw)
	}
	if n, err := Reseal("thegrid"); err != nil || n != 2 {
		t.Fatalf("Reseal = %d, %v", n, err)
	}
	for name, st := range map[string]string{"a": a, "b": b} {
		use(t, st)
		for _, n := range []string{"GV_TEST_R1", "GV_TEST_R2"} {
			if got := Resolve("thegrid", n); got != "v-"+n {
				t.Errorf("host %s %s = %q", name, n, got)
			}
		}
	}
	// A host that is not a recipient cannot reseal, and changes nothing.
	host(t)
	before, _ := os.ReadFile(filepath.Join(StoreDir(), "thegrid", "GV_TEST_R1.age"))
	if _, err := Reseal("thegrid"); !errors.Is(err, ErrNotRecipient) {
		t.Errorf("stranger Reseal err = %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(StoreDir(), "thegrid", "GV_TEST_R1.age"))
	if !bytes.Equal(before, after) {
		t.Error("failed Reseal rewrote a file")
	}
}

func TestExportLinesShellSafe(t *testing.T) {
	scratch(t)
	if _, _, err := InitIdentity(); err != nil {
		t.Fatal(err)
	}
	hostile := "it's $HOME `echo pwned` \\ \"q\"\nline2 $(id)"
	if err := Set(Global, "GV_TEST_HOSTILE", []byte(hostile)); err != nil {
		t.Fatal(err)
	}
	if err := Set(Global, "GV_TEST_PLAIN", []byte("plain")); err != nil {
		t.Fatal(err)
	}
	lines, err := ExportLines("", []string{"GV_TEST_HOSTILE", "GV_TEST_PLAIN"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `eval "$1"; printf %s "$GV_TEST_HOSTILE"`, "sh", lines)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	if string(out) != hostile {
		t.Errorf("round trip through eval:\n got %q\nwant %q", out, hostile)
	}
	if !strings.HasPrefix(lines, "export GV_TEST_HOSTILE='") || !strings.Contains(lines, "\nexport GV_TEST_PLAIN='plain'\n") {
		t.Errorf("unexpected shape:\n%s", lines)
	}
	if _, err := ExportLines("", []string{"GV_TEST_PLAIN", "GV_TEST_MISSING"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing name err = %v", err)
	}
	if _, err := ExportLines("", []string{"BAD-NAME"}); err == nil {
		t.Error("invalid name accepted")
	}
}

func TestInitIdentityIdempotentAndModes(t *testing.T) {
	_, state := scratch(t)
	r1, created, err := InitIdentity()
	if err != nil || !created || !strings.HasPrefix(r1, "age1") {
		t.Fatalf("first init: %q %v %v", r1, created, err)
	}
	p := filepath.Join(state, "age", "identity.txt")
	if IdentityPath() != p {
		t.Fatalf("IdentityPath = %s, want %s", IdentityPath(), p)
	}
	before, _ := os.ReadFile(p)
	r2, created, err := InitIdentity()
	if err != nil || created || r2 != r1 {
		t.Fatalf("second init: %q %v %v", r2, created, err)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Error("identity overwritten")
	}
	if r, err := Recipient(); err != nil || r != r1 {
		t.Errorf("Recipient = %q %v", r, err)
	}
	if m := mode(t, p); m != 0o600 {
		t.Errorf("identity mode %o, want 600", m)
	}
	if m := mode(t, filepath.Dir(p)); m != 0o700 {
		t.Errorf("identity dir mode %o, want 700", m)
	}
	if !bytes.Contains(before, []byte("AGE-SECRET-KEY-1")) {
		t.Error("identity file has no secret key")
	}
}

func TestPathsFollowHomeAndStateDir(t *testing.T) {
	home1, state1 := scratch(t)
	if _, _, err := InitIdentity(); err != nil {
		t.Fatal(err)
	}
	if err := Set(Global, "GV_TEST_PATHS", []byte("v")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(state1, "age", "identity.txt"),
		filepath.Join(home1, ".config", "grove", "secrets", "global", "GV_TEST_PATHS.age"),
		filepath.Join(home1, ".config", "grove", "secrets", "global", "recipients.txt"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s: %v", p, err)
		}
	}
	if Resolve("", "GV_TEST_PATHS") != "v" {
		t.Fatal("baseline resolve failed")
	}
	t.Setenv("HOME", t.TempDir()) // other store: nothing there
	if got := Resolve("", "GV_TEST_PATHS"); got != "" {
		t.Errorf("resolved %q from a HOME without the store", got)
	}
	t.Setenv("HOME", home1)
	t.Setenv("GROVE_STATE_DIR", t.TempDir()) // same store, no identity
	if got := Resolve("", "GV_TEST_PATHS"); got != "" {
		t.Errorf("resolved %q without an identity", got)
	}
	if _, _, err := Lookup("", "GV_TEST_PATHS"); !errors.Is(err, ErrNoIdentity) {
		t.Errorf("err = %v, want ErrNoIdentity", err)
	}
}

func TestListAndRemove(t *testing.T) {
	scratch(t)
	if got, err := List(); err != nil || got != nil {
		t.Fatalf("empty store List = %v %v", got, err)
	}
	if _, _, err := InitIdentity(); err != nil {
		t.Fatal(err)
	}
	for _, e := range []Entry{{"B_KEY", "thegrid"}, {"A_KEY", Global}, {"Z_KEY", Global}, {"A_KEY", "thegrid"}} {
		if err := Set(e.Namespace, e.Name, []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	// Noise that must not show up as a secret.
	os.WriteFile(filepath.Join(StoreDir(), Global, "notes.txt"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(StoreDir(), Global, "BAD-NAME.age"), []byte("x"), 0o600)
	os.MkdirAll(filepath.Join(StoreDir(), "Not_A_Label"), 0o700)
	os.WriteFile(filepath.Join(StoreDir(), "Not_A_Label", "X.age"), []byte("x"), 0o600)

	want := []Entry{{"A_KEY", Global}, {"Z_KEY", Global}, {"A_KEY", "thegrid"}, {"B_KEY", "thegrid"}}
	got, err := List()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("List = %v %v\nwant %v", got, err, want)
	}
	if err := Remove(Global, "Z_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(Global, "Z_KEY"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Remove err = %v", err)
	}
	if got, _ := List(); len(got) != 3 {
		t.Errorf("after Remove: %v", got)
	}
}

// TestNoSideChannels: the package that holds plaintext must not write
// state events, spawn processes, or talk to the network.
func TestNoSideChannels(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{`"os/exec"`, `"net"`, `"net/`, `internal/state"`, "state.Append", `"syscall"`} {
			if bytes.Contains(src, []byte(bad)) {
				t.Errorf("%s contains %s", f, bad)
			}
		}
	}
}
