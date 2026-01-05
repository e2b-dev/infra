package block

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// FileSize returns the size of the cache on disk.
// The size might differ from the dirty size, as it may not be fully on disk.
func (c *Cache) FileSize() (int64, error) {
	var stat syscall.Stat_t
	err := syscall.Stat(c.filePath, &stat)
	if err != nil {
		return 0, fmt.Errorf("failed to get file stats: %w", err)
	}

	var fsStat syscall.Statfs_t
	err = syscall.Statfs(c.filePath, &fsStat)
	if err != nil {
		return 0, fmt.Errorf("failed to get disk stats for path %s: %w", c.filePath, err)
	}

	return stat.Blocks * fsStat.Bsize, nil
}

func (c *Cache) copyProcessMemory(
	ctx context.Context,
	pid int,
	ranges []Range,
) error {
	var start int64

	for i := 0; i < len(ranges); i += IOV_MAX {
		segmentRanges := ranges[i:min(i+IOV_MAX, len(ranges))]

		remote := make([]unix.RemoteIovec, len(segmentRanges))

		var segmentSize int64

		for j, r := range segmentRanges {
			remote[j] = unix.RemoteIovec{
				Base: uintptr(r.Start),
				Len:  int(r.Size),
			}

			segmentSize += r.Size
		}

		local := []unix.Iovec{
			{
				Base: c.address(start),
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
				c.dirty.Store(start+blockOff, struct{}{})
			}

			start += segmentSize

			break
		}
	}

	return nil
}
