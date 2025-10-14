package testutils

import "context"

// contentSlicer exposes byte slice via the Slicer interface.
// This is used for testing purposes.
type contentSlicer struct {
	content  []byte
	pagesize int64
	size     int64
}

func newContentSlicer(content []byte, pagesize int64) *contentSlicer {
	return &contentSlicer{
		content:  content,
		pagesize: pagesize,
		size:     int64(len(content)),
	}
}

func (s *contentSlicer) Slice(_ context.Context, offset, size int64) ([]byte, error) {
	return s.content[offset : offset+size], nil
}

func (s *contentSlicer) Size() (int64, error) {
	return s.size, nil
}

func (s *contentSlicer) BlockSize() int64 {
	return s.pagesize
}
