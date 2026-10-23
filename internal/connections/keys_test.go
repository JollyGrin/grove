package connections

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

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

func TestKeysHostRow(t *testing.T) {
	hosts := map[string]*config.Host{"pc": {SSH: "dean@pc", GV: "gv"}}
	mk := func(stored []string, out string, err error) Env {
		env := keysEnv(nil, nil, nil, nil)
		env.Cfg.Hosts = hosts
		env.KeysStored = func() []string { return stored }
		env.Output = func(_ time.Duration, name string, args ...string) (string, error) {
			if name != "ssh" || args[len(args)-1] != "gv keys ls --json" {
				t.Errorf("probe = %s %v", name, args)
			}
			return out, err
		}
		return env
	}
	remoteJSON := `{"schema_version":1,"keys":[{"name":"A","namespace":"global"},{"name":"B","namespace":"ws"}]}`
	for _, tc := range []struct {
		name   string
		stored []string
		out    string
		err    error
		want   State
		info   string
	}{
		{"complete", []string{"global/A", "ws/B"}, remoteJSON, nil, StateOK, "2 present"},
		{"missing one", []string{"global/A", "global/B", "ws/B"}, remoteJSON, nil, StateWarn, "global/B"},
		{"old gv", []string{"global/A"}, "unknown command", nil, StateMissing, "predate"},
		{"ssh down", []string{"global/A"}, "", errors.New("exit 255"), StateMissing, "exit 255"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := mk(tc.stored, tc.out, tc.err)
			rows := keysHostConnections(env)
			if len(rows) != 1 || rows[0].ID != "keys:host:pc" || rows[0].Severity != SeverityWarn {
				t.Fatalf("rows = %+v", rows)
			}
			st := rows[0].Check(env)
			if st.State != tc.want || !strings.Contains(st.Info, tc.info) {
				t.Errorf("status = %+v", st)
			}
		})
	}
	if rows := keysHostConnections(mk(nil, "", nil)); len(rows) != 0 {
		t.Errorf("no stored secrets must mean no row, got %+v", rows)
	}
}
