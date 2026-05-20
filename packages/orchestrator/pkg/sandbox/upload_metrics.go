//go:build linux

package sandbox

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	uploadArtifactData   = "data"
	uploadArtifactHeader = "header"
)

var (
	uploadUncompressedBytes  = utils.Must(telemetry.GetHistogram(meter, telemetry.UploadUncompressedBytes))
	uploadCompressedBytes    = utils.Must(telemetry.GetHistogram(meter, telemetry.UploadCompressedBytes))
	uploadCompressionRatioBp = utils.Must(telemetry.GetHistogram(meter, telemetry.UploadCompressionRatioBp))
)

func recordUploadCompression(ctx context.Context, artifact, fileType, useCase string, cfg storage.CompressConfig, uncompressed, compressed int64) {
	attrs := metric.WithAttributes(
		attribute.String("artifact", artifact),
		attribute.String("file_type", fileType),
		attribute.String("use_case", useCase),
		attribute.String("compression.type", cfg.CompressionType().String()),
		attribute.Int("compression.level", cfg.Level),
	)

	uploadUncompressedBytes.Record(ctx, uncompressed, attrs)
	uploadCompressedBytes.Record(ctx, compressed, attrs)
	uploadCompressionRatioBp.Record(ctx, ratioBp(compressed, uncompressed), attrs)
}

func storeHeaderWithMetrics(ctx context.Context, store storage.StorageProvider, path, fileType, useCase string, h *headers.Header) error {
	if h.IncompletePendingUpload {
		return fmt.Errorf("refusing to persist incomplete header for %s", path)
	}

	data, err := headers.SerializeHeader(h)
	if err != nil {
		return fmt.Errorf("serialize header: %w", err)
	}

	blob, err := store.OpenBlob(ctx, path, storage.MetadataObjectType)
	if err != nil {
		return fmt.Errorf("open blob %s: %w", path, err)
	}

	if err := blob.Put(ctx, data); err != nil {
		return err
	}

	size := int64(len(data))
	recordUploadCompression(ctx, uploadArtifactHeader, fileType, useCase, storage.CompressConfig{}, size, size)

	return nil
}
