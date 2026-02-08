package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	handlersmocks "github.com/e2b-dev/infra/packages/api/internal/handlers/mocks"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
)

func TestValidateNetworkConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		network    *api.SandboxNetworkConfig
		wantErr    bool
		wantCode   int
		wantErrMsg string
	}{
		{
			name:    "nil network config is valid",
			network: nil,
			wantErr: false,
		},
		{
			name:    "empty network config is valid",
			network: &api.SandboxNetworkConfig{},
			wantErr: false,
		},
		{
			name: "valid deny_out with CIDR",
			network: &api.SandboxNetworkConfig{
				DenyOut: &[]string{"10.0.0.0/8"},
			},
			wantErr: false,
		},
		{
			name: "invalid deny_out entry",
			network: &api.SandboxNetworkConfig{
				DenyOut: &[]string{"not-a-cidr"},
			},
			wantErr:    true,
			wantCode:   http.StatusBadRequest,
			wantErrMsg: "invalid denied CIDR not-a-cidr",
		},
		// Domain validation tests
		{
			name: "allow_out with domain requires deny_out block-all",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"example.com"},
			},
			wantErr:    true,
			wantCode:   http.StatusBadRequest,
			wantErrMsg: ErrMsgDomainsRequireBlockAll,
		},
		{
			name: "allow_out with domain and block-all deny_out is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"example.com"},
				DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
			},
			wantErr: false,
		},
		{
			name: "allow_out with domain and partial deny_out is invalid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"example.com"},
				DenyOut:  &[]string{"10.0.0.0/8"},
			},
			wantErr:    true,
			wantCode:   http.StatusBadRequest,
			wantErrMsg: ErrMsgDomainsRequireBlockAll,
		},
		{
			name: "allow_out with wildcard domain requires deny_out block-all",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"*.example.com"},
			},
			wantErr:    true,
			wantCode:   http.StatusBadRequest,
			wantErrMsg: ErrMsgDomainsRequireBlockAll,
		},
		{
			name: "allow_out with wildcard domain and block-all deny_out is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"*.example.com"},
				DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
			},
			wantErr: false,
		},
		// CIDR validation tests
		{
			name: "allow_out with CIDR without deny_out is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.0.0.0/8"},
			},
			wantErr: false,
		},
		{
			name: "allow_out with CIDR and deny_out block-all is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.0.0.0/8"},
				DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
			},
			wantErr: false,
		},
		{
			name: "allow_out with IP without deny_out is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"8.8.8.8"},
			},
			wantErr: false,
		},
		{
			name: "allow_out with IP and deny_out block-all is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"8.8.8.8"},
				DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
			},
			wantErr: false,
		},
		// CIDR intersection validation tests
		{
			name: "allow_out CIDR not covered by deny_out CIDR is valid (no intersection check)",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.0.0.0/8"},
				DenyOut:  &[]string{"192.168.0.0/16"}, // No intersection, but still valid
			},
			wantErr: false,
		},
		{
			name: "allow_out CIDR covered by intersecting deny_out CIDR is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.1.0.0/16"},
				DenyOut:  &[]string{"10.0.0.0/8"}, // Deny covers allow
			},
			wantErr: false,
		},
		{
			name: "allow_out CIDR covers deny_out CIDR is valid (intersection exists)",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.0.0.0/8"},
				DenyOut:  &[]string{"10.1.0.0/16"}, // Allow covers deny - still valid intersection
			},
			wantErr: false,
		},
		{
			name: "allow_out IP covered by deny_out CIDR is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.1.2.3"},
				DenyOut:  &[]string{"10.0.0.0/8"},
			},
			wantErr: false,
		},
		{
			name: "allow_out IP not covered by deny_out CIDR is valid (no intersection check)",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"8.8.8.8"},
				DenyOut:  &[]string{"10.0.0.0/8"},
			},
			wantErr: false,
		},
		{
			name: "multiple allow_out CIDRs partial deny_out coverage is valid (no intersection check)",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.0.0.0/8", "192.168.0.0/16"},
				DenyOut:  &[]string{"10.0.0.0/8"}, // Only covers first, but still valid
			},
			wantErr: false,
		},
		{
			name: "multiple allow_out CIDRs covered by multiple deny_out CIDRs is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.0.0.0/8", "192.168.0.0/16"},
				DenyOut:  &[]string{"10.0.0.0/8", "192.168.0.0/16"},
			},
			wantErr: false,
		},
		// Mixed domain and CIDR tests
		{
			name: "allow_out with domain and CIDR without deny_out block-all is invalid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"example.com", "8.8.8.8"},
				DenyOut:  &[]string{"10.0.0.0/8"},
			},
			wantErr:    true,
			wantCode:   http.StatusBadRequest,
			wantErrMsg: ErrMsgDomainsRequireBlockAll,
		},
		{
			name: "allow_out with domain and CIDR with deny_out block-all is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"example.com", "8.8.8.8"},
				DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateNetworkConfig(tt.network)

			if tt.wantErr {
				if err == nil {
					t.Errorf("validateNetworkConfig() expected error, got nil")

					return
				}

				if err.Code != tt.wantCode {
					t.Errorf("validateNetworkConfig() error code = %v, want %v", err.Code, tt.wantCode)
				}

				if err.ClientMsg != tt.wantErrMsg {
					t.Errorf("validateNetworkConfig() error message = %q, want %q", err.ClientMsg, tt.wantErrMsg)
				}
			} else if err != nil {
				t.Errorf("validateNetworkConfig() unexpected error: %v", err)
			}
		})
	}
}

func TestOrchestrator_convertVolumeMounts(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)

	t.Run("InvalidVolumeMountsError.Error() returns expected string", func(t *testing.T) {
		t.Parallel()

		err := InvalidVolumeMountsError{[]InvalidMount{
			{0, "reason1"},
			{2, "reason2"},
		}}
		expected := "invalid mounts:\n\t- volume mount #0: reason1\n\t- volume mount #2: reason2"
		assert.Equal(t, expected, err.Error())
	})

	testCases := map[string]struct {
		expectFeatureFlag bool
		expectResources   bool
		volumesEnabled    bool
		input             []api.SandboxVolumeMount
		database          []queries.CreateVolumeParams
		volumeTypes       []string
		expected          []*orchestrator.SandboxVolumeMount
		err               error
	}{
		"missing volume reports correct error": {
			expectFeatureFlag: true,
			expectResources:   true,
			volumesEnabled:    true,
			input: []api.SandboxVolumeMount{
				{Name: "vol1"},
			},
			volumeTypes: []string{},
			err:         InvalidVolumeMountsError{[]InvalidMount{{0, "volume 'vol1' not found"}}},
		},
		"partial success returns error": {
			expectFeatureFlag: true,
			expectResources:   true,
			volumesEnabled:    true,
			input: []api.SandboxVolumeMount{
				{Name: "vol1", Path: "/vol1"},
				{Name: "vol2", Path: "/vol2"},
			},
			database: []queries.CreateVolumeParams{
				{Name: "vol1", VolumeType: "local"},
			},
			volumeTypes: []string{"local"},
			err:         InvalidVolumeMountsError{[]InvalidMount{{1, "volume 'vol2' not found"}}},
		},
		"empty volume mounts": {
			input:    []api.SandboxVolumeMount{},
			expected: []*orchestrator.SandboxVolumeMount{},
		},
		"feature flag disabled": {
			expectFeatureFlag: true,
			volumesEnabled:    false,
			input: []api.SandboxVolumeMount{
				{Name: "vol1", Path: "/vol1"},
			},
			err: ErrVolumeMountsDisabled,
		},
		"empty path reports error": {
			expectFeatureFlag: true,
			expectResources:   true,
			volumesEnabled:    true,
			input: []api.SandboxVolumeMount{
				{Name: "vol1", Path: ""},
			},
			database: []queries.CreateVolumeParams{
				{Name: "vol1", VolumeType: "local"},
			},
			volumeTypes: []string{"local"},
			err:         InvalidVolumeMountsError{[]InvalidMount{{0, "path cannot be empty"}}},
		},
		"non-absolute path reports error": {
			expectFeatureFlag: true,
			expectResources:   true,
			volumesEnabled:    true,
			input: []api.SandboxVolumeMount{
				{Name: "vol1", Path: "relative/path"},
			},
			database: []queries.CreateVolumeParams{
				{Name: "vol1", VolumeType: "local"},
			},
			volumeTypes: []string{"local"},
			err:         InvalidVolumeMountsError{[]InvalidMount{{0, "path must be absolute"}}},
		},
		"non-clean path reports error": {
			expectFeatureFlag: true,
			expectResources:   true,
			volumesEnabled:    true,
			input: []api.SandboxVolumeMount{
				{Name: "vol1", Path: "/path/./to/somewhere"},
			},
			database: []queries.CreateVolumeParams{
				{Name: "vol1", VolumeType: "local"},
			},
			volumeTypes: []string{"local"},
			err:         InvalidVolumeMountsError{[]InvalidMount{{0, "path must not contain any '.' or '..' components"}}},
		},
		"duplicate paths report error": {
			expectFeatureFlag: true,
			expectResources:   true,
			volumesEnabled:    true,
			input: []api.SandboxVolumeMount{
				{Name: "vol1", Path: "/path"},
				{Name: "vol2", Path: "/path"},
			},
			database: []queries.CreateVolumeParams{
				{Name: "vol1", VolumeType: "local"},
				{Name: "vol2", VolumeType: "local"},
			},
			volumeTypes: []string{"local"},
			err:         InvalidVolumeMountsError{[]InvalidMount{{1, "path '/path' is already used"}}},
		},
		"multiple invalid mounts report all errors": {
			expectFeatureFlag: true,
			expectResources:   true,
			volumesEnabled:    true,
			input: []api.SandboxVolumeMount{
				{Name: "missing", Path: "/path1"},
				{Name: "vol1", Path: "relative"},
				{Name: "vol2", Path: "/path2"},
				{Name: "vol3", Path: "/path2"},
			},
			database: []queries.CreateVolumeParams{
				{Name: "vol1", VolumeType: "local"},
				{Name: "vol2", VolumeType: "local"},
				{Name: "vol3", VolumeType: "local"},
			},
			volumeTypes: []string{"local"},
			err: InvalidVolumeMountsError{[]InvalidMount{
				{0, "volume 'missing' not found"},
				{1, "path must be absolute"},
				{3, "path '/path2' is already used"},
			}},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			teamID := testutils.CreateTestTeam(t, db)

			for _, v := range tc.database {
				_, err := db.SqlcClient.CreateVolume(t.Context(),
					queries.CreateVolumeParams{
						Name:       v.Name,
						TeamID:     teamID,
						VolumeType: v.VolumeType,
					},
				)
				require.NoError(t, err)
			}

			ffClient := handlersmocks.NewMockFeatureFlagsClient(t)
			if tc.expectFeatureFlag {
				ffClient.EXPECT().
					BoolFlag(mock.Anything, mock.Anything).
					Return(tc.volumesEnabled)
			}

			actual, err := createOrchestratorVolumeMounts(
				t.Context(), db.SqlcClient, ffClient,
				teamID, tc.input,
			)
			assert.Equal(t, tc.err, err)
			assert.Equal(t, tc.expected, actual)
		})
	}

	t.Run("existing volumes are returned", func(t *testing.T) {
		t.Parallel()

		teamID := testutils.CreateTestTeam(t, db)

		dbVolume, err := db.SqlcClient.CreateVolume(t.Context(),
			queries.CreateVolumeParams{
				Name:       "vol1",
				TeamID:     teamID,
				VolumeType: "local",
			},
		)
		require.NoError(t, err)

		ffClient := handlersmocks.NewMockFeatureFlagsClient(t)
		ffClient.EXPECT().
			BoolFlag(mock.Anything, mock.Anything).
			Return(true)

		actual, err := createOrchestratorVolumeMounts(
			t.Context(), db.SqlcClient, ffClient,
			teamID, []api.SandboxVolumeMount{
				{Name: "vol1", Path: "/vol1"},
			},
		)
		require.NoError(t, err)
		assert.Equal(t, []*orchestrator.SandboxVolumeMount{
			{Id: dbVolume.ID.String(), Path: "/vol1", Type: "local"},
		}, actual)
	})
}
