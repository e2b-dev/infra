package events

import (
	"context"
	"testing"

	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"

	sharedevents "github.com/e2b-dev/infra/packages/shared/pkg/events"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

func newTestFeatureFlags(t *testing.T) (*featureflags.Client, *ldtestdata.TestDataSource) {
	t.Helper()

	source := ldtestdata.DataSource()
	ff, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(context.Background()) })

	return ff, source
}

func setWriteFanoutFlag(t *testing.T, source *ldtestdata.TestDataSource, value bool) {
	t.Helper()

	source.Update(source.Flag(featureflags.ClickhouseWriteFanoutFlag.Key()).VariationForAll(value))
}

func TestGatedClickhouseDelivery_PublishFlagOffDrops(t *testing.T) {
	t.Parallel()

	ff, source := newTestFeatureFlags(t)
	setWriteFanoutFlag(t, source, false)
	d := &GatedClickhouseDelivery{ClickhouseDelivery: &ClickhouseDelivery{}, ff: ff}

	err := d.Publish(context.Background(), "key", sharedevents.SandboxEvent{SandboxID: "sbx-1"})
	require.NoError(t, err)
}

func TestClickhouseDelivery_PublishSkipsFlagCheck(t *testing.T) {
	t.Parallel()

	_, source := newTestFeatureFlags(t)
	// Even with the flag off, ungated deliveries write unconditionally.
	setWriteFanoutFlag(t, source, false)
	d := &ClickhouseDelivery{}

	require.Panics(t, func() {
		_ = d.Publish(context.Background(), "key", sharedevents.SandboxEvent{})
	}, "ungated delivery should bypass the flag and reach batcher.Push (panics on nil batcher)")
}

func TestGatedClickhouseDelivery_PublishNilFeatureFlagsDrops(t *testing.T) {
	t.Parallel()

	d := &GatedClickhouseDelivery{ClickhouseDelivery: &ClickhouseDelivery{}, ff: nil}

	err := d.Publish(context.Background(), "key", sharedevents.SandboxEvent{})
	require.NoError(t, err)
}
