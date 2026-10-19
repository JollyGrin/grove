package config

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path with data via a temp file in the same dir
// + rename, so a concurrent reader (`gv statusline` reads config.yaml on
// every status line refresh) sees the old file or the new one, never a
// torn write. A symlinked path is resolved first and its TARGET replaced
// — renaming over the link would turn a dotfiles-managed link into a
// regular file. An existing file keeps its permissions; a new one gets
// mode.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		path = r
	} else if target, lerr := os.Readlink(path); lerr == nil {
		// A dangling link: write where it points.
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
