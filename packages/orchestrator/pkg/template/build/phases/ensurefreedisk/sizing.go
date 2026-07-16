package ensurefreedisk

import (
	"errors"
	"fmt"
	"math"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/units"
)

// computeGrownSize assumes the caller has already validated the source
// geometry (see validateSourceGeometry), including that the block size
// matches header.RootfsBlockSize.
func computeGrownSize(currentSize, targetFree, freeBefore int64) (int64, error) {
	if currentSize <= 0 {
		return 0, fmt.Errorf("invalid current size: %d", currentSize)
	}
	if targetFree < 0 {
		return 0, fmt.Errorf("invalid target free space: %d", targetFree)
	}
	if freeBefore >= targetFree {
		return 0, fmt.Errorf("free space before (%d) is already at or above target (%d)", freeBefore, targetFree)
	}
	if freeBefore < 0 && targetFree > math.MaxInt64+freeBefore {
		return 0, errors.New("free-space shortage overflows")
	}

	shortage := targetFree - freeBefore
	if currentSize > math.MaxInt64-shortage {
		return 0, errors.New("grown size overflows")
	}

	size := currentSize + shortage
	resizeUnit := units.MBToBytes(1)
	if remainder := size % resizeUnit; remainder != 0 {
		add := resizeUnit - remainder
		if size > math.MaxInt64-add {
			return 0, errors.New("aligned grown size overflows")
		}
		size += add
	}

	return size, nil
}

func validateSourceGeometry(sourceSize, blockSize int64, headerSize, headerBlockSize uint64) error {
	if sourceSize <= 0 {
		return fmt.Errorf("invalid source size: %d", sourceSize)
	}
	if blockSize != int64(header.RootfsBlockSize) {
		return fmt.Errorf("unsupported source block size: %d", blockSize)
	}
	if headerSize > math.MaxInt64 || headerBlockSize > math.MaxInt64 {
		return errors.New("source header geometry overflows")
	}
	if sourceSize != int64(headerSize) || blockSize != int64(headerBlockSize) {
		return fmt.Errorf("source geometry mismatch: device=%d/%d header=%d/%d", sourceSize, blockSize, headerSize, headerBlockSize)
	}
	if sourceSize%blockSize != 0 {
		return fmt.Errorf("source size %d is not block aligned", sourceSize)
	}

	return nil
}
