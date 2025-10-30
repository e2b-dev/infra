package testutils

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

// MemorySlicer exposes byte slice via the Slicer interface.
// This is used for testing purposes.
type MemorySlicer struct {
	content  []byte
	pagesize int64

	accessed *block.Tracker
}

var _ block.Slicer = (*MemorySlicer)(nil)

func newMemorySlicer(content []byte, pagesize int64) *MemorySlicer {
	return &MemorySlicer{
		content:  content,
		pagesize: pagesize,
		accessed: block.NewTracker(pagesize),
	}
}

func (s *MemorySlicer) Slice(_ context.Context, offset, size int64) ([]byte, error) {
	for i := offset; i < offset+size; i += s.pagesize {
		s.accessed.Add(i)
	}

	return s.content[offset : offset+size], nil
}

func (s *MemorySlicer) Size() (int64, error) {
	return int64(len(s.content)), nil
}

func (s *MemorySlicer) Content() []byte {
	return s.content
}

// Offsets returns offsets of the content that were accessed via the Slice method.
func (s *MemorySlicer) Accessed() *block.Tracker {
	return s.accessed.Clone()
}
