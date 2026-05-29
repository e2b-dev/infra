//go:build linux

package block

import (
	"fmt"
	"math"
	"os"

	"github.com/tklauser/go-sysconf"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	// IOV_MAX is the limit of the vectors that can be passed in a single ioctl call.
	IOV_MAX = utils.Must(getIOVMax())

	PAGE_SIZE = os.Getpagesize()
	PAGE_MASK = ^(int64(PAGE_SIZE) - 1)
	INT_MAX   = int64(math.MaxInt32)

	// This is maximum bytes that can be read/written in a single operation.
	//
	// https://unix.stackexchange.com/questions/794316/why-linux-read-avoids-using-full-2-gib-in-one-call
	// https://stackoverflow.com/questions/70368651/why-cant-linux-write-more-than-2147479552-bytes
	MAX_RW_COUNT = INT_MAX & PAGE_MASK
)

func getIOVMax() (int, error) {
	iovMax, err := sysconf.Sysconf(sysconf.SC_IOV_MAX)
	if err != nil {
		return 0, fmt.Errorf("failed to get IOV_MAX: %w", err)
	}

	return int(iovMax), nil
}

func getAlignedMaxRwCount(blockSize int64) int64 {
	return (MAX_RW_COUNT / blockSize) * blockSize
}

// drainIovs walks items, calling op on each IOV_MAX- and MAX_RW_COUNT-
// bounded batch and advancing the destination offset by the batch bytes.
// Items must be pre-split so no single item exceeds the byte cap.
func drainIovs[T any](
	items []T,
	sizeOf func(T) int64,
	blockSize int64,
	op func(destOff int64, batch []T, batchBytes int64) error,
) error {
	maxBytes := getAlignedMaxRwCount(blockSize)

	var destOff int64
	i := 0
	for i < len(items) {
		batchBytes := int64(0)
		start := i
		for ; i < len(items); i++ {
			sz := sizeOf(items[i])
			if i-start >= IOV_MAX || batchBytes+sz > maxBytes {
				break
			}
			batchBytes += sz
		}
		if i == start {
			return fmt.Errorf("iov item at index %d (size %d) exceeds max %d", i, sizeOf(items[i]), maxBytes)
		}
		if err := op(destOff, items[start:i], batchBytes); err != nil {
			return err
		}
		destOff += batchBytes
	}

	return nil
}
