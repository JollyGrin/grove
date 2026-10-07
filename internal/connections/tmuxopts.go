package connections

// grove-169: the tmux-config rows. A fresh installer whose dotfiles set
// `base-index 1` broke grove (grove-168) with no diagnostic pointing at
// the cause; doctor now surfaces every non-default option grove's window
// handling depends on. Quiet when all is default: rows are built only for
// the options whose live value differs from tmux's default, so the board
// and its N/M count are untouched on a stock machine.

// tmuxOption is one global option doctor reads, with tmux's default and
// what a non-default value means for grove.
type tmuxOption struct {
	name  string
	def   string
	state State // StateOK = info (visible, not scary); StateWarn = can break grove
	info  string
	fix   string
}

// tmuxOptions is in render order.
var tmuxOptions = []tmuxOption{
	{name: "base-index", def: "0", state: StateOK, info: "non-default, supported since #168"},
	{name: "pane-base-index", def: "0", state: StateOK, info: "non-default, supported since #168"},
	{name: "renumber-windows", def: "off", state: StateOK, info: "non-default, supported since #168"},
	{
		name: "allow-rename", def: "off", state: StateWarn,
		info: "foreground programs can rename windows via escape sequence, which can break grove's worker window-name resolution",
		fix:  "set -gw allow-rename off   # in ~/.tmux.conf; separate from the automatic-rename grove pins off per window",
	},
}

// tmuxOptionConnections returns one row per tmux option whose live global
// value is not tmux's default. The machine is queried once here, at
// manifest-build time, because a "quiet when fine" check cannot be an
// always-present row: Render prints every row. Nil seam (fake env) or a
// missing tmux binary drops the section; an exec failure yields no rows
// rather than a false warning.
func tmuxOptionConnections(env Env) []Connection {
	if env.TmuxGlobalOptions == nil || env.LookPath == nil {
		return nil
	}
	if _, err := env.LookPath("tmux"); err != nil {
		return nil
	}
	names := make([]string, 0, len(tmuxOptions))
	for _, o := range tmuxOptions {
		names = append(names, o.name)
	}
	live := env.TmuxGlobalOptions(names...)
	var conns []Connection
	for _, o := range tmuxOptions {
		v, ok := live[o.name]
		if !ok || v == o.def {
			continue
		}
		o := o
		conns = append(conns, Connection{
			ID:          "tmux-option:" + o.name,
			Kind:        KindBinary,
			Severity:    SeverityWarn,
			RequiredFor: []string{"grab", "ui"},
			Title:       "tmux " + o.name + " " + v,
			Fix:         o.fix,
			Check: func(Env) Status {
				return Status{State: o.state, Info: o.info}
			},
		})
	}
	return conns
}
