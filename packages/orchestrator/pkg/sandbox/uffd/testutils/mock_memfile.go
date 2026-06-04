//go:build linux

package testutils

import (
	"context"
	"testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// MockMemfile is a mock implementation of block.ReadonlyDevice for testing.
type MockMemfile struct {
	t *testing.T
}

// NewMockMemfile creates a new mock memfile for testing.
func NewMockMemfile(t *testing.T) *MockMemfile {
	return &MockMemfile{t: t}
}

// ReadAt implements block.ReadonlyDevice.
func (m *MockMemfile) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	// Return zeros for testing
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// Size implements block.ReadonlyDevice.
func (m *MockMemfile) Size(ctx context.Context) (int64, error) {
	return 1024 * 1024 * 1024, nil // 1GB
}

// Close implements io.Closer.
func (m *MockMemfile) Close() error {
	return nil
}

// Slice implements block.Slicer.
func (m *MockMemfile) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	data := make([]byte, length)
	_, err := m.ReadAt(ctx, data, off)
	return data, err
}

// BlockSize implements block.ReadonlyDevice.
func (m *MockMemfile) BlockSize() int64 {
	return 4096
}

// Header implements block.ReadonlyDevice.
func (m *MockMemfile) Header() *header.Header {
	return &header.Header{}
}

// SwapHeader implements block.ReadonlyDevice.
func (m *MockMemfile) SwapHeader(h *header.Header) {
	// No-op for mock
}
