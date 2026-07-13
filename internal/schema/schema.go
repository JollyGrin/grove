// Package schema pins the surface-plugin contract version
// (docs/plugins.md). Every machine-readable surface grove emits carries
// it: `--json` payloads as a top-level `schema_version`, events.jsonl
// records as `v`. The contract is additive-only — grove only ever adds
// fields, and consumers must ignore unknown keys — so this number moves
// only on a removal or rename, which is a major break for every plugin.
package schema

// Version is the current contract version.
const Version = 1

// Envelope is the shape of every `--json` payload: a top-level object
// carrying schema_version plus the command's payload under one named key
// (e.g. "tasks", "report", "rows"). Keys per command are listed in
// docs/plugins.md.
func Envelope(key string, payload any) map[string]any {
	return map[string]any{"schema_version": Version, key: payload}
}
