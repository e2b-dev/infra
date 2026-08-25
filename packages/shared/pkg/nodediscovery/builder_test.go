package nodediscovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func TestBuildServiceDiscoveryProvider_SelectsTheAdapterCaseInsensitively(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config Config
		want   Discovery
	}{
		"DNS": {
			config: Config{Provider: "DNS", OrchestratorPort: cachedPort, DNSQuery: []string{"orchestrator.service"}, DNSResolverAddress: "127.0.0.1:53"},
			want:   &DnsServiceDiscovery{},
		},
		"lowercase dns": {
			config: Config{Provider: "dns", OrchestratorPort: cachedPort, DNSQuery: []string{"orchestrator.service"}, DNSResolverAddress: "127.0.0.1:53"},
			want:   &DnsServiceDiscovery{},
		},
		"NOMAD": {
			config: Config{Provider: "NOMAD", OrchestratorPort: cachedPort, NomadEndpoint: "http://127.0.0.1:4646", NomadToken: "token"},
			want:   &NomadServiceDiscovery{},
		},
		"STATIC": {
			config: Config{Provider: "STATIC", OrchestratorPort: cachedPort, StaticEndpoints: []string{"10.0.0.1"}},
			want:   &StaticServiceDiscovery{},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := BuildServiceDiscoveryProvider(tt.config, logger.NewNopLogger())
			require.NoError(t, err)
			assert.IsType(t, tt.want, got)
		})
	}
}

func TestBuildServiceDiscoveryProvider_RejectsIncompleteConfig(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config  Config
		wantErr error
	}{
		"unknown provider":           {config: Config{Provider: "CONSUL"}},
		"DNS without resolver":       {config: Config{Provider: "DNS", DNSQuery: []string{"orchestrator.service"}}, wantErr: ErrMissingDNSResolver},
		"DNS without query":          {config: Config{Provider: "DNS", DNSResolverAddress: "127.0.0.1:53"}, wantErr: ErrMissingDNSQuery},
		"K8S-PODS without namespace": {config: Config{Provider: "K8S-PODS", PodLabels: testLabelSelector}, wantErr: ErrMissingPodNamespace},
		"K8S-PODS without labels":    {config: Config{Provider: "K8S-PODS", PodNamespace: testNamespace}, wantErr: ErrMissingPodLabels},
		"NOMAD without endpoint":     {config: Config{Provider: "NOMAD", NomadToken: "token"}, wantErr: ErrMissingNomadEndpoint},
		"NOMAD without token":        {config: Config{Provider: "NOMAD", NomadEndpoint: "http://127.0.0.1:4646"}, wantErr: ErrMissingNomadToken},
		"STATIC without endpoints":   {config: Config{Provider: "STATIC"}, wantErr: ErrMissingStaticEndpoints},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := BuildServiceDiscoveryProvider(tt.config, logger.NewNopLogger())
			require.Error(t, err)
			assert.Nil(t, got)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestStaticServiceDiscovery_MapsEveryEndpointToTheConfiguredPort(t *testing.T) {
	t.Parallel()

	sd := NewStaticServiceDiscovery([]string{"10.0.0.1", "10.0.0.2"}, cachedPort)

	items, err := sd.ListInstances(t.Context())
	require.NoError(t, err)
	require.Len(t, items, 2)

	assert.Equal(t, "10.0.0.1:5008", items[0].Address())
	assert.Equal(t, "10.0.0.2:5008", items[1].Address())
}
