package rootfs

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
)

const ublkBackendLockStripes = 256

// ublkBackend Adapt block.Device to ublk-go's io.ReaderAt/WriterAt.
type ublkBackend struct {
	ctx       context.Context
	dev       block.Device
	blockSize int64
	bufPool   sync.Pool
	locks     []sync.Mutex
}

func newUblkBackend(ctx context.Context, dev block.Device) *ublkBackend {
	bs := dev.BlockSize()
	return &ublkBackend{
		ctx:       ctx,
		dev:       dev,
		blockSize: bs,
		locks:     make([]sync.Mutex, ublkBackendLockStripes),
		bufPool: sync.Pool{
			New: func() any {
				b := make([]byte, bs)
				return &b
			},
		},
	}
}

func (b *ublkBackend) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	if b.isAligned(off, len(p)) {
		tmp := make([]byte, len(p))
		n, err := b.dev.ReadAt(b.ctx, tmp, off)
		if n > 0 {
			copied := n
			if copied > len(p) {
				copied = len(p)
			}
			copy(p, tmp[:copied])
		}

		if err != nil {
			if n > len(p) {
				n = len(p)
			}

			return n, err
		}

		return len(p), nil
	}

	alignedOff, alignedLen := b.alignedRange(off, len(p))
	tmp := make([]byte, alignedLen)

	n, err := b.dev.ReadAt(b.ctx, tmp, alignedOff)
	if err != nil {
		return n, err
	}

	start := int(off - alignedOff)
	copy(p, tmp[start:start+len(p)])

	return len(p), nil
}

func (b *ublkBackend) WriteAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	alignedOff, alignedLen := b.alignedRange(off, len(p))
	unlock := b.lockRange(alignedOff, alignedLen)
	defer unlock()

	if b.isAligned(off, len(p)) {
		return b.dev.WriteAt(p, off)
	}

	tmp := make([]byte, alignedLen)
	requestEnd := off + int64(len(p))

	for blockOff := alignedOff; blockOff < alignedOff+int64(alignedLen); blockOff += b.blockSize {
		blockStart := blockOff
		blockEnd := blockOff + b.blockSize
		tmpStart := int(blockStart - alignedOff)
		tmpEnd := tmpStart + int(b.blockSize)
		blockBuf := tmp[tmpStart:tmpEnd]

		writeStart := max(blockStart, off)
		writeEnd := min(blockEnd, requestEnd)

		if writeStart > blockStart || writeEnd < blockEnd {
			n, err := b.dev.ReadAt(b.ctx, blockBuf, blockStart)
			if err != nil {
				return n, err
			}
		}

		copyStart := int(writeStart - off)
		copyEnd := int(writeEnd - off)
		blockCopyStart := int(writeStart - blockStart)
		copy(blockBuf[blockCopyStart:blockCopyStart+(copyEnd-copyStart)], p[copyStart:copyEnd])
	}

	n, err := b.dev.WriteAt(tmp, alignedOff)
	if err != nil {
		return n, err
	}

	if n != alignedLen {
		return n, fmt.Errorf("short aligned write: wrote %d want %d", n, alignedLen)
	}

	return len(p), nil
}

func (b *ublkBackend) isAligned(off int64, length int) bool {
	return off%b.blockSize == 0 && int64(length)%b.blockSize == 0
}

func (b *ublkBackend) alignedRange(off int64, length int) (int64, int) {
	alignedOff := (off / b.blockSize) * b.blockSize
	end := off + int64(length)
	alignedEnd := ((end + b.blockSize - 1) / b.blockSize) * b.blockSize

	return alignedOff, int(alignedEnd - alignedOff)
}

func (b *ublkBackend) lockRange(off int64, length int) func() {
	if length <= 0 {
		return func() {}
	}

	startBlock := off / b.blockSize
	endBlock := (off + int64(length) - 1) / b.blockSize
	stripes := make([]int, 0, endBlock-startBlock+1)
	seen := make(map[int]struct{}, endBlock-startBlock+1)

	for blockIdx := startBlock; blockIdx <= endBlock; blockIdx++ {
		stripe := int(blockIdx % int64(len(b.locks)))
		if _, ok := seen[stripe]; ok {
			continue
		}

		seen[stripe] = struct{}{}
		stripes = append(stripes, stripe)
	}

	sort.Ints(stripes)
	for _, stripe := range stripes {
		b.locks[stripe].Lock()
	}

	return func() {
		for i := len(stripes) - 1; i >= 0; i-- {
			b.locks[stripes[i]].Unlock()
		}
	}
}
