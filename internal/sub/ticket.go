package sub

// ForTicket filters records down to one ticket's runs — `gv cost
// --context`'s delegation section (grove-289). Zero records in, zero out:
// the section must work whether or not sub.jsonl exists yet.
func ForTicket(records []Record, ticket string) []Record {
	var out []Record
	for _, r := range records {
		if r.Ticket == ticket {
			out = append(out, r)
		}
	}
	return out
}
