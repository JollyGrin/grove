package connections

// gv-keys doctor rows (design §3/§6). Both are conditional — a machine
// that never touched `gv keys` prints nothing here. Neither decrypts or
// reads a value: identity health is a stat, and "held" is directory
// names (Env.KeysHeld).

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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
