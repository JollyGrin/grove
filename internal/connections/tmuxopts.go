package connections

// grove-169: the tmux-config rows. A fresh installer whose dotfiles set
// `base-index 1` + `pane-base-index 1` broke the cockpit (grove-168) and
// nothing pointed at the cause; `gv doctor` is what they run when
// something is wrong, so it surfaces every tmux option grove cares about
// that is set away from its default. Quiet by construction: a row only
// EXISTS for a non-default option, so a stock-config board is unchanged.

import "fmt"

// tmuxOption is one global tmux option doctor watches.
type tmuxOption struct {
	name    string
	def     string // tmux's own default
	warn    bool   // non-default is a warn row (else an informational ✓ row)
	info    string // the Info text for a non-default value
	fix     string // the warn row's remedy
	explain string // the Title suffix after "<name> <value>"
}

// tmuxWatchedOptions is the list, in render order. base-index /
// pane-base-index / renumber-windows are supported since grove-168
// (pane and window targets resolve by %N / @N id, never by index), so
// they are information, not a problem. allow-rename is a different knob
// from the automatic-rename grove pins off per window: with it on, a
// foreground program can retitle its window via escape sequence, which
// breaks grove's window-name resolution for workers.
var tmuxWatchedOptions = []tmuxOption{
	{name: "base-index", def: "0", info: "non-default, supported since #168"},
	{name: "pane-base-index", def: "0", info: "non-default, supported since #168"},
	{name: "renumber-windows", def: "off", info: "non-default, supported since #168"},
	{
		name: "allow-rename", def: "off", warn: true,
		info: "foreground programs can rename windows via escape sequence — can break grove's window-name resolution for workers",
		fix:  "set -g allow-rename off (in ~/.tmux.conf)",
	},
}

// tmuxOptionConnections returns one row per watched option whose global
// value differs from tmux's default; none when tmux is missing (the
// binary row already covers that), when the seam is absent, or when the
// query fails. The query runs once here, at manifest-build time, so the
// row set itself is the signal — a default-config machine prints no new
// lines.
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
	values := env.TmuxGlobalOptions(names...)
	var conns []Connection
	for _, o := range tmuxWatchedOptions {
		v, ok := values[o.name]
		if !ok || v == o.def {
			continue
		}
		st := Status{State: StateOK, Info: o.info}
		if o.warn {
			st.State = StateWarn
		}
		conns = append(conns, Connection{
			ID:          "tmux-option:" + o.name,
			Kind:        KindTmuxOption,
			Severity:    SeverityWarn,
			RequiredFor: []string{"grab", "ui"},
			Title:       fmt.Sprintf("tmux %s %s", o.name, v),
			Fix:         o.fix,
			Check:       func(Env) Status { return st },
		})
	}
	return conns
}
