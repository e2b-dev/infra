package peerserver

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
)

var _ SeekableSource = &seekableSource{}

// seekableSource serves seekable diff files (memfile, rootfs.ext4).
// Supports Size and random-access streaming via offset/length.
type seekableSource struct {
	diff build.Diff
}

func (f *seekableSource) Size(_ context.Context) (int64, error) {
	return f.diff.FileSize()
}

func (f *seekableSource) Exists(_ context.Context) (bool, error) {
	return false, ErrNotSupported
}

func (f *seekableSource) Stream(ctx context.Context, offset, length int64, sender Sender) error {
	ctx, span := tracer.Start(ctx, "stream-seekable-file", trace.WithAttributes(
		attribute.Int64("offset", offset),
		attribute.Int64("length", length),
	))
	defer span.End()

	// P2P always serves uncompressed bytes — pass nil FrameTable.
	// Loop over blocks to serve the full requested length.
	for sent := int64(0); sent < length; {
		data, err := f.diff.Block(ctx, offset+sent, nil)
		if err != nil {
			span.RecordError(err)

			return fmt.Errorf("block diff at offset %d: %w", offset+sent, err)
		}

		// Clamp to remaining requested length.
		if remaining := length - sent; int64(len(data)) > remaining {
			data = data[:remaining]
		}

		sent += int64(len(data))

		if err := sender.Send(data); err != nil {
			span.RecordError(err)

			return fmt.Errorf("send diff chunk: %w", err)
		}
	}

	return nil
}
