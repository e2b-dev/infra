package sandboxcountscache

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

type countingSource struct {
	calls  *atomic.Int32
	counts map[uuid.UUID]int64
}

func (c countingSource) TeamRunningSandboxCounts(context.Context) (map[uuid.UUID]int64, error) {
	c.calls.Add(1)
	time.Sleep(100 * time.Millisecond)

	return c.counts, nil
}

func TestCountsCacheIsSharedAcrossAPIInstances(t *testing.T) {
	t.Parallel()

	redisClient := redis_utils.SetupInstance(t)
	teamID := uuid.New()
	counts := map[uuid.UUID]int64{teamID: 100_000}
	calls := &atomic.Int32{}

	first := NewCountsCache(countingSource{calls: calls, counts: counts}, redisClient)
	second := NewCountsCache(countingSource{calls: calls, counts: counts}, redisClient)

	start := make(chan struct{})
	results := make([]map[uuid.UUID]int64, 2)
	group := errgroup.Group{}
	group.Go(func() error {
		<-start
		result, err := first.TeamRunningSandboxCounts(t.Context())
		results[0] = result

		return err
	})
	group.Go(func() error {
		<-start
		result, err := second.TeamRunningSandboxCounts(t.Context())
		results[1] = result

		return err
	})
	close(start)

	require.NoError(t, group.Wait())
	assert.Equal(t, counts, results[0])
	assert.Equal(t, counts, results[1])
	assert.Equal(t, int32(1), calls.Load())
}
