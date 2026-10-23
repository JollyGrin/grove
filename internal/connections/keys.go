package connections

// gv-keys doctor rows (design §3/§6). Both are conditional — a machine
// that never touched `gv keys` prints nothing here. Neither decrypts or
// reads a value: identity health is a stat, and "held" is directory
// names (Env.KeysHeld).

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/remote"
	"github.com/JollyGrin/grove/internal/secrets"
)

// keysConnections returns keys:identity (when an identity exists, or
// secrets are stored without one) and one keys:unresolved row naming the profile
// token env vars that nothing holds. KeysIdentityPath == "" (fake envs)
// drops the section.
func keysConnections(env Env) []Connection {
	if env.KeysIdentityPath == "" {
		return nil
	}
	var held map[string]bool
	if env.KeysHeld != nil {
		held = env.KeysHeld()
	}
	var conns []Connection

	_, statErr := env.Stat(env.KeysIdentityPath)
	if statErr == nil || len(held) > 0 {
		conns = append(conns, Connection{
			ID:          "keys:identity",
			Kind:        KindFile,
			Severity:    SeverityWarn,
			RequiredFor: []string{"grab", "keys"},
			Title:       "age identity for encrypted secrets",
			Fix:         "gv keys init",
			Check:       checkKeysIdentity,
		})
	}

	if len(held) > 0 || statErr == nil || envFileExists(env) {
		conns = append(conns, legacyEnvConnection(env))
	}

	if env.Cfg == nil {
		return conns
	}
	seen := map[string]bool{}
	var missing []string
	for _, p := range env.Cfg.ModelProfiles {
		if p == nil || p.AuthTokenEnv == "" || seen[p.AuthTokenEnv] {
			continue
		}
		seen[p.AuthTokenEnv] = true
		name := p.AuthTokenEnv
		if held[name] || env.Getenv(name) != "" || envFileHasVar(env, filepath.Join(env.Home, ".config", "grove", ".env"), name) {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		info := strings.Join(missing, ", ") + " — in no secrets namespace nor the process env"
		conns = append(conns, Connection{
			ID:          "keys:unresolved",
			Kind:        KindEnv,
			Severity:    SeverityWarn,
			RequiredFor: []string{"grab"},
			Title:       "model profile tokens resolve",
			Fix:         "gv keys set <NAME>",
			Check:       func(Env) Status { return Status{State: StateWarn, Info: info} },
		})
	}
	return conns
}

func legacyEnvPath(env Env) string { return filepath.Join(env.Home, ".config", "grove", ".env") }

func envFileExists(env Env) bool {
	if env.Stat == nil {
		return false
	}
	_, err := env.Stat(legacyEnvPath(env))
	return err == nil
}

// storedGlobal is the set of names the global namespace holds.
func storedGlobal(env Env) map[string]bool {
	out := map[string]bool{}
	if env.KeysStored == nil {
		return out
	}
	for _, s := range env.KeysStored() {
		if n, ok := strings.CutPrefix(s, secrets.Global+"/"); ok {
			out[n] = true
		}
	}
	return out
}

// legacyEnvConnection is keys:legacy-env (gv-keys 07): warn while the
// pre-store ~/.config/grove/.env holds a name the encrypted store lacks.
// Names only — the file is parsed, never echoed. Ticket 10 removes it.
func legacyEnvConnection(env Env) Connection {
	return Connection{
		ID:          "keys:legacy-env",
		Kind:        KindFile,
		Severity:    SeverityWarn,
		RequiredFor: []string{"grab", "keys"},
		Title:       "legacy .env secrets imported into the encrypted store",
		Fix:         "gv keys import --rm",
		Check: func(e Env) Status {
			path := legacyEnvPath(e)
			raw, err := e.ReadFile(path)
			if err != nil {
				return Status{State: StateOK, Info: "no " + path}
			}
			have := storedGlobal(e)
			var missing []string
			for n := range secrets.ParseDotenv(raw) {
				if !have[n] {
					missing = append(missing, n)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				return Status{State: StateWarn, Info: path + " holds " + strings.Join(missing, ", ") + " not in the encrypted store"}
			}
			return Status{State: StateOK, Info: path + " — every name is in the store; delete it with gv keys import --rm"}
		},
	}
}

// checkKeysIdentity: present, the file 0600 and its directory 0700.
func checkKeysIdentity(e Env) Status {
	fi, err := e.Stat(e.KeysIdentityPath)
	if err != nil {
		return Status{State: StateWarn, Info: "secrets are stored but this host has no identity — " + e.KeysIdentityPath + " is missing"}
	}
	if fi != nil && fi.Mode().Perm() != 0o600 {
		return Status{State: StateWarn, Info: fmt.Sprintf("%s is %04o, want 0600", e.KeysIdentityPath, fi.Mode().Perm())}
	}
	if di, err := e.Stat(filepath.Dir(e.KeysIdentityPath)); err == nil && di != nil && di.Mode().Perm() != 0o700 {
		return Status{State: StateWarn, Info: fmt.Sprintf("%s is %04o, want 0700", filepath.Dir(e.KeysIdentityPath), di.Mode().Perm())}
	}
	return Status{State: StateOK, Info: e.KeysIdentityPath}
}

// keysHostConnections adds one keys:host:<H> row per configured remote
// host, only on a machine that stores secrets (conditional, like the other
// keys rows): it runs `<gv> keys ls --json` over the same BatchMode ssh as
// the host probe and names the locally stored secrets H lacks.
func keysHostConnections(env Env) []Connection {
	if env.KeysIdentityPath == "" || env.KeysStored == nil || len(env.KeysStored()) == 0 {
		return nil
	}
	var conns []Connection
	for _, name := range env.Cfg.HostNames() {
		h := env.Cfg.Hosts[name]
		conns = append(conns, Connection{
			ID:          "keys:host:" + name,
			Kind:        KindRemoteHost,
			Severity:    SeverityWarn,
			RequiredFor: []string{"grab --host"},
			Title:       "secrets present on host " + name,
			Fix:         "gv keys push --host " + name,
			Check:       checkKeysHost(h),
		})
	}
	return conns
}

func checkKeysHost(h *config.Host) func(Env) Status {
	return func(e Env) Status {
		if e.Output == nil {
			return Status{State: StateMissing, Info: "no probe"}
		}
		out, err := e.Output(remoteProbeTimeout, "ssh",
			"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", h.SSH, "--", remote.Quote(h.GV)+" keys ls --json")
		if err != nil {
			return Status{State: StateMissing, Info: "ssh " + h.SSH + ": " + err.Error()}
		}
		var env struct {
			Keys []struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"keys"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			return Status{State: StateMissing, Info: "no gv keys ls --json from " + h.SSH + " — its gv may predate gv keys (gv update --yes)"}
		}
		have := map[string]bool{}
		for _, k := range env.Keys {
			have[k.Namespace+"/"+k.Name] = true
		}
		var missing []string
		for _, s := range e.KeysStored() {
			if !have[s] {
				missing = append(missing, s)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			return Status{State: StateWarn, Info: "host lacks " + strings.Join(missing, ", ")}
		}
		return Status{State: StateOK, Info: fmt.Sprintf("%d present", len(have))}
	}
}
