//go:build !darwin

package resource

import "errors"

// Read is unimplemented off darwin — grove's runtime targets macOS (jetsam,
// tmux, `open`). A non-darwin build (CI cross-compile, a Linux contributor)
// gets a usable binary; the memory gauge simply stays hidden (Mem.OK() is
// false) rather than shelling out to a Linux-specific source we don't need.
func Read() (Mem, error) {
	return Mem{}, errors.New("resource: memory read unsupported off darwin")
}
