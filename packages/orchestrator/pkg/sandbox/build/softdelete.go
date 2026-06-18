//go:build linux

package build

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var softDeleteCheckMetric = utils.Must(meter.Int64Counter(
	"orchestrator.build.soft_delete_check",
	metric.WithDescription("Storage-index soft-delete checks on data-layer open")))

func (b *StorageDiff) softDeleteErr() error {
	if b.softDeleted.Load() {
		return fmt.Errorf("%s %s: %w", b.buildID, b.diffType, storage.ErrObjectSoftDeleted)
	}

	return nil
}

// startSoftDeleteCheck runs the check in the background so it never adds latency
// to the read path. No-op (no GCS access) when the check flag is off.
func (b *StorageDiff) startSoftDeleteCheck(ctx context.Context) {
	ff := b.flags
	if ff == nil || !ff.BoolFlag(ctx, featureflags.StorageSoftDeleteCheckFlag) {
		return
	}

	go b.softDeleteCheck(ctx, ff)
}

func (b *StorageDiff) softDeleteCheck(ctx context.Context, ff *featureflags.Client) {
	// Read the tombstone off the data object this diff actually serves — the
	// object the storage index prunes — not the header, which a deduped
	// ancestor layer is never read from.
	path := b.dataPath.Load()
	if path == nil {
		return
	}
	blob, err := b.persistence.OpenBlob(ctx, *path, storage.MetadataObjectType)
	if err != nil {
		return
	}

	md, err := storage.BlobCustomMetadata(ctx, blob)
	if err != nil {
		return
	}

	// Indexing a nil map is safe: "" / false.
	marker, softDeleted := md[storage.ObjectMetadataSoftDeleted]
	enforce := ff.BoolFlag(ctx, featureflags.StorageSoftDeleteEnforceFlag)
	failed := softDeleted && enforce

	softDeleteCheckMetric.Add(ctx, 1, metric.WithAttributes(
		attribute.String("artifact", string(b.diffType)),
		attribute.Bool("soft_deleted", softDeleted),
		attribute.Bool("failed", failed),
	))

	if !softDeleted {
		return
	}

	logger.L().Error(ctx, "storage-index soft-deleted layer in use",
		logger.WithBuildID(b.buildID),
		zap.String("artifact", string(b.diffType)),
		zap.String("marker", marker),
		zap.Bool("enforce", enforce),
	)

	if failed {
		b.softDeleted.Store(true)
	}
}
