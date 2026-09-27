package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"golang.org/x/term"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/feature"
	"github.com/JollyGrin/grove/internal/serve"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
)

// cmdServe is the `gv serve <slug>` / `gv serve stop <slug>` glue
// (grove-380, feature trains Decision 7). Decisions live in internal/serve.
func cmdServe(args []string) error {
	if len(args) > 0 && args[0] == "stop" {
		return cmdServeStop(args[1:])
	}
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	timeout := fs.Duration("timeout", 60*time.Second, "how long to wait for run.sh's GROVE_READY line")
	pos := parseAnywhere(fs, args)
	if len(pos) != 1 {
		return fmt.Errorf("usage: gv serve <slug> [--timeout 60s] | gv serve stop <slug>")
	}
	slug := pos[0]
	ws := ambient.ws
	if ws == nil {
		return fmt.Errorf("gv serve runs a workspace's .grove/run.sh — run it inside a workspace")
	}
	echoWorkspace()
	features, err := state.LoadFeatures(stateDir())
	if err != nil {
		return err
	}
	f := state.OpenFeature(features, slug)
	if f == nil {
		return fmt.Errorf("no open feature %q — `gv feature ls` lists them", slug)
	}
	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	repo, ok := cfg.Repos[f.Repo]
	if !ok {
		return fmt.Errorf("feature %s names repo %q, which is not in this workspace's config", slug, f.Repo)
	}

	script, sha, err := serve.ReadScript(ws.Root)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no %s — the serve contract: a script that reads GROVE_WORKTREE, GROVE_BRANCH, GROVE_PORT, GROVE_FEATURE and prints `%s <url-or-path>` once up", serve.ScriptPath(ws.Root), serve.ReadyMarker)
	}
	if err != nil {
		return err
	}
	ledger, err := state.LoadServe(stateDir())
	if err != nil {
		return err
	}
	if !serve.Trusted(ledger, sha) {
		if err := trustScript(ws.Root, script, sha); err != nil {
			return err
		}
	}

	session := cockpitSessionFor(ws)
	window := serve.Window(slug)
	if _, live := tmux.WindowIDExact(session, window); live {
		return fmt.Errorf("%q is already running in %s — `gv serve stop %s` first", window, session, slug)
	}
	wt := serve.WorktreePath(repo.Path, slug)
	tip, err := serve.PrepareWorktree(repo.Path, f.Branch, wt)
	if err != nil {
		return err
	}
	port := serve.Port(ledger, slug)
	if err := serve.WriteSnapshot(stateDir(), slug, script); err != nil {
		return err
	}
	if err := tmux.EnsureWorkspaceSession(session, ws.Root); err != nil {
		return err
	}
	logPath := serve.LogPath(stateDir(), slug)
	if _, err := tmux.NewWindowExec(session, window, wt,
		serve.Env(wt, f.Branch, slug, port),
		serve.Command(serve.SnapshotPath(stateDir(), slug), logPath, slug)); err != nil {
		return err
	}
	fmt.Printf("→ %s: run.sh on %s @ %s (port %d) in window %q — waiting up to %s\n",
		slug, f.Branch, shortSHA(tip), port, window, *timeout)
	alive := func() bool { _, ok := tmux.WindowIDExact(session, window); return ok }
	ready, err := serve.WaitReady(logPath, *timeout, 250*time.Millisecond, alive)
	if err != nil {
		return err
	}
	if err := state.Append(stateDir(), state.Event{Type: state.EvFeatureServed, Data: map[string]string{
		"slug": slug, "port": strconv.Itoa(port), "tip": tip, "window": window, "url": ready,
	}}); err != nil {
		return err
	}
	fmt.Printf("✓ %s served — `gv serve stop %s` stops it\n", slug, slug)
	fmt.Println(ready)
	return nil
}

// trustScript is the gate: on a TTY, show the script and its sha and ask;
// `y` records run_script_trusted. Non-TTY refuses. No flag bypasses it.
func trustScript(root string, script []byte, sha string) error {
	path := serve.ScriptPath(root)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("%s (sha256 %s) is not trusted — run `gv serve` in a terminal to review and trust it", path, sha)
	}
	fmt.Printf("── %s ──\n%s", path, script)
	if len(script) > 0 && script[len(script)-1] != '\n' {
		fmt.Println()
	}
	fmt.Printf("── sha256 %s ──\n", sha)
	fmt.Print("trust and run? [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if !serve.Confirmed(line) {
		return fmt.Errorf("not trusted — nothing ran")
	}
	return state.Append(stateDir(), state.Event{Type: state.EvRunScriptTrusted, Data: map[string]string{"sha256": sha}})
}

func cmdServeStop(args []string) error {
	fs := flag.NewFlagSet("serve stop", flag.ExitOnError)
	pos := parseAnywhere(fs, args)
	if len(pos) != 1 {
		return fmt.Errorf("usage: gv serve stop <slug>")
	}
	slug := pos[0]
	if err := feature.ValidateSlug(slug); err != nil {
		return err
	}
	stopped, err := stopServe(slug)
	if err != nil {
		return err
	}
	if !stopped {
		fmt.Printf("no %q window in %s — nothing to stop\n", serve.Window(slug), cockpitSessionFor(ambient.ws))
		return nil
	}
	fmt.Printf("✓ %s stopped\n", slug)
	return nil
}

// stopServe kills only the exact "▶ <slug>" window of the workspace
// session and records feature_serve_stopped; false when none was running.
func stopServe(slug string) (bool, error) {
	id, ok := tmux.WindowIDExact(cockpitSessionFor(ambient.ws), serve.Window(slug))
	if !ok {
		return false, nil
	}
	if err := tmux.KillWindowID(id); err != nil {
		return false, err
	}
	return true, state.Append(stateDir(), state.Event{Type: state.EvFeatureServeStopped, Data: map[string]string{"slug": slug}})
}

// teardownServe is `gv feature close`'s serve cleanup: stop the window,
// then remove the serve worktree — only the detached <slug>-serve checkout
// gv creates, never anything else.
func teardownServe(cfg *config.Config, f *state.Feature) {
	if stopped, err := stopServe(f.Slug); err != nil {
		fmt.Fprintf(os.Stderr, "warning: stopping serve: %v\n", err)
	} else if stopped {
		fmt.Printf("→ stopped %q\n", serve.Window(f.Slug))
	}
	repo, ok := cfg.Repos[f.Repo]
	if !ok {
		return
	}
	wt := serve.WorktreePath(repo.Path, f.Slug)
	ok, err := serve.Removable(repo.Path, wt)
	if err != nil || !ok {
		return
	}
	killPathProcesses(wt, f.Slug+" serve")
	if err := serve.RemoveWorktree(repo.Path, wt); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		return
	}
	fmt.Printf("→ removed serve worktree %s\n", wt)
}

// serveStatuses fills each open row's status.serve field: one ledger read, one
// run.sh hash, one tmux lookup and one local rev-parse per feature — no
// network, no fetch.
func serveStatuses(rows []feature.Row) {
	ledger, err := state.LoadServe(stateDir())
	if err != nil {
		return
	}
	scriptSHA := ""
	if ambient.ws != nil {
		if _, sha, err := serve.ReadScript(ambient.ws.Root); err == nil {
			scriptSHA = sha
		}
	}
	cfg, _ := loadCfg()
	session := cockpitSessionFor(ambient.ws)
	for i := range rows {
		r := &rows[i]
		if r.Closed != nil || r.Status == nil {
			continue
		}
		in := serve.StatusInput{ScriptSHA: scriptSHA, TrustedSHA: ledger.TrustedSHA, Served: ledger.Served[r.Slug]}
		_, in.WindowLive = tmux.WindowIDExact(session, serve.Window(r.Slug))
		if cfg != nil {
			if repo, ok := cfg.Repos[r.Repo]; ok {
				in.BranchTip = serve.BranchTip(repo.Path, r.Branch)
			}
		}
		st := serve.Derive(in)
		r.Serve = &st
	}
}
