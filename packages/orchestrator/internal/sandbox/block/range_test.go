package block

import (
	"iter"
	"slices"
	"testing"

	"github.com/bits-and-blooms/bitset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rangeOffsets returns the block offsets contained in the range.
// This assumes the Range.Start is a multiple of the blockSize.
func rangeOffsets(r *Range, blockSize int64) iter.Seq[int64] {
	return func(yield func(offset int64) bool) {
		getOffsets(r.Start, r.End(), blockSize)(yield)
	}
}

func getOffsets(start, end int64, blockSize int64) iter.Seq[int64] {
	return func(yield func(offset int64) bool) {
		for off := start; off < end; off += blockSize {
			if !yield(off) {
				return
			}
		}
	}
}

func TestRange_End(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		start    int64
		size     int64
		expected int64
	}{
		{
			name:     "zero size",
			start:    100,
			size:     0,
			expected: 100,
		},
		{
			name:     "single byte",
			start:    0,
			size:     1,
			expected: 1,
		},
		{
			name:     "multiple bytes",
			start:    10,
			size:     20,
			expected: 30,
		},
		{
			name:     "large size",
			start:    0,
			size:     1024 * 1024,
			expected: 1024 * 1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := Range{
				Start: tt.start,
				Size:  tt.size,
			}
			assert.Equal(t, tt.expected, r.End())
		})
	}
}

func TestNewRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		start    int64
		size     int64
		expected Range
	}{
		{
			name:  "basic range",
			start: 0,
			size:  4096,
			expected: Range{
				Start: 0,
				Size:  4096,
			},
		},
		{
			name:  "non-zero start",
			start: 8192,
			size:  2048,
			expected: Range{
				Start: 8192,
				Size:  2048,
			},
		},
		{
			name:  "zero size",
			start: 100,
			size:  0,
			expected: Range{
				Start: 100,
				Size:  0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := NewRange(tt.start, tt.size)
			assert.Equal(t, tt.expected, r)
		})
	}
}

func TestNewRangeFromBlocks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		startIdx       int64
		numberOfBlocks int64
		blockSize      int64
		expected       Range
	}{
		{
			name:           "single block at start",
			startIdx:       0,
			numberOfBlocks: 1,
			blockSize:      4096,
			expected: Range{
				Start: 0,
				Size:  4096,
			},
		},
		{
			name:           "multiple blocks",
			startIdx:       0,
			numberOfBlocks: 3,
			blockSize:      4096,
			expected: Range{
				Start: 0,
				Size:  12288, // 3 * 4096
			},
		},
		{
			name:           "blocks starting at non-zero index",
			startIdx:       5,
			numberOfBlocks: 2,
			blockSize:      4096,
			expected: Range{
				Start: 20480, // 5 * 4096
				Size:  8192,  // 2 * 4096
			},
		},
		{
			name:           "zero blocks",
			startIdx:       10,
			numberOfBlocks: 0,
			blockSize:      4096,
			expected: Range{
				Start: 40960, // 10 * 4096
				Size:  0,
			},
		},
		{
			name:           "different block size",
			startIdx:       0,
			numberOfBlocks: 4,
			blockSize:      8192,
			expected: Range{
				Start: 0,
				Size:  32768, // 4 * 8192
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := NewRangeFromBlocks(tt.startIdx, tt.numberOfBlocks, tt.blockSize)
			assert.Equal(t, tt.expected, r)
		})
	}
}

func TestRange_Offsets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		range_    Range
		blockSize int64
		expected  []int64
	}{
		{
			name: "single block",
			range_: Range{
				Start: 0,
				Size:  4096,
			},
			blockSize: 4096,
			expected:  []int64{0},
		},
		{
			name: "multiple blocks",
			range_: Range{
				Start: 0,
				Size:  12288, // 3 * 4096
			},
			blockSize: 4096,
			expected:  []int64{0, 4096, 8192},
		},
		{
			name: "non-zero start",
			range_: Range{
				Start: 8192,
				Size:  8192, // 2 * 4096
			},
			blockSize: 4096,
			expected:  []int64{8192, 12288},
		},
		{
			name: "zero size",
			range_: Range{
				Start: 4096,
				Size:  0,
			},
			blockSize: 4096,
			expected:  []int64{},
		},
		{
			name: "smaller than block size",
			range_: Range{
				Start: 0,
				Size:  1024,
			},
			blockSize: 4096,
			expected:  []int64{0},
		},
		{
			name: "different block size",
			range_: Range{
				Start: 0,
				Size:  16384, // 4 * 4096
			},
			blockSize: 8192,
			expected:  []int64{0, 8192},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			offsets := slices.Collect(rangeOffsets(&tt.range_, tt.blockSize))
			if len(tt.expected) == 0 {
				assert.Empty(t, offsets)
			} else {
				assert.Equal(t, tt.expected, offsets)
			}
		})
	}
}

func TestRange_Offsets_Iteration(t *testing.T) {
	t.Parallel()
	// Test that iteration can be stopped early
	r := Range{
		Start: 0,
		Size:  40960, // 10 * 4096
	}
	blockSize := int64(4096)

	var collected []int64
	for offset := range rangeOffsets(&r, blockSize) {
		collected = append(collected, offset)
		if len(collected) >= 3 {
			break
		}
	}

	assert.Len(t, collected, 3)
	assert.Equal(t, []int64{0, 4096, 8192}, collected)
}

func TestBitsetRanges_Empty(t *testing.T) {
	t.Parallel()
	b := bitset.New(100)
	blockSize := int64(4096)

	ranges := slices.Collect(BitsetRanges(b, blockSize))
	assert.Empty(t, ranges)
}

func TestBitsetRanges_SingleBit(t *testing.T) {
	t.Parallel()
	b := bitset.New(100)
	b.Set(5)
	blockSize := int64(4096)

	ranges := slices.Collect(BitsetRanges(b, blockSize))
	require.Len(t, ranges, 1)
	assert.Equal(t, Range{
		Start: 20480, // 5 * 4096
		Size:  4096,
	}, ranges[0])
}

func TestBitsetRanges_Contiguous(t *testing.T) {
	t.Parallel()
	b := bitset.New(100)
	// Set bits 2, 3, 4, 5
	b.Set(2)
	b.Set(3)
	b.Set(4)
	b.Set(5)
	blockSize := int64(4096)

	ranges := slices.Collect(BitsetRanges(b, blockSize))
	require.Len(t, ranges, 1)
	assert.Equal(t, Range{
		Start: 8192,  // 2 * 4096
		Size:  16384, // 4 * 4096
	}, ranges[0])
}

func TestBitsetRanges_MultipleRanges(t *testing.T) {
	t.Parallel()
	b := bitset.New(100)
	// Set bits 1, 2, 3 (contiguous)
	b.Set(1)
	b.Set(2)
	b.Set(3)
	// Gap
	// Set bits 7, 8 (contiguous)
	b.Set(7)
	b.Set(8)
	blockSize := int64(4096)

	ranges := slices.Collect(BitsetRanges(b, blockSize))
	require.Len(t, ranges, 2)
	assert.Equal(t, Range{
		Start: 4096,  // 1 * 4096
		Size:  12288, // 3 * 4096
	}, ranges[0])
	assert.Equal(t, Range{
		Start: 28672, // 7 * 4096
		Size:  8192,  // 2 * 4096
	}, ranges[1])
}

func TestBitsetRanges_AllSet(t *testing.T) {
	t.Parallel()
	b := bitset.New(10)
	for i := range uint(10) {
		b.Set(i)
	}
	blockSize := int64(4096)

	ranges := slices.Collect(BitsetRanges(b, blockSize))
	require.Len(t, ranges, 1)
	assert.Equal(t, Range{
		Start: 0,
		Size:  40960, // 10 * 4096
	}, ranges[0])
}

func TestBitsetRanges_EndOfBitset(t *testing.T) {
	t.Parallel()
	b := bitset.New(20)
	// Set bits 15, 16, 17, 18, 19 (at the end)
	for i := uint(15); i < 20; i++ {
		b.Set(i)
	}
	blockSize := int64(4096)

	ranges := slices.Collect(BitsetRanges(b, blockSize))
	require.Len(t, ranges, 1)
	assert.Equal(t, Range{
		Start: 61440, // 15 * 4096
		Size:  20480, // 5 * 4096
	}, ranges[0])
}

func TestBitsetRanges_Sparse(t *testing.T) {
	t.Parallel()
	b := bitset.New(100)
	// Set individual bits with gaps
	b.Set(0)
	b.Set(10)
	b.Set(20)
	b.Set(30)
	blockSize := int64(4096)

	ranges := slices.Collect(BitsetRanges(b, blockSize))
	require.Len(t, ranges, 4)
	assert.Equal(t, Range{Start: 0, Size: 4096}, ranges[0])
	assert.Equal(t, Range{Start: 40960, Size: 4096}, ranges[1])
	assert.Equal(t, Range{Start: 81920, Size: 4096}, ranges[2])
	assert.Equal(t, Range{Start: 122880, Size: 4096}, ranges[3])
}

func TestGetSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ranges   []Range
		expected int64
	}{
		{
			name:     "empty",
			ranges:   []Range{},
			expected: 0,
		},
		{
			name: "single range",
			ranges: []Range{
				{Start: 0, Size: 4096},
			},
			expected: 4096,
		},
		{
			name: "multiple ranges",
			ranges: []Range{
				{Start: 0, Size: 4096},
				{Start: 8192, Size: 8192},
				{Start: 16384, Size: 4096},
			},
			expected: 16384, // 4096 + 8192 + 4096
		},
		{
			name: "zero size ranges",
			ranges: []Range{
				{Start: 0, Size: 0},
				{Start: 4096, Size: 4096},
				{Start: 8192, Size: 0},
			},
			expected: 4096,
		},
		{
			name: "large sizes",
			ranges: []Range{
				{Start: 0, Size: 1024 * 1024},
				{Start: 1024 * 1024, Size: 2 * 1024 * 1024},
			},
			expected: 3 * 1024 * 1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			size := GetSize(tt.ranges)
			assert.Equal(t, tt.expected, size)
		})
	}
}

func TestRange_Offsets_EdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		range_    Range
		blockSize int64
		expected  []int64
	}{
		{
			name: "exact block boundary end",
			range_: Range{
				Start: 0,
				Size:  12288, // exactly 3 blocks
			},
			blockSize: 4096,
			expected:  []int64{0, 4096, 8192},
		},
		{
			name: "one byte over block boundary",
			range_: Range{
				Start: 0,
				Size:  12289, // 3 blocks + 1 byte
			},
			blockSize: 4096,
			expected:  []int64{0, 4096, 8192, 12288},
		},
		{
			name: "one byte less than block boundary",
			range_: Range{
				Start: 0,
				Size:  12287, // 3 blocks - 1 byte
			},
			blockSize: 4096,
			expected:  []int64{0, 4096, 8192},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			offsets := slices.Collect(rangeOffsets(&tt.range_, tt.blockSize))
			assert.Equal(t, tt.expected, offsets)
		})
	}
}
