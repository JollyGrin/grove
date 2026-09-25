package chatweb

import "time"

// SetPoll shortens a server's stream tick for tests that watch several.
func SetPoll(s *Server, d time.Duration) { s.poll = d }
