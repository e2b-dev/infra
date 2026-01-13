package header

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/bits-and-blooms/bitset"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
)

const (
	PageSize        = 2 << 11
	HugepageSize    = 2 << 20
	RootfsBlockSize = 2 << 11

	// BufferPoolSize is the size of each buffer in the pool (4MB = 2 huge pages)
	BufferPoolSize = 4 * 1024 * 1024
	// MaxParallelWorkers limits parallel processing
	MaxParallelWorkers = 8
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/storage/header")

var (
	EmptyHugePage = make([]byte, HugepageSize)
	EmptyBlock    = make([]byte, RootfsBlockSize)
)

// bufferPool provides reusable buffers for parallel diff processing
var bufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, BufferPoolSize)
		return &buf
	},
}

func IsEmptyBlock(block []byte, blockSize int64) (bool, error) {
	var emptyBuf []byte
	switch blockSize {
	case HugepageSize:
		emptyBuf = EmptyHugePage
	case RootfsBlockSize:
		emptyBuf = EmptyBlock
	default:
		return false, fmt.Errorf("block size not supported: %d", blockSize)
	}

	return bytes.Equal(block, emptyBuf), nil
}

// changedPage represents a 4KB page that differs from the original
type changedPage struct {
	pageIdx uint   // 4KB page index
	data    []byte // Copy of the 4KB page data
}

// WriteDiffWithTrace writes the diff between source and originalMemfile at 4KB page granularity.
// It compares each dirty huge page (blockSize) with the original and only writes the 4KB pages that differ.
// Processing is done in parallel using a buffer pool.
func WriteDiffWithTrace(ctx context.Context, source io.ReaderAt, originalMemfile Slicer, blockSize int64, dirtyBig *bitset.BitSet, diff io.Writer) (*DiffMetadata, error) {
	_, childSpan := tracer.Start(ctx, "create-diff")
	defer childSpan.End()
	childSpan.SetAttributes(attribute.Int64("dirty.length", int64(dirtyBig.Count())))
	childSpan.SetAttributes(attribute.Int64("block.size", blockSize))

	return writeDiffParallel(ctx, source, originalMemfile, blockSize, dirtyBig, diff)
}

func writeDiffParallel(ctx context.Context, source io.ReaderAt, originalMemfile Slicer, blockSize int64, dirtyBig *bitset.BitSet, diff io.Writer) (*DiffMetadata, error) {
	// Collect all dirty huge page indices
	var hugePageIndices []uint
	for idx, ok := dirtyBig.NextSet(0); ok; idx, ok = dirtyBig.NextSet(idx + 1) {
		hugePageIndices = append(hugePageIndices, idx)
	}

	if len(hugePageIndices) == 0 {
		return &DiffMetadata{
			Dirty:     bitset.New(0),
			Empty:     bitset.New(0),
			BlockSize: blockSize,
		}, nil
	}

	// Channel to collect changed pages from workers
	resultsCh := make(chan []changedPage, len(hugePageIndices))

	// Process huge pages in parallel
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(MaxParallelWorkers)

	for _, hugePageIdx := range hugePageIndices {
		hugePageIdx := hugePageIdx // capture for goroutine
		eg.Go(func() error {
			return processHugePage(egCtx, source, originalMemfile, blockSize, hugePageIdx, resultsCh)
		})
	}

	// Wait for all workers and close results channel
	go func() {
		eg.Wait()
		close(resultsCh)
	}()

	// Collect all results
	var allChanges []changedPage
	for changes := range resultsCh {
		allChanges = append(allChanges, changes...)
	}

	// Check for errors from workers
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	// Sort by page index for consistent ordering
	sortChangedPages(allChanges)

	// Build dirty bitset and write to diff
	dirty := bitset.New(0)
	empty := bitset.New(0)

	for _, change := range allChanges {
		dirty.Set(change.pageIdx)
		if _, err := diff.Write(change.data); err != nil {
			return nil, fmt.Errorf("error writing to diff: %w", err)
		}
	}

	return &DiffMetadata{
		Dirty:     dirty,
		Empty:     empty,
		BlockSize: blockSize,
	}, nil
}

func processHugePage(ctx context.Context, source io.ReaderAt, originalMemfile Slicer, blockSize int64, hugePageIdx uint, results chan<- []changedPage) error {
	// Get buffer from pool
	bufPtr := bufferPool.Get().(*[]byte)
	buf := *bufPtr
	defer bufferPool.Put(bufPtr)

	// Ensure buffer is large enough for one huge page
	if int64(len(buf)) < blockSize {
		buf = make([]byte, blockSize)
	}

	offset := int64(hugePageIdx) * blockSize

	// Read current state from source
	_, err := source.ReadAt(buf[:blockSize], offset)
	if err != nil {
		return fmt.Errorf("error reading from source at offset %d: %w", offset, err)
	}

	// Read original state
	originalPage, err := originalMemfile.Slice(ctx, offset, blockSize)
	if err != nil {
		return fmt.Errorf("error reading from original memfile at offset %d: %w", offset, err)
	}

	// Compare 4KB pages and collect changes
	var changes []changedPage
	pagesPerHugePage := blockSize / PageSize

	for i := int64(0); i < pagesPerHugePage; i++ {
		pageStart := i * PageSize
		pageEnd := pageStart + PageSize

		if bytes.Equal(buf[pageStart:pageEnd], originalPage[pageStart:pageEnd]) {
			continue
		}

		// Calculate global 4KB page index
		globalPageIdx := uint(hugePageIdx)*uint(pagesPerHugePage) + uint(i)

		// Copy the page data (buffer will be returned to pool)
		pageCopy := make([]byte, PageSize)
		copy(pageCopy, buf[pageStart:pageEnd])

		changes = append(changes, changedPage{
			pageIdx: globalPageIdx,
			data:    pageCopy,
		})
	}

	results <- changes
	return nil
}

func sortChangedPages(pages []changedPage) {
	// Simple insertion sort - typically small number of changes
	for i := 1; i < len(pages); i++ {
		j := i
		for j > 0 && pages[j].pageIdx < pages[j-1].pageIdx {
			pages[j], pages[j-1] = pages[j-1], pages[j]
			j--
		}
	}
}
