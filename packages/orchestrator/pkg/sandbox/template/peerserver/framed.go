package peerserver

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
)

var _ FramedSource = &framedSource{}

// framedSource serves framed diff files (memfile, rootfs.ext4).
// Supports Size and random-access streaming via offset/length.
type framedSource struct {
	diff build.Diff
}

func (f *framedSource) Size(_ context.Context) (int64, error) {
	return f.diff.FileSize()
}

func (f *framedSource) Exists(_ context.Context) (bool, error) {
	return false, ErrNotSupported
}

func (f *framedSource) Stream(ctx context.Context, offset, length int64, sender Sender) error {
	ctx, span := tracer.Start(ctx, "stream-framed-file", trace.WithAttributes(
		attribute.Int64("offset", offset),
		attribute.Int64("length", length),
	))
	defer span.End()

	// P2P always serves uncompressed bytes — pass nil FrameTable.
	data, err := f.diff.SliceBlock(ctx, offset, length, nil)
	if err != nil {
		span.RecordError(err)

		return fmt.Errorf("get block at offset %d: %w", offset, err)
	}

	blockSize := int(f.diff.BlockSize())

	for len(data) > 0 {
		take := min(len(data), blockSize)
		if err := sender.Send(data[:take]); err != nil {
			span.RecordError(err)

			return fmt.Errorf("send diff chunk: %w", err)
		}

		data = data[take:]
	}

	return nil
}
