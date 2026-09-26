package chatweb

import "time"

// SetPoll shortens a server's stream tick for tests that watch several.
func SetPoll(s *Server, d time.Duration) { s.poll = d }

// SetListFeed shortens the list stream's poll and keep-alive for tests.
func SetListFeed(s *Server, every, heartbeat time.Duration) {
	s.list.every, s.heartbeat = every, heartbeat
}
