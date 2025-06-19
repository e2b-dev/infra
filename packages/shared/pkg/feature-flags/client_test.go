package feature_flags

import (
	"context"
	"testing"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/stretchr/testify/assert"
)

const (
	flagName = "demo-feature-flag"
)

func TestOfflineDatastore(t *testing.T) {
	clientCtx := ldcontext.NewBuilder(flagName).Build()
	client, err := NewClient()
	defer func() {
		_ = client.Close(context.Background())
	}()

	assert.NoError(t, err)

	// value is not set so it should be default (false)
	flagValue, _ := client.Ld.BoolVariation(flagName, clientCtx, false)
	assert.False(t, flagValue)

	LaunchDarklyOfflineStore.Update(
		LaunchDarklyOfflineStore.Flag(flagName).VariationForAll(true),
	)

	// value is set manually in datastore and should be taken from there
	flagValue, _ = client.Ld.BoolVariation(flagName, clientCtx, false)
	assert.True(t, flagValue)
}
