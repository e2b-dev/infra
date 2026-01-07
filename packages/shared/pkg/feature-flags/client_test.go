package feature_flags

import (
	"testing"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	flagName = "demo-feature-flag"
)

func TestOfflineDatastore(t *testing.T) {
	t.Parallel()
	clientCtx := ldcontext.NewBuilder(flagName).Build()
	client, err := NewClient()
	require.NoError(t, err)

	t.Cleanup(func() {
		err = client.Close(t.Context())
		assert.NoError(t, err)
	})

	// value is not set so it should be default (false)
	flagValue, _ := client.ld.BoolVariation(flagName, clientCtx, false)
	assert.False(t, flagValue)

	launchDarklyOfflineStore.Update(
		launchDarklyOfflineStore.Flag(flagName).VariationForAll(true),
	)

	// value is set manually in datastore and should be taken from there
	flagValue, _ = client.ld.BoolVariation(flagName, clientCtx, false)
	assert.True(t, flagValue)
}
