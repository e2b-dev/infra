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

func recordUploadCompression(ctx context.Context, artifact, fileType string, cfg storage.CompressConfig, uncompressed, compressed int64) {
	attrs := metric.WithAttributes(
		attribute.String("artifact", artifact),
		attribute.String("file_type", uploadMetricFileType(fileType)),
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

func storeHeaderWithMetrics(ctx context.Context, store storage.StorageProvider, path, fileType string, h *headers.Header) error {
	cfg, stored, uncompressed, err := headers.StoreHeader(ctx, store, path, h)
	if err != nil {
		return err
	}

	recordUploadCompression(ctx, uploadArtifactHeader, fileType, cfg, uncompressed, stored)

	return nil
}
