// Package account is grove's store of Claude subscription accounts
// (claude-accounts design, docs/plans/2026-10-07-claude-accounts-design.md
// §Decisions 4, 6, 7). An account is a name plus a one-year OAuth token
// minted by `claude setup-token`; the interactive /login is always present
// as the implicit account `login`, which has no token.
//
// Interim storage (Decision 4) lives under <state dir>/accounts/:
// <name>.token (0600), <name>.minted (the add date, standing in for the
// token's mint date), and `active` holding a bare name. The dir is 0700.
// Every reader of a token goes through TokenSource, the one seam: it
// answers from the encrypted secrets store (`gv keys import` moves tokens
// there as CLAUDE_ACCOUNT_<NAME>) and falls back to the file.
//
// The invariant: a token VALUE never leaves this package's files — no
// error, listing, or --json row carries it. Names are public; values are
// not.
package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/secrets"
	"github.com/JollyGrin/grove/internal/state"
)

// Login is the reserved name of the implicit account: the interactive
// /login credentials Claude Code keeps itself.
const Login = "login"

// ExpiryWarnDays is when doctor starts warning; ExpiryDays is when the
// token is dead: setup-token tokens expire after one year (design
// §Mechanism, documented cost 4).
const (
	ExpiryWarnDays = 330
	ExpiryDays     = 365
)

// ConnectorsNote is the dim caveat on every token row (design §Mechanism,
// documented cost 1).
const ConnectorsNote = "model requests only — no claude.ai connectors / Remote Control"

// maxTokenBytes bounds what `add` reads from stdin; a setup-token value is
// ~100 bytes, so anything near this is a paste of the wrong thing.
const maxTokenBytes = 4096

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ErrUsage marks a caller mistake (a bad or reserved name) as opposed to a
// store failure; the CLI renders it as a usage error.
var ErrUsage = errors.New("usage")

// ValidateName accepts ^[a-z][a-z0-9-]{0,31}$ and refuses the reserved
// `login`.
func ValidateName(name string) error {
	if name == Login {
		return fmt.Errorf("%w: %q is reserved for the interactive login", ErrUsage, Login)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%w: account name %q must match ^[a-z][a-z0-9-]{0,31}$", ErrUsage, name)
	}
	return nil
}

// Store is the accounts directory. Now is the clock expiry is judged by;
// nil = time.Now.
type Store struct {
	Dir string
	Now func() time.Time
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Open returns the store under a grove state dir.
func Open(stateDir string) Store { return Store{Dir: filepath.Join(stateDir, "accounts")} }

// Account is one configured token account.
type Account struct {
	Name      string
	TokenPath string
	Minted    time.Time // the add date; the token's mtime when the record is missing
}

func (s Store) tokenPath(name string) string  { return filepath.Join(s.Dir, name+".token") }
func (s Store) mintedPath(name string) string { return filepath.Join(s.Dir, name+".minted") }
func (s Store) activePath() string            { return filepath.Join(s.Dir, "active") }

// ensureDir creates the store dir 0700 and tightens an existing one.
func (s Store) ensureDir() error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(s.Dir, 0o700)
}

// writeFile0600 replaces path atomically (temp + rename) with mode 0600.
func writeFile0600(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if err := f.Chmod(0o600); err != nil {
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

// Add stores the token read from r under name, replacing any earlier one
// (re-adding is how a token is renewed), and records now as its mint
// date. The token is the trimmed input; empty or multi-word input is
// refused without echoing it. replaced reports an overwrite.
func (s Store) Add(name string, r io.Reader, now time.Time) (replaced bool, err error) {
	if err := ValidateName(name); err != nil {
		return false, err
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxTokenBytes+1))
	if err != nil {
		return false, fmt.Errorf("reading token from stdin: %w", err)
	}
	if len(raw) > maxTokenBytes {
		return false, fmt.Errorf("stdin holds more than %d bytes — paste only the token `claude setup-token` printed", maxTokenBytes)
	}
	tok := strings.TrimSpace(string(raw))
	if tok == "" {
		return false, fmt.Errorf("no token on stdin — pipe or paste what `claude setup-token` printed")
	}
	if strings.ContainsAny(tok, " \t\r\n") {
		return false, fmt.Errorf("stdin holds more than one word — paste only the token `claude setup-token` printed")
	}
	if err := s.ensureDir(); err != nil {
		return false, err
	}
	if _, err := os.Stat(s.tokenPath(name)); err == nil {
		replaced = true
	}
	inStore := stored(name)
	if inStore {
		replaced = true
	}
	// The mint record goes first, and is rolled back if the token swap
	// fails: a new token must never carry the old date (it would hide
	// expiry), and a failed add must leave the old token's date intact.
	oldMinted, mintedErr := os.ReadFile(s.mintedPath(name))
	if err := writeFile0600(s.mintedPath(name), []byte(now.UTC().Format(time.RFC3339)+"\n")); err != nil {
		return false, err
	}
	// A token already in the store is renewed there (TokenSource prefers
	// it) and no plaintext file is re-created.
	var werr error
	if inStore {
		werr = secrets.Set(secrets.Global, SecretName(name), []byte(tok))
		if werr == nil {
			if err := os.Remove(s.tokenPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				werr = err
			}
		}
	} else {
		werr = writeFile0600(s.tokenPath(name), []byte(tok+"\n"))
	}
	if err := werr; err != nil {
		if mintedErr == nil {
			_ = writeFile0600(s.mintedPath(name), oldMinted)
		} else {
			_ = os.Remove(s.mintedPath(name))
		}
		return false, err
	}
	return replaced, nil
}

// List returns every configured account, sorted by name. A missing store
// is an empty list.
func (s Store) List() ([]Account, error) {
	out, err := s.listFiles()
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, a := range out {
		have[a.Name] = true
	}
	// A token imported then removed from disk lives only in the store; its
	// .minted record (not a secret) is what names it here.
	if entries, err := os.ReadDir(s.Dir); err == nil {
		for _, e := range entries {
			name, ok := strings.CutSuffix(e.Name(), ".minted")
			if ok && !have[name] && ValidateName(name) == nil && stored(name) {
				out = append(out, s.account(name))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// listFiles is the accounts whose token is a file on disk.
func (s Store) listFiles() ([]Account, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Account
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".token")
		if !ok || e.IsDir() || ValidateName(name) != nil {
			continue
		}
		out = append(out, s.account(name))
	}
	return out, nil
}

// account reads one account's record: its mint date from <name>.minted,
// else the token file's mtime.
func (s Store) account(name string) Account {
	a := Account{Name: name, TokenPath: s.tokenPath(name)}
	if raw, err := os.ReadFile(s.mintedPath(name)); err == nil {
		a.Minted, _ = time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	}
	if a.Minted.IsZero() {
		if fi, err := os.Stat(a.TokenPath); err == nil {
			a.Minted = fi.ModTime()
		}
	}
	return a
}

// Expired reports a token at or past its one-year life.
func Expired(minted, now time.Time) bool { return AgeDays(minted, now) >= ExpiryDays }

// ExpiredFix is the remedy for an expired token.
func ExpiredFix(name string) string {
	return "re-run `claude setup-token`, then `gv account add " + name + "` with the new token"
}

// Names lists the selectable names: `login` first, then every account.
func (s Store) Names() []string {
	names := []string{Login}
	accts, _ := s.List()
	for _, a := range accts {
		names = append(names, a.Name)
	}
	return names
}

func (s Store) exists(name string) bool {
	if stored(name) {
		return true
	}
	fi, err := os.Stat(s.tokenPath(name))
	return err == nil && fi.Mode().IsRegular()
}

// SecretName is the encrypted-store name holding account name's token
// (gv keys import): upper-cased, `-` → `_`.
func SecretName(name string) string {
	return "CLAUDE_ACCOUNT_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

func stored(name string) bool { return secrets.Has(secrets.Global, SecretName(name)) }

// materialize writes the stored token to a 0600 file under the runtime
// dir (tmpfs where XDG_RUNTIME_DIR is set) so the launching shell can
// `cat` it — WrapAccount reads a path. "" when the store cannot answer.
func (s Store) materialize(name string) (string, error) {
	tok := secrets.Resolve("", SecretName(name))
	if tok == "" {
		return "", nil
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("grove-%d", os.Getuid()))
	}
	dir = filepath.Join(dir, "grove-accounts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name+".token")
	return p, writeFile0600(p, []byte(tok+"\n"))
}

// ImportTokens moves every <name>.token into the global secrets store as
// CLAUDE_ACCOUNT_<NAME>. Results are keyed by the store name, never the
// value. With rm, a token file is deleted only for a name imported by this
// call — a skipped name keeps its file, so a differing value is never lost.
// The .minted record is written first if absent: it is what names the
// account once the file is gone.
func (s Store) ImportTokens(force bool, reveal int, rm bool) ([]secrets.ImportResult, error) {
	accts, err := s.listFiles()
	if err != nil {
		return nil, err
	}
	vals := map[string]string{}
	byStore := map[string]string{}
	for _, a := range accts {
		raw, err := os.ReadFile(a.TokenPath)
		if err != nil {
			return nil, err
		}
		tok := strings.TrimSpace(string(raw))
		if tok == "" {
			continue
		}
		vals[SecretName(a.Name)] = tok
		byStore[SecretName(a.Name)] = a.Name
	}
	res := secrets.ImportValues(vals, force, reveal)
	if rm {
		for _, r := range res {
			if r.Status == secrets.ImportStored {
				if _, err := os.Stat(s.mintedPath(byStore[r.Name])); err != nil {
					a := s.account(byStore[r.Name])
					if err := writeFile0600(s.mintedPath(a.Name), []byte(a.Minted.UTC().Format(time.RFC3339)+"\n")); err != nil {
						return res, err
					}
				}
				if err := os.Remove(s.tokenPath(byStore[r.Name])); err != nil && !errors.Is(err, os.ErrNotExist) {
					return res, err
				}
			}
		}
	}
	return res, nil
}

func (s Store) notFound(name string) error {
	return fmt.Errorf("no account %q — configured: %s", name, strings.Join(s.Names(), ", "))
}

// Active returns the active account's name as recorded, "" for login. It
// does not check the account still exists (ActiveTokenPath does; doctor
// reports the dangling case).
func (s Store) Active() (string, error) {
	raw, err := os.ReadFile(s.activePath())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(raw))
	if name == Login {
		name = ""
	}
	return name, nil
}

// Use makes name the active account; `login` clears it.
func (s Store) Use(name string) error {
	if name == Login {
		err := os.Remove(s.activePath())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	if !s.exists(name) {
		return s.notFound(name)
	}
	if a := s.account(name); Expired(a.Minted, s.now()) {
		return fmt.Errorf("account %s's token EXPIRED (added %dd ago; tokens last one year) — %s",
			name, AgeDays(a.Minted, s.now()), ExpiredFix(name))
	}
	if err := s.ensureDir(); err != nil {
		return err
	}
	return writeFile0600(s.activePath(), []byte(name+"\n"))
}

// Remove deletes an account's token, mint record and recorded usage (a
// re-added name must not inherit the old subscription's numbers). When it was the
// active account, the active record goes too (the default falls back to
// login) and wasActive says so. Callers check InUse first.
func (s Store) Remove(name string) (wasActive bool, err error) {
	if err := ValidateName(name); err != nil {
		return false, err
	}
	if !s.exists(name) {
		return false, s.notFound(name)
	}
	if err := os.Remove(s.tokenPath(name)); err != nil && !(errors.Is(err, os.ErrNotExist) && stored(name)) {
		return false, err
	}
	if stored(name) {
		if err := secrets.Remove(secrets.Global, SecretName(name)); err != nil {
			return false, err
		}
	}
	_ = os.Remove(s.mintedPath(name))
	if err := os.Remove(s.UsagePath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if active, _ := s.Active(); active == name {
		wasActive = true
		if err := os.Remove(s.activePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return wasActive, err
		}
	}
	return wasActive, nil
}

// TokenSource is the seam every token reader goes through: the path of
// name's token file, which the launching shell reads (`cat`) so the value
// never enters argv or a send-keys line. A token held in the encrypted
// store (gv keys import) is decrypted to a 0600 file on the runtime dir.
func (s Store) TokenSource(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	if !s.exists(name) {
		return "", s.notFound(name)
	}
	// The encrypted store first, the plaintext file second (the one-release
	// window; gv-keys ticket 10 drops the file).
	if stored(name) {
		if p, err := s.materialize(name); err != nil {
			return "", err
		} else if p != "" {
			return p, nil
		}
	}
	return s.tokenPath(name), nil
}

// ActiveTokenPath is `gv account token-path`: the active account's token
// path, "" for login. An active record naming a missing account is an
// error, never a silent login.
func (s Store) ActiveTokenPath() (string, error) {
	active, err := s.Active()
	if err != nil || active == "" {
		return "", err
	}
	p, err := s.TokenSource(active)
	if err != nil {
		return "", fmt.Errorf("active account: %w", err)
	}
	return p, nil
}

// InUse lists the tickets of non-done tasks pinned to name, sorted.
func InUse(tasks []*state.Task, name string) []string {
	var out []string
	for _, t := range tasks {
		if t != nil && !t.Done && t.Account == name {
			out = append(out, t.Ticket)
		}
	}
	sort.Strings(out)
	return out
}

// PinnedDone lists the tickets of done tasks (done, untracked or handed
// off) still pinned to name, sorted: they don't block `rm`, but `gv adopt`
// would honour the pin and then refuse it.
func PinnedDone(tasks []*state.Task, name string) []string {
	var out []string
	for _, t := range tasks {
		if t != nil && t.Done && t.Account == name {
			out = append(out, t.Ticket)
		}
	}
	sort.Strings(out)
	return out
}

// LoginEmail reads the interactive login's email from ~/.claude.json
// (`oauthAccount.emailAddress`); "" when absent or unreadable.
func LoginEmail(home string) string {
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return ""
	}
	var doc struct {
		OAuthAccount struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	return doc.OAuthAccount.EmailAddress
}

// AgeDays is whole days from minted to now (never negative).
func AgeDays(minted, now time.Time) int {
	if minted.IsZero() || now.Before(minted) {
		return 0
	}
	return int(now.Sub(minted).Hours() / 24)
}

// Row is one `gv account ls` row and its --json shape (a plugin
// contract — additive-only, docs/plugins.md).
type Row struct {
	Name         string `json:"name"`
	Active       bool   `json:"active"`
	Email        string `json:"email,omitempty"`
	TokenAgeDays int    `json:"token_age_days"`
	Pinned       int    `json:"pinned"`
	Connectors   bool   `json:"connectors"`
	Expired      bool   `json:"expired"`
	Usage        *Usage `json:"usage,omitempty"` // only with --usage
}

// Rows builds the listing: the implicit login row first (with its email,
// connectors available), then every token account. pinned counts non-done
// tasks per account name.
func (s Store) Rows(home string, now time.Time, tasks []*state.Task) ([]Row, error) {
	accts, err := s.List()
	if err != nil {
		return nil, err
	}
	active, err := s.Active()
	if err != nil {
		return nil, err
	}
	rows := []Row{{
		Name:       Login,
		Active:     active == "",
		Email:      LoginEmail(home),
		Pinned:     len(InUse(tasks, Login)),
		Connectors: true,
	}}
	for _, a := range accts {
		rows = append(rows, Row{
			Name:         a.Name,
			Active:       a.Name == active,
			TokenAgeDays: AgeDays(a.Minted, now),
			Pinned:       len(InUse(tasks, a.Name)),
			Expired:      Expired(a.Minted, now),
		})
	}
	return rows, nil
}
