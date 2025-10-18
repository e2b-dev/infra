package testutils

import "context"

// contentSlicer exposes byte slice via the Slicer interface.
// This is used for testing purposes.
type contentSlicer struct {
	content []byte
}

func newContentSlicer(content []byte) *contentSlicer {
	return &contentSlicer{content: content}
}

func (s *contentSlicer) Slice(_ context.Context, offset, size int64) ([]byte, error) {
	return s.content[offset : offset+size], nil
}
