package populate_redis

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/memory"
	redisstorage "github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/redis"
)

func newTestStorage(t *testing.T, client redis.UniversalClient) *PopulateRedisStorage {
	t.Helper()

	return NewStorage(memory.NewStorage(), redisstorage.NewStorage(client))
}

func newTestSandbox(sandboxID string) sandbox.Sandbox {
	return sandbox.Sandbox{
		SandboxID:         sandboxID,
		TemplateID:        "template",
		ClientID:          "client",
		TeamID:            uuid.New(),
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
		State:             sandbox.StateRunning,
	}
}

func TestPopulateRedisStorage_GetFallsBackToRedis(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	storageA := newTestStorage(t, redis.NewClient(&redis.Options{Addr: m.Addr()}))
	storageB := newTestStorage(t, redis.NewClient(&redis.Options{Addr: m.Addr()}))

	sbx := newTestSandbox("sandbox-a")

	require.NoError(t, storageA.Add(ctx, sbx))

	got, err := storageB.Get(ctx, sbx.SandboxID)
	require.NoError(t, err)
	assert.Equal(t, sbx.SandboxID, got.SandboxID)

	cached, err := storageB.memoryBackend.Get(ctx, sbx.SandboxID)
	require.NoError(t, err)
	assert.Equal(t, sbx.SandboxID, cached.SandboxID)
}

func TestPopulateRedisStorage_UpdateSyncsRedisAndMemory(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	storageA := newTestStorage(t, redis.NewClient(&redis.Options{Addr: m.Addr()}))
	storageB := newTestStorage(t, redis.NewClient(&redis.Options{Addr: m.Addr()}))

	sbx := newTestSandbox("sandbox-b")
	require.NoError(t, storageA.Add(ctx, sbx))

	updateCalls := 0
	updated, err := storageB.Update(ctx, sbx.SandboxID, func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		updateCalls++
		sbx.TemplateID = "updated"

		return sbx, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, updateCalls)
	assert.Equal(t, "updated", updated.TemplateID)

	redisSbx, err := storageA.redisBackend.Get(ctx, sbx.SandboxID)
	require.NoError(t, err)
	assert.Equal(t, "updated", redisSbx.TemplateID)

	memorySbx, err := storageB.memoryBackend.Get(ctx, sbx.SandboxID)
	require.NoError(t, err)
	assert.Equal(t, "updated", memorySbx.TemplateID)
}

func TestPopulateRedisStorage_StartRemovingUpdatesRedis(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	storageA := newTestStorage(t, redis.NewClient(&redis.Options{Addr: m.Addr()}))
	storageB := newTestStorage(t, redis.NewClient(&redis.Options{Addr: m.Addr()}))

	sbx := newTestSandbox("sandbox-c")
	originalEndTime := sbx.EndTime
	require.NoError(t, storageA.Add(ctx, sbx))

	alreadyDone, callback, err := storageA.StartRemoving(ctx, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	got, err := storageB.Get(ctx, sbx.SandboxID)
	require.NoError(t, err)
	assert.Equal(t, sandbox.StatePausing, got.State)
	assert.True(t, got.EndTime.Before(originalEndTime))

	callback(ctx, nil)
}
