package audit

import "fmt"

// SweepOffer is one proposed interactive sweep action (also the row shape
// of the `gv sweep --json` dry-run contract). Building offers is pure and
// table-tested here; confirming and acting stays in cmd/gv — audit
// reports, sweep offers, the human disposes.
type SweepOffer struct {
	Ticket string `json:"ticket"`
	Class  Class  `json:"class"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}

// SweepOffers maps audited task rows to sweep offers: merged → done,
// abandoned → untrack --rm, idle → pause (grove-92). A paused task is the
// operator's bookmark and yields NO offer of any kind — checked on the
// paused fact, not just the class, because classification precedence lets
// Merged outrank Paused (a paused task whose PR merged still reads
// Merged; sweep must still keep its hands off — only `gv adopt` or an
// explicit `gv untrack`/`gv done` touches a paused task).
func SweepOffers(rows []TaskResult) []SweepOffer {
	var offers []SweepOffer
	for _, r := range rows {
		if r.Facts.Paused {
			continue
		}
		switch r.Class {
		case Merged:
			detail := ""
			if r.PR != nil {
				detail = fmt.Sprintf("PR #%d merged", r.PR.Number)
			}
			offers = append(offers, SweepOffer{Ticket: r.Ticket, Class: r.Class, Action: "done (full cleanup incl. remote branch)", Detail: detail})
		case Abandoned:
			offers = append(offers, SweepOffer{Ticket: r.Ticket, Class: r.Class, Action: "untrack --rm (worktree + local branch)", Detail: "remote branch kept"})
		case Idle:
			offers = append(offers, SweepOffer{Ticket: r.Ticket, Class: r.Class, Action: "pause (kill window; resume: gv adopt)"})
		}
	}
	return offers
}
