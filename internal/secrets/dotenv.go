package secrets

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/JollyGrin/grove/internal/config"
)

// ParseDotenv reads the tolerant `.env` grammar grove has always
// accepted: `export NAME=value` or bare `NAME=value`, the value quoted
// ('…' or "…") or not, `#` lines ignored, an empty value skipped, the last
// assignment of a name wins (like sourcing). Names that are not valid
// secret names are dropped.
func ParseDotenv(raw []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, v, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !ValidName(name) {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if v != "" {
			out[name] = v
		}
	}
	return out
}

// legacyEnv is the one-release fallback (gv-keys design §3): a name the
// store and the process env lack is read from ~/.config/grove/.env.
// Ticket gv-keys 10 removes it, with the openrouter copy of the parser.
func legacyEnv(name string) string {
	raw, err := os.ReadFile(config.SecretsPath())
	if err != nil {
		return ""
	}
	return ParseDotenv(raw)[name]
}

// Has reports whether ns holds name, without decrypting anything.
func Has(ns, name string) bool {
	if checkNS(ns) != nil || checkName(name) != nil {
		return false
	}
	_, err := os.Stat(secretPath(ns, name))
	return err == nil
}

// Import statuses.
const (
	ImportStored  = "imported"
	ImportSkipped = "skipped" // already in the store and not forced
	ImportFailed  = "failed"
)

// ImportResult is one name's outcome — never its value.
type ImportResult struct {
	Name       string
	Status     string
	Err        error // set when Status is ImportFailed
	PreviewErr error // a refused --reveal; the secret is still stored
}

// ImportValues encrypts each name=value into the global namespace, sorted
// by name. A name already stored is skipped unless force. Failures are per
// name and do not stop the rest.
func ImportValues(vals map[string]string, force bool, reveal int) []ImportResult {
	names := make([]string, 0, len(vals))
	for n := range vals {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []ImportResult
	for _, n := range names {
		if !force && Has(Global, n) {
			out = append(out, ImportResult{Name: n, Status: ImportSkipped})
			continue
		}
		pe, err := SetWithMeta(Global, n, []byte(vals[n]), "", reveal)
		if err != nil {
			out = append(out, ImportResult{Name: n, Status: ImportFailed, Err: err})
			continue
		}
		out = append(out, ImportResult{Name: n, Status: ImportStored, PreviewErr: pe})
	}
	return out
}

// ImportDotenv imports every assignment of the .env at path.
func ImportDotenv(path string, force bool, reveal int) ([]ImportResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	vals := ParseDotenv(raw)
	if len(vals) == 0 {
		return nil, fmt.Errorf("%s holds no NAME=value assignments", path)
	}
	return ImportValues(vals, force, reveal), nil
}

// UnimportedNames lists the names in the legacy .env that the global
// store does not hold, sorted. Nothing is decrypted.
func UnimportedNames(raw []byte) []string {
	var out []string
	for n := range ParseDotenv(raw) {
		if !Has(Global, n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
