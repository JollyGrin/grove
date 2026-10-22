package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/secrets"
	"github.com/JollyGrin/grove/internal/workspace"
)

// keysScratch isolates the store (HOME) and identity (GROVE_STATE_DIR),
// sets an ambient workspace labelled "canopy", and generates the identity.
func keysScratch(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROVE_STATE_DIR", t.TempDir())
	old := ambient.ws
	ambient.ws = &workspace.Workspace{Root: t.TempDir(), Label: "canopy"}
	t.Cleanup(func() { ambient.ws = old })
	if _, _, err := secrets.InitIdentity(); err != nil {
		t.Fatal(err)
	}
	return home
}

// pipeStdin makes os.Stdin a pipe holding s (so it is not a terminal).
func pipeStdin(t *testing.T, s string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(s); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close() })
}

func TestParseKeysArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want keysArgs
		err  bool
	}{
		{[]string{"X"}, keysArgs{pos: []string{"X"}}, false},
		{[]string{"X", "--global"}, keysArgs{pos: []string{"X"}, global: true}, false},
		{[]string{"X", "--workspace"}, keysArgs{pos: []string{"X"}, workspace: true}, false},
		{[]string{"X", "--workspace", "foo"}, keysArgs{pos: []string{"X"}, workspace: true, label: "foo"}, false},
		{[]string{"--workspace", "X"}, keysArgs{pos: []string{"X"}, workspace: true}, false},
		{[]string{"--json"}, keysArgs{json: true}, false},
		{[]string{"X", "--global", "--workspace"}, keysArgs{}, true},
		{[]string{"--nope"}, keysArgs{}, true},
	} {
		got, err := parseKeysArgs(tc.args)
		if (err != nil) != tc.err {
			t.Errorf("%v: err = %v", tc.args, err)
			continue
		}
		if !tc.err && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: got %+v want %+v", tc.args, got, tc.want)
		}
	}
}

func TestKeysSetPositionalValueIsUsageError(t *testing.T) {
	keysScratch(t)
	err := cmdKeys([]string{"set", "X", "hunter2"})
	if err == nil || !strings.Contains(err.Error(), "stdin") || !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("want a usage error naming stdin/prompt, got %v", err)
	}
	if es, _ := secrets.List(); len(es) != 0 {
		t.Errorf("a positional value must store nothing, got %v", es)
	}
}

func TestKeysSetNamespaces(t *testing.T) {
	home := keysScratch(t)
	pipeStdin(t, "v1\n")
	if err := cmdKeys([]string{"set", "X"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "grove", "secrets", "canopy", "X.age")); err != nil {
		t.Errorf("workspace default did not land in secrets/canopy/: %v", err)
	}
	pipeStdin(t, "v2\n")
	if err := cmdKeys([]string{"set", "X", "--global"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "grove", "secrets", "global", "X.age")); err != nil {
		t.Errorf("--global did not land in secrets/global/: %v", err)
	}
	if v, ns, _ := secrets.Lookup("canopy", "X"); v != "v1" || ns != "canopy" {
		t.Errorf("Lookup = %q in %q (trailing newline must be stripped)", v, ns)
	}
	// No ambient workspace → global.
	ambient.ws = nil
	pipeStdin(t, "v3")
	if err := cmdKeys([]string{"set", "Y"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "grove", "secrets", "global", "Y.age")); err != nil {
		t.Errorf("no workspace should default to global: %v", err)
	}
	if err := cmdKeys([]string{"rm", "Y"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdKeys([]string{"rm", "Y"}); err == nil {
		t.Error("removing twice should error")
	}
}

func TestKeysLsNeverPrintsAValue(t *testing.T) {
	keysScratch(t)
	for _, args := range [][]string{{"set", "A_KEY"}, {"set", "A_KEY", "--global"}, {"set", "B_KEY", "--global"}} {
		pipeStdin(t, "ZZZSENTINEL\n")
		if err := cmdKeys(args); err != nil {
			t.Fatal(err)
		}
	}
	text := captureStdout(t, func() {
		if err := cmdKeys([]string{"ls"}); err != nil {
			t.Fatal(err)
		}
	})
	js := captureStdout(t, func() {
		if err := cmdKeys([]string{"ls", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	for name, out := range map[string]string{"text": text, "json": js} {
		if strings.Contains(out, "ZZZSENTINEL") {
			t.Errorf("%s output leaks a value:\n%s", name, out)
		}
		if !strings.Contains(out, "A_KEY") || !strings.Contains(out, "B_KEY") {
			t.Errorf("%s output lacks the names:\n%s", name, out)
		}
	}
	if !strings.Contains(text, "shadowed") {
		t.Errorf("shadowed global row not marked:\n%s", text)
	}
	for _, f := range []string{`"schema_version"`, `"keys"`, `"namespace"`, `"recipients"`, `"shadowed"`} {
		if !strings.Contains(js, f) {
			t.Errorf("json lacks %s:\n%s", f, js)
		}
	}
}

func TestKeysRecipientIsOneLine(t *testing.T) {
	keysScratch(t)
	out := captureStdout(t, func() {
		if err := cmdKeys([]string{"recipient"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.HasPrefix(out, "age1") || strings.Count(out, "\n") != 1 || strings.Contains(strings.TrimSpace(out), " ") {
		t.Errorf("recipient output = %q", out)
	}
	if strings.Contains(out, "AGE-SECRET-KEY") {
		t.Error("identity printed")
	}
}

// The wizard's keys step (--yes): generates an identity, a second run is a
// no-op, and neither run prints the identity.
func TestInitKeysStep(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GROVE_STATE_DIR", t.TempDir())
	var first, second bytes.Buffer
	if err := initKeysStep(&first, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "generated") || !keysIdentityPresent() {
		t.Fatalf("first run did not generate: %q", first.String())
	}
	before, _ := os.ReadFile(secrets.IdentityPath())
	if err := initKeysStep(&second, true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(secrets.IdentityPath())
	if !bytes.Equal(before, after) || !strings.Contains(second.String(), "already") {
		t.Errorf("second run must be a no-op: %q", second.String())
	}
	if strings.Contains(first.String()+second.String(), "AGE-SECRET-KEY") {
		t.Error("identity printed")
	}
	var off bytes.Buffer
	if err := initKeysStep(&off, false); err != nil || off.Len() != 0 {
		t.Errorf("off must do nothing: %q %v", off.String(), err)
	}
}

func TestParseKeysExec(t *testing.T) {
	names, argv, err := parseKeysExec([]string{"A,B", "--", "sh", "-c", "x -- y"})
	if err != nil || !reflect.DeepEqual(names, []string{"A", "B"}) || !reflect.DeepEqual(argv, []string{"sh", "-c", "x -- y"}) {
		t.Errorf("got %v %v %v", names, argv, err)
	}
	for _, bad := range [][]string{nil, {"A"}, {"A", "true"}, {"A", "--"}, {"--", "true"}, {"A,", "--", "true"}, {"A", "B", "--", "true"}} {
		if _, _, err := parseKeysExec(bad); err == nil {
			t.Errorf("%v: want usage error", bad)
		}
	}
}

func TestEmptyArgWarning(t *testing.T) {
	if w := emptyArgWarning([]string{"sh", "-c", `echo ""`}); w != "" {
		t.Errorf("script-internal empty string warned: %q", w)
	}
	w := emptyArgWarning([]string{"curl", "-H", ""})
	if !strings.Contains(w, "sh -c '") || !strings.Contains(w, "YOUR shell") {
		t.Errorf("warning = %q", w)
	}
}
