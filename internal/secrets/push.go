package secrets

// Push/receive (design §5): one namespace's ciphertext, recipients file
// and sidecars travel as a single JSON object over an existing ssh hop.
// This file is transport-free — the caller supplies how the remote's
// recipient is fetched and how the payload is delivered — so the logic is
// testable without ssh. Nothing here ever holds a plaintext value: files
// are copied as they sit on disk.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"filippo.io/age"
	"gopkg.in/yaml.v3"
)

// maxPayload bounds what `receive` will read from stdin.
const maxPayload = 64 << 20

const armorHeader = "-----BEGIN AGE ENCRYPTED FILE-----"

// Payload is the wire form of one namespace.
type Payload struct {
	Namespace  string            `json:"namespace"`
	Recipients string            `json:"recipients"`
	Secrets    map[string]string `json:"secrets"`
	Meta       map[string]string `json:"meta"`
}

// BuildPayload reads ns's recipients.txt, every NAME.age and every sidecar
// verbatim.
func BuildPayload(ns string) (*Payload, error) {
	if err := checkNS(ns); err != nil {
		return nil, err
	}
	rec, err := os.ReadFile(filepath.Join(nsDir(ns), recipientsFile))
	if err != nil {
		return nil, fmt.Errorf("namespace %s: %w", ns, err)
	}
	all, err := List()
	if err != nil {
		return nil, err
	}
	p := &Payload{Namespace: ns, Recipients: string(rec), Secrets: map[string]string{}, Meta: map[string]string{}}
	for _, e := range all {
		if e.Namespace != ns {
			continue
		}
		raw, err := os.ReadFile(secretPath(ns, e.Name))
		if err != nil {
			return nil, err
		}
		p.Secrets[e.Name] = string(raw)
		m, err := os.ReadFile(metaPath(ns, e.Name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		p.Meta[e.Name] = string(m)
	}
	return p, nil
}

// Encode renders the payload as one JSON object.
func (p *Payload) Encode() ([]byte, error) { return json.Marshal(p) }

// DecodePayload parses a payload, refusing anything over maxPayload.
func DecodePayload(r io.Reader) (*Payload, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxPayload+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPayload {
		return nil, fmt.Errorf("payload exceeds %d bytes", maxPayload)
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("payload is not valid JSON: %w", err)
	}
	return &p, nil
}

// validate checks everything before any byte is written: the namespace,
// every name, that each secret is armored age ciphertext (a plaintext
// slipped in here would be stored as-is), that sidecars parse, and that
// recipients.txt parses.
func (p *Payload) validate() error {
	if err := checkNS(p.Namespace); err != nil {
		return err
	}
	if _, err := age.ParseRecipients(strings.NewReader(p.Recipients)); err != nil {
		return fmt.Errorf("recipients: %w", err)
	}
	for name, body := range p.Secrets {
		if err := checkName(name); err != nil {
			return err
		}
		if !strings.HasPrefix(body, armorHeader) {
			return fmt.Errorf("secret %s is not age ciphertext", name)
		}
	}
	for name, body := range p.Meta {
		if err := checkName(name); err != nil {
			return err
		}
		if _, ok := p.Secrets[name]; !ok {
			return fmt.Errorf("sidecar for %s has no secret", name)
		}
		var m Meta
		if err := yaml.Unmarshal([]byte(body), &m); err != nil {
			return fmt.Errorf("sidecar for %s: %w", name, err)
		}
	}
	return nil
}

// Receive validates the payload, then writes it into this host's store:
// all files are staged next to their targets first and renamed in only
// once every one staged, so a bad payload or a full disk changes nothing.
// A file already holding the same bytes is left untouched. Returns the
// number of secrets in the payload.
func Receive(r io.Reader) (int, error) {
	p, err := DecodePayload(r)
	if err != nil {
		return 0, err
	}
	if err := p.validate(); err != nil {
		return 0, err
	}
	if err := mkdirPrivate(nsDir(p.Namespace)); err != nil {
		return 0, err
	}
	files := map[string]string{filepath.Join(nsDir(p.Namespace), recipientsFile): p.Recipients}
	for name, body := range p.Secrets {
		files[secretPath(p.Namespace, name)] = body
	}
	for name, body := range p.Meta {
		files[metaPath(p.Namespace, name)] = body
	}
	paths := make([]string, 0, len(files))
	for path, body := range files {
		if cur, err := os.ReadFile(path); err == nil && string(cur) == body {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	staged := map[string]string{}
	unstage := func() {
		for _, tmp := range staged {
			os.Remove(tmp)
		}
	}
	for _, path := range paths {
		tmp, err := stage(path, []byte(files[path]))
		if err != nil {
			unstage()
			return 0, err
		}
		staged[path] = tmp
	}
	for _, path := range paths {
		if err := os.Rename(staged[path], path); err != nil {
			unstage()
			return 0, err
		}
		delete(staged, path)
	}
	return len(p.Secrets), nil
}

// stage writes data to a 0600 temp file beside path and returns its name.
func stage(path string, data []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// PushResult is what a completed push reports.
type PushResult struct {
	Count     int
	Recipient string
}

// Push shares ns with a remote host: fetch its recipient, add it to the
// namespace (seeding this host first so it never locks itself out), reseal
// everything to the full list, and hand the payload to send.
func Push(ns, hostLabel string, fetchRecipient func() (string, error), send func(payload []byte) (int, error)) (*PushResult, error) {
	if err := checkNS(ns); err != nil {
		return nil, err
	}
	all, err := List()
	if err != nil {
		return nil, err
	}
	n := 0
	for _, e := range all {
		if e.Namespace == ns {
			n++
		}
	}
	if n == 0 {
		return nil, fmt.Errorf("namespace %s holds no secrets — nothing to push", ns)
	}
	key, err := fetchRecipient()
	if err != nil {
		return nil, err
	}
	if err := seedRecipients(ns); err != nil {
		return nil, err
	}
	if _, err := AddRecipient(ns, hostLabel, key); err != nil {
		return nil, err
	}
	if _, err := Reseal(ns); err != nil {
		return nil, err
	}
	p, err := BuildPayload(ns)
	if err != nil {
		return nil, err
	}
	raw, err := p.Encode()
	if err != nil {
		return nil, err
	}
	got, err := send(raw)
	if err != nil {
		return nil, err
	}
	if got != len(p.Secrets) {
		return nil, fmt.Errorf("%s reports %d secrets received, sent %d", hostLabel, got, len(p.Secrets))
	}
	return &PushResult{Count: got, Recipient: strings.TrimSpace(key)}, nil
}

// ShortRecipient renders age1…xxxx (the last four characters).
func ShortRecipient(r string) string {
	if len(r) < 8 {
		return r
	}
	return "age1…" + r[len(r)-4:]
}

// RemoteErr turns a failed remote keys hop into an operator-facing error.
// An old remote gv answers `unknown command "keys"` (or, with keys but no
// receive, `unknown keys verb`); ssh's own failure is exit 255.
func RemoteErr(host, what string, code int, stderr string) error {
	msg := strings.TrimSpace(stderr)
	switch {
	case strings.Contains(msg, "unknown command") || strings.Contains(msg, "unknown keys verb"):
		return fmt.Errorf("%s's gv predates gv keys — run `gv update --yes` there", host)
	case code == 255:
		return fmt.Errorf("ssh to %s failed: %s", host, msg)
	case msg == "":
		return fmt.Errorf("%s: gv keys %s exited %d", host, what, code)
	}
	return fmt.Errorf("%s: gv keys %s: %s", host, what, msg)
}

// ParseRemoteRecipient takes the stdout of the remote `gv keys recipient`.
func ParseRemoteRecipient(host, out string) (string, error) {
	r := strings.TrimSpace(out)
	if r == "" || strings.ContainsAny(r, "\r\n") {
		return "", fmt.Errorf("%s: expected one age recipient from `gv keys recipient`, got %q", host, strings.TrimSpace(out))
	}
	return r, nil
}
