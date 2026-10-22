package secrets

// Row is one `gv keys ls` line — the --json envelope's element. Names and
// counts only; there is no value field to leak.
type Row struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Recipients int    `json:"recipients"`
	// Shadowed marks a global row that the ambient workspace's own
	// namespace overrides under the same name.
	Shadowed bool `json:"shadowed"`
}

// Rows lists every stored secret for display. label is the ambient
// workspace label ("" = none); a global row whose name that workspace's
// namespace also holds is marked Shadowed.
func Rows(label string) ([]Row, error) {
	entries, err := List()
	if err != nil {
		return nil, err
	}
	inLabel := map[string]bool{}
	if label != "" {
		for _, e := range entries {
			if e.Namespace == label {
				inLabel[e.Name] = true
			}
		}
	}
	counts := map[string]int{}
	rows := make([]Row, 0, len(entries))
	for _, e := range entries {
		n, ok := counts[e.Namespace]
		if !ok {
			rs, err := Recipients(e.Namespace)
			if err != nil {
				return nil, err
			}
			n = len(rs)
			counts[e.Namespace] = n
		}
		rows = append(rows, Row{
			Name:       e.Name,
			Namespace:  e.Namespace,
			Recipients: n,
			Shadowed:   e.Namespace == Global && label != Global && inLabel[e.Name],
		})
	}
	return rows, nil
}

// Held reports the set of names stored in any namespace — what doctor
// asks ("is this auth_token_env resolvable at all?"). Nothing decrypted.
func Held() (map[string]bool, error) {
	entries, err := List()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, e := range entries {
		out[e.Name] = true
	}
	return out, nil
}
