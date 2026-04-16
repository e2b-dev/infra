package block

import (
	"context"
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// CompositeDevice presents two block devices as a single contiguous device.
// Reads/writes at offsets [0, sizeA) go to device A (rootfs).
// Reads/writes at offsets [sizeA, sizeA+sizeB) go to device B (overlay upper),
// with offsets shifted so device B sees them starting at 0.
//
// The guest sees one /dev/vda. With a GPT partition table baked into the image,
// the kernel exposes /dev/vda1 (device A region) and /dev/vda2 (device B region).
type CompositeDevice struct {
	a     Device // rootfs region [0, sizeA)
	b     Device // overlay region [sizeA, sizeA+sizeB)
	sizeA int64
	sizeB int64
}

var _ Device = (*CompositeDevice)(nil)

func NewCompositeDevice(a, b Device, sizeA, sizeB int64) *CompositeDevice {
	return &CompositeDevice{
		a:     a,
		b:     b,
		sizeA: sizeA,
		sizeB: sizeB,
	}
}

func (c *CompositeDevice) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	end := off + int64(len(p))

	// Entirely in region A
	if end <= c.sizeA {
		return c.a.ReadAt(ctx, p, off)
	}

	// Entirely in region B
	if off >= c.sizeA {
		return c.b.ReadAt(ctx, p, off-c.sizeA)
	}

	// Spans both regions — split the read
	splitAt := c.sizeA - off
	n1, err := c.a.ReadAt(ctx, p[:splitAt], off)
	if err != nil {
		return n1, fmt.Errorf("composite read A: %w", err)
	}

	n2, err := c.b.ReadAt(ctx, p[splitAt:], 0)
	if err != nil {
		return n1 + n2, fmt.Errorf("composite read B: %w", err)
	}

	return n1 + n2, nil
}

func (c *CompositeDevice) WriteAt(p []byte, off int64) (int, error) {
	end := off + int64(len(p))

	if end <= c.sizeA {
		return c.a.WriteAt(p, off)
	}

	if off >= c.sizeA {
		return c.b.WriteAt(p, off-c.sizeA)
	}

	// Spans both
	splitAt := c.sizeA - off
	n1, err := c.a.WriteAt(p[:splitAt], off)
	if err != nil {
		return n1, fmt.Errorf("composite write A: %w", err)
	}

	n2, err := c.b.WriteAt(p[splitAt:], 0)
	if err != nil {
		return n1 + n2, fmt.Errorf("composite write B: %w", err)
	}

	return n1 + n2, nil
}

func (c *CompositeDevice) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	end := off + length

	if end <= c.sizeA {
		return c.a.Slice(ctx, off, length)
	}

	if off >= c.sizeA {
		return c.b.Slice(ctx, off-c.sizeA, length)
	}

	return nil, fmt.Errorf("slice spanning composite boundary not supported")
}

func (c *CompositeDevice) Size(_ context.Context) (int64, error) {
	return c.sizeA + c.sizeB, nil
}

func (c *CompositeDevice) BlockSize() int64 {
	return c.a.BlockSize()
}

func (c *CompositeDevice) Header() *header.Header {
	return c.a.Header()
}

func (c *CompositeDevice) Close() error {
	return firstErr(c.a.Close(), c.b.Close())
}

// DeviceB returns the overlay device so the caller can export its diff
// independently (e.g., for FS-only pause).
func (c *CompositeDevice) DeviceB() Device {
	return c.b
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}

	return nil
}

// Ensure CompositeDevice satisfies io.WriterAt (required by Device via nbd.Provider).
var _ io.WriterAt = (*CompositeDevice)(nil)
