package ioutils

import "io"

// MultiCloser wraps a reader and closes multiple underlying closers.
type MultiCloser struct {
	io.Reader

	closers []io.Closer
}

// NewMultiCloser creates a new MultiCloser that wraps the given reader
// and closes all provided closers when Close is called.
func NewMultiCloser(reader io.Reader, closers ...io.Closer) *MultiCloser {
	return &MultiCloser{
		Reader:  reader,
		closers: closers,
	}
}

func (m *MultiCloser) Close() error {
	var firstErr error
	for _, c := range m.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}
