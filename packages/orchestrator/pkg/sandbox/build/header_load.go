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
	loadV4InitialBackoff = 100 * time.Millisecond
	loadV4MaxBackoff     = 5 * time.Second
)

// PollRemoteStorageForHeader polls storage for the post-upload V4 header for buildID/fileType.
// ErrObjectNotExist is the only retryable error; any other LoadHeader error
// returns immediately.
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
	for {
		h, err := header.LoadHeader(ctx, store, hdrPath)
		if err == nil {
			return h, nil
		}
		if !errors.Is(err, storage.ErrObjectNotExist) {
			return nil, fmt.Errorf("load V4 header for %s/%s: %w", buildID, t, err)
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
