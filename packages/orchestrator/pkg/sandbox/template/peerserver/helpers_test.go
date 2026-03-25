package peerserver

// collectSender accumulates all data passed to Send.
type collectSender struct {
	data []byte
}

func (s *collectSender) Send(chunk []byte) error {
	s.data = append(s.data, chunk...)

	return nil
}
