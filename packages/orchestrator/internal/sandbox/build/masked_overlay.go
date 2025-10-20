package build

import (
	"context"
	"fmt"

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

	mask := block.NewTrackerFromBitSet(upper.Header(), upper.BlockSize())

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

func (m *MaskedOverlay) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	return m.lower.ReadAt(ctx, p, off)
}

func (m *MaskedOverlay) Size() (int64, error) {
	return m.lower.Size()
}

func (m *MaskedOverlay) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	if m.mask.Has(off) {
		return m.upper.Slice(ctx, off, length)
	}

	return m.lower.Slice(ctx, off, length)
}
