package build

import (
	"context"
	"fmt"
	"slices"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type MaskedOverlay struct {
	lower block.ReadonlyDevice
	upper block.ReadonlyDevice

	mask *block.Tracker
}

var _ block.ReadonlyDevice = (*MaskedOverlay)(nil)

func NewMaskedOverlay(lower, upper block.ReadonlyDevice) (*MaskedOverlay, error) {
	if lower.BlockSize() != upper.BlockSize() {
		return nil, fmt.Errorf("lower and upper block sizes do not match")
	}

	lowerSize, err := lower.Size()
	if err != nil {
		return nil, err
	}

	upperSize, err := upper.Size()
	if err != nil {
		return nil, err
	}

	if lowerSize != upperSize {
		return nil, fmt.Errorf("lower and upper sizes do not match")
	}

	upperMapping := slices.Collect(upper.Header().FilterMapping(upper.Header().Metadata.BuildId))
	mask := block.NewTrackerFromMapping(upperMapping, upper.BlockSize())

	return &MaskedOverlay{
		lower: lower,
		upper: upper,
		mask:  mask,
	}, nil
}

func (m *MaskedOverlay) BlockSize() int64 {
	return m.lower.BlockSize()
}

func (m *MaskedOverlay) Close() error {
	return nil
}

func (m *MaskedOverlay) Header() *header.Header {
	return m.lower.Header()
}

// TODO: We should be able to read by bigger chunks than just the block size
func (m *MaskedOverlay) ReadAt(ctx context.Context, p []byte, off int64) (n int, err error) {
	for n < len(p) {
		currentOff := off + int64(n)
		if m.mask.Has(currentOff) {
			readN, err := m.upper.ReadAt(ctx, p[n:], currentOff)

			n += readN

			if err != nil {
				return n, err
			}
		} else {
			readN, err := m.lower.ReadAt(ctx, p[n:], currentOff)

			n += readN

			if err != nil {
				return n, err
			}
		}
	}

	return n, nil
}

func (m *MaskedOverlay) Size() (int64, error) {
	return m.lower.Size()
}

// TODO: We should handle the slice even for more than one block size
func (m *MaskedOverlay) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	if m.mask.Has(off) {
		return m.upper.Slice(ctx, off, length)
	}

	return m.lower.Slice(ctx, off, length)
}
