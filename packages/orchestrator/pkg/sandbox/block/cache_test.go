package block

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"os"
	"syscall"
	"testing"
	"unsafe"

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

func TestCacheExportToDiff_ZeroDirtyBlockMarkedEmpty(t *testing.T) {
	t.Parallel()

	const blockSize = header.RootfsBlockSize
	cache, err := NewCache(blockSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})

	zeroBlock := make([]byte, blockSize)
	n, err := cache.WriteAt(zeroBlock, 0)
	require.NoError(t, err)
	require.Equal(t, int(blockSize), n)

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	diffMetadata, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.EqualValues(t, 1, diffMetadata.Dirty.Count(), "zero-filled dirty block should be emitted as dirty payload")
	require.EqualValues(t, 0, diffMetadata.Empty.Count(), "zero-filled dirty block should not be tracked in empty metadata")

	stat, err := out.Stat()
	require.NoError(t, err)
	require.EqualValues(t, blockSize, stat.Size(), "zero-filled dirty block should write block payload bytes")
}

func TestCacheExportToDiff_NonZeroDirtyBlockMarkedDirty(t *testing.T) {
	t.Parallel()

	const blockSize = header.RootfsBlockSize
	cache, err := NewCache(blockSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})

	nonZeroBlock := bytes.Repeat([]byte{0x01}, int(blockSize))
	n, err := cache.WriteAt(nonZeroBlock, 0)
	require.NoError(t, err)
	require.Equal(t, int(blockSize), n)

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	diffMetadata, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.EqualValues(t, 1, diffMetadata.Dirty.Count(), "non-zero dirty block should be tracked in dirty metadata")
	require.EqualValues(t, 0, diffMetadata.Empty.Count(), "non-zero dirty block should not be tracked as empty")

	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)

	payload, err := io.ReadAll(out)
	require.NoError(t, err)
	require.Equal(t, nonZeroBlock, payload)
}

func TestExportToDiff_NoDirtyBlocks(t *testing.T) {
	t.Parallel()

	blockSize := int64(4096)
	size := blockSize * 8

	cache, err := NewCache(size, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { cache.Close() })

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	meta, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.Equal(t, uint(0), meta.Dirty.Count(), "no dirty blocks expected")

	info, err := out.Stat()
	require.NoError(t, err)
	require.Equal(t, int64(0), info.Size(), "output file should be empty")
}

func TestExportToDiff_SingleBlock(t *testing.T) {
	t.Parallel()

	blockSize := int64(4096)
	size := blockSize * 8

	cache, err := NewCache(size, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { cache.Close() })

	pattern := make([]byte, blockSize)
	_, err = rand.Read(pattern)
	require.NoError(t, err)

	_, err = cache.WriteAt(pattern, 3*blockSize)
	require.NoError(t, err)

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	meta, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.Equal(t, uint(1), meta.Dirty.Count())
	require.True(t, meta.Dirty.Test(3), "block 3 should be dirty")

	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	exported, err := io.ReadAll(out)
	require.NoError(t, err)

	require.Len(t, exported, int(blockSize))
	require.Equal(t, pattern, exported)
}

func TestExportToDiff_SparseBlocks(t *testing.T) {
	t.Parallel()

	blockSize := int64(4096)
	numBlocks := int64(16)
	size := blockSize * numBlocks

	cache, err := NewCache(size, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { cache.Close() })

	dirtyIndices := []int64{1, 5, 14}
	patterns := make(map[int64][]byte)

	for _, idx := range dirtyIndices {
		buf := make([]byte, blockSize)
		_, err = rand.Read(buf)
		require.NoError(t, err)
		patterns[idx] = buf

		_, err = cache.WriteAt(buf, idx*blockSize)
		require.NoError(t, err)
	}

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	meta, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.Equal(t, uint(len(dirtyIndices)), meta.Dirty.Count())
	for _, idx := range dirtyIndices {
		require.True(t, meta.Dirty.Test(uint(idx)), "block %d should be dirty", idx)
	}

	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	exported, err := io.ReadAll(out)
	require.NoError(t, err)

	require.Len(t, exported, int(blockSize)*len(dirtyIndices))

	for i, idx := range dirtyIndices {
		start := int64(i) * blockSize
		require.Equal(t, patterns[idx], exported[start:start+blockSize], "block %d content mismatch", idx)
	}
}

func TestExportToDiff_AllBlocksDirty(t *testing.T) {
	t.Parallel()

	blockSize := int64(4096)
	numBlocks := int64(8)
	size := blockSize * numBlocks

	cache, err := NewCache(size, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { cache.Close() })

	fullData := make([]byte, size)
	_, err = rand.Read(fullData)
	require.NoError(t, err)

	_, err = cache.WriteAt(fullData, 0)
	require.NoError(t, err)

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	meta, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.Equal(t, uint(numBlocks), meta.Dirty.Count())

	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	exported, err := io.ReadAll(out)
	require.NoError(t, err)

	require.Equal(t, fullData, exported)
}

func TestExportToDiff_ContiguousBlocks(t *testing.T) {
	t.Parallel()

	blockSize := int64(4096)
	numBlocks := int64(16)
	size := blockSize * numBlocks

	cache, err := NewCache(size, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { cache.Close() })

	contiguousData := make([]byte, blockSize*4)
	_, err = rand.Read(contiguousData)
	require.NoError(t, err)

	_, err = cache.WriteAt(contiguousData, 4*blockSize)
	require.NoError(t, err)

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	meta, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.Equal(t, uint(4), meta.Dirty.Count())

	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	exported, err := io.ReadAll(out)
	require.NoError(t, err)

	require.Equal(t, contiguousData, exported)
}

func TestExportToDiff_ZeroSizeCache(t *testing.T) {
	t.Parallel()

	cache, err := NewCache(0, header.PageSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { cache.Close() })

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	meta, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.Equal(t, uint(0), meta.Dirty.Count())
	require.Equal(t, uint(0), meta.Empty.Count())
}

func TestExportToDiff_LargerBlockSize(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.HugepageSize)
	numBlocks := int64(4)
	size := blockSize * numBlocks

	cache, err := NewCache(size, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { cache.Close() })

	data := make([]byte, blockSize)
	_, err = rand.Read(data)
	require.NoError(t, err)

	_, err = cache.WriteAt(data, blockSize)
	require.NoError(t, err)

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	meta, err := cache.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.Equal(t, uint(1), meta.Dirty.Count())
	require.True(t, meta.Dirty.Test(1))
	require.Equal(t, uint(0), meta.Empty.Count())

	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	exported, err := io.ReadAll(out)
	require.NoError(t, err)

	require.Equal(t, data, exported)
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
