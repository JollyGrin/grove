package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/secrets"
	"github.com/JollyGrin/grove/internal/workspace"
)

const keysUsage = "usage: gv keys init | recipient | set NAME [--global | --workspace [L]] | ls [--json] | rm NAME [--global | --workspace [L]] | exec NAME[,NAME…] -- <cmd…>"

// keysArgs is the parsed tail of a `gv keys set|rm|ls` line.
type keysArgs struct {
	pos       []string
	global    bool
	workspace bool
	label     string // the optional L after --workspace
	json      bool
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
		return keysSet(ns, a.pos[0])
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

func keysSet(ns, name string) error {
	if !secrets.ValidName(name) {
		return fmt.Errorf("invalid secret name %q (want ^[A-Za-z_][A-Za-z0-9_]*$)", name)
	}
	val, err := readSecretValue(name)
	if err != nil {
		return err
	}
	if err := secrets.Set(ns, name, val); err != nil {
		return err
	}
	fmt.Printf("✓ stored %s in %s\n", name, ns)
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
		fmt.Printf("%-28s %-14s %d recipient(s)%s\n", r.Name, r.Namespace, r.Recipients, mark)
	}
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
