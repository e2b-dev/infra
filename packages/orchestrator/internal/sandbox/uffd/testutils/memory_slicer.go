package testutils

import (
	"context"
	"maps"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

// memorySlicer exposes byte slice via the Slicer interface.
// This is used for testing purposes.
type memorySlicer struct {
	content  []byte
	pagesize int64
	size     int64

	offsets map[int64]struct{}
	mu      sync.Mutex
}

var _ block.Slicer = (*memorySlicer)(nil)

func newMemorySlicer(content []byte, pagesize int64) *memorySlicer {
	return &memorySlicer{
		content:  content,
		pagesize: pagesize,
		size:     int64(len(content)),
	}
}

func (s *memorySlicer) Slice(_ context.Context, offset, size int64) ([]byte, error) {
	s.mu.Lock()
	s.offsets[offset] = struct{}{}
	s.mu.Unlock()

	return s.content[offset : offset+size], nil
}

func (s *memorySlicer) Size() (int64, error) {
	return s.size, nil
}

func (s *memorySlicer) BlockSize() int64 {
	return s.pagesize
}

func (s *memorySlicer) Content() []byte {
	return s.content
}

// Offsets returns offsets of the content that were accessed via the Slice method.
func (s *memorySlicer) SlicedOffsets() map[int64]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.offsets)
}
