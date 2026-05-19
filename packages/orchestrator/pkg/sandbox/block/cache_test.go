//go:build linux

package block

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"syscall"
	"testing"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func allocateTestMemory(t *testing.T, size uint64, pageSize uint64) (addr uint64, expectedData []byte) {
	t.Helper()

	mem, memoryStart, err := testutils.NewPageMmap(t, size, pageSize)
	require.NoError(t, err)

	n, err := rand.Read(mem)
	require.NoError(t, err)
	require.Equal(t, len(mem), n)

	return uint64(memoryStart), mem
}

func TestCopyFromProcess_FullRange(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	size := pageSize * 30

	addr, mem := allocateTestMemory(t, uint64(size), uint64(pageSize))

	ranges := []Range{
		{Start: int64(addr), Size: size},
	}

	cache, err := NewCacheFromProcessMemory(
		t.Context(),
		pageSize,
		t.TempDir()+"/cache",
		os.Getpid(),
		ranges,
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		cache.Close()
	})

	data := make([]byte, size)
	n, err := cache.ReadAt(data, 0)
	require.NoError(t, err)
	require.Equal(t, int(size), n)

	require.NoError(t, compareData(data[:n], mem[:n]))
}

func TestCopyFromProcess_LargeRanges(t *testing.T) {
	t.Parallel()

	pageSize := uint64(header.PageSize)
	totalSize := pageSize * 5

	addr, mem := allocateTestMemory(t, totalSize, pageSize)

	ranges := []Range{
		{Start: int64(addr), Size: int64(pageSize)},
		{Start: int64(addr + pageSize*3), Size: int64(pageSize)},
		{Start: int64(addr + pageSize), Size: int64(pageSize)},
	}

	cache, err := NewCacheFromProcessMemory(
		t.Context(),
		int64(pageSize),
		t.TempDir()+"/cache",
		os.Getpid(),
		ranges,
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		cache.Close()
	})

	data1 := make([]byte, pageSize)
	n, err := cache.ReadAt(data1, 0)
	require.NoError(t, err)
	require.Equal(t, int(pageSize), n)
	require.NoError(t, compareData(data1[:n], mem[0:pageSize]))

	data2 := make([]byte, pageSize)
	n, err = cache.ReadAt(data2, int64(pageSize))
	require.NoError(t, err)
	require.Equal(t, int(pageSize), n)
	require.NoError(t, compareData(data2[:n], mem[pageSize*3:pageSize*4]))

	data3 := make([]byte, pageSize)
	n, err = cache.ReadAt(data3, int64(pageSize*2))
	require.NoError(t, err)
	require.Equal(t, int(pageSize), n)
	require.NoError(t, compareData(data3[:n], mem[pageSize:pageSize*2]))
}

func TestCopyFromProcess_MultipleRanges(t *testing.T) {
	t.Parallel()

	numRanges := 1500
	pageSize := uint64(header.PageSize)
	rangeSize := int64(pageSize * 64)

	totalSize := rangeSize * int64(numRanges)

	addr, mem := allocateTestMemory(t, uint64(totalSize), pageSize)

	ranges := make([]Range, numRanges)
	for i := range numRanges {
		ranges[i] = Range{
			Start: int64(addr) + int64(i)*rangeSize,
			Size:  rangeSize,
		}
	}

	cache, err := NewCacheFromProcessMemory(
		t.Context(),
		int64(pageSize),
		t.TempDir()+"/cache",
		os.Getpid(),
		ranges,
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		cache.Close()
	})

	checkCount := min(numRanges, 10)
	for i := range checkCount {
		actualOffset := int64(i) * rangeSize
		alignedOffset := (actualOffset / int64(pageSize)) * int64(pageSize)

		data := make([]byte, pageSize)

		n, err := cache.ReadAt(data, alignedOffset)
		require.NoError(t, err)
		require.Equal(t, int(pageSize), n)

		require.NoError(t, compareData(data[:n], mem[alignedOffset:alignedOffset+int64(pageSize)]))
	}
}

func TestCopyFromProcess_HugepageToRegularPage(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.HugepageSize)
	size := pageSize * 10

	mem, addr, err := testutils.NewPageMmap(t, uint64(size), uint64(pageSize))
	require.NoError(t, err)

	n, err := rand.Read(mem)
	require.NoError(t, err)
	require.Equal(t, len(mem), n)

	ranges := []Range{
		{Start: int64(addr), Size: pageSize * 2},
		{Start: int64(addr) + pageSize*4, Size: pageSize * 4},
	}

	cache, err := NewCacheFromProcessMemory(
		t.Context(),
		// Regular 4KiB pages.
		header.PageSize,
		t.TempDir()+"/cache",
		os.Getpid(),
		ranges,
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		cache.Close()
	})

	data := make([]byte, pageSize*2)
	n, err = cache.ReadAt(data, 0)
	require.NoError(t, err)
	require.Equal(t, int(pageSize*2), n)
	require.NoError(t, compareData(data[:n], mem[0:pageSize*2]))

	data = make([]byte, pageSize*4)
	n, err = cache.ReadAt(data, pageSize*2)
	require.NoError(t, err)
	require.Equal(t, int(pageSize*4), n)
	require.NoError(t, compareData(data[:n], mem[pageSize*4:pageSize*8]))
}

func TestSliceDirectOutOfBoundsReturnsBytesNotAvailable(t *testing.T) {
	t.Parallel()

	cache, err := NewCache(16, 4, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	_, err = cache.sliceDirect(16, 4)
	require.ErrorIs(t, err, BytesNotAvailableError{})

	_, err = cache.sliceDirect(32, 4)
	require.ErrorIs(t, err, BytesNotAvailableError{})
}

func TestEmptyRanges(t *testing.T) {
	t.Parallel()

	c, err := NewCacheFromProcessMemory(
		t.Context(),
		header.PageSize,
		t.TempDir()+"/cache",
		os.Getpid(),
		nil,
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		c.Close()
	})
}

func TestCacheExportToDiff_ZeroBlockRoutesToEmpty(t *testing.T) {
	t.Parallel()

	const blockSize = header.RootfsBlockSize
	cache, err := NewCache(blockSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})

	_, err = cache.WriteAt(make([]byte, blockSize), 0)
	require.NoError(t, err)

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	diffMetadata, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.EqualValues(t, 0, diffMetadata.Dirty.GetCardinality())
	require.EqualValues(t, 1, diffMetadata.Empty.GetCardinality())

	stat, err := out.Stat()
	require.NoError(t, err)
	require.EqualValues(t, 0, stat.Size())
}

func TestCacheExportToDiff_ZeroBlockMapsToEmpty(t *testing.T) {
	t.Parallel()

	const blockSize = header.RootfsBlockSize
	cache, err := NewCache(blockSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})

	_, err = cache.WriteAt(make([]byte, blockSize), 0)
	require.NoError(t, err)

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	diffMetadata, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	baseBuildID := uuid.New()
	originalHeader, err := header.NewHeader(
		header.NewTemplateMetadata(baseBuildID, uint64(blockSize), uint64(blockSize)),
		nil,
	)
	require.NoError(t, err)

	diffHeader, err := diffMetadata.ToDiffHeader(t.Context(), originalHeader, uuid.New())
	require.NoError(t, err)

	mapped, err := diffHeader.GetShiftedMapping(t.Context(), 0)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, mapped.BuildId, "zero block should be exported as Empty (uuid.Nil)")
}

func TestCacheExportToDiff_MixedZeroBlockSplitsIntoEmptyAndDirty(t *testing.T) {
	t.Parallel()

	const blockSize = header.RootfsBlockSize
	const size = blockSize * 3

	cache, err := NewCache(size, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})

	nonZeroBlock := bytes.Repeat([]byte{0xAB}, int(blockSize))

	_, err = cache.WriteAt(make([]byte, blockSize), 0)
	require.NoError(t, err)

	_, err = cache.WriteAt(nonZeroBlock, blockSize)
	require.NoError(t, err)

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	diffMetadata, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.EqualValues(t, 1, diffMetadata.Dirty.GetCardinality())
	require.EqualValues(t, 1, diffMetadata.Empty.GetCardinality())
	require.True(t, diffMetadata.Empty.Contains(0))
	require.True(t, diffMetadata.Dirty.Contains(1))

	baseBuildID := uuid.New()
	originalHeader, err := header.NewHeader(
		header.NewTemplateMetadata(baseBuildID, uint64(blockSize), uint64(size)),
		nil,
	)
	require.NoError(t, err)

	snapshotBuildID := uuid.New()
	diffHeader, err := diffMetadata.ToDiffHeader(t.Context(), originalHeader, snapshotBuildID)
	require.NoError(t, err)

	firstBlock, err := diffHeader.GetShiftedMapping(t.Context(), 0)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, firstBlock.BuildId, "zero block should map to Empty")

	secondBlock, err := diffHeader.GetShiftedMapping(t.Context(), blockSize)
	require.NoError(t, err)
	require.Equal(t, snapshotBuildID, secondBlock.BuildId, "non-zero dirty block should map to the snapshot diff")

	thirdBlock, err := diffHeader.GetShiftedMapping(t.Context(), 2*blockSize)
	require.NoError(t, err)
	require.Equal(t, baseBuildID, thirdBlock.BuildId, "clean blocks should keep the base mapping")
}

func TestCacheWriteZeroesAt_AlignedRangeMapsToEmpty(t *testing.T) {
	t.Parallel()

	const blockSize = header.RootfsBlockSize
	cache, err := NewCache(blockSize*4, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })

	_, err = cache.WriteAt(bytes.Repeat([]byte{0xAB}, int(blockSize)), blockSize)
	require.NoError(t, err)

	n, err := cache.WriteZeroesAt(0, 2*blockSize)
	require.NoError(t, err)
	require.Equal(t, 2*blockSize, n)

	got := make([]byte, 2*blockSize)
	_, err = cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.True(t, header.IsZero(got), "punched range must read back as zero")

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	diffMetadata, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.EqualValues(t, 0, diffMetadata.Dirty.GetCardinality())
	require.EqualValues(t, 2, diffMetadata.Empty.GetCardinality())
}

func TestCacheExportToDiff_NonContiguousDirtyBlocksPreserveRangeOrder(t *testing.T) {
	t.Parallel()

	const blockSize = header.RootfsBlockSize
	const size = blockSize * 5

	cache, err := NewCache(size, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})

	firstBlock := bytes.Repeat([]byte{0x11}, int(blockSize))
	secondBlock := bytes.Repeat([]byte{0x22}, int(blockSize))

	_, err = cache.WriteAt(firstBlock, 0)
	require.NoError(t, err)

	_, err = cache.WriteAt(secondBlock, 3*blockSize)
	require.NoError(t, err)

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	diffMetadata, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.EqualValues(t, 2, diffMetadata.Dirty.GetCardinality())
	require.True(t, diffMetadata.Dirty.Contains(0))
	require.True(t, diffMetadata.Dirty.Contains(3))
	require.EqualValues(t, 0, diffMetadata.Empty.GetCardinality())

	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	exported, err := io.ReadAll(out)
	require.NoError(t, err)
	require.Equal(t, append(firstBlock, secondBlock...), exported)
}

func TestCache_ZeroLengthIsCachedAndSetIsCached(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	const size int64 = blockSize * 10

	cache, err := NewCache(size, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})

	// Before any writes, isCached(0, blockSize) should be false
	require.False(t, cache.isCached(0, blockSize), "block 0 should not be cached initially")

	// Zero-length isCached should return true (vacuous truth) and NOT check dirty map
	require.True(t, cache.isCached(0, 0), "zero-length isCached should return true (no-op)")

	// Zero-length setIsCached should be a no-op and NOT mark anything as cached
	cache.setIsCached(0, 0)

	// Block 0 should still not be cached after zero-length setIsCached
	require.False(t, cache.isCached(0, blockSize), "block 0 should still not be cached after zero-length setIsCached")

	// Test with various offsets to ensure zero-length is always a no-op
	cache.setIsCached(blockSize, 0)
	require.False(t, cache.isCached(blockSize, blockSize), "block 1 should not be cached after zero-length setIsCached")

	cache.setIsCached(blockSize*5, 0)
	require.False(t, cache.isCached(blockSize*5, blockSize), "block 5 should not be cached after zero-length setIsCached")
}

func compareData(readBytes []byte, expectedBytes []byte) error {
	// The bytes.Equal is the first place in this flow that actually touches the uffd managed memory and triggers the pagefault, so any deadlocks will manifest here.
	if !bytes.Equal(readBytes, expectedBytes) {
		idx, want, got := testutils.FirstDifferentByte(readBytes, expectedBytes)

		return fmt.Errorf("content mismatch: want '%x, got %x at index %d", want, got, idx)
	}

	return nil
}

func TestSplitOversizedRanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ranges   []Range
		maxSize  int64
		expected []Range
	}{
		{
			name:     "empty input",
			ranges:   nil,
			maxSize:  100,
			expected: nil,
		},
		{
			name: "all ranges within limit",
			ranges: []Range{
				{Start: 0, Size: 50},
				{Start: 100, Size: 50},
			},
			maxSize: 100,
			expected: []Range{
				{Start: 0, Size: 50},
				{Start: 100, Size: 50},
			},
		},
		{
			name: "range exactly at limit",
			ranges: []Range{
				{Start: 0, Size: 100},
			},
			maxSize: 100,
			expected: []Range{
				{Start: 0, Size: 100},
			},
		},
		{
			name: "single oversized range splits evenly",
			ranges: []Range{
				{Start: 0, Size: 300},
			},
			maxSize: 100,
			expected: []Range{
				{Start: 0, Size: 100},
				{Start: 100, Size: 100},
				{Start: 200, Size: 100},
			},
		},
		{
			name: "single oversized range with remainder",
			ranges: []Range{
				{Start: 0, Size: 250},
			},
			maxSize: 100,
			expected: []Range{
				{Start: 0, Size: 100},
				{Start: 100, Size: 100},
				{Start: 200, Size: 50},
			},
		},
		{
			name: "mixed ranges - some need splitting",
			ranges: []Range{
				{Start: 0, Size: 50},
				{Start: 100, Size: 250},
				{Start: 400, Size: 80},
			},
			maxSize: 100,
			expected: []Range{
				{Start: 0, Size: 50},
				{Start: 100, Size: 100},
				{Start: 200, Size: 100},
				{Start: 300, Size: 50},
				{Start: 400, Size: 80},
			},
		},
		{
			name: "range just over limit",
			ranges: []Range{
				{Start: 0, Size: 101},
			},
			maxSize: 100,
			expected: []Range{
				{Start: 0, Size: 100},
				{Start: 100, Size: 1},
			},
		},
		{
			name: "preserves start addresses correctly",
			ranges: []Range{
				{Start: 1000, Size: 250},
			},
			maxSize: 100,
			expected: []Range{
				{Start: 1000, Size: 100},
				{Start: 1100, Size: 100},
				{Start: 1200, Size: 50},
			},
		},
		{
			name: "demonstrate unoptimal split",
			ranges: []Range{
				{Start: 1000, Size: 250},
				{Start: 1250, Size: 250},
			},
			maxSize: 240,
			expected: []Range{
				{Start: 1000, Size: 240},
				{Start: 1240, Size: 10},
				{Start: 1250, Size: 240},
				{Start: 1490, Size: 10},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := splitOversizedRanges(tt.ranges, tt.maxSize)
			require.Equal(t, tt.expected, result)
		})
	}
}

// This test is used to verify that the code correctly splits the ranges when the total size exceeds MAX_RW_COUNT.
func TestCopyFromProcess_Exceed_MAX_RW_COUNT(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	// We allocate more than MAX_RW_COUNT to trigger the MAX_RW_COUNT error if the ranges are not split correctly.
	size := ((MAX_RW_COUNT + 4*pageSize + pageSize - 1) / pageSize) * pageSize

	// Initialize the memory we will copy from.
	mem, addr, err := testutils.NewPageMmap(t, uint64(size), uint64(pageSize))
	require.NoError(t, err)

	n, err := rand.Read(mem)
	require.NoError(t, err)
	require.Equal(t, len(mem), n)

	ranges := []Range{
		// We make it so that at least one of the ranges is larger than MAX_RW_COUNT.
		{Start: int64(addr), Size: ((MAX_RW_COUNT + 2*pageSize + pageSize - 1) / pageSize) * pageSize},
		{Start: int64(addr) + ((MAX_RW_COUNT+2*pageSize+pageSize-1)/pageSize)*pageSize, Size: ((2*pageSize + pageSize - 1) / pageSize) * pageSize},
	}

	cache, err := NewCacheFromProcessMemory(
		t.Context(),
		// Regular 4KiB pages for the cache/mmap we will copy to.
		header.PageSize,
		t.TempDir()+"/cache",
		os.Getpid(),
		ranges,
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		cache.Close()
	})

	data := make([]byte, size)
	n, err = cache.ReadAt(data, 0)
	require.NoError(t, err)
	require.Equal(t, int(size), n)
	require.NoError(t, compareData(data[:n], mem[0:size]))
}

// Tests for a misalignment of the block size and the MAX_RW_COUNT that causes incorrect dirty tracking.
func TestCopyFromProcess_MAX_RW_COUNT_Misalignment_Hugepage(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.HugepageSize)
	// We allocate more than MAX_RW_COUNT/pageSize + 2 to misalign the dirty tracking if the range split is unaligned to the block size.
	size := ((MAX_RW_COUNT/pageSize + 2) * pageSize)

	mem, addr, err := testutils.NewPageMmap(t, uint64(size), uint64(pageSize))
	require.NoError(t, err)

	n, err := rand.Read(mem)
	require.NoError(t, err)
	require.Equal(t, len(mem), n)

	ranges := []Range{
		{Start: int64(addr), Size: size},
	}

	cache, err := NewCacheFromProcessMemory(
		t.Context(),
		header.HugepageSize,
		t.TempDir()+"/cache",
		os.Getpid(),
		ranges,
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		cache.Close()
	})

	for _, offset := range header.BlocksOffsets(size, pageSize) {
		buf := make([]byte, pageSize)
		n, err := cache.ReadAt(buf, offset)
		require.NoError(t, err)
		require.Equal(t, int(pageSize), n)
		require.NoError(t, compareData(buf, mem[offset:offset+pageSize]))
	}
}

func BenchmarkCopyFromHugepagesFile(b *testing.B) {
	pageSize := int64(header.HugepageSize)
	size := pageSize * 500

	b.StopTimer()
	for {
		l := int(math.Ceil(float64(size)/float64(pageSize)) * float64(pageSize))
		mem, err := syscall.Mmap(
			-1,
			0,
			l,
			syscall.PROT_READ|syscall.PROT_WRITE,
			syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS|unix.MAP_HUGETLB|unix.MAP_HUGE_2MB,
		)

		require.NoError(b, err)

		addr := uintptr(unsafe.Pointer(&mem[0]))

		n, err := rand.Read(mem)
		require.NoError(b, err)
		require.Equal(b, len(mem), n)

		var totalCovered int64
		numRanges := 40
		ranges := make([]Range, 0, numRanges)
		cur := int64(addr)

		for i := range numRanges {
			sizePages := int64(1 + (i % 5)) // pseudo-random but deterministic
			sizeR := sizePages * pageSize
			if totalCovered+sizeR > size*8/10 && i > 0 { // Stop if we have covered ~80% total
				break
			}
			ranges = append(ranges, Range{
				Start: cur,
				Size:  sizeR,
			})
			cur += sizeR + pageSize // GAP of 1 page between each range
			totalCovered += sizeR
		}

		pid := os.Getpid()

		filePath := b.TempDir() + "/cache"

		size := GetSize(ranges)

		cache, err := NewCache(size, header.PageSize, filePath, false)
		require.NoError(b, err)

		b.StartTimer()
		if !b.Loop() {
			b.StopTimer()
			err = cache.Close()
			require.NoError(b, err)
			err = syscall.Munmap(mem)
			require.NoError(b, err)

			break
		}

		err = cache.copyProcessMemory(b.Context(), pid, ranges)
		require.NoError(b, err)

		b.StopTimer()

		err = cache.Close()
		require.NoError(b, err)

		err = syscall.Munmap(mem)
		require.NoError(b, err)

		b.SetBytes(GetSize(ranges))
	}
}

// buildPackedSrcCache returns a Cache holding mem's dirty ranges concatenated
// in BitsetRanges order — the layout Cache.Dedup expects from a packed export.
func buildPackedSrcCache(t *testing.T, mem []byte, dirty *roaring.Bitmap, blockSize int64) *Cache {
	t.Helper()

	var packed []byte
	for r := range BitsetRanges(dirty, blockSize) {
		packed = append(packed, mem[r.Start:r.Start+r.Size]...)
	}

	cache, err := NewCache(int64(len(packed)), blockSize, t.TempDir()+"/src-cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	if len(packed) > 0 {
		_, err = cache.WriteAt(packed, 0)
		require.NoError(t, err)
	}

	return cache
}

func TestCacheDedup_AllPagesMatch(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 2

	data := make([]byte, size)
	_, err := rand.Read(data)
	require.NoError(t, err)

	dirty := fullDirty(size, blockSize)
	src := buildPackedSrcCache(t, data, dirty, blockSize)

	cache, meta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: data},
		dirty,
		blockSize,
		t.TempDir()+"/dedup",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	sz, err := cache.Size()
	require.NoError(t, err)
	require.EqualValues(t, 0, sz, "no pages differ — dedup cache must be empty")

	require.EqualValues(t, 0, meta.Dirty.GetCardinality())
	require.EqualValues(t, 0, meta.Empty.GetCardinality())
	require.EqualValues(t, header.PageSize, meta.BlockSize)
}

func TestCacheDedup_AllPagesDiffer(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 2

	srcData := make([]byte, size)
	_, err := rand.Read(srcData)
	require.NoError(t, err)
	origData := make([]byte, size) // all zeros — every page differs

	dirty := fullDirty(size, blockSize)
	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	cache, meta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: origData},
		dirty,
		blockSize,
		t.TempDir()+"/dedup",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	numPages := size / pageSize
	require.EqualValues(t, numPages, meta.Dirty.GetCardinality())
	require.EqualValues(t, header.PageSize, meta.BlockSize)

	for i := range numPages {
		got := make([]byte, pageSize)
		n, err := cache.ReadAt(got, i*pageSize)
		require.NoError(t, err)
		require.Equal(t, int(pageSize), n)
		require.Equal(t, srcData[i*pageSize:(i+1)*pageSize], got, "page %d", i)
	}
}

func TestCacheDedup_MixedPages(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 3 // 12 pages across 3 blocks

	origData := make([]byte, size)
	_, err := rand.Read(origData)
	require.NoError(t, err)

	srcData := make([]byte, size)
	copy(srcData, origData)

	differingPage2 := bytes.Repeat([]byte{0xAA}, int(pageSize))
	differingPage7 := bytes.Repeat([]byte{0xBB}, int(pageSize))
	copy(srcData[2*pageSize:], differingPage2)
	copy(srcData[7*pageSize:], differingPage7)

	dirty := fullDirty(size, blockSize)
	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	cache, meta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: origData},
		dirty,
		blockSize,
		t.TempDir()+"/dedup",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 2, meta.Dirty.GetCardinality())
	require.True(t, meta.Dirty.Contains(2))
	require.True(t, meta.Dirty.Contains(7))

	got := make([]byte, pageSize)
	_, err = cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, differingPage2, got, "first packed page is the differing page at idx 2")

	_, err = cache.ReadAt(got, pageSize)
	require.NoError(t, err)
	require.Equal(t, differingPage7, got, "second packed page is the differing page at idx 7")
}

// Regression: pageIdx is computed from r.Start + chunkOff + i and must
// correctly account for non-zero Range starts.
func TestCacheDedup_NonZeroRangeStart(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 3

	origData := make([]byte, size)
	_, err := rand.Read(origData)
	require.NoError(t, err)

	srcData := make([]byte, size)
	copy(srcData, origData)

	// Differ page 9 (inside the third block).
	differing := bytes.Repeat([]byte{0xCC}, int(pageSize))
	copy(srcData[9*pageSize:], differing)

	// Only the third block is dirty — Range starts at blockSize*2.
	dirty := roaring.New()
	dirty.Add(2)

	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	cache, meta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: origData},
		dirty,
		blockSize,
		t.TempDir()+"/dedup",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 1, meta.Dirty.GetCardinality())
	require.True(t, meta.Dirty.Contains(9))

	got := make([]byte, pageSize)
	_, err = cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, differing, got)
}

// Two non-contiguous block-level Ranges. Exercises cacheOff advancement
// across independent BitsetRanges iterations.
func TestCacheDedup_MultipleRanges(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 4 // 16 pages

	origData := make([]byte, size)
	_, err := rand.Read(origData)
	require.NoError(t, err)

	srcData := make([]byte, size)
	copy(srcData, origData)

	p1 := bytes.Repeat([]byte{0xD1}, int(pageSize))
	p13 := bytes.Repeat([]byte{0xD2}, int(pageSize))
	copy(srcData[1*pageSize:], p1)   // inside block 0
	copy(srcData[13*pageSize:], p13) // inside block 3

	dirty := roaring.New()
	dirty.Add(0)
	dirty.Add(3)

	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	cache, meta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: origData},
		dirty,
		blockSize,
		t.TempDir()+"/dedup",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 2, meta.Dirty.GetCardinality())
	require.True(t, meta.Dirty.Contains(1))
	require.True(t, meta.Dirty.Contains(13))

	got := make([]byte, pageSize)
	_, err = cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, p1, got)

	_, err = cache.ReadAt(got, pageSize)
	require.NoError(t, err)
	require.Equal(t, p13, got)
}

// BitsetRanges merges contiguous bits into one Range; the inner loop must
// walk r.Size, not blockSize.
func TestCacheDedup_ContiguousDirtyBlocks(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 5 // 20 pages

	origData := make([]byte, size)
	_, err := rand.Read(origData)
	require.NoError(t, err)

	srcData := make([]byte, size)
	copy(srcData, origData)

	// Modify one page in each of three contiguous dirty blocks.
	page4 := bytes.Repeat([]byte{0xA1}, int(pageSize))
	page9 := bytes.Repeat([]byte{0xA2}, int(pageSize))
	page14 := bytes.Repeat([]byte{0xA3}, int(pageSize))
	copy(srcData[4*pageSize:], page4)   // block 1
	copy(srcData[9*pageSize:], page9)   // block 2
	copy(srcData[14*pageSize:], page14) // block 3

	// Blocks 1, 2, 3 — BitsetRanges merges them into a single 3-block Range.
	dirty := roaring.New()
	dirty.Add(1)
	dirty.Add(2)
	dirty.Add(3)

	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	cache, meta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: origData},
		dirty,
		blockSize,
		t.TempDir()+"/dedup",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 3, meta.Dirty.GetCardinality())
	require.True(t, meta.Dirty.Contains(4))
	require.True(t, meta.Dirty.Contains(9))
	require.True(t, meta.Dirty.Contains(14))

	cases := []struct {
		offset int64
		want   []byte
	}{
		{0, page4},
		{pageSize, page9},
		{2 * pageSize, page14},
	}
	for _, tc := range cases {
		got := make([]byte, pageSize)
		_, err := cache.ReadAt(got, tc.offset)
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "page at packed offset %d", tc.offset)
	}
}

func TestCacheDedup_EmptyDirtyBitmap(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 2

	data := make([]byte, size)
	dirty := roaring.New()
	src := buildPackedSrcCache(t, data, dirty, blockSize)

	cache, meta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: data},
		dirty,
		blockSize,
		t.TempDir()+"/dedup",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	sz, err := cache.Size()
	require.NoError(t, err)
	require.EqualValues(t, 0, sz)
	require.EqualValues(t, 0, meta.Dirty.GetCardinality())
	require.EqualValues(t, 0, meta.Empty.GetCardinality())
}

func TestCacheDedup_ContextCancellation(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 4

	data := make([]byte, size)
	_, err := rand.Read(data)
	require.NoError(t, err)

	dirty := fullDirty(size, blockSize)
	src := buildPackedSrcCache(t, data, dirty, blockSize)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err = src.Dedup(
		ctx,
		&fakeOriginalDevice{data: data},
		dirty,
		blockSize,
		t.TempDir()+"/dedup",
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestCacheDedup_OriginalMemfileReadError(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize

	data := make([]byte, size)
	_, err := rand.Read(data)
	require.NoError(t, err)

	dirty := fullDirty(size, blockSize)
	src := buildPackedSrcCache(t, data, dirty, blockSize)

	sentinel := errors.New("base read failed")
	_, _, err = src.Dedup(
		t.Context(),
		&erroringOriginalDevice{
			fakeOriginalDevice: fakeOriginalDevice{data: data},
			err:                sentinel,
		},
		dirty,
		blockSize,
		t.TempDir()+"/dedup",
	)
	require.ErrorIs(t, err, sentinel)
}

// Pages that match the base and happen to be all-zero must be recorded in
// Empty (so the merged header maps them to uuid.Nil → zero-fill at read),
// rather than relying on a fall-through to the parent's diff. Non-zero
// matches must stay unmapped so the merge keeps the parent's real mapping.
func TestCacheDedup_ZeroMatchingPagesGoIntoEmpty(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 2 // 8 pages

	// Base: pages 0..3 zero, pages 4..7 random non-zero.
	origData := make([]byte, size)
	_, err := rand.Read(origData[4*pageSize:])
	require.NoError(t, err)

	// Source matches base exactly — no Dirty pages.
	srcData := make([]byte, size)
	copy(srcData, origData)

	dirty := fullDirty(size, blockSize)
	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	cache, meta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: origData},
		dirty,
		blockSize,
		t.TempDir()+"/dedup",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 0, meta.Dirty.GetCardinality(), "no pages differ from base")

	// Only the zero pages (0..3) should be in Empty.
	require.EqualValues(t, 4, meta.Empty.GetCardinality())
	for i := range uint32(4) {
		require.True(t, meta.Empty.Contains(i), "zero-matching page %d should be in Empty", i)
	}
	for i := uint32(4); i < 8; i++ {
		require.False(t, meta.Empty.Contains(i), "non-zero-matching page %d should not be in Empty", i)
	}
}
