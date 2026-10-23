package secrets

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
)

const dotenvSample = `# provider keys
export A_KEY=alpha-value-0001
B_KEY=bravo-value-0002
export C_KEY="charlie-value-0003"
D_KEY='delta-value-0004'
A_KEY=alpha-value-last
EMPTY=
not a line
`

func TestParseDotenv(t *testing.T) {
	got := ParseDotenv([]byte(dotenvSample))
	want := map[string]string{
		"A_KEY": "alpha-value-last", // last wins
		"B_KEY": "bravo-value-0002",
		"C_KEY": "charlie-value-0003",
		"D_KEY": "delta-value-0004",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func writeEnv(t *testing.T, home, body string) string {
	t.Helper()
	p := filepath.Join(home, ".config", "grove", ".env")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveLegacyEnv(t *testing.T) {
	home, _ := scratch(t)
	if _, _, err := InitIdentity(); err != nil {
		t.Fatal(err)
	}
	if config.SecretsPath() != filepath.Join(home, ".config", "grove", ".env") {
		t.Fatalf("SecretsPath = %s", config.SecretsPath())
	}
	writeEnv(t, home, "ONLY_ENV=from-dotenv\nBOTH=from-dotenv\n")
	if err := Set(Global, "BOTH", []byte("from-store")); err != nil {
		t.Fatal(err)
	}
	if got := Resolve("", "ONLY_ENV"); got != "from-dotenv" {
		t.Errorf("env-only name = %q", got)
	}
	if got := Resolve("", "BOTH"); got != "from-store" {
		t.Errorf("store must win over .env, got %q", got)
	}
	if _, ns, _ := Lookup("", "ONLY_ENV"); ns != "legacy-env" {
		t.Errorf("namespace = %q", ns)
	}
}

func TestImportDotenv(t *testing.T) {
	home, _ := scratch(t)
	if _, _, err := InitIdentity(); err != nil {
		t.Fatal(err)
	}
	path := writeEnv(t, home, dotenvSample)
	before, _ := os.ReadFile(path)

	if err := Set(Global, "B_KEY", []byte("already-stored")); err != nil {
		t.Fatal(err)
	}
	res, err := ImportDotenv(path, false, 4)
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, r := range res {
		status[r.Name] = r.Status
		if r.Status == ImportStored && r.PreviewErr == nil {
			t.Errorf("%s: short value must report a refused preview, not fail", r.Name)
		}
	}
	if status["B_KEY"] != ImportSkipped || status["A_KEY"] != ImportStored || status["C_KEY"] != ImportStored || status["D_KEY"] != ImportStored {
		t.Errorf("statuses = %v", status)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("import must leave the file untouched")
	}
	if v, _, _ := Lookup("", "B_KEY"); v != "already-stored" {
		t.Errorf("skipped name was overwritten: %q", v)
	}
	if v, _, _ := Lookup("", "A_KEY"); v != "alpha-value-last" {
		t.Errorf("A_KEY = %q", v)
	}

	res, _ = ImportDotenv(path, true, 0)
	for _, r := range res {
		if r.Name == "B_KEY" && r.Status != ImportStored {
			t.Errorf("--force must import B_KEY, got %s", r.Status)
		}
	}
	if v, _, _ := Lookup("", "B_KEY"); v != "bravo-value-0002" {
		t.Errorf("forced B_KEY = %q", v)
	}
	if got := UnimportedNames([]byte(dotenvSample + "NEW_KEY=x\n")); !reflect.DeepEqual(got, []string{"NEW_KEY"}) {
		t.Errorf("UnimportedNames = %v", got)
	}
	if _, err := ImportDotenv(filepath.Join(home, "nope"), false, 0); err == nil || strings.Contains(err.Error(), "alpha") {
		t.Errorf("missing file: %v", err)
	}
}
