package build

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	loadV4InitialBackoff     = 100 * time.Millisecond
	loadV4MaxBackoff         = 5 * time.Second
	loadV4MaxTransientErrors = 3
)

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
	hdrPath := storage.Paths{BuildID: buildID.String()}.HeaderFile(string(t))
	deadline := time.Now().Add(budget)

	backoff := loadV4InitialBackoff
	transientErrs := 0
	for {
		var (
			h   *header.Header
			err error
		)
		if hl, ok := store.(header.P2PHeaderLoader); ok {
			h, err = hl.LoadHeader(ctx, hdrPath)
		} else {
			h, err = header.LoadHeader(ctx, store, hdrPath)
		}
		if err == nil {
			return h, nil
		}
		if !errors.Is(err, storage.ErrObjectNotExist) {
			transientErrs++
			if transientErrs >= loadV4MaxTransientErrors {
				return nil, fmt.Errorf("load V4 header for %s/%s after %d attempts: %w", buildID, t, transientErrs, err)
			}
		} else {
			transientErrs = 0
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("V4 header for %s/%s not visible after %s: %w", buildID, t, budget, err)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case hintErr := <-hint:
			if hintErr != nil {
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
