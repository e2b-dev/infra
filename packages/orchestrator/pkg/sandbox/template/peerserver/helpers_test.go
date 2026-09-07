//go:build linux

package peerserver

// collectSender accumulates all data passed to Send, and records the size of
// each individual call so tests can assert the per-message bound the transport
// enforces, not just the reassembled payload.
type collectSender struct {
	data  []byte
	sends []int
}

func (s *collectSender) Send(chunk []byte) error {
	s.data = append(s.data, chunk...)
	s.sends = append(s.sends, len(chunk))

	return nil
}
