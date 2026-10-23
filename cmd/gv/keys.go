package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/JollyGrin/grove/internal/account"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/remote"
	"github.com/JollyGrin/grove/internal/secrets"
	"github.com/JollyGrin/grove/internal/workspace"
)

const keysUsage = "usage: gv keys init | recipient | set NAME [--type TEXT] [--reveal N] [--global | --workspace [L]] | ls [--json] | rm NAME [--global | --workspace [L]] | exec NAME[,NAME…] -- <cmd…> | push --host H [--global | --workspace [L]] | reseal [--global | --workspace [L]] | import [PATH] [--rm] [--force] [--reveal N]"

// keysArgs is the parsed tail of a `gv keys set|rm|ls` line.
type keysArgs struct {
	pos       []string
	global    bool
	workspace bool
	label     string // the optional L after --workspace
	json      bool
	typ       string // --type
	reveal    int    // --reveal
}

// parseKeysArgs reads NAME plus the namespace flags. `--workspace` takes
// the next argument as its label L only when NAME has already been seen,
// so `set NAME --workspace L` is unambiguous and `set --workspace NAME`
// means the ambient workspace.
func parseKeysArgs(args []string) (keysArgs, error) {
	var a keysArgs
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--global":
			a.global = true
		case arg == "--json":
			a.json = true
		case arg == "--type" || arg == "--reveal":
			if i+1 >= len(args) {
				return a, fmt.Errorf("%s needs a value", arg)
			}
			i++
			if arg == "--type" {
				a.typ = args[i]
				break
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return a, fmt.Errorf("--reveal wants a non-negative number, got %q", args[i])
			}
			a.reveal = n
		case arg == "--workspace":
			a.workspace = true
			if len(a.pos) > 0 && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				a.label = args[i]
			}
		case strings.HasPrefix(arg, "-") && arg != "-":
			return a, fmt.Errorf("unknown flag %s\n%s", arg, keysUsage)
		default:
			a.pos = append(a.pos, arg)
		}
	}
	if a.global && a.workspace {
		return a, errors.New("--global and --workspace are mutually exclusive")
	}
	return a, nil
}

// namespace resolves where set/rm act: --global, --workspace [L], else
// the ambient workspace's label, else global.
func (a keysArgs) namespace() (string, error) {
	ambientLabel := ""
	if ambient.ws != nil {
		ambientLabel = ambient.ws.Label
	}
	switch {
	case a.global:
		return secrets.Global, nil
	case a.workspace && a.label != "":
		return a.label, nil
	case a.workspace && ambientLabel == "":
		return "", errors.New("--workspace needs a label (gv keys set NAME --workspace L) or to run inside a workspace")
	case ambientLabel != "":
		return ambientLabel, nil
	}
	return secrets.Global, nil
}

func cmdKeys(args []string) error {
	if len(args) == 0 {
		return errors.New(keysUsage)
	}
	verb, rest := args[0], args[1:]
	if verb == "exec" {
		return keysExec(rest)
	}
	if verb == "env" {
		return keysEnv(os.Stdout, rest)
	}
	switch verb {
	case "push":
		return keysPush(rest)
	case "reseal":
		return keysReseal(rest)
	case "receive":
		return keysReceive(os.Stdin, os.Stdout)
	case "import":
		return keysImport(os.Stdout, rest)
	}
	a, err := parseKeysArgs(rest)
	if err != nil {
		return err
	}
	switch verb {
	case "init":
		return keysInit(os.Stdout)
	case "recipient":
		if len(a.pos) != 0 {
			return errors.New("usage: gv keys recipient")
		}
		r, err := secrets.Recipient()
		if err != nil {
			return err
		}
		fmt.Println(r)
		return nil
	case "set":
		if len(a.pos) > 1 {
			return errors.New("gv keys set takes only a NAME — the value is never an argument (it would sit in ps and shell history); pipe it on stdin or type it at the hidden prompt")
		}
		if len(a.pos) != 1 {
			return errors.New("usage: gv keys set NAME [--global | --workspace [L]]  (value on stdin, or hidden prompt)")
		}
		ns, err := a.namespace()
		if err != nil {
			return err
		}
		return keysSet(ns, a.pos[0], a.typ, a.reveal)
	case "ls":
		if len(a.pos) != 0 {
			return errors.New("usage: gv keys ls [--json]")
		}
		return keysLs(a.json)
	case "rm":
		if len(a.pos) != 1 {
			return errors.New("usage: gv keys rm NAME [--global | --workspace [L]]")
		}
		ns, err := a.namespace()
		if err != nil {
			return err
		}
		if err := secrets.Remove(ns, a.pos[0]); err != nil {
			return err
		}
		fmt.Printf("✓ removed %s from %s\n", a.pos[0], ns)
		return nil
	}
	return fmt.Errorf("unknown keys verb %q\n%s", verb, keysUsage)
}

// keysEnv is the internal verb the launch wrap evals: the export lines for
// exactly the secrets one profile references. Never to a terminal — the
// output is plaintext. A missing name is a non-zero exit that names it, so
// the wrap stops before `exec`.
func keysEnv(w io.Writer, args []string) error {
	if (len(args) != 2 && len(args) != 4) || args[0] != "--profile" || (len(args) == 4 && args[2] != "--workspace-root") {
		return errors.New("usage: gv keys env --profile NAME [--workspace-root DIR]  (internal: the launch wrap evals it)")
	}
	// A worker's cwd is its worktree, which may live outside the workspace
	// and not walk up (the run-setup precedent): the root travels in argv.
	if len(args) == 4 {
		ambient.ws = workspace.Find(args[3])
	}
	if f, ok := w.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return errors.New("gv keys env prints secrets in plain text and refuses a terminal; it is meant for the launch wrap (eval \"$(gv keys env --profile NAME)\")")
	}
	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	_, p, err := cfg.ResolveProfile(args[1], nil)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("no model profile %q", args[1])
	}
	label := ""
	if ambient.ws != nil {
		label = ambient.ws.Label
	}
	out, err := secrets.ExportLines(label, config.ProfileSecretNames(p))
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			ns := "global"
			if label != "" {
				ns = label + ", global"
			}
			return fmt.Errorf("profile %q: %w — searched process env, %s; set it with `gv keys set NAME` (on a remote host: `gv keys push --host`)", args[1], err, ns)
		}
		return fmt.Errorf("profile %q: %w", args[1], err)
	}
	_, err = io.WriteString(w, out)
	return err
}

// keysInit generates this host's identity (idempotent) and says what it
// is and is not. It prints the recipient only — never the identity.
func keysInit(w io.Writer) error {
	r, created, err := secrets.InitIdentity()
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(w, "✓ generated this host's identity: %s\n", secrets.IdentityPath())
	} else {
		fmt.Fprintf(w, "• this host already has an identity: %s\n", secrets.IdentityPath())
	}
	fmt.Fprintf(w, "recipient: %s\n", r)
	fmt.Fprintln(w, "losing this identity loses this host's access, not the secrets — other recipient hosts keep theirs.")
	return nil
}

// initKeysStep is the wizard's keys step: nothing unless the operator (or
// --yes) said on; idempotent, so a second run only says it is present.
func initKeysStep(w io.Writer, on bool) error {
	if !on {
		return nil
	}
	return keysInit(w)
}

// readSecretValue takes the value from stdin when piped, else a hidden
// prompt on the terminal. Never from argv.
func readSecretValue(name string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprintf(os.Stderr, "value for %s (hidden): ", name)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return b, err
	}
	const limit = 1 << 20
	b, err := io.ReadAll(io.LimitReader(os.Stdin, limit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, fmt.Errorf("value exceeds %d bytes", limit)
	}
	return b, nil
}

func keysSet(ns, name, typ string, reveal int) error {
	if !secrets.ValidName(name) {
		return fmt.Errorf("invalid secret name %q (want ^[A-Za-z_][A-Za-z0-9_]*$)", name)
	}
	val, err := readSecretValue(name)
	if err != nil {
		return err
	}
	previewErr, err := secrets.SetWithMeta(ns, name, val, typ, reveal)
	if err != nil {
		return err
	}
	fmt.Printf("✓ stored %s in %s\n", name, ns)
	if previewErr != nil {
		fmt.Printf("• no preview stored: %v\n", previewErr)
	}
	// Push is never automatic — a hint only, when others hold this namespace.
	if rs, err := secrets.Recipients(ns); err == nil && len(rs) > 1 {
		fmt.Printf("• %d other recipients — gv keys push --host … to sync\n", len(rs)-1)
	}
	return nil
}

func keysLs(asJSON bool) error {
	label := ""
	if ambient.ws != nil {
		label = ambient.ws.Label
	}
	rows, err := secrets.Rows(label)
	if err != nil {
		return err
	}
	if asJSON {
		return emitJSON("keys", rows)
	}
	if len(rows) == 0 {
		fmt.Println("no keys stored — gv keys set NAME")
		return nil
	}
	for _, r := range rows {
		mark := ""
		if r.Shadowed {
			mark = "  (shadowed by this workspace)"
		}
		preview := r.Preview
		if preview == "" {
			preview = "***"
		}
		fmt.Printf("%-28s %-14s %-24s %-26s %d recipient(s)%s\n", r.Name, r.Namespace, r.Type, preview, r.Recipients, mark)
	}
	return nil
}

// parseNSFlags reads `[--global | --workspace [L]]` (plus --host for push)
// where --workspace takes the next non-flag word as its label — unlike
// parseKeysArgs there is no NAME to disambiguate against.
func parseNSFlags(args []string, allowHost bool) (a keysArgs, host string, err error) {
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--global":
			a.global = true
		case arg == "--workspace":
			a.workspace = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				a.label = args[i]
			}
		case allowHost && arg == "--host":
			if i+1 >= len(args) {
				return a, "", errors.New("--host needs a value")
			}
			i++
			host = args[i]
		case allowHost && strings.HasPrefix(arg, "--host="):
			host = strings.TrimPrefix(arg, "--host=")
		default:
			return a, "", fmt.Errorf("unexpected argument %q\n%s", arg, keysUsage)
		}
	}
	if a.global && a.workspace {
		return a, "", errors.New("--global and --workspace are mutually exclusive")
	}
	return a, host, nil
}

func keysReseal(args []string) error {
	a, _, err := parseNSFlags(args, false)
	if err != nil {
		return err
	}
	ns, err := a.namespace()
	if err != nil {
		return err
	}
	n, err := secrets.Reseal(ns)
	if err != nil {
		return err
	}
	fmt.Printf("✓ resealed %d secrets in %s\n", n, ns)
	return nil
}

// keysReceive is the remote half of push: ciphertext on stdin, the count
// of secrets on stdout. Refuses a terminal — nobody types this.
func keysReceive(in *os.File, w io.Writer) error {
	if term.IsTerminal(int(in.Fd())) {
		return errors.New("gv keys receive is internal: it reads a payload from stdin (gv keys push --host H sends it)")
	}
	n, err := secrets.Receive(in)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, n)
	return nil
}

// keysPush shares one namespace with host H over the existing ssh hop.
// Namespace defaults to global, not the ambient workspace: the remote has
// no ambient workspace either (the relay does not cd).
func keysPush(args []string) error {
	a, host, err := parseNSFlags(args, true)
	if err != nil {
		return err
	}
	if host == "" {
		return errors.New("usage: gv keys push --host H [--global | --workspace [L]]")
	}
	ns := secrets.Global
	if a.workspace {
		if ns, err = a.namespace(); err != nil {
			return err
		}
	}
	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	res, err := secrets.Push(ns, host,
		func() (string, error) {
			var out, errb bytes.Buffer
			code, err := remote.RunDetached(cfg, host, "keys", []string{"recipient"}, &out, &errb)
			if err != nil {
				return "", err
			}
			if code != 0 {
				return "", secrets.RemoteErr(host, "recipient", code, errb.String())
			}
			return secrets.ParseRemoteRecipient(host, out.String())
		},
		func(payload []byte) (int, error) {
			var out, errb bytes.Buffer
			code, err := remote.RunWithInput(cfg, host, "keys", []string{"receive"}, bytes.NewReader(payload), &out, &errb)
			if err != nil {
				return 0, err
			}
			if code != 0 {
				return 0, secrets.RemoteErr(host, "receive", code, errb.String())
			}
			n, err := strconv.Atoi(strings.TrimSpace(out.String()))
			if err != nil {
				return 0, fmt.Errorf("%s: unexpected reply from `gv keys receive`: %q", host, strings.TrimSpace(out.String()))
			}
			return n, nil
		})
	if err != nil {
		return err
	}
	fmt.Printf("✓ %d secrets → %s (%s, namespace %s)\n", res.Count, host, secrets.ShortRecipient(res.Recipient), ns)
	return nil
}

func keysIdentityPresent() bool {
	_, err := os.Stat(secrets.IdentityPath())
	return err == nil
}

const keysExecHelp = `usage: gv keys exec NAME[,NAME…] -- <cmd> [args…]

Runs <cmd> with exactly the named secrets added to ITS environment — no shell,
argv passed as-is. Output is the command's own. The exit code is the command's.

The reference must be expanded inside the child, never by your shell:

  # wrong — your shell expands $OPENROUTER_API_KEY (unset there) to ""
  gv keys exec OPENROUTER_API_KEY -- curl -s -H "Authorization: Bearer $OPENROUTER_API_KEY" https://openrouter.ai/api/v1/credits

  # right — single quotes hand the reference to the child's sh
  gv keys exec OPENROUTER_API_KEY -- sh -c 'curl -s -H "Authorization: Bearer $OPENROUTER_API_KEY" https://openrouter.ai/api/v1/credits'
`

// parseKeysExec splits `NAME[,NAME…] -- cmd args…`. Everything after the
// first `--` is the command, verbatim.
func parseKeysExec(args []string) (names, argv []string, err error) {
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || sep != 1 || len(args) < 3 {
		return nil, nil, errors.New("usage: gv keys exec NAME[,NAME…] -- <cmd> [args…]  (gv keys exec --help)")
	}
	for _, n := range strings.Split(args[0], ",") {
		if n == "" {
			return nil, nil, fmt.Errorf("empty name in %q\nusage: gv keys exec NAME[,NAME…] -- <cmd> [args…]", args[0])
		}
		names = append(names, n)
	}
	return names, args[2:], nil
}

// emptyArgWarning is the caller-shell-expansion tell: an empty argument
// is what `$NAME` becomes when the caller's shell expands it. "" when argv
// has none.
func emptyArgWarning(argv []string) string {
	for _, a := range argv {
		if a == "" {
			return `gv keys exec: warning: an argument after -- is empty — if you wrote $NAME, YOUR shell expanded it (to "") before gv ran.
  right: gv keys exec NAME -- sh -c 'curl -H "Authorization: Bearer $NAME" …'   (single quotes: the child's sh expands it)
`
		}
	}
	return ""
}

// keysExec resolves the named secrets, then replaces this process with
// the command: the value exists here only between decrypt and exec.
func keysExec(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Print(keysExecHelp)
		return nil
	}
	names, argv, err := parseKeysExec(args)
	if err != nil {
		return err
	}
	label := ""
	if ambient.ws != nil {
		label = ambient.ws.Label
	}
	env, err := secrets.ExecEnv(label, names, os.Environ())
	if err != nil {
		return err
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	if w := emptyArgWarning(argv); w != "" {
		fmt.Fprint(os.Stderr, w)
	}
	return syscall.Exec(path, argv, env)
}

// legacyEnvPresent reports whether the pre-store secrets file exists.
func legacyEnvPresent() bool {
	_, err := os.Stat(secrets.LegacyEnvPath())
	return err == nil
}

// keysImportStep is the wizard's import step: encrypt the legacy .env and
// the account tokens, delete nothing.
func keysImportStep(w io.Writer) error {
	return keysImport(w, nil)
}

// keysImport moves the plaintext secrets into the encrypted store: every
// assignment of PATH (default ~/.config/grove/.env) and every account
// token file, into the global namespace. Prints names, never values.
func keysImport(w io.Writer, args []string) error {
	var path string
	var rm, force bool
	reveal := 0
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--rm":
			rm = true
		case a == "--force":
			force = true
		case a == "--reveal":
			if i+1 >= len(args) {
				return errors.New("--reveal needs a value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return fmt.Errorf("--reveal wants a non-negative number, got %q", args[i])
			}
			reveal = n
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %s\n%s", a, keysUsage)
		case path == "":
			path = a
		default:
			return errors.New("usage: gv keys import [PATH] [--rm] [--force] [--reveal N]")
		}
	}
	explicit := path != ""
	if path == "" {
		path = secrets.LegacyEnvPath()
	}
	if _, _, err := secrets.InitIdentity(); err != nil {
		return err
	}
	report := func(res []secrets.ImportResult) {
		for _, r := range res {
			switch r.Status {
			case secrets.ImportStored:
				fmt.Fprintf(w, "✓ imported %s\n", r.Name)
				if r.PreviewErr != nil {
					fmt.Fprintf(w, "• no preview stored for %s: %v\n", r.Name, r.PreviewErr)
				}
			case secrets.ImportSkipped:
				fmt.Fprintf(w, "• skipped %s — already in the store (--force to overwrite)\n", r.Name)
			default:
				fmt.Fprintf(w, "✗ %s: %v\n", r.Name, r.Err)
			}
		}
	}

	envRes, envErr := secrets.ImportDotenv(path, force, reveal)
	switch {
	case envErr == nil:
		fmt.Fprintf(w, "%s:\n", path)
		report(envRes)
	case errors.Is(envErr, os.ErrNotExist) && !explicit:
		// No .env is fine — tokens may still be waiting.
	default:
		return envErr
	}

	accts := account.Open(config.StateDir())
	tokRes, err := accts.ImportTokens(force, reveal, rm)
	if err != nil {
		return err
	}
	if len(tokRes) > 0 {
		fmt.Fprintln(w, "account tokens:")
		report(tokRes)
	}
	if envErr != nil && len(tokRes) == 0 {
		return fmt.Errorf("nothing to import — %s does not exist and no account tokens are stored", path)
	}

	if !rm {
		if envErr == nil {
			fmt.Fprintf(w, "• %s is kept — delete it with `gv keys import --rm` once workers run clean\n", path)
		}
		return nil
	}
	if envErr == nil {
		envOK := true
		for _, r := range envRes {
			if r.Status != secrets.ImportStored {
				envOK = false
			}
		}
		if !envOK {
			fmt.Fprintf(w, "• %s kept — a name was skipped or failed; fix that (or --force) and rerun with --rm\n", path)
		} else if err := os.Remove(path); err != nil {
			return err
		} else {
			fmt.Fprintf(w, "✓ deleted %s\n", path)
		}
	}
	fmt.Fprintln(w, "• a `set -a; . ~/.config/grove/.env` line in a shell rc now reads nothing — remove it")
	return nil
}
