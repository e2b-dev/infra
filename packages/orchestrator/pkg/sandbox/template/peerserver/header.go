//go:build linux

package peerserver

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var _ BlobSource = &headerSource{}

// headerSource serves serialized block-device header files (memfile.header, rootfs.ext4.header).
type headerSource struct {
	getDevice func(ctx context.Context) (block.ReadonlyDevice, error)
}

func (f *headerSource) Exists(_ context.Context) (bool, error) {
	return false, ErrNotSupported
}

func (f *headerSource) Stream(ctx context.Context, sender Sender) error {
	ctx, span := tracer.Start(ctx, "stream-header-file")
	defer span.End()

	device, err := f.getDevice(ctx)
	if err != nil {
		span.RecordError(err)

		return fmt.Errorf("get device: %w", err)
	}

	h := device.Header()
	if h == nil {
		return ErrNotAvailable
	}

	// Rely on the V5 format on the wire.
	wire := *h
	meta := *h.Metadata
	meta.Version = header.MetadataVersionV5
	wire.Metadata = &meta
	wire.IncompletePendingUpload = true

	data, err := header.SerializeHeader(&wire)
	if err != nil {
		span.RecordError(err)

		return fmt.Errorf("serialize header: %w", err)
	}

	return sender.Send(data)
}
