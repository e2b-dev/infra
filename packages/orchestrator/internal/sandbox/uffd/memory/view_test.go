package memory

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
)

func TestView(t *testing.T) {
	pagesize := uint64(4096)

	data := testutils.RandomPages(pagesize, 128)

	size, err := data.Size()
	require.NoError(t, err)

	memoryArea, memoryStart, unmap, err := testutils.NewPageMmap(uint64(size), pagesize)
	require.NoError(t, err)

	defer unmap()

	n := copy(memoryArea[0:size], data.Content())
	require.Equal(t, int(size), n)

	m := NewMapping([]Region{
		{
			BaseHostVirtAddr: uintptr(memoryStart),
			Size:             uintptr(size),
			Offset:           uintptr(0),
			PageSize:         uintptr(pagesize),
		},
	})

	pc, err := NewView(os.Getpid(), m)
	require.NoError(t, err)

	defer pc.Close()

	for i := 0; i < int(size); i += int(pagesize) {
		readBytes := make([]byte, pagesize)
		_, err := pc.ReadAt(readBytes, int64(i))
		require.NoError(t, err)

		expectedBytes := data.Content()[i : i+int(pagesize)]

		if !bytes.Equal(readBytes, expectedBytes) {
			idx, want, got := testutils.FirstDifferentByte(readBytes, expectedBytes)
			t.Fatalf("content mismatch: want %v, got %v at index %d", want, got, idx)
		}
	}
}
