package secrets

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"filippo.io/age"
	"filippo.io/age/armor"

	"github.com/JollyGrin/grove/internal/config"
)

const (
	recipientsFile = "recipients.txt"
	ext            = ".age"
)

// Entry is one stored secret — a name and where it lives, never a value.
type Entry struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

func nsDir(ns string) string            { return filepath.Join(StoreDir(), ns) }
func secretPath(ns, name string) string { return filepath.Join(nsDir(ns), name+ext) }

// Set encrypts value to ns's recipients and writes NAME.age atomically.
// One trailing newline is stripped (what `printf x |` and a prompt give).
// A namespace with no recipients.txt yet is seeded with this host's
// recipient, so the first Set on a fresh host needs only InitIdentity.
func Set(ns, name string, value []byte) error {
	if err := checkNS(ns); err != nil {
		return err
	}
	if err := checkName(name); err != nil {
		return err
	}
	value = bytes.TrimSuffix(value, []byte("\n"))
	if len(value) == 0 {
		return fmt.Errorf("refusing to store an empty value for %s", name)
	}
	if err := seedRecipients(ns); err != nil {
		return err
	}
	rcpts, err := parseRecipients(ns)
	if err != nil {
		return err
	}
	sealed, err := seal(value, rcpts)
	if err != nil {
		return err
	}
	return writeAtomic(secretPath(ns, name), sealed)
}

// seedRecipients writes recipients.txt with this host alone if absent.
func seedRecipients(ns string) error {
	if _, err := os.Stat(filepath.Join(nsDir(ns), recipientsFile)); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	r, err := Recipient()
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	if host == "" || !singleLine(host) {
		host = "this host"
	}
	_, err = AddRecipient(ns, host, r)
	return err
}

// Resolve returns the value for name, or "" when unset or unreadable.
// Order: process env > <label>/ > global/. label "" means global only.
func Resolve(label, name string) string {
	v, _, err := Lookup(label, name)
	if err != nil {
		return ""
	}
	return v
}

// Lookup is Resolve with the reason: it also reports which namespace
// answered ("env" for the process environment). A name the workspace
// namespace holds but this host cannot decrypt is an error, never a
// silent fall-through to global — that would launch with the wrong key.
func Lookup(label, name string) (value, namespace string, err error) {
	if err := checkName(name); err != nil {
		return "", "", err
	}
	if v := os.Getenv(name); v != "" {
		return v, "env", nil
	}
	chain := []string{Global}
	if label != "" && label != Global {
		if err := checkNS(label); err != nil {
			return "", "", err
		}
		chain = []string{label, Global}
	}
	var id *age.X25519Identity
	for _, ns := range chain {
		path := secretPath(ns, name)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return "", "", err
		}
		if id == nil {
			if id, err = loadIdentity(); err != nil {
				return "", "", err
			}
		}
		b, err := open(path, ns, name, id)
		if err != nil {
			return "", "", err
		}
		return string(b), ns, nil
	}
	return "", "", fmt.Errorf("%s: %w", name, ErrNotFound)
}

// List returns every stored name, sorted by namespace then name. It reads
// directory entries only — no identity needed, nothing decrypted.
func List() ([]Entry, error) {
	nss, err := os.ReadDir(StoreDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, d := range nss {
		if !d.IsDir() || !ValidNamespace(d.Name()) {
			continue
		}
		files, err := os.ReadDir(nsDir(d.Name()))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			name, ok := strings.CutSuffix(f.Name(), ext)
			if !ok || f.IsDir() || !ValidName(name) {
				continue
			}
			out = append(out, Entry{Name: name, Namespace: d.Name()})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Remove deletes ns/NAME.age.
func Remove(ns, name string) error {
	if err := checkNS(ns); err != nil {
		return err
	}
	if err := checkName(name); err != nil {
		return err
	}
	err := os.Remove(secretPath(ns, name))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s in %s: %w", name, ns, ErrNotFound)
	}
	return err
}

// Recipients lists ns's recipient strings (comments dropped). A namespace
// with no recipients.txt has none.
func Recipients(ns string) ([]string, error) {
	if err := checkNS(ns); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(nsDir(ns), recipientsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// AddRecipient appends recipient to ns's recipients.txt under a
// `# host: <label>` comment. Already present is a no-op (added false).
// Existing files are NOT resealed — call Reseal for that.
func AddRecipient(ns, label, recipient string) (added bool, err error) {
	if err := checkNS(ns); err != nil {
		return false, err
	}
	if !singleLine(label) {
		return false, fmt.Errorf("recipient label must be one line")
	}
	recipient = strings.TrimSpace(recipient)
	if !singleLine(recipient) || strings.HasPrefix(recipient, "#") {
		return false, fmt.Errorf("invalid recipient %q", recipient)
	}
	if rs, err := age.ParseRecipients(strings.NewReader(recipient)); err != nil || len(rs) != 1 {
		return false, fmt.Errorf("invalid recipient %q: %v", recipient, err)
	}
	have, err := Recipients(ns)
	if err != nil {
		return false, err
	}
	for _, r := range have {
		if r == recipient {
			return false, nil
		}
	}
	if err := mkdirPrivate(nsDir(ns)); err != nil {
		return false, err
	}
	path := filepath.Join(nsDir(ns), recipientsFile)
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if len(raw) > 0 && !bytes.HasSuffix(raw, []byte("\n")) {
		raw = append(raw, '\n')
	}
	raw = append(raw, fmt.Sprintf("# host: %s\n%s\n", label, recipient)...)
	return true, writeAtomic(path, raw)
}

// Reseal re-encrypts every secret in ns to its current recipients.txt and
// returns how many it rewrote. Everything is decrypted before anything is
// written, so a file this host cannot open aborts with nothing changed.
func Reseal(ns string) (int, error) {
	if err := checkNS(ns); err != nil {
		return 0, err
	}
	all, err := List()
	if err != nil {
		return 0, err
	}
	var names []string
	for _, e := range all {
		if e.Namespace == ns {
			names = append(names, e.Name)
		}
	}
	if len(names) == 0 {
		return 0, nil
	}
	id, err := loadIdentity()
	if err != nil {
		return 0, err
	}
	rcpts, err := parseRecipients(ns)
	if err != nil {
		return 0, err
	}
	sealed := make(map[string][]byte, len(names))
	for _, name := range names {
		plain, err := open(secretPath(ns, name), ns, name, id)
		if err != nil {
			return 0, err
		}
		if sealed[name], err = seal(plain, rcpts); err != nil {
			return 0, err
		}
	}
	for _, name := range names {
		if err := writeAtomic(secretPath(ns, name), sealed[name]); err != nil {
			return 0, err
		}
	}
	return len(names), nil
}

// ExportLines resolves each name and renders `export NAME='value'` lines
// for a launch shell to eval, quoted by config.ShellQuote. Any name that
// does not resolve fails the whole call — a worker never launches with a
// partial environment.
func ExportLines(label string, names []string) (string, error) {
	var b strings.Builder
	for _, name := range names {
		v, _, err := Lookup(label, name)
		if err != nil {
			return "", err
		}
		b.WriteString("export " + name + "=" + config.ShellQuote(v) + "\n")
	}
	return b.String(), nil
}

func parseRecipients(ns string) ([]age.Recipient, error) {
	path := filepath.Join(nsDir(ns), recipientsFile)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rs, err := age.ParseRecipients(f)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return rs, nil
}

func seal(plain []byte, rcpts []age.Recipient) ([]byte, error) {
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, rcpts...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if err := aw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func open(path, ns, name string, id age.Identity) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, err := age.Decrypt(armor.NewReader(f), id)
	if err != nil {
		var nim *age.NoIdentityMatchError
		if errors.As(err, &nim) {
			return nil, &NotRecipientError{Name: name, Namespace: ns, Err: nim}
		}
		return nil, fmt.Errorf("decrypt %s: %w", path, err)
	}
	return readAll(r)
}
