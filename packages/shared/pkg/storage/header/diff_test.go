package header

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/bits-and-blooms/bitset"
	"github.com/stretchr/testify/assert"
)

func createSource(blockSize int, blocksData []byte) []byte {
	sourceSlice := make([]byte, blockSize*len(blocksData))
	for i, data := range blocksData {
		sourceSlice[i*blockSize] = data
	}
	return sourceSlice
}

func TestCreateDiff_Hugepage(t *testing.T) {
	blockSize := HugepageSize
	sourceSlice := createSource(blockSize, []byte{1, 0, 3, 4, 5})

	source := bytes.NewReader(sourceSlice)
	dirty := bitset.New(0)
	dirty.Set(0)
	dirty.Set(1)
	dirty.Set(4)

	diff := bytes.NewBuffer(nil)
	m, err := writeDiff(source, int64(blockSize), dirty, diff)
	assert.NoError(t, err)

	expectedDiffData := createSource(blockSize, []byte{1, 5})
	assert.Equal(t, expectedDiffData, diff.Bytes())

	assert.Equal(t, "0000000000000000000000000000000000000000000000000000000000000010.", m.Empty.DumpAsBits())
}

func TestCreateDiff_RootfsBlock(t *testing.T) {
	blockSize := RootfsBlockSize
	sourceSlice := createSource(blockSize, []byte{1, 0, 3, 4, 5})

	source := bytes.NewReader(sourceSlice)
	dirty := bitset.New(0)
	dirty.Set(0)
	dirty.Set(1)
	dirty.Set(4)

	diff := bytes.NewBuffer(nil)
	m, err := writeDiff(source, int64(blockSize), dirty, diff)
	assert.NoError(t, err)

	expectedDiffData := createSource(blockSize, []byte{1, 5})
	assert.Equal(t, expectedDiffData, diff.Bytes())

	assert.Equal(t, "0000000000000000000000000000000000000000000000000000000000000010.", m.Empty.DumpAsBits())
}

func TestCreateDiff_UnsupportedBlockSize(t *testing.T) {
	blockSize := 42
	sourceSlice := createSource(blockSize, []byte{1, 0, 3, 4, 5})

	source := bytes.NewReader(sourceSlice)
	dirty := bitset.New(0)
	dirty.Set(0)
	dirty.Set(1)
	dirty.Set(4)

	diff := bytes.NewBuffer(nil)
	_, err := writeDiff(source, int64(blockSize), dirty, diff)

	assert.Error(t, err)
}

func TestCreateDiff_AllEmptyBlocks(t *testing.T) {
	blockSize := HugepageSize
	sourceSlice := createSource(blockSize, []byte{0, 0, 0, 0, 0})

	source := bytes.NewReader(sourceSlice)
	dirty := bitset.New(0)
	dirty.Set(0)
	dirty.Set(1)
	dirty.Set(2)
	dirty.Set(3)
	dirty.Set(4)

	diff := bytes.NewBuffer(nil)
	m, err := writeDiff(source, int64(blockSize), dirty, diff)
	assert.NoError(t, err)

	assert.Equal(t, "0000000000000000000000000000000000000000000000000000000000011111.", m.Empty.DumpAsBits())
}

func TestCreateDiff_EmptyDirtyBitset(t *testing.T) {
	blockSize := HugepageSize
	sourceSlice := createSource(blockSize, []byte{1, 2, 3})

	source := bytes.NewReader(sourceSlice)
	dirty := bitset.New(0)
	// No blocks are marked as dirty

	diff := bytes.NewBuffer(nil)
	m, err := writeDiff(source, int64(blockSize), dirty, diff)
	assert.NoError(t, err)

	// Verify no data was written to diff
	assert.Equal(t, 0, diff.Len())

	assert.Empty(t, m.Dirty.DumpAsBits())
	assert.Empty(t, m.Empty.DumpAsBits())
}

type errorReader struct{}

func (e errorReader) ReadAt(p []byte, off int64) (n int, err error) {
	return 0, fmt.Errorf("simulated read error")
}

func TestCreateDiff_ReadError(t *testing.T) {
	blockSize := HugepageSize
	source := errorReader{}

	dirty := bitset.New(0)
	dirty.Set(0) // Mark one block as dirty to trigger ReadAt

	diff := bytes.NewBuffer(nil)
	_, err := writeDiff(source, int64(blockSize), dirty, diff)

	// Verify that the error from ReadAt is propagated
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error reading from source")
	assert.Contains(t, err.Error(), "simulated read error")
}

// errorWriter implements io.Writer and always returns an error
type errorWriter struct{}

func (e errorWriter) Write(p []byte) (n int, err error) {
	return 0, fmt.Errorf("simulated write error")
}

func TestCreateDiff_WriteError(t *testing.T) {
	blockSize := HugepageSize
	// Create a source with non-empty data to ensure Write is called
	sourceSlice := createSource(blockSize, []byte{1, 2, 3})
	source := bytes.NewReader(sourceSlice)

	dirty := bitset.New(0)
	dirty.Set(0) // Mark one block as dirty to trigger Write

	diff := errorWriter{}
	_, err := writeDiff(source, int64(blockSize), dirty, diff)

	// Verify that the error from Write is propagated
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error writing to diff")
	assert.Contains(t, err.Error(), "simulated write error")
}

func TestCreateDiff_LargeIndex(t *testing.T) {
	blockSize := RootfsBlockSize
	// Create a source that can handle large offsets
	largeSource := &largeOffsetReader{
		data: []byte{42}, // Non-empty data to ensure it's not considered empty
	}

	dirty := bitset.New(0)
	// Set a very large index to test offset calculation
	largeIndex := uint(1000000)
	dirty.Set(largeIndex)

	diff := bytes.NewBuffer(nil)
	m, err := writeDiff(largeSource, int64(blockSize), dirty, diff)
	assert.NoError(t, err)

	// Verify the large index is still marked as dirty
	assert.True(t, m.Dirty.Test(largeIndex))
	assert.False(t, m.Empty.Test(largeIndex))

	// Verify the data was written to diff
	assert.Equal(t, blockSize, diff.Len())
	assert.Equal(t, byte(42), diff.Bytes()[0])
}

// largeOffsetReader implements io.ReaderAt and handles large offsets
type largeOffsetReader struct {
	data []byte
}

func (r *largeOffsetReader) ReadAt(p []byte, off int64) (n int, err error) {
	// Always return the same data regardless of offset
	copy(p, r.data)
	// Fill the rest with zeros
	for i := len(r.data); i < len(p); i++ {
		p[i] = 0
	}
	return len(p), nil
}
