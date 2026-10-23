package secrets

import (
	"errors"
	"fmt"
	"strings"
)

// ExecEnv returns a copy of base with exactly the named secrets added,
// each overriding any same-name entry. Names are validated before any
// decrypt, duplicates collapse, and a name that does not resolve fails
// the whole call (naming the namespaces searched) so nothing runs with a
// partial grant. base is never modified.
func ExecEnv(label string, names []string, base []string) ([]string, error) {
	var uniq []string
	seen := map[string]bool{}
	for _, n := range names {
		if err := checkName(n); err != nil {
			return nil, err
		}
		if !seen[n] {
			seen[n] = true
			uniq = append(uniq, n)
		}
	}
	vals := make([]string, len(uniq))
	for i, n := range uniq {
		v, _, err := Lookup(label, n)
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("secret %s not found (searched %s)", n, searched(label))
		}
		if err != nil {
			return nil, err
		}
		vals[i] = v
	}
	env := make([]string, 0, len(base)+len(uniq))
	for _, kv := range base {
		if k, _, ok := strings.Cut(kv, "="); ok && seen[k] {
			continue
		}
		env = append(env, kv)
	}
	for i, n := range uniq {
		env = append(env, n+"="+vals[i])
	}
	return env, nil
}

func searched(label string) string {
	if label != "" && label != Global {
		return fmt.Sprintf("the process environment, workspace %q, global", label)
	}
	return "the process environment, global"
}
