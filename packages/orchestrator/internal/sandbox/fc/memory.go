package fc

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/bits-and-blooms/bitset"
	"github.com/tklauser/go-sysconf"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// IOV_MAX is the limit of the vectors that can be passed in a single ioctl call.
var IOV_MAX = utils.Must(getIOVMax())

const (
	oomMinBackoff = 100 * time.Millisecond
	oomMaxJitter  = 100 * time.Millisecond
)

// MemoryInfo returns the memory info for the sandbox.
// The dirty field represents mincore resident pages—essentially pages that were faulted in.
// The empty field represents pages that are *resident*, but also completely empty.
func (p *Process) MemoryInfo(ctx context.Context, blockSize int64) (*header.DiffMetadata, error) {
	return p.client.memoryInfo(ctx, blockSize)
}

func (p *Process) ExportMemory(
	ctx context.Context,
	include *bitset.BitSet,
	cachePath string,
	blockSize int64,
) (*block.Cache, error) {
	m, err := p.client.memoryMappings(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get memory mappings: %w", err)
	}

	var remoteRanges []block.Range

	for r := range block.BitsetRanges(include, blockSize) {
		hostVirtRanges, err := m.GetHostVirtRanges(r.Start, int64(r.Size))
		if err != nil {
			return nil, fmt.Errorf("failed to get host virt ranges: %w", err)
		}

		remoteRanges = append(remoteRanges, hostVirtRanges...)
	}

	size := block.GetSize(remoteRanges)

	pid, err := p.Pid()
	if err != nil {
		return nil, fmt.Errorf("failed to get pid: %w", err)
	}

	cache, err := block.NewCache(int64(size), blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	err = copyProcessMemory(ctx, pid, remoteRanges, cache)
	if err != nil {
		// Close the cache even if the copy fails.
		return nil, fmt.Errorf("failed to copy process memory: %w", errors.Join(err, cache.Close()))
	}

	return cache, nil
}

func copyProcessMemory(
	ctx context.Context,
	pid int,
	ranges []block.Range,
	cache *block.Cache,
) error {
	var start uint64

	for i := 0; i < len(ranges); i += int(IOV_MAX) {
		segmentRanges := ranges[i:min(i+int(IOV_MAX), len(ranges))]

		remote := make([]unix.RemoteIovec, len(segmentRanges))

		var segmentSize uint64

		for j, r := range segmentRanges {
			remote[j] = unix.RemoteIovec{
				Base: uintptr(r.Start),
				Len:  int(r.Size),
			}

			segmentSize += r.Size
		}

		local := []unix.Iovec{
			{
				Base: cache.Address(start),
				// We could keep this as full cache length, but we might as well be exact here.
				Len: segmentSize,
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

			if uint64(n) != segmentSize {
				return fmt.Errorf("failed to read memory: expected %d bytes, got %d", segmentSize, n)
			}

			start += segmentSize

			break
		}
	}

	return nil
}

func getIOVMax() (int64, error) {
	iovMax, err := sysconf.Sysconf(sysconf.SC_IOV_MAX)
	if err != nil {
		return 0, fmt.Errorf("failed to get IOV_MAX: %w", err)
	}

	return iovMax, nil
}
