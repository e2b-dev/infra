package feature_flags

import (
	"testing"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
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

func TestSentinelJSONFlag_ReturnsCodeDefault(t *testing.T) {
	t.Parallel()

	ds := ldtestdata.DataSource()
	client, err := NewClientWithDatasource(ds)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close(t.Context()) })

	fallback := ldvalue.ObjectBuild().Set("key", ldvalue.String("value")).Build()
	sentinel := JSONFlagSentinel
	flag := JSONFlag{name: "sentinel-json-test", fallback: fallback, sentinel: &sentinel}

	// LD returns the sentinel (empty object) → getFlag should return the code fallback
	ds.Update(ds.Flag(flag.name).ValueForAll(sentinel))
	result := client.JSONFlag(t.Context(), flag)
	assert.True(t, result.Equal(fallback))
}

func TestSentinelJSONFlag_NonSentinelPassesThrough(t *testing.T) {
	t.Parallel()

	ds := ldtestdata.DataSource()
	client, err := NewClientWithDatasource(ds)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close(t.Context()) })

	fallback := ldvalue.ObjectBuild().Set("key", ldvalue.String("value")).Build()
	sentinel := JSONFlagSentinel
	flag := JSONFlag{name: "sentinel-json-passthrough", fallback: fallback, sentinel: &sentinel}

	// LD returns a non-sentinel value → it passes through
	override := ldvalue.ObjectBuild().Set("other", ldvalue.Int(42)).Build()
	ds.Update(ds.Flag(flag.name).ValueForAll(override))
	result := client.JSONFlag(t.Context(), flag)
	assert.True(t, result.Equal(override))
}

func TestBoolFlag_NoSentinel(t *testing.T) {
	t.Parallel()

	ds := ldtestdata.DataSource()
	client, err := NewClientWithDatasource(ds)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close(t.Context()) })

	flag := BoolFlag{name: "bool-no-sentinel", fallback: true}
	ds.Update(ds.Flag(flag.name).VariationForAll(false))
	result := client.BoolFlag(t.Context(), flag)
	assert.False(t, result) // bool flags have no sentinel, value passes through
}

func TestNonSentinelJSONFlag_PassesThroughEmptyObject(t *testing.T) {
	t.Parallel()

	ds := ldtestdata.DataSource()
	client, err := NewClientWithDatasource(ds)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close(t.Context()) })

	// A JSONFlag created with newJSONFlag (no sentinel) should pass through
	// even if LD returns an empty object.
	fallback := ldvalue.ObjectBuild().Set("key", ldvalue.String("value")).Build()
	flag := JSONFlag{name: "non-sentinel-json-test", fallback: fallback}

	emptyObj := ldvalue.ObjectBuild().Build()
	ds.Update(ds.Flag(flag.name).ValueForAll(emptyObj))
	result := client.JSONFlag(t.Context(), flag)
	assert.True(t, result.Equal(emptyObj)) // no sentinel, empty object passes through
}
