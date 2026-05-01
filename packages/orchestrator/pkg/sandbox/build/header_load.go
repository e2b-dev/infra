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

// LoadV4 polls storage for the post-upload V4 header for buildID/fileType.
// ErrObjectNotExist is the only retryable error; any other LoadHeader error
// returns immediately.
//
// hint is an optional accelerator: when it receives, the next poll runs
// immediately (e.g., a Redis pubsub signal that the upload finished). A nil
// channel never fires, so callers without hint plumbing fall through to the
// ticker-only path. budget bounds total wait time.
//
// Pattern mirrors api/internal/sandbox/storage/redis/state_change.go's
// waitForTransition: pubsub-as-hint with a fallback ticker for missed signals.
func LoadV4(
	ctx context.Context,
	store storage.StorageProvider,
	buildID uuid.UUID,
	t DiffType,
	hint <-chan struct{},
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
		case <-hint:
			backoff = loadV4InitialBackoff
		case <-time.After(backoff):
			if backoff < loadV4MaxBackoff {
				backoff *= 2
			}
		}
	}
}
