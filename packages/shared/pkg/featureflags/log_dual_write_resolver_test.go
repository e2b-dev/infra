package featureflags

import (
	"testing"
	"time"

	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogsDualWriteResolverNilClient(t *testing.T) {
	t.Parallel()

	resolver := NewLogsDualWriteResolver(nil)

	assert.False(t, resolver.Resolve(t.Context()))
}

func TestLogsDualWriteResolverCachesUntilTTL(t *testing.T) {
	t.Parallel()

	source := ldtestdata.DataSource()
	client, err := NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, client.Close(t.Context()))
	})

	source.Update(source.Flag(LogsDualWriteFlag.Key()).VariationForAll(false))
	resolver := newLogsDualWriteResolverWithTTL(client, time.Hour)

	assert.False(t, resolver.Resolve(t.Context()))

	source.Update(source.Flag(LogsDualWriteFlag.Key()).VariationForAll(true))
	assert.False(t, resolver.Resolve(t.Context()), "should serve cached value before TTL expiry")
}

func TestLogsDualWriteResolverRefreshesAfterTTL(t *testing.T) {
	t.Parallel()

	source := ldtestdata.DataSource()
	client, err := NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, client.Close(t.Context()))
	})

	source.Update(source.Flag(LogsDualWriteFlag.Key()).VariationForAll(false))
	resolver := newLogsDualWriteResolverWithTTL(client, 0)

	assert.False(t, resolver.Resolve(t.Context()))

	source.Update(source.Flag(LogsDualWriteFlag.Key()).VariationForAll(true))
	assert.True(t, resolver.Resolve(t.Context()), "should refresh after TTL expiry")
}

func TestLogsDualWriteResolverDefaultTTL(t *testing.T) {
	t.Parallel()

	resolver := NewLogsDualWriteResolver(nil)

	assert.Equal(t, logsDualWriteCacheTTL, resolver.ttl)
}
