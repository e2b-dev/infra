//go:build linux

package sandbox

import (
	"context"

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
	uploadCompressionRatioBp.Record(ctx, uploadRatioBp(compressed, uncompressed), attrs)
}

func uploadRatioBp(compressed, uncompressed int64) int64 {
	if uncompressed <= 0 || compressed < 0 {
		return 0
	}

	return compressed * 10000 / uncompressed
}

func storeHeaderWithMetrics(ctx context.Context, store storage.StorageProvider, path, fileType, useCase string, h *headers.Header) error {
	if err := headers.StoreHeader(ctx, store, path, h); err != nil {
		return err
	}

	data, err := headers.SerializeHeader(h)
	if err != nil {
		return err
	}

	size := int64(len(data))
	recordUploadCompression(ctx, uploadArtifactHeader, fileType, useCase, storage.CompressConfig{}, size, size)

	return nil
}
