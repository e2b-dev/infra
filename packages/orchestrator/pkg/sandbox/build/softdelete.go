//go:build linux

package build

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// soft-delete check outcomes, the metric's "result" dimension. They are
// mutually exclusive: a tombstone hit (checked + soft_deleted) is distinct from
// the object being unreadable (not_found / error), so a missing object never
// masquerades as "not soft-deleted".
const (
	checkResultChecked     = "checked"     // metadata read; soft_deleted tells the verdict
	checkResultNotFound    = "not_found"   // object does not exist (e.g. peer-only or already gone)
	checkResultError       = "error"       // metadata could not be read (transient/permission)
	checkResultUnsupported = "unsupported" // backend cannot read custom metadata
)

var softDeleteCheckMetric = utils.Must(meter.Int64Counter(
	"orchestrator.build.soft_delete_check",
	metric.WithDescription("Storage-index soft-delete checks on data-layer open")))

// recordCheck emits one metric point per check. result distinguishes a read
// (checked) from an unreadable object (not_found/error); soft_deleted/failed
// are only meaningful when result==checked. marker carries the tombstone value
// (matching the log), set only on a hit so its action_id doesn't inflate
// cardinality on the common no-tombstone path.
func (b *StorageDiff) recordCheck(ctx context.Context, result string, softDeleted, failed bool, marker string) {
	softDeleteCheckMetric.Add(ctx, 1, metric.WithAttributes(
		attribute.String("artifact", string(b.diffType)),
		attribute.String("result", result),
		attribute.Bool("soft_deleted", softDeleted),
		attribute.Bool("failed", failed),
		attribute.String("marker", marker),
	))
}

func classifyCheckError(err error) string {
	switch {
	case errors.Is(err, storage.ErrObjectNotExist):
		return checkResultNotFound
	case errors.Is(err, storage.ErrMetadataUnsupported):
		return checkResultUnsupported
	default:
		return checkResultError
	}
}

func (b *StorageDiff) softDeleteErr() error {
	// Fail closed only while the tombstoned path is still the active one: a peer
	// transition that repointed dataPath makes the old verdict stop matching.
	if sp := b.softDeletedPath.Load(); sp != nil && *sp == b.source.Load().dataPath {
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
	path := b.source.Load().dataPath
	if path == "" {
		return
	}
	blob, err := b.persistence.OpenBlob(ctx, path)
	if err != nil {
		result := classifyCheckError(err)
		b.recordCheck(ctx, result, false, false, "")
		logger.L().Warn(ctx, "storage-index soft-delete check could not open object",
			logger.WithBuildID(b.buildID), zap.String("artifact", string(b.diffType)),
			zap.String("result", result), zap.String("object", path), zap.Error(err))

		return
	}

	md, err := storage.BlobCustomMetadata(ctx, blob)
	if err != nil {
		result := classifyCheckError(err)
		// A backend that can't read metadata is a deterministic gap, not a
		// transient one: under enforce, fail closed rather than serve a
		// possibly-tombstoned layer (BlobCustomMetadata used to return no error
		// here, which silently failed open).
		if result == checkResultUnsupported && ff.BoolFlag(ctx, featureflags.StorageSoftDeleteEnforceFlag) {
			b.recordCheck(ctx, result, false, true, "")
			b.softDeletedPath.Store(&path)
			logger.L().Error(ctx, "storage-index soft-delete unverifiable; failing closed",
				logger.WithBuildID(b.buildID), zap.String("artifact", string(b.diffType)),
				zap.String("result", result), zap.String("object", path), zap.Error(err))

			return
		}
		b.recordCheck(ctx, result, false, false, "")
		logger.L().Warn(ctx, "storage-index soft-delete check could not read object metadata",
			logger.WithBuildID(b.buildID), zap.String("artifact", string(b.diffType)),
			zap.String("result", result),
			zap.String("object", path), zap.Error(err))

		return
	}

	// Indexing a nil map is safe: "" / false.
	marker, softDeleted := md[storage.ObjectMetadataSoftDeleted]
	enforce := ff.BoolFlag(ctx, featureflags.StorageSoftDeleteEnforceFlag)
	failed := softDeleted && enforce

	b.recordCheck(ctx, checkResultChecked, softDeleted, failed, marker)

	if !softDeleted {
		return
	}

	logger.L().Error(ctx, "storage-index soft-deleted layer in use",
		logger.WithBuildID(b.buildID),
		zap.String("artifact", string(b.diffType)),
		zap.String("marker", marker),
		zap.Bool("enforce", enforce),
	)

	// Record the tombstoned path; softDeleteErr enforces only while it still
	// equals the active source's dataPath, so recording a superseded path here is
	// harmless (it can never match the live value) — no check-then-store race.
	if failed {
		b.softDeletedPath.Store(&path)
	}
}
