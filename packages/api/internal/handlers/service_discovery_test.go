package handlers

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"

	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery"
)

func TestServiceDiscoveryProvider_OnlyAnUnsetProviderDefaults(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		configured string
		localEnv   bool
		want       string
	}{
		"unset in a local environment has no Nomad to reach": {
			configured: "", localEnv: true, want: cfg.ServiceDiscoveryProviderLocal,
		},
		"unset anywhere else is Nomad": {
			configured: "", localEnv: false, want: cfg.ServiceDiscoveryProviderNomad,
		},
		"nomad named explicitly survives a local environment": {
			configured: cfg.ServiceDiscoveryProviderNomad, localEnv: true, want: cfg.ServiceDiscoveryProviderNomad,
		},
		"kubernetes named explicitly survives too": {
			configured: cfg.ServiceDiscoveryProviderKubernetes, localEnv: true, want: cfg.ServiceDiscoveryProviderKubernetes,
		},
		"local stays local": {
			configured: cfg.ServiceDiscoveryProviderLocal, localEnv: false, want: cfg.ServiceDiscoveryProviderLocal,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := serviceDiscoveryProvider(cfg.Config{ServiceDiscoveryProvider: tt.configured}, tt.localEnv)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Both planes have to resolve without a Nomad agent: the template-builder plane
// feeds the local cluster, which is the only source of orchestrator nodes when
// the Nomad node sync is off, so a plane that errors here leaves the API
// permanently unhealthy.
func TestNewServiceDiscovery_AnUnsetProviderResolvesBothPlanesLocally(t *testing.T) {
	t.Parallel()

	config := cfg.Config{LocalOrchestratorAddress: "127.0.0.1:5008"}

	sd, err := newServiceDiscovery(config, nil, serviceDiscoveryProvider(config, true))
	require.NoError(t, err)

	for name, plane := range map[string]servicediscovery.Discoverer{
		"nodes":            sd.nodes,
		"templateBuilders": sd.templateBuilders,
	} {
		instances, err := plane.ListInstances(t.Context())
		require.NoErrorf(t, err, "the %s plane must resolve without a Nomad agent", name)
		require.Lenf(t, instances, 1, "the %s plane must report the local orchestrator", name)
		assert.Equal(t, "127.0.0.1:5008", instances[0].Address())
	}
}

func TestNewServiceDiscovery_ExplicitProviderSurvivesALocalEnvironment(t *testing.T) {
	t.Parallel()

	kubeErr := errors.New("no kubeconfig")
	config := cfg.Config{ServiceDiscoveryProvider: cfg.ServiceDiscoveryProviderKubernetes}

	_, err := newServiceDiscovery(
		config,
		func() (kubernetes.Interface, error) { return nil, kubeErr },
		serviceDiscoveryProvider(config, true),
	)
	require.ErrorIs(t, err, kubeErr)
}

// The clusters registry owns orchestrator nodes only when the resolved provider
// is the static local one — there both planes are the same single instance.
// Deriving it from the environment instead builds a Nomad or Kubernetes node
// plane that neither the periodic loop nor on-demand discovery ever consults,
// so an orchestrator that is not also a template builder never registers.
func TestLocalRegistryOwnsOrchestrators_FollowsTheProviderNotTheEnvironment(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		configured string
		localEnv   bool
		wantOwns   bool
	}{
		"unset in a local environment resolves to local, so the registry owns them": {
			configured: "", localEnv: true, wantOwns: true,
		},
		"nomad named explicitly in a local environment keeps its node plane": {
			configured: cfg.ServiceDiscoveryProviderNomad, localEnv: true, wantOwns: false,
		},
		"kubernetes named explicitly in a local environment keeps its node plane": {
			configured: cfg.ServiceDiscoveryProviderKubernetes, localEnv: true, wantOwns: false,
		},
		"nomad anywhere else keeps its node plane": {
			configured: "", localEnv: false, wantOwns: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := serviceDiscoveryProvider(cfg.Config{ServiceDiscoveryProvider: tt.configured}, tt.localEnv)
			assert.Equal(t, tt.wantOwns, provider == cfg.ServiceDiscoveryProviderLocal)
		})
	}
}
