//go:build linux

package block

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	oomMinBackoff = 100 * time.Millisecond
	oomMaxJitter  = 100 * time.Millisecond
)

func (c *Cache) copyProcessMemory(
	ctx context.Context,
	pid int,
	rs []Range,
) error {
	// We need to align the maximum read/write count to the block size, so we can use mark the offsets as dirty correctly.
	// Because the MAX_RW_COUNT is not aligned to arbitrary block sizes, we need to align it to the block size we use for the cache.
	alignedRwCount := getAlignedMaxRwCount(c.blockSize)

	// We need to split the ranges because the Kernel does not support reading/writing more than MAX_RW_COUNT bytes in a single operation.
	ranges := splitOversizedRanges(rs, alignedRwCount)

	var offset int64
	var rangeIdx int64

	for {
		var remote []unix.RemoteIovec

		var segmentSize int64

		// We iterate over the range of all ranges until we have reached the limit of the IOV_MAX,
		// or until the next range would overflow the MAX_RW_COUNT.
		for ; rangeIdx < int64(len(ranges)); rangeIdx++ {
			r := ranges[rangeIdx]

			if len(remote) == IOV_MAX {
				break
			}

			if segmentSize+r.Size > alignedRwCount {
				break
			}

			remote = append(remote, unix.RemoteIovec{
				Base: uintptr(r.Start),
				Len:  int(r.Size),
			})

			segmentSize += r.Size
		}

		if len(remote) == 0 {
			break
		}

		address, err := c.address(offset)
		if err != nil {
			return fmt.Errorf("failed to get address: %w", err)
		}

		local := []unix.Iovec{
			{
				Base: address,
				// We could keep this as full cache length, but we might as well be exact here.
				Len: uint64(segmentSize),
			},
		}

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// We could retry only on the remaining segment size, but for simplicity we retry the whole segment.
			n, err := unix.ProcessVMReadv(pid,
				local,
				remote,
				0,
			)
			if errors.Is(err, unix.EAGAIN) {
				continue
			}
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.ENOMEM) {
				time.Sleep(oomMinBackoff + time.Duration(rand.Intn(int(oomMaxJitter.Milliseconds())))*time.Millisecond)

				continue
			}

			if err != nil {
				return fmt.Errorf("failed to read memory: %w", err)
			}

			if int64(n) != segmentSize {
				return fmt.Errorf("failed to read memory: expected %d bytes, got %d", segmentSize, n)
			}

			for _, blockOff := range header.BlocksOffsets(segmentSize, c.blockSize) {
				c.dirty.Store(offset+blockOff, struct{}{})
			}

			offset += segmentSize

			break
		}
	}

	return nil
}
