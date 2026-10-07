package connections

// grove-169: the tmux-config rows. A fresh installer whose dotfiles set
// `base-index 1` + `pane-base-index 1` broke grove with no diagnostic
// pointing at the cause (grove-168). Doctor now reads the global options
// grove is sensitive to and reports only the non-default ones — a
// default-config machine sees no row at all, so the board stays quiet
// when nothing is worth a glance.

import "strings"

// tmuxOption is one global option doctor watches, with tmux's own
// default and whether a non-default value is merely informational
// (grove supports it since grove-168) or a real warning.
type tmuxOption struct {
	name    string
	def     string
	warn    bool
	warnMsg string
	fix     string
}

const tmuxOptionInfo = "non-default, supported since #168"

// tmuxWatchedOptions is the watch list in render order.
var tmuxWatchedOptions = []tmuxOption{
	{name: "base-index", def: "0"},
	{name: "pane-base-index", def: "0"},
	{name: "renumber-windows", def: "off"},
	{
		name: "allow-rename", def: "off", warn: true,
		warnMsg: "foreground programs can rename windows via escape sequence and break grove's window-name resolution (worker windows are pinned off; the cockpit and adopted windows are not)",
		fix:     "set-option -g allow-rename off   # in ~/.tmux.conf",
	},
}

// tmuxOptionConnections returns one row per watched option whose value
// differs from tmux's default. No rows when tmux is absent (the binary
// row already says so), when the env has no reader, or when every option
// is at its default. The probe runs here, once, rather than per Check:
// a row's existence is what the probe decides.
func tmuxOptionConnections(env Env) []Connection {
	if env.TmuxGlobalOptions == nil || env.LookPath == nil {
		return nil
	}
	if _, err := env.LookPath("tmux"); err != nil {
		return nil
	}
	names := make([]string, 0, len(tmuxWatchedOptions))
	for _, o := range tmuxWatchedOptions {
		names = append(names, o.name)
	}
	got := env.TmuxGlobalOptions(names...)
	var conns []Connection
	for _, o := range tmuxWatchedOptions {
		v, ok := got[o.name]
		if !ok || strings.TrimSpace(v) == o.def {
			continue
		}
		st := Status{State: StateOK, Info: tmuxOptionInfo}
		if o.warn {
			st = Status{State: StateWarn, Info: o.warnMsg}
		}
		conns = append(conns, Connection{
			ID:          "tmux-option:" + o.name,
			Kind:        KindTmuxOption,
			Severity:    SeverityWarn,
			RequiredFor: []string{"grab", "ui"},
			Title:       "tmux " + o.name + " " + v,
			Fix:         o.fix,
			Check:       func(Env) Status { return st },
		})
	}
	return conns
}
