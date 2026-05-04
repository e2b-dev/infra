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

	// V4 headers served via P2P are always for in-flight builds — peers stop
	// being routed once the upload finalizes (peerStorageProvider switches to
	// base/GCS via the uploaded flag). Force the wire bit on regardless of
	// the in-memory state so consumers reliably treat these bytes as a
	// pending diff and refresh from GCS once the upload lands. V3 has no
	// in-flight notion on the wire, so it ships as-is and is treated as final.
	wire := *h
	if wire.Metadata.Version >= header.MetadataVersionV4 {
		wire.IncompletePendingUpload = true
	}

	data, err := header.SerializeHeader(&wire)
	if err != nil {
		span.RecordError(err)

		return fmt.Errorf("serialize header: %w", err)
	}

	return sender.Send(data)
}
