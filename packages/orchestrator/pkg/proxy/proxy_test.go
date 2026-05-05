package proxy

import (
	"context"
	"testing"

	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
)

func newFFWithOrchAcceptsCombinedHost(t *testing.T, enabled bool) *featureflags.Client {
	t.Helper()

	source := ldtestdata.DataSource()
	source.Update(source.Flag(featureflags.OrchAcceptsCombinedHostFlag.Key()).VariationForAll(enabled))

	ff, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(context.Background()) })

	return ff
}

func TestOrchestratorHeaderRoutingMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		flagEnabled bool
		want        reverseproxy.HeaderRoutingMode
	}{
		{
			name:        "flag disabled",
			flagEnabled: false,
			want:        reverseproxy.HeaderRoutingDisabled,
		},
		{
			name:        "flag enabled",
			flagEnabled: true,
			want:        reverseproxy.HeaderRoutingEnabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ff := newFFWithOrchAcceptsCombinedHost(t, tt.flagEnabled)

			require.Equal(t, tt.want, orchestratorHeaderRoutingMode(t.Context(), ff))
		})
	}
}
