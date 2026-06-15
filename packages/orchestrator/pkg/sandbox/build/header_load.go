//go:build linux

package build

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	loadV4InitialBackoff     = 100 * time.Millisecond
	loadV4MaxBackoff         = 5 * time.Second
	loadV4MaxTransientErrors = 3
)

// uploadHeaderPollWait measures how long the upload-side poll waited for the
// finalized header to become visible in storage. Result attribute is
// "ok" / "deadline_exceeded" / "transient_errors" / "ctx_cancelled" /
// "upload_failed", file_type is memfile|rootfs.
var uploadHeaderPollWait = utils.Must(buildMeter.Int64Histogram(
	"orchestrator.storage.upload.header_poll_wait",
	metric.WithDescription("Duration of the upload-side wait for the finalized V4 header to appear"),
	metric.WithUnit("ms"),
))

// PollRemoteStorageForHeader polls storage for the post-upload V4 header for buildID/fileType.
// ErrObjectNotExist is retried until the budget expires; other LoadHeader
// errors are tolerated up to loadV4MaxTransientErrors consecutive occurrences
// (e.g. transient GCS hiccups during the rare window between the upload-done
// signal and object visibility) before giving up.
//
// hint is an optional accelerator. A nil error received on the channel says
// "the upload just finished, poll storage now"; a non-nil error says "the
// upload failed" and PollRemoteStorageForHeader returns it immediately without further polling.
// A nil channel never fires, so callers without hint plumbing fall through to
// the ticker-only path. budget bounds total wait time.
func PollRemoteStorageForHeader(
	ctx context.Context,
	store storage.StorageProvider,
	buildID uuid.UUID,
	t DiffType,
	hint <-chan error,
	budget time.Duration,
) (*header.Header, error) {
	start := time.Now()
	result := "ok"
	defer func() {
		uploadHeaderPollWait.Record(ctx, time.Since(start).Milliseconds(), metric.WithAttributes(
			attribute.String("file_type", string(t)),
			attribute.String("result", result),
		))
	}()

	hdrPath := storage.Paths{BuildID: buildID.String()}.HeaderFile(string(t))
	deadline := time.Now().Add(budget)

	backoff := loadV4InitialBackoff
	transientErrs := 0
	for {
		h, _, err := header.LoadHeader(ctx, store, hdrPath)
		if err == nil {
			return h, nil
		}
		if !errors.Is(err, storage.ErrObjectNotExist) {
			transientErrs++
			if transientErrs >= loadV4MaxTransientErrors {
				result = "transient_errors"

				return nil, fmt.Errorf("load V4 header for %s/%s after %d attempts: %w", buildID, t, transientErrs, err)
			}
		} else {
			transientErrs = 0
		}
		if !time.Now().Before(deadline) {
			result = "deadline_exceeded"

			return nil, fmt.Errorf("V4 header for %s/%s not visible after %s: %w", buildID, t, budget, err)
		}

		select {
		case <-ctx.Done():
			result = "ctx_cancelled"

			return nil, ctx.Err()
		case hintErr := <-hint:
			if hintErr != nil {
				result = "upload_failed"

				return nil, fmt.Errorf("upload signaled failure for %s/%s: %w", buildID, t, hintErr)
			}
			backoff = loadV4InitialBackoff
		case <-time.After(backoff):
			if backoff < loadV4MaxBackoff {
				backoff *= 2
			}
		}
	}
}
