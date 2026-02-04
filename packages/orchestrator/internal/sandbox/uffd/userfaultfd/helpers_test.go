package userfaultfd

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/bits-and-blooms/bitset"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
)

// HugepageStats contains hugepage memory usage information.
type HugepageStats struct {
	Total    uint64 // Total number of hugepages
	Free     uint64 // Number of free hugepages
	Reserved uint64 // Number of reserved hugepages
	Surplus  uint64 // Number of surplus hugepages
}

// Used returns the number of hugepages currently in use.
func (s HugepageStats) Used() uint64 {
	return s.Total - s.Free
}

// GetHugepageStats reads hugepage statistics from /proc/meminfo.
func GetHugepageStats() (HugepageStats, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return HugepageStats{}, fmt.Errorf("failed to open /proc/meminfo: %w", err)
	}
	defer f.Close()

	var stats HugepageStats
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		var target *uint64
		switch fields[0] {
		case "HugePages_Total:":
			target = &stats.Total
		case "HugePages_Free:":
			target = &stats.Free
		case "HugePages_Rsvd:":
			target = &stats.Reserved
		case "HugePages_Surp:":
			target = &stats.Surplus
		default:
			continue
		}

		val, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return HugepageStats{}, fmt.Errorf("failed to parse %s: %w", fields[0], err)
		}
		*target = val
	}

	return stats, scanner.Err()
}

type testConfig struct {
	name string
	// Page size of the memory area.
	pagesize uint64
	// Number of pages in the memory area.
	numberOfPages uint64
	// Operations to trigger on the memory area.
	operations []operation
}

type operationMode uint32

const (
	operationModeRead operationMode = 1 << iota
	operationModeWrite
	operationModeDontNeed
	operationModeRemove
)

type operation struct {
	// Offset in bytes. Must be smaller than the (numberOfPages-1) * pagesize as it reads a page and it must be aligned to the pagesize from the testConfig.
	offset int64
	mode   operationMode
}

type testHandler struct {
	memoryArea *[]byte
	pagesize   uint64
	data       *MemorySlicer
	// Returns offsets of the pages that were faulted.
	// It can only be called once.
	offsetsOnce func() ([]uint, error)
	mutex       sync.Mutex
}

func (h *testHandler) executeRead(ctx context.Context, op operation) error {
	readBytes := (*h.memoryArea)[op.offset : op.offset+int64(h.pagesize)]

	expectedBytes, err := h.data.Slice(ctx, op.offset, int64(h.pagesize))
	if err != nil {
		return err
	}

	// The bytes.Equal is the first place in this flow that actually touches the uffd managed memory and triggers the pagefault, so any deadlocks will manifest here.
	if !bytes.Equal(readBytes, expectedBytes) {
		idx, want, got := testutils.FirstDifferentByte(readBytes, expectedBytes)

		return fmt.Errorf("content mismatch: want '%x, got %x at index %d", want, got, idx)
	}

	return nil
}

func (h *testHandler) executeWrite(ctx context.Context, op operation) error {
	bytesToWrite, err := h.data.Slice(ctx, op.offset, int64(h.pagesize))
	if err != nil {
		return err
	}

	// An unprotected parallel write to map might result in an undefined behavior.
	h.mutex.Lock()
	defer h.mutex.Unlock()

	n := copy((*h.memoryArea)[op.offset:op.offset+int64(h.pagesize)], bytesToWrite)
	if n != int(h.pagesize) {
		return fmt.Errorf("copy length mismatch: want %d, got %d", h.pagesize, n)
	}

	return nil
}

func (h *testHandler) dontNeedMemory(ctx context.Context, op operation) error {
	before, err := GetHugepageStats()
	if err != nil {
		return fmt.Errorf("failed to get hugepage stats before: %w", err)
	}

	err = unix.Madvise((*h.memoryArea)[op.offset:op.offset+int64(h.pagesize)], unix.MADV_FREE)
	if err != nil {
		return fmt.Errorf("failed to madvise: %w", err)
	}

	after, err := GetHugepageStats()
	if err != nil {
		return fmt.Errorf("failed to get hugepage stats after: %w", err)
	}

	fmt.Printf("MADV_DONTNEED: before(used=%d, free=%d) -> after(used=%d, free=%d), delta_free=%+d\n",
		before.Used(), before.Free, after.Used(), after.Free, int64(after.Free)-int64(before.Free))

	return nil
}

// Get a bitset of the offsets of the operations for the given mode.
func getOperationsOffsets(ops []operation, m operationMode) []uint {
	b := bitset.New(0)

	for _, operation := range ops {
		if operation.mode&m != 0 {
			b.Set(uint(operation.offset))
		}
	}

	return slices.Collect(b.EachSet())
}
