//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	uploadFileMemfile       = "memfile"
	uploadFileRootfs        = "rootfs"
	uploadFileMemfileHeader = "memfile-header"
	uploadFileRootfsHeader  = "rootfs-header"
	uploadFileSnap          = "snap"
	uploadFileMeta          = "meta"
)

var (
	uploadUncompressedBytes = utils.Must(telemetry.GetHistogram(meter, telemetry.UploadUncompressedBytes))
	uploadCompressedBytes   = utils.Must(telemetry.GetHistogram(meter, telemetry.UploadCompressedBytes))
	uploadCompressionRatio  = utils.Must(telemetry.GetFloatHistogram(meter, telemetry.UploadCompressionRatio))
)

func recordUploadCompression(ctx context.Context, fileType string, cfg storage.CompressConfig, uncompressed, compressed int64) {
	attrs := metric.WithAttributes(
		attribute.String("file_type", fileType),
		attribute.String("compression.type", cfg.CompressionType().String()),
		attribute.Int("compression.level", cfg.Level),
	)

	uploadUncompressedBytes.Record(ctx, uncompressed, attrs)
	uploadCompressedBytes.Record(ctx, compressed, attrs)
	uploadCompressionRatio.Record(ctx, uploadRatio(compressed, uncompressed), attrs)
}

// uploadRatio returns compressed/uncompressed as a fraction (1.0 = no
// compression). May exceed 1 when an artifact expands; emitted with unit {1}.
func uploadRatio(compressed, uncompressed int64) float64 {
	if uncompressed <= 0 || compressed < 0 {
		return 0
	}

	return float64(compressed) / float64(uncompressed)
}

func storeHeaderWithMetrics(ctx context.Context, store storage.StorageProvider, path, fileType string, h *headers.Header, opts ...storage.PutOption) error {
	cfg, stored, uncompressed, err := headers.StoreHeader(ctx, store, path, h, opts...)
	if err != nil {
		return err
	}

	recordUploadCompression(ctx, fileType, cfg, uncompressed, stored)

	return nil
}

func uploadBlobWithMetrics(ctx context.Context, store storage.StorageProvider, path string, sourcePath, fileType string, opts ...storage.PutOption) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("%s stat: %w", fileType, err)
	}
	if err := storage.UploadBlob(ctx, store, path, sourcePath, opts...); err != nil {
		return err
	}
	recordUploadCompression(ctx, fileType, storage.CompressConfig{}, info.Size(), info.Size())

	return nil
}
