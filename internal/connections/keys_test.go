package connections

import (
	"os"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
)

type fakeInfo struct {
	os.FileInfo
	mode os.FileMode
}

func (f fakeInfo) Mode() os.FileMode { return f.mode }

const idPath = "/state/age/identity.txt"

func keysEnv(files map[string]os.FileMode, held []string, getenv map[string]string, profiles map[string]string) Env {
	cfg := &config.Config{ModelProfiles: map[string]*config.ModelProfile{}}
	for n, tok := range profiles {
		cfg.ModelProfiles[n] = &config.ModelProfile{AuthTokenEnv: tok}
	}
	heldSet := map[string]bool{}
	for _, h := range held {
		heldSet[h] = true
	}
	return Env{
		Cfg:    cfg,
		Getenv: func(k string) string { return getenv[k] },
		Stat: func(name string) (os.FileInfo, error) {
			if m, ok := files[name]; ok {
				return fakeInfo{mode: m}, nil
			}
			return nil, os.ErrNotExist
		},
		ReadFile:         func(string) ([]byte, error) { return nil, os.ErrNotExist },
		Home:             "/home/u",
		KeysIdentityPath: idPath,
		KeysHeld:         func() map[string]bool { return heldSet },
	}
}

func TestKeysIdentityRow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]os.FileMode
		held  []string
		want  State // "" = no row
	}{
		{"never used keys: no row", nil, nil, ""},
		{"healthy", map[string]os.FileMode{idPath: 0o600, "/state/age": 0o700}, nil, StateOK},
		{"loose file", map[string]os.FileMode{idPath: 0o644, "/state/age": 0o700}, nil, StateWarn},
		{"loose dir", map[string]os.FileMode{idPath: 0o600, "/state/age": 0o755}, nil, StateWarn},
		{"secrets but no identity", nil, []string{"X"}, StateWarn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := keysEnv(tc.files, tc.held, nil, nil)
			var row *Connection
			for _, c := range keysConnections(env) {
				if c.ID == "keys:identity" {
					c := c
					row = &c
				}
			}
			if tc.want == "" {
				if row != nil {
					t.Fatalf("unexpected row %+v", row)
				}
				return
			}
			if row == nil {
				t.Fatal("no keys:identity row")
			}
			if got := row.Check(env); got.State != tc.want {
				t.Errorf("state = %s (%s), want %s", got.State, got.Info, tc.want)
			}
			if row.Severity != SeverityWarn || row.Fix != "gv keys init" {
				t.Errorf("row = %+v", row)
			}
		})
	}
}

func TestKeysUnresolvedRow(t *testing.T) {
	profiles := map[string]string{"zai": "ZAI_KEY", "or": "OPENROUTER_API_KEY", "dup": "ZAI_KEY"}
	for _, tc := range []struct {
		name    string
		held    []string
		getenv  map[string]string
		want    string // substring of Info; "" = no row
		notWant string
	}{
		{"nothing holds either", nil, nil, "OPENROUTER_API_KEY, ZAI_KEY", ""},
		{"one in a namespace", []string{"ZAI_KEY"}, nil, "OPENROUTER_API_KEY", "ZAI_KEY"},
		{"one in the process env", nil, map[string]string{"ZAI_KEY": "v"}, "OPENROUTER_API_KEY", "ZAI_KEY"},
		{"all held", []string{"ZAI_KEY", "OPENROUTER_API_KEY"}, nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := keysEnv(nil, tc.held, tc.getenv, profiles)
			var row *Connection
			for _, c := range keysConnections(env) {
				if c.ID == "keys:unresolved" {
					c := c
					row = &c
				}
			}
			if tc.want == "" {
				if row != nil {
					t.Fatalf("unexpected row %+v", row)
				}
				return
			}
			if row == nil {
				t.Fatal("no keys:unresolved row")
			}
			st := row.Check(env)
			if st.State != StateWarn || !strings.Contains(st.Info, tc.want) || (tc.notWant != "" && strings.Contains(st.Info, tc.notWant)) {
				t.Errorf("status = %+v", st)
			}
			if row.Severity != SeverityWarn || row.Fix != "gv keys set <NAME>" {
				t.Errorf("row = %+v", row)
			}
		})
	}
}

func TestKeysRowsDroppedInFakeEnv(t *testing.T) {
	env := happyEnv(testConfig())
	for _, r := range EvaluateAll(env) {
		if strings.HasPrefix(r.Connection.ID, "keys:") {
			t.Errorf("fake env grew %s", r.Connection.ID)
		}
	}
}
