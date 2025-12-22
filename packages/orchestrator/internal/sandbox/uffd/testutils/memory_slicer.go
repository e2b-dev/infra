package testutils

import (
	"context"
)

// MemorySlicer exposes byte slice via the Slicer interface.
// This is used for testing purposes.
type MemorySlicer struct {
	content  []byte
	pagesize int64
}

func NewMemorySlicer(content []byte, pagesize int64) *MemorySlicer {
	return &MemorySlicer{
		content:  content,
		pagesize: pagesize,
	}
}

func (s *MemorySlicer) Slice(_ context.Context, offset, size int64) ([]byte, error) {
	return s.content[offset : offset+size], nil
}

func (s *MemorySlicer) Size() (int64, error) {
	return int64(len(s.content)), nil
}

func (s *MemorySlicer) Content() []byte {
	return s.content
}

func (s *MemorySlicer) BlockSize() int64 {
	return s.pagesize
}
