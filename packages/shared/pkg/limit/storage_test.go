package limit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestAcquireUploadSlotNilLimiter(t *testing.T) {
	t.Parallel()

	var l *Limiter
	release, err := l.AcquireUploadSlot(t.Context())
	require.NoError(t, err)
	release()
}

func TestAcquireUploadSlotBlocksWhenFull(t *testing.T) {
	t.Parallel()

	sem, err := utils.NewAdjustableSemaphore(1)
	require.NoError(t, err)
	l := &Limiter{storageUploadLimiter: sem}

	release, err := l.AcquireUploadSlot(t.Context())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = l.AcquireUploadSlot(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	release()
	release, err = l.AcquireUploadSlot(t.Context())
	require.NoError(t, err)
	release()
}
