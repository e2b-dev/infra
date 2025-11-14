package memory

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
)

func TestViewSingleRegionFullRead(t *testing.T) {
	t.Parallel()

	pagesize := uint64(4096)

	data := testutils.RandomPages(pagesize, 128)

	size, err := data.Size()
	require.NoError(t, err)

	memoryArea, memoryStart, err := testutils.NewPageMmap(t, uint64(size), pagesize)
	require.NoError(t, err)

	n := copy(memoryArea[0:size], data.Content())
	require.Equal(t, int(size), n)

	m := NewMapping([]Region{
		{
			BaseHostVirtAddr: memoryStart,
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
			assert.Fail(t, "content mismatch", "want '%x', got '%x' at index %d", want, got, idx)
		}
	}
}

func TestViewSingleRegionPartialRead(t *testing.T) {
	t.Parallel()

	pagesize := uint64(4096)
	numberOfPages := uint64(32)

	data := testutils.RandomPages(pagesize, numberOfPages)

	size, err := data.Size()
	require.NoError(t, err)

	memoryArea, memoryStart, err := testutils.NewPageMmap(t, uint64(size), pagesize)
	require.NoError(t, err)

	n := copy(memoryArea[0:size], data.Content())
	require.Equal(t, int(size), n)

	m := NewMapping([]Region{
		{
			BaseHostVirtAddr: memoryStart,
			Size:             uintptr(size),
			Offset:           uintptr(0),
			PageSize:         uintptr(pagesize),
		},
	})

	pc, err := NewView(os.Getpid(), m)
	require.NoError(t, err)

	defer pc.Close()

	// Read at the start of the region
	readBytes := make([]byte, pagesize)
	n, err = pc.ReadAt(readBytes, 0)
	require.NoError(t, err)
	assert.Equal(t, int(size), n)
	expectedBytes := data.Content()
	if !bytes.Equal(readBytes, expectedBytes) {
		idx, want, got := testutils.FirstDifferentByte(readBytes, expectedBytes)
		assert.Fail(t, "content mismatch", "want '%x', got '%x' at index %d", want, got, idx)
	}

	// Read in the middle of the region
	readBytes = make([]byte, pagesize)
	n, err = pc.ReadAt(readBytes, int64(numberOfPages/2*pagesize))
	require.NoError(t, err)
	assert.Equal(t, int(pagesize), n)
	expectedBytes = data.Content()
	if !bytes.Equal(readBytes, expectedBytes) {
		idx, want, got := testutils.FirstDifferentByte(readBytes, expectedBytes)
		assert.Fail(t, "content mismatch", "want '%x', got '%x' at index %d", want, got, idx)
	}

	// Read at the end of the region
	readBytes = make([]byte, pagesize)
	n, err = pc.ReadAt(readBytes, int64(numberOfPages*pagesize-pagesize))
	require.NoError(t, err)
	assert.Equal(t, int(size), n)
	expectedBytes = data.Content()
	if !bytes.Equal(readBytes, expectedBytes) {
		idx, want, got := testutils.FirstDifferentByte(readBytes, expectedBytes)
		assert.Fail(t, "content mismatch", "want '%x', got '%x' at index %d", want, got, idx)
	}
}

func TestViewMultipleRegions(t *testing.T) {
	t.Parallel()

	pagesize := uint64(4096)

	// Create three separate memory regions with gaps between them
	region1Pages := uint64(32)
	region2Pages := uint64(64)
	region3Pages := uint64(16)

	data1 := testutils.RandomPages(pagesize, region1Pages)
	data2 := testutils.RandomPages(pagesize, region2Pages)
	data3 := testutils.RandomPages(pagesize, region3Pages)

	size1, err := data1.Size()
	require.NoError(t, err)
	size2, err := data2.Size()
	require.NoError(t, err)
	size3, err := data3.Size()
	require.NoError(t, err)

	// Create three separate memory mappings
	memoryArea1, memoryStart1, err := testutils.NewPageMmap(t, uint64(size1), pagesize)
	require.NoError(t, err)
	memoryArea2, memoryStart2, err := testutils.NewPageMmap(t, uint64(size2), pagesize)
	require.NoError(t, err)
	memoryArea3, memoryStart3, err := testutils.NewPageMmap(t, uint64(size3), pagesize)
	require.NoError(t, err)

	// Copy data to each region
	n1 := copy(memoryArea1[0:size1], data1.Content())
	require.Equal(t, int(size1), n1)
	n2 := copy(memoryArea2[0:size2], data2.Content())
	require.Equal(t, int(size2), n2)
	n3 := copy(memoryArea3[0:size3], data3.Content())
	require.Equal(t, int(size3), n3)

	// Create mapping with three regions at different offsets
	// Region 1: offset 0, size size1
	// Region 2: offset size1 + gap, size size2
	// Region 3: offset size1 + gap + size2 + gap, size size3
	gap := uint64(8192) // 2 pages gap between regions
	offset2 := uint64(size1) + gap
	offset3 := offset2 + uint64(size2) + gap

	m := NewMapping([]Region{
		{
			BaseHostVirtAddr: memoryStart1,
			Size:             uintptr(size1),
			Offset:           0,
			PageSize:         uintptr(pagesize),
		},
		{
			BaseHostVirtAddr: memoryStart2,
			Size:             uintptr(size2),
			Offset:           uintptr(offset2),
			PageSize:         uintptr(pagesize),
		},
		{
			BaseHostVirtAddr: memoryStart3,
			Size:             uintptr(size3),
			Offset:           uintptr(offset3),
			PageSize:         uintptr(pagesize),
		},
	})

	pc, err := NewView(os.Getpid(), m)
	require.NoError(t, err)

	defer pc.Close()

	// Test reading from first region
	for i := 0; i < int(size1); i += int(pagesize) {
		readBytes := make([]byte, pagesize)
		_, err := pc.ReadAt(readBytes, int64(i))
		require.NoError(t, err)

		expectedBytes := data1.Content()[i : i+int(pagesize)]
		if !bytes.Equal(readBytes, expectedBytes) {
			idx, want, got := testutils.FirstDifferentByte(readBytes, expectedBytes)
			assert.Fail(t, "region 1 content mismatch at offset %d: want '%x', got '%x' at index %d", i, want, got, idx)
		}
	}

	// Test reading from second region
	for i := 0; i < int(size2); i += int(pagesize) {
		readBytes := make([]byte, pagesize)
		_, err := pc.ReadAt(readBytes, int64(offset2)+int64(i))
		require.NoError(t, err)

		expectedBytes := data2.Content()[i : i+int(pagesize)]
		if !bytes.Equal(readBytes, expectedBytes) {
			idx, want, got := testutils.FirstDifferentByte(readBytes, expectedBytes)
			assert.Fail(t, "region 2 content mismatch at offset %d: want '%x', got '%x' at index %d", i, want, got, idx)
		}
	}

	// Test reading from third region
	for i := 0; i < int(size3); i += int(pagesize) {
		readBytes := make([]byte, pagesize)
		_, err := pc.ReadAt(readBytes, int64(offset3)+int64(i))
		require.NoError(t, err)

		expectedBytes := data3.Content()[i : i+int(pagesize)]
		if !bytes.Equal(readBytes, expectedBytes) {
			idx, want, got := testutils.FirstDifferentByte(readBytes, expectedBytes)
			assert.Fail(t, "region 3 content mismatch at offset %d: want '%x', got '%x' at index %d", i, want, got, idx)
		}
	}

	// Test reading that spans within a single region (not crossing boundaries)
	// Read 2 pages from middle of region 2
	readSize := int(2 * pagesize)
	readOffset := int64(offset2) + int64(pagesize)
	readBytes := make([]byte, readSize)
	_, err = pc.ReadAt(readBytes, readOffset)
	require.NoError(t, err)

	expectedBytes := data2.Content()[int(pagesize) : int(pagesize)+readSize]
	if !bytes.Equal(readBytes, expectedBytes) {
		idx, want, got := testutils.FirstDifferentByte(readBytes, expectedBytes)
		assert.Fail(t, "region 2 span read mismatch: want '%x', got '%x' at index %d", want, got, idx)
	}

	// Test reading that would cross region boundary (should fail at gap)
	// Try to read from end of region 1 into gap
	readBytes = make([]byte, int(pagesize*2))
	_, err = pc.ReadAt(readBytes, int64(size1)-int64(pagesize))
	require.Error(t, err, "reading across region boundary should fail")
}
