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
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var IOV_MAX = utils.Must(getIOVMax())

const (
	oomBackoff = 100 * time.Millisecond
	oomJitter  = 100 * time.Millisecond
)

func (p *Process) Memory(ctx context.Context) (*memory.View, error) {
	pid, err := p.Pid()
	if err != nil {
		return nil, fmt.Errorf("failed to get process pid: %w", err)
	}

	info, err := p.client.instanceInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance info: %w", err)
	}

	mapping, err := memory.NewMappingFromFCInfo(info.MemoryRegions)
	if err != nil {
		return nil, fmt.Errorf("failed to create memory mapping: %w", err)
	}

	view, err := memory.NewView(pid, mapping)
	if err != nil {
		return nil, fmt.Errorf("failed to create memory view: %w", err)
	}

	return view, nil
}

func (p *Process) CreateMemoryFile(
	ctx context.Context,
	memfilePath string,
	blockSize int64,
	dirty *bitset.BitSet,
	empty *bitset.BitSet,
) (*block.Cache, error) {
	info, err := p.client.instanceInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance info: %w", err)
	}

	m, err := memory.NewMappingFromFCInfo(info.MemoryRegions)
	if err != nil {
		return nil, fmt.Errorf("failed to create memory mapping: %w", err)
	}

	toCopy := dirty.Difference(empty)

	var remoteRanges []block.Range

	for r := range block.BitsetRanges(toCopy) {
		hostVirtRanges, err := m.GetHostVirtRanges(r.Start, int64(r.Size))
		if err != nil {
			return nil, fmt.Errorf("failed to get host virt ranges: %w", err)
		}

		remoteRanges = append(remoteRanges, hostVirtRanges...)
	}

	size := block.GetSize(remoteRanges)

	cache, err := block.NewCache(int64(size), blockSize, memfilePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	pid, err := p.Pid()
	if err != nil {
		return nil, fmt.Errorf("failed to get process pid: %w", err)
	}

	err = copyProcessMemory(ctx, pid, remoteRanges, cache)
	if err != nil {
		return nil, fmt.Errorf("failed to copy process memory: %w", err)
	}

	return cache, nil
}

func createMemory(
	ctx context.Context,
	memfilePath string,
	pid int,
	m *memory.Mapping,
	dirty *bitset.BitSet,
	empty *bitset.BitSet,
	blockSize int64,
) (*block.Cache, error) {
	toCopy := dirty.Difference(empty)

	var remoteRanges []block.Range

	for r := range block.BitsetRanges(toCopy) {
		hostVirtRanges, err := m.GetHostVirtRanges(r.Start, int64(r.Size))
		if err != nil {
			return nil, fmt.Errorf("failed to get host virt ranges: %w", err)
		}

		remoteRanges = append(remoteRanges, hostVirtRanges...)
	}

	size := block.GetSize(remoteRanges)

	cache, err := block.NewCache(int64(size), blockSize, memfilePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	err = copyProcessMemory(ctx, pid, remoteRanges, cache)
	if err != nil {
		return nil, fmt.Errorf("failed to copy process memory: %w", err)
	}

	return cache, nil
}

func copyProcessMemory(
	ctx context.Context,
	pid int,
	ranges []block.Range,
	local *block.Cache,
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
				Base: local.Address(start),
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
			_, err := unix.ProcessVMReadv(pid,
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
				time.Sleep(oomBackoff + time.Duration(rand.Intn(int(oomJitter.Milliseconds())))*time.Millisecond)

				continue
			}

			if err != nil {
				return fmt.Errorf("failed to read memory: %w", err)
			}

			start += segmentSize
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
