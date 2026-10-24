package connections

// grove-568: the kickoff-override row. A repo's `prompt:` replaces the
// embedded kickoff template wholesale, so it silently misses every later
// template move; `prompt_overlay:` is the supported way to customize.

import (
	"fmt"

	"github.com/JollyGrin/grove/internal/kickoff"
)

// kickoffConnections is the warn row, present only when at least one repo
// overrides the template wholesale — silent at zero.
func kickoffConnections(env Env) []Connection {
	if env.Cfg == nil || env.CfgErr != nil {
		return nil
	}
	n := kickoff.Overrides(kickoff.Report(env.Cfg.Repos))
	if n == 0 {
		return nil
	}
	return []Connection{{
		ID:          "kickoff-override",
		Kind:        KindFile,
		Severity:    SeverityWarn,
		RequiredFor: []string{"grab"},
		Title:       fmt.Sprintf("%d repo(s) override the kickoff template wholesale", n),
		Fix:         "gv brains (KICKOFFS lists them); migrate each prompt: to prompt_overlay:",
		Check:       func(Env) Status { return Status{State: StateWarn, Info: kickoff.OverrideNote} },
	}}
}
