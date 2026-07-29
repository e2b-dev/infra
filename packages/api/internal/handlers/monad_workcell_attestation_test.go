package handlers

import (
	"maps"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
)

func TestMonadRunningSourceBuildID(t *testing.T) {
	t.Parallel()

	currentBuildID := uuid.New()
	sourceBuildID := uuid.New()

	t.Run("direct create uses authoritative running build", func(t *testing.T) {
		t.Parallel()

		got, err := monadRunningSourceBuildID(sandboxtypes.Sandbox{
			TemplateID:     "template-test",
			BaseTemplateID: "template-test",
			BuildID:        currentBuildID,
			Metadata: map[string]string{
				monadMetadataImageID: sourceBuildID.String(),
			},
		})
		require.NoError(t, err)
		assert.Equal(t, currentBuildID, got)
	})

	t.Run("resumed sandbox uses persisted source build", func(t *testing.T) {
		t.Parallel()

		got, err := monadRunningSourceBuildID(sandboxtypes.Sandbox{
			TemplateID:     "pause-snapshot-template",
			BaseTemplateID: "template-test",
			BuildID:        currentBuildID,
			Metadata: map[string]string{
				monadMetadataImageID: sourceBuildID.String(),
			},
		})
		require.NoError(t, err)
		assert.Equal(t, sourceBuildID, got)
	})

	t.Run("resumed sandbox fails closed without source build", func(t *testing.T) {
		t.Parallel()

		_, err := monadRunningSourceBuildID(sandboxtypes.Sandbox{
			TemplateID:     "pause-snapshot-template",
			BaseTemplateID: "template-test",
			BuildID:        currentBuildID,
			Metadata:       map[string]string{},
		})
		require.Error(t, err)
	})
}

func TestBuildMonadWorkcellAttestation(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	allowInternet := false
	allowPublic := false
	source := monadWorkcellAttestationSource{
		sandboxID:  "i-test",
		templateID: "template-test",
		buildID:    buildID,
		metadata: map[string]string{
			monadMetadataProvider:         monadProviderE2B,
			monadMetadataTemplateID:       "template-test",
			monadMetadataImageID:          buildID.String(),
			monadMetadataIdentityFidelity: monadIdentityImageAttested,
			monadMetadataPlacement:        "us-east4",
		},
		cpuCount:            2,
		memoryMB:            2048,
		allowInternetAccess: &allowInternet,
		network: &dbtypes.SandboxNetworkConfig{
			Ingress: &dbtypes.SandboxNetworkIngressConfig{AllowPublicAccess: &allowPublic},
			Egress:  &dbtypes.SandboxNetworkEgressConfig{DeniedAddresses: []string{"0.0.0.0/0"}},
		},
		autoPause:  true,
		autoResume: &dbtypes.SandboxAutoResumeConfig{Policy: dbtypes.SandboxAutoResumeOff},
	}

	got, err := buildMonadWorkcellAttestation(source, "gcp", "us-east4")
	require.NoError(t, err)
	assert.Equal(t, api.N1, got.SchemaVersion)
	assert.Equal(t, "i-test", got.SandboxId)
	assert.Equal(t, api.E2b, got.Provider)
	assert.Equal(t, "gcp", got.Cloud)
	assert.Equal(t, "us-east4", got.Region)
	assert.Equal(t, "template-test", got.TemplateId)
	assert.Equal(t, api.ImageAttested, got.IdentityFidelity)
	assert.Equal(t, buildID, got.ImageId)
	assert.EqualValues(t, 2, got.Resources.CpuCount)
	assert.EqualValues(t, 2048, got.Resources.MemoryMb)
	assert.False(t, got.Network.AllowInternetAccess)
	assert.False(t, got.Network.AllowPublicTraffic)
	require.NotNil(t, got.Network.DenyOut)
	assert.Equal(t, []string{"0.0.0.0/0"}, *got.Network.DenyOut)
	assert.Equal(t, api.Pause, got.Lifecycle.OnTimeout)
	assert.False(t, got.Lifecycle.AutoResume)
	require.NotNil(t, got.Lifecycle.PauseFidelity)
	assert.Equal(t, api.FilesystemAndMemory, *got.Lifecycle.PauseFidelity)

	source.network.Egress.DeniedAddresses[0] = "mutated"
	assert.Equal(t, []string{"0.0.0.0/0"}, *got.Network.DenyOut, "attestation must not alias mutable sandbox state")
}

func TestBuildMonadWorkcellAttestationFailsClosed(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	allowInternet := true
	allowPublic := false
	valid := monadWorkcellAttestationSource{
		sandboxID:  "i-test",
		templateID: "template-test",
		buildID:    buildID,
		metadata: map[string]string{
			monadMetadataProvider:         monadProviderE2B,
			monadMetadataTemplateID:       "template-test",
			monadMetadataImageID:          buildID.String(),
			monadMetadataIdentityFidelity: monadIdentityImageAttested,
			monadMetadataPlacement:        "us-east4",
		},
		cpuCount:            2,
		memoryMB:            2048,
		allowInternetAccess: &allowInternet,
		network: &dbtypes.SandboxNetworkConfig{
			Ingress: &dbtypes.SandboxNetworkIngressConfig{AllowPublicAccess: &allowPublic},
		},
	}

	tests := []struct {
		name   string
		mutate func(*monadWorkcellAttestationSource)
	}{
		{name: "provider", mutate: func(s *monadWorkcellAttestationSource) { s.metadata[monadMetadataProvider] = "other" }},
		{name: "template", mutate: func(s *monadWorkcellAttestationSource) { s.metadata[monadMetadataTemplateID] = "other" }},
		{name: "image", mutate: func(s *monadWorkcellAttestationSource) { s.metadata[monadMetadataImageID] = uuid.NewString() }},
		{name: "fidelity", mutate: func(s *monadWorkcellAttestationSource) {
			s.metadata[monadMetadataIdentityFidelity] = "template-reference"
		}},
		{name: "placement", mutate: func(s *monadWorkcellAttestationSource) { s.metadata[monadMetadataPlacement] = "other-region" }},
		{name: "internet policy", mutate: func(s *monadWorkcellAttestationSource) { s.allowInternetAccess = nil }},
		{name: "network policy", mutate: func(s *monadWorkcellAttestationSource) { s.network = nil }},
		{name: "ingress policy", mutate: func(s *monadWorkcellAttestationSource) { s.network.Ingress.AllowPublicAccess = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := valid
			source.metadata = make(map[string]string, len(valid.metadata))
			maps.Copy(source.metadata, valid.metadata)
			source.network = &dbtypes.SandboxNetworkConfig{
				Ingress: &dbtypes.SandboxNetworkIngressConfig{AllowPublicAccess: &allowPublic},
			}
			tt.mutate(&source)

			_, err := buildMonadWorkcellAttestation(source, "gcp", "us-east4")
			require.Error(t, err)
		})
	}
}

func TestBuildMonadWorkcellAttestationLifecycle(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	allowInternet := true
	allowPublic := true
	source := monadWorkcellAttestationSource{
		sandboxID:  "i-test",
		templateID: "template-test",
		buildID:    buildID,
		metadata: map[string]string{
			monadMetadataProvider:         monadProviderE2B,
			monadMetadataTemplateID:       "template-test",
			monadMetadataImageID:          buildID.String(),
			monadMetadataIdentityFidelity: monadIdentityImageAttested,
			monadMetadataPlacement:        "us-east4",
		},
		cpuCount:            1,
		memoryMB:            512,
		allowInternetAccess: &allowInternet,
		network: &dbtypes.SandboxNetworkConfig{
			Ingress: &dbtypes.SandboxNetworkIngressConfig{AllowPublicAccess: &allowPublic},
		},
	}

	kill, err := buildMonadWorkcellAttestation(source, "gcp", "us-east4")
	require.NoError(t, err)
	assert.Equal(t, api.Kill, kill.Lifecycle.OnTimeout)
	assert.Nil(t, kill.Lifecycle.PauseFidelity)

	source.autoPause = true
	source.autoPauseFilesystemOnly = true
	source.autoResume = &dbtypes.SandboxAutoResumeConfig{Policy: dbtypes.SandboxAutoResumeAny}
	pause, err := buildMonadWorkcellAttestation(source, "gcp", "us-east4")
	require.NoError(t, err)
	assert.Equal(t, api.Pause, pause.Lifecycle.OnTimeout)
	assert.True(t, pause.Lifecycle.AutoResume)
	require.NotNil(t, pause.Lifecycle.PauseFidelity)
	assert.Equal(t, api.FilesystemOnly, *pause.Lifecycle.PauseFidelity)
}
