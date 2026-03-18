package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
)

func TestToSandboxDetailLifecycle(t *testing.T) {
	t.Parallel()

	assert.Equal(
		t,
		api.SandboxLifecycle{
			AutoResume: false,
			OnTimeout:  api.SandboxOnTimeout("kill"),
		},
		toSandboxDetailLifecycle(nil, false),
	)
	assert.Equal(
		t,
		api.SandboxLifecycle{
			AutoResume: false,
			OnTimeout:  api.SandboxOnTimeout("pause"),
		},
		toSandboxDetailLifecycle(&dbtypes.SandboxAutoResumeConfig{Policy: dbtypes.SandboxAutoResumeOff}, true),
	)
	assert.Equal(
		t,
		api.SandboxLifecycle{
			AutoResume: true,
			OnTimeout:  api.SandboxOnTimeout("kill"),
		},
		toSandboxDetailLifecycle(&dbtypes.SandboxAutoResumeConfig{Policy: dbtypes.SandboxAutoResumeAny}, false),
	)
}

func TestToSandboxDetailNetworkConfig(t *testing.T) {
	t.Parallel()

	assert.Equal(t, api.SandboxNetworkConfig{}, toSandboxDetailNetworkConfig(nil))

	allowPublicTraffic := false
	maskRequestHost := "sandbox.internal"

	got := toSandboxDetailNetworkConfig(&dbtypes.SandboxNetworkConfig{
		Ingress: &dbtypes.SandboxNetworkIngressConfig{
			AllowPublicAccess: &allowPublicTraffic,
			MaskRequestHost:   &maskRequestHost,
		},
		Egress: &dbtypes.SandboxNetworkEgressConfig{
			AllowedAddresses: []string{"8.8.8.8", "*.example.com"},
			DeniedAddresses:  []string{"10.0.0.0/8"},
		},
	})

	require.NotNil(t, got.AllowPublicTraffic)
	assert.False(t, *got.AllowPublicTraffic)
	require.NotNil(t, got.MaskRequestHost)
	assert.Equal(t, "sandbox.internal", *got.MaskRequestHost)
	require.NotNil(t, got.AllowOut)
	assert.Equal(t, []string{"8.8.8.8", "*.example.com"}, *got.AllowOut)
	require.NotNil(t, got.DenyOut)
	assert.Equal(t, []string{"10.0.0.0/8"}, *got.DenyOut)
}
