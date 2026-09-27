// Package secrets is grove's age-encrypted secret store (gv-keys design,
// docs/plans/2026-09-27-secure-keys-design.md §2): one per-host X25519
// identity in the GLOBAL state dir, and one store under the config dir
// holding a namespace per workspace label plus `global`, each with a
// recipients.txt and one armored NAME.age per secret.
//
// Values never leave this package except as the return of Resolve/Lookup
// and the ExportLines text a launch shell evals. No state events, no
// subprocesses, no network — push/receive transport lives elsewhere.
package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"filippo.io/age"

	"github.com/JollyGrin/grove/internal/config"
)

// Global is the namespace shared by every workspace.
const Global = "global"

// labelPattern is the shape of a workspace-label namespace. `global`
// matches it too and is simply the reserved one.
var labelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// maxSecret caps a decrypted value; API keys are ~100 bytes, so anything
// near this is a wrong file, not a secret.
const maxSecret = 1 << 20

var (
	// ErrNotRecipient matches (errors.Is) a *NotRecipientError.
	ErrNotRecipient = errors.New("this host's identity is not a recipient")
	// ErrNoIdentity: this host has no identity.txt yet.
	ErrNoIdentity = errors.New("no age identity on this host — run `gv keys init`")
	// ErrNotFound: no namespace (and no env var) holds the name.
	ErrNotFound = errors.New("secret not found")
)

// NotRecipientError is a decrypt that no identity on this host could open.
// It unwraps to the underlying *age.NoIdentityMatchError.
type NotRecipientError struct {
	Name, Namespace string
	Err             *age.NoIdentityMatchError
}

func (e *NotRecipientError) Error() string {
	return fmt.Sprintf("this host's identity is not a recipient of `%s` (namespace %s) — run `gv keys push --host <this host>` from a host that is", e.Name, e.Namespace)
}

func (e *NotRecipientError) Unwrap() error { return e.Err }

func (e *NotRecipientError) Is(target error) bool { return target == ErrNotRecipient }

// IdentityPath is this host's private key. Always the global StateDir —
// an identity belongs to the host, never to a workspace.
func IdentityPath() string {
	return filepath.Join(config.StateDir(), "age", "identity.txt")
}

// StoreDir is the one physical store; namespaces are its subdirectories.
func StoreDir() string {
	return filepath.Join(config.Dir(), "secrets")
}

// ValidName reports whether name is usable as a secret name (a filename
// and an env var both).
func ValidName(name string) bool { return config.ValidEnvKey(name) }

// ValidNamespace reports whether ns is `global` or a workspace label.
func ValidNamespace(ns string) bool { return labelPattern.MatchString(ns) }

func checkNS(ns string) error {
	if !ValidNamespace(ns) {
		return fmt.Errorf("invalid namespace %q (want %q or %s)", ns, Global, labelPattern.String())
	}
	return nil
}

func checkName(name string) error {
	if !ValidName(name) {
		return fmt.Errorf("invalid secret name %q (want ^[A-Za-z_][A-Za-z0-9_]*$)", name)
	}
	return nil
}

// InitIdentity generates this host's identity if absent and returns its
// public recipient. It never overwrites: an existing identity is read
// back and created is false.
func InitIdentity() (recipient string, created bool, err error) {
	path := IdentityPath()
	if id, err := loadIdentity(); err == nil {
		return id.Recipient().String(), false, nil
	} else if !errors.Is(err, ErrNoIdentity) {
		return "", false, err
	}
	if err := mkdirPrivate(filepath.Dir(path)); err != nil {
		return "", false, err
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", false, err
	}
	body := fmt.Sprintf("# grove host identity — never copy this file; every host has its own\n# public key: %s\n%s\n",
		id.Recipient(), id)
	// O_EXCL: a concurrent init that won the race keeps its key.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return InitIdentity()
		}
		return "", false, err
	}
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(path)
		return "", false, err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", false, err
	}
	return id.Recipient().String(), true, nil
}

// Recipient is this host's public key.
func Recipient() (string, error) {
	id, err := loadIdentity()
	if err != nil {
		return "", err
	}
	return id.Recipient().String(), nil
}

func loadIdentity() (*age.X25519Identity, error) {
	raw, err := os.ReadFile(IdentityPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoIdentity
	}
	if err != nil {
		return nil, err
	}
	ids, err := age.ParseIdentities(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", IdentityPath(), err)
	}
	for _, id := range ids {
		if x, ok := id.(*age.X25519Identity); ok {
			return x, nil
		}
	}
	return nil, fmt.Errorf("%s holds no X25519 identity", IdentityPath())
}

// mkdirPrivate creates dir 0700, and tightens it if it already existed.
func mkdirPrivate(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// writeAtomic writes data to path via temp + rename, mode 0600.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	if _, err := f.Write(data); err != nil {
		return cleanup(err)
	}
	if err := f.Sync(); err != nil {
		return cleanup(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// readAll reads r with the maxSecret cap.
func readAll(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxSecret+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxSecret {
		return nil, fmt.Errorf("secret exceeds %d bytes", maxSecret)
	}
	return b, nil
}

// singleLine rejects recipient labels that would break recipients.txt.
func singleLine(s string) bool { return !strings.ContainsAny(s, "\r\n") }
