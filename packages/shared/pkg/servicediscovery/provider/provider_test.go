package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery"
)

const (
	cachedPort        = 5008
	testNamespace     = "e2b"
	testLabelSelector = "app.kubernetes.io/name=orchestrator"
)

func TestFromConfig_SelectsTheAdapterCaseInsensitively(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config servicediscovery.Config
		cached bool
	}{
		"DNS": {
			config: servicediscovery.Config{Provider: "DNS", OrchestratorPort: cachedPort, DNSQuery: []string{"orchestrator.service"}, DNSResolverAddress: "127.0.0.1:53"},
			cached: true,
		},
		"lowercase dns": {
			config: servicediscovery.Config{Provider: "dns", OrchestratorPort: cachedPort, DNSQuery: []string{"orchestrator.service"}, DNSResolverAddress: "127.0.0.1:53"},
			cached: true,
		},
		"NOMAD": {
			config: servicediscovery.Config{Provider: "NOMAD", OrchestratorPort: cachedPort, NomadEndpoint: "http://127.0.0.1:4646", NomadToken: "token"},
			cached: true,
		},
		"STATIC": {
			config: servicediscovery.Config{Provider: "STATIC", OrchestratorPort: cachedPort, StaticEndpoints: []string{"10.0.0.1"}},
			cached: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := New(tt.config, logger.NewNopLogger())
			require.NoError(t, err)

			// A cached backend has read nothing until its first refresh; a
			// direct one answers from configuration straight away. That
			// difference is what the caller sees, so assert it rather than the
			// concrete type, which the split puts out of reach.
			_, listErr := got.ListInstances(t.Context())
			if tt.cached {
				require.ErrorIs(t, listErr, servicediscovery.ErrNotYetSynced)

				return
			}
			require.NoError(t, listErr)
		})
	}
}

func TestFromConfig_RejectsIncompleteConfig(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config  servicediscovery.Config
		wantErr error
	}{
		"unknown provider":           {config: servicediscovery.Config{Provider: "CONSUL"}},
		"DNS without resolver":       {config: servicediscovery.Config{Provider: "DNS", DNSQuery: []string{"orchestrator.service"}}, wantErr: ErrMissingDNSResolver},
		"DNS without query":          {config: servicediscovery.Config{Provider: "DNS", DNSResolverAddress: "127.0.0.1:53"}, wantErr: ErrMissingDNSQuery},
		"K8S-PODS without namespace": {config: servicediscovery.Config{Provider: "K8S-PODS", PodLabels: testLabelSelector}, wantErr: ErrMissingPodNamespace},
		"K8S-PODS without labels":    {config: servicediscovery.Config{Provider: "K8S-PODS", PodNamespace: testNamespace}, wantErr: ErrMissingPodLabels},
		"NOMAD without endpoint":     {config: servicediscovery.Config{Provider: "NOMAD", NomadToken: "token"}, wantErr: ErrMissingNomadEndpoint},
		"NOMAD without token":        {config: servicediscovery.Config{Provider: "NOMAD", NomadEndpoint: "http://127.0.0.1:4646"}, wantErr: ErrMissingNomadToken},
		"STATIC without endpoints":   {config: servicediscovery.Config{Provider: "STATIC"}, wantErr: ErrMissingStaticEndpoints},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := New(tt.config, logger.NewNopLogger())
			require.Error(t, err)
			assert.Nil(t, got)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestStatic_MapsEveryEndpointToTheConfiguredPort(t *testing.T) {
	t.Parallel()

	sd := servicediscovery.NewStatic([]string{"10.0.0.1", "10.0.0.2"}, cachedPort)

	items, err := sd.ListInstances(t.Context())
	require.NoError(t, err)
	require.Len(t, items, 2)

	assert.Equal(t, "10.0.0.1:5008", items[0].Address())
	assert.Equal(t, "10.0.0.2:5008", items[1].Address())
}
