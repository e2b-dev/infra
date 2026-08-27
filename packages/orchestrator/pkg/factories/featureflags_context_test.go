//go:build linux

package factories

import (
	"testing"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

var instanceGroupTestFlag = featureflags.NewBoolFlag("orchestrator-instance-group-context-test", false)

func TestInstanceGroupContextProviderBuildsTheContext(t *testing.T) {
	t.Parallel()

	provider := instanceGroupContextProvider("orch-client-pool-region-rig")
	require.NotNil(t, provider)

	instanceGroupContext := provider(t.Context())
	require.NoError(t, instanceGroupContext.Err())

	assert.Equal(t, ldcontext.Kind("instance-group"), instanceGroupContext.Kind())
	assert.Equal(t, "orch-client-pool-region-rig", instanceGroupContext.Key())
}

// A node without a name still registers a provider, so what matters is that the
// context it yields cannot win a rule. Driven through the client rather than
// asserting on the context, because being dropped from the multi-context is the
// behaviour that keeps an unset name inert.
func TestInstanceGroupContextWithoutANameMatchesNothing(t *testing.T) {
	t.Parallel()

	for name, instanceGroupName := range map[string]string{
		"unset":      "",
		"whitespace": "   ",
		"tab":        "\t",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := instanceGroupContextProvider(instanceGroupName)

			// An empty key is what mergeContexts drops on. A blank-but-present
			// key would instead reach LaunchDarkly as a real context that
			// happens to match nothing, which BoolFlag alone cannot tell apart.
			assert.Empty(t, provider(t.Context()).Key())

			client := instanceGroupTestClient(t)
			client.RegisterContextProvider(provider)

			assert.False(t, client.BoolFlag(t.Context(), instanceGroupTestFlag))
		})
	}
}

// The kind and key only matter insofar as a rule targeting them wins the
// evaluation, so this exercises the whole path through the client.
func TestInstanceGroupContextMatchesFlagRules(t *testing.T) {
	t.Parallel()

	client := instanceGroupTestClient(t)

	assert.False(t, client.BoolFlag(t.Context(), instanceGroupTestFlag))

	client.RegisterContextProvider(instanceGroupContextProvider("orch-client-pool-region-rig"))

	assert.True(t, client.BoolFlag(t.Context(), instanceGroupTestFlag))
}

// instanceGroupTestClient serves instanceGroupTestFlag true only to the one
// instance group named below.
func instanceGroupTestClient(t *testing.T) *featureflags.Client {
	t.Helper()

	source := ldtestdata.DataSource()
	source.Update(
		source.Flag(instanceGroupTestFlag.Key()).
			VariationForKey("instance-group", "orch-client-pool-region-rig", true).
			FallthroughVariation(false),
	)

	client, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, client.Close(t.Context()))
	})

	return client
}
