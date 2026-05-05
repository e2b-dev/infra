package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	handlersmocks "github.com/e2b-dev/infra/packages/api/internal/handlers/mocks"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestBuildAutoResumeConfig(t *testing.T) {
	t.Parallel()

	configPtr := func(v bool) *api.SandboxAutoResumeConfig {
		return &api.SandboxAutoResumeConfig{Enabled: v}
	}

	tests := []struct {
		name       string
		in         *api.SandboxAutoResumeConfig
		wantNil    bool
		wantPolicy dbtypes.SandboxAutoResumePolicy
	}{
		{
			name:    "nil config returns nil",
			in:      nil,
			wantNil: true,
		},
		{
			name:       "true maps to any policy",
			in:         configPtr(true),
			wantPolicy: dbtypes.SandboxAutoResumeAny,
		},
		{
			name:       "false maps to off policy",
			in:         configPtr(false),
			wantPolicy: dbtypes.SandboxAutoResumeOff,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := buildAutoResumeConfig(tt.in)

			if tt.wantNil {
				if got != nil {
					t.Fatalf("buildAutoResumeConfig() = %#v, want nil", got)
				}

				return
			}

			if got == nil {
				t.Fatalf("buildAutoResumeConfig() = nil, want non-nil config")
			}

			if got.Policy != tt.wantPolicy {
				t.Fatalf("buildAutoResumeConfig().Policy = %v, want %v", got.Policy, tt.wantPolicy)
			}
		})
	}
}

func TestValidateLifecycleAliases(t *testing.T) {
	t.Parallel()

	autoPause := true
	autoResume := &api.SandboxAutoResumeConfig{Enabled: true}
	lifecycleAutoResume := true
	onTimeout := api.Pause

	tests := []struct {
		name    string
		body    api.NewSandbox
		wantErr bool
	}{
		{
			name: "top level auto pause conflicts with lifecycle on timeout",
			body: api.NewSandbox{
				AutoPause: &autoPause,
				Lifecycle: &api.NewSandboxLifecycle{
					OnTimeout: &onTimeout,
				},
			},
			wantErr: true,
		},
		{
			name: "top level auto resume conflicts with lifecycle auto resume",
			body: api.NewSandbox{
				AutoResume: autoResume,
				Lifecycle: &api.NewSandboxLifecycle{
					AutoResume: &lifecycleAutoResume,
				},
			},
			wantErr: true,
		},
		{
			name: "single lifecycle surface is accepted",
			body: api.NewSandbox{
				Lifecycle: &api.NewSandboxLifecycle{
					AutoResume: &lifecycleAutoResume,
					OnTimeout:  &onTimeout,
				},
			},
		},
		{
			name: "single top level surface is accepted",
			body: api.NewSandbox{
				AutoPause:  &autoPause,
				AutoResume: autoResume,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateLifecycleAliases(tt.body)
			if tt.wantErr {
				require.NotNil(t, err)
			} else {
				require.Nil(t, err)
			}
		})
	}
}

func TestBuildKeepaliveConfig(t *testing.T) {
	t.Parallel()

	shortTimeout := int32(30)
	validTimeout := int32(120)

	tests := []struct {
		name        string
		lifecycle   *api.NewSandboxLifecycle
		wantErr     bool
		wantTimeout uint64
	}{
		{
			name: "nil lifecycle returns nil",
		},
		{
			name: "default timeout",
			lifecycle: &api.NewSandboxLifecycle{
				Keepalive: &api.SandboxKeepalive{
					Traffic: &api.SandboxTrafficKeepalive{Enabled: true},
				},
			},
			wantTimeout: dbtypes.SandboxTrafficKeepaliveTimeoutDefault,
		},
		{
			name: "timeout must exceed throttle",
			lifecycle: &api.NewSandboxLifecycle{
				Keepalive: &api.SandboxKeepalive{
					Traffic: &api.SandboxTrafficKeepalive{Enabled: true, Timeout: &shortTimeout},
				},
			},
			wantErr: true,
		},
		{
			name: "explicit valid timeout",
			lifecycle: &api.NewSandboxLifecycle{
				Keepalive: &api.SandboxKeepalive{
					Traffic: &api.SandboxTrafficKeepalive{Enabled: true, Timeout: &validTimeout},
				},
			},
			wantTimeout: uint64(validTimeout),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := buildKeepaliveConfig(tt.lifecycle)
			if tt.wantErr {
				require.NotNil(t, err)
				return
			}
			require.Nil(t, err)
			if tt.lifecycle == nil {
				require.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			require.NotNil(t, got.Traffic)
			require.Equal(t, tt.wantTimeout, got.Traffic.Timeout)
		})
	}
}

func TestSandboxLifecycleToAPI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		autoPause            bool
		autoResumeConfig     *dbtypes.SandboxAutoResumeConfig
		keepalive            *dbtypes.SandboxKeepaliveConfig
		wantAutoResume       bool
		wantTrafficKeepalive bool
		wantOnTimeout        api.SandboxOnTimeout
	}{
		{
			name:          "default kills without auto resume",
			wantOnTimeout: api.Kill,
		},
		{
			name:          "auto pause changes timeout policy",
			autoPause:     true,
			wantOnTimeout: api.Pause,
		},
		{
			name: "traffic keepalive is independent from disabled auto resume",
			autoResumeConfig: &dbtypes.SandboxAutoResumeConfig{
				Policy: dbtypes.SandboxAutoResumeOff,
			},
			keepalive: &dbtypes.SandboxKeepaliveConfig{
				Traffic: &dbtypes.SandboxTrafficKeepaliveConfig{Enabled: true, Timeout: 300},
			},
			wantTrafficKeepalive: true,
			wantOnTimeout:        api.Kill,
		},
		{
			name: "auto resume and traffic keepalive can both be enabled",
			autoResumeConfig: &dbtypes.SandboxAutoResumeConfig{
				Policy: dbtypes.SandboxAutoResumeAny,
			},
			keepalive: &dbtypes.SandboxKeepaliveConfig{
				Traffic: &dbtypes.SandboxTrafficKeepaliveConfig{Enabled: true, Timeout: 300},
			},
			wantAutoResume:       true,
			wantTrafficKeepalive: true,
			wantOnTimeout:        api.Kill,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := sandboxLifecycleToAPI(tt.autoPause, tt.autoResumeConfig, tt.keepalive)

			assert.Equal(t, tt.wantAutoResume, got.AutoResume)
			if tt.wantTrafficKeepalive {
				require.NotNil(t, got.Keepalive)
				require.NotNil(t, got.Keepalive.Traffic)
				assert.True(t, got.Keepalive.Traffic.Enabled)
			} else {
				assert.Nil(t, got.Keepalive)
			}
			assert.Equal(t, tt.wantOnTimeout, got.OnTimeout)
		})
	}
}

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
			mockFF := handlersmocks.NewMockFeatureFlagsClient(t)
			err := validateNetworkConfig(context.Background(), mockFF, uuid.Nil, "", tt.network)

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

			actual, err := convertAPIVolumesToOrchestratorVolumes(
				t.Context(), db.SqlcClient, ffClient,
				teamID, tc.input, &queries.EnvBuild{EnvdVersion: utils.ToPtr(minEnvdVersionForVolumes)},
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

		actual, err := convertAPIVolumesToOrchestratorVolumes(
			t.Context(), db.SqlcClient, ffClient,
			teamID, []api.SandboxVolumeMount{
				{Name: "vol1", Path: "/vol1"},
			}, &queries.EnvBuild{EnvdVersion: utils.ToPtr(minEnvdVersionForVolumes)},
		)
		require.NoError(t, err)
		assert.Equal(t, []*orchestrator.SandboxVolumeMount{
			{Id: dbVolume.ID.String(), Name: "vol1", Path: "/vol1", Type: "local"},
		}, actual)
	})
}

func TestPostSandboxes_MissingTagDisclosure(t *testing.T) {
	t.Parallel()

	t.Run("public template returns tag-specific not found", func(t *testing.T) {
		t.Parallel()
		assertMissingTagDisclosure(t, true, "public-missing-tag")
	})

	t.Run("private template stays generic not found", func(t *testing.T) {
		t.Parallel()
		assertMissingTagDisclosure(t, false, "private-missing-tag")
	})

	t.Run("public template without default tag names default", func(t *testing.T) {
		t.Parallel()
		assertMissingDefaultTagDisclosure(t)
	})
}

func TestPostSandboxes_MissingBareAliasUsesPromotedFallbackKey(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	requesterTeamID := testutils.CreateTestTeam(t, db)
	requesterTeamSlug := testutils.GetTeamSlug(t, ctx, db, requesterTeamID)

	store := &APIStore{
		templateCache: templatecache.NewTemplateCache(db.SqlcClient, redis),
	}
	defer func() {
		require.NoError(t, store.templateCache.Close(ctx))
	}()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)

	body, err := json.Marshal(api.PostSandboxesJSONRequestBody{TemplateID: "desktop"})
	require.NoError(t, err)

	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/sandboxes", bytes.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetTeamInfo(ginCtx, &authtypes.Team{
		Team: &authqueries.Team{
			ID:   requesterTeamID,
			Slug: requesterTeamSlug,
		},
		Limits: &authtypes.TeamLimits{MaxLengthHours: 24},
	})

	//nolint:contextcheck // PostSandboxes reads ctx from ginCtx.Request.Context().
	store.PostSandboxes(ginCtx)

	require.Equal(t, http.StatusNotFound, recorder.Code)

	var apiErr api.Error
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &apiErr))

	assert.Equal(t, int32(http.StatusNotFound), apiErr.Code)
	assert.Equal(t, "template 'desktop' not found", apiErr.Message)
}

// Valid tag exists on a private template owned by another team. A non-owner
// requester must receive the same generic 404 used for missing tags so access
// denials don't leak template existence.
func TestPostSandboxes_PrivateTemplateHidesAccessDenied(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	ownerTeamID := testutils.CreateTestTeam(t, db)
	ownerTeamSlug := testutils.GetTeamSlug(t, ctx, db, ownerTeamID)
	requesterTeamID := testutils.CreateTestTeam(t, db)
	requesterTeamSlug := testutils.GetTeamSlug(t, ctx, db, requesterTeamID)

	store := &APIStore{
		templateCache: templatecache.NewTemplateCache(db.SqlcClient, redis),
	}
	defer func() {
		require.NoError(t, store.templateCache.Close(ctx))
	}()

	alias := "private-valid-tag"
	tag := "v2"

	templateID := createTestTemplate(ctx, t, db, ownerTeamID)
	setTemplatePublic(ctx, t, db, templateID, false)
	createTestTemplateAliasWithName(ctx, t, db, templateID, alias, &ownerTeamSlug)

	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "ready")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, tag)

	templateRef := id.WithTag(id.WithNamespace(ownerTeamSlug, alias), tag)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)

	body, err := json.Marshal(api.PostSandboxesJSONRequestBody{TemplateID: templateRef})
	require.NoError(t, err)

	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/sandboxes", bytes.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetTeamInfo(ginCtx, &authtypes.Team{
		Team: &authqueries.Team{
			ID:   requesterTeamID,
			Slug: requesterTeamSlug,
		},
		Limits: &authtypes.TeamLimits{MaxLengthHours: 24},
	})

	//nolint:contextcheck // PostSandboxes reads ctx from ginCtx.Request.Context().
	store.PostSandboxes(ginCtx)

	require.Equal(t, http.StatusNotFound, recorder.Code)

	var apiErr api.Error
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &apiErr))

	assert.Equal(t, int32(http.StatusNotFound), apiErr.Code)
	assert.Equal(t, fmt.Sprintf("template '%s' not found", id.WithNamespace(ownerTeamSlug, alias)), apiErr.Message)
}

func assertMissingTagDisclosure(t *testing.T, public bool, alias string) {
	t.Helper()

	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	ownerTeamID := testutils.CreateTestTeam(t, db)
	ownerTeamSlug := testutils.GetTeamSlug(t, ctx, db, ownerTeamID)
	requesterTeamID := testutils.CreateTestTeam(t, db)
	requesterTeamSlug := testutils.GetTeamSlug(t, ctx, db, requesterTeamID)

	store := &APIStore{
		templateCache: templatecache.NewTemplateCache(db.SqlcClient, redis),
	}
	defer func() {
		require.NoError(t, store.templateCache.Close(ctx))
	}()

	templateID := createTestTemplate(ctx, t, db, ownerTeamID)
	setTemplatePublic(ctx, t, db, templateID, public)
	createTestTemplateAliasWithName(ctx, t, db, templateID, alias, &ownerTeamSlug)

	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "ready")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, id.DefaultTag)

	templateRef := id.WithTag(id.WithNamespace(ownerTeamSlug, alias), "v2")
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)

	body, err := json.Marshal(api.PostSandboxesJSONRequestBody{TemplateID: templateRef})
	require.NoError(t, err)

	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/sandboxes", bytes.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetTeamInfo(ginCtx, &authtypes.Team{
		Team: &authqueries.Team{
			ID:   requesterTeamID,
			Slug: requesterTeamSlug,
		},
		Limits: &authtypes.TeamLimits{MaxLengthHours: 24},
	})

	//nolint:contextcheck // PostSandboxes reads ctx from ginCtx.Request.Context().
	store.PostSandboxes(ginCtx)

	require.Equal(t, http.StatusNotFound, recorder.Code)

	var apiErr api.Error
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &apiErr))

	var wantMessage string
	if public {
		wantMessage = fmt.Sprintf("tag 'v2' does not exist for template '%s'", id.WithNamespace(ownerTeamSlug, alias))
	} else {
		wantMessage = fmt.Sprintf("template '%s' not found", id.WithNamespace(ownerTeamSlug, alias))
	}

	assert.Equal(t, int32(http.StatusNotFound), apiErr.Code)
	assert.Equal(t, wantMessage, apiErr.Message)
}

func assertMissingDefaultTagDisclosure(t *testing.T) {
	t.Helper()

	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	ownerTeamID := testutils.CreateTestTeam(t, db)
	ownerTeamSlug := testutils.GetTeamSlug(t, ctx, db, ownerTeamID)
	requesterTeamID := testutils.CreateTestTeam(t, db)
	requesterTeamSlug := testutils.GetTeamSlug(t, ctx, db, requesterTeamID)

	store := &APIStore{
		templateCache: templatecache.NewTemplateCache(db.SqlcClient, redis),
	}
	defer func() {
		require.NoError(t, store.templateCache.Close(ctx))
	}()

	alias := "public-missing-default-tag"
	templateID := createTestTemplate(ctx, t, db, ownerTeamID)
	createTestTemplateAliasWithName(ctx, t, db, templateID, alias, &ownerTeamSlug)

	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "ready")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, "dev")

	templateRef := id.WithNamespace(ownerTeamSlug, alias)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)

	body, err := json.Marshal(api.PostSandboxesJSONRequestBody{TemplateID: templateRef})
	require.NoError(t, err)

	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/sandboxes", bytes.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetTeamInfo(ginCtx, &authtypes.Team{
		Team: &authqueries.Team{
			ID:   requesterTeamID,
			Slug: requesterTeamSlug,
		},
		Limits: &authtypes.TeamLimits{MaxLengthHours: 24},
	})

	//nolint:contextcheck // PostSandboxes reads ctx from ginCtx.Request.Context().
	store.PostSandboxes(ginCtx)

	require.Equal(t, http.StatusNotFound, recorder.Code)

	var apiErr api.Error
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &apiErr))

	assert.Equal(t, int32(http.StatusNotFound), apiErr.Code)
	assert.Equal(t, fmt.Sprintf("tag 'default' does not exist for template '%s'", templateRef), apiErr.Message)
}

func setTemplatePublic(ctx context.Context, t *testing.T, db *testutils.Database, templateID string, public bool) {
	t.Helper()

	err := db.SqlcClient.TestsRawSQL(
		ctx,
		"UPDATE public.envs SET public = $2 WHERE id = $1",
		templateID,
		public,
	)
	require.NoError(t, err)
}

func createTestTemplate(ctx context.Context, t *testing.T, db *testutils.Database, teamID uuid.UUID) string {
	t.Helper()

	templateID := "base-env-" + uuid.New().String()

	err := db.SqlcClient.TestsRawSQL(
		ctx,
		"INSERT INTO public.envs (id, team_id, public, updated_at, source) VALUES ($1, $2, $3, NOW(), 'template')",
		templateID,
		teamID,
		true,
	)
	require.NoError(t, err)

	return templateID
}

func ffEnabled(t *testing.T) *handlersmocks.MockFeatureFlagsClient {
	t.Helper()
	ff := handlersmocks.NewMockFeatureFlagsClient(t)
	ff.EXPECT().BoolFlag(mock.Anything, mock.Anything, mock.Anything).Return(true)

	return ff
}

func ffDisabled(t *testing.T) *handlersmocks.MockFeatureFlagsClient {
	t.Helper()
	ff := handlersmocks.NewMockFeatureFlagsClient(t)
	ff.EXPECT().BoolFlag(mock.Anything, mock.Anything, mock.Anything).Return(false)

	return ff
}

func ffUnused(t *testing.T) *handlersmocks.MockFeatureFlagsClient {
	t.Helper()

	return handlersmocks.NewMockFeatureFlagsClient(t) // no expectations — must not be called
}

func rulesMap(entries map[string][]api.SandboxNetworkRule) *map[string][]api.SandboxNetworkRule {
	return &entries
}

func simpleRule(headers map[string]string) api.SandboxNetworkRule {
	h := headers

	return api.SandboxNetworkRule{
		Transform: &api.SandboxNetworkTransform{Headers: &h},
	}
}

func TestValidateNetworkRules(t *testing.T) {
	t.Parallel()

	teamID := uuid.New()

	tests := []struct {
		name        string
		envdVersion string
		rules       *map[string][]api.SandboxNetworkRule
		setupFF     func(t *testing.T) *handlersmocks.MockFeatureFlagsClient
		wantCode    int
		wantMsg     string // substring of ClientMsg; empty means expect no error
	}{
		// ── nil / empty ──────────────────────────────────────────────────────────
		{
			name:    "nil rules are valid",
			rules:   nil,
			setupFF: ffUnused,
		},
		{
			name:        "empty rules map is valid",
			envdVersion: minEnvdVersionForNetworkRules,
			rules:       rulesMap(map[string][]api.SandboxNetworkRule{}),
			setupFF:     ffEnabled,
		},
		// ── feature flag ─────────────────────────────────────────────────────────
		{
			name:     "feature flag disabled returns 400",
			rules:    rulesMap(map[string][]api.SandboxNetworkRule{"api.openai.com": {}}),
			setupFF:  ffDisabled,
			wantCode: http.StatusBadRequest,
			wantMsg:  "not available for your team",
		},
		// ── envd version ─────────────────────────────────────────────────────────
		{
			name:     "missing envd version returns 400",
			rules:    rulesMap(map[string][]api.SandboxNetworkRule{"api.openai.com": {}}),
			setupFF:  ffEnabled,
			wantCode: http.StatusBadRequest,
			wantMsg:  "template must be rebuilt: envd version is not set",
		},
		{
			name:        "envd version below minimum returns 400",
			envdVersion: "0.5.12",
			rules:       rulesMap(map[string][]api.SandboxNetworkRule{"api.openai.com": {}}),
			setupFF:     ffEnabled,
			wantCode:    http.StatusBadRequest,
			wantMsg:     "template must be rebuilt",
		},
		{
			name:        "envd version at minimum is valid",
			envdVersion: minEnvdVersionForNetworkRules,
			rules:       rulesMap(map[string][]api.SandboxNetworkRule{"api.openai.com": {}}),
			setupFF:     ffEnabled,
		},
		// ── domain count ─────────────────────────────────────────────────────────
		{
			name: "exactly max domains is valid",
			rules: func() *map[string][]api.SandboxNetworkRule {
				m := make(map[string][]api.SandboxNetworkRule, maxNetworkRuleDomains)
				for i := range maxNetworkRuleDomains {
					m[fmt.Sprintf("domain%d.example.com", i)] = nil
				}

				return &m
			}(),
			envdVersion: minEnvdVersionForNetworkRules,
			setupFF:     ffEnabled,
		},
		{
			name: "one over max domains returns 400",
			rules: func() *map[string][]api.SandboxNetworkRule {
				m := make(map[string][]api.SandboxNetworkRule, maxNetworkRuleDomains+1)
				for i := range maxNetworkRuleDomains + 1 {
					m[fmt.Sprintf("domain%d.example.com", i)] = nil
				}

				return &m
			}(),
			envdVersion: minEnvdVersionForNetworkRules,
			setupFF:     ffEnabled,
			wantCode:    http.StatusBadRequest,
			wantMsg:     fmt.Sprintf("at most %d domains", maxNetworkRuleDomains),
		},
		// ── domain key validation ─────────────────────────────────────────────────
		{
			name:        "empty domain key returns 400",
			envdVersion: minEnvdVersionForNetworkRules,
			rules:       rulesMap(map[string][]api.SandboxNetworkRule{"": {}}),
			setupFF:     ffEnabled,
			wantCode:    http.StatusBadRequest,
			wantMsg:     "must not be empty",
		},
		{
			name:        "domain exceeding max length returns 400",
			envdVersion: minEnvdVersionForNetworkRules,
			rules:       rulesMap(map[string][]api.SandboxNetworkRule{strings.Repeat("a", maxNetworkRuleDomainLen+1): {}}),
			setupFF:     ffEnabled,
			wantCode:    http.StatusBadRequest,
			wantMsg:     "maximum length",
		},
		{
			name:        "invalid domain returns 400",
			envdVersion: minEnvdVersionForNetworkRules,
			rules:       rulesMap(map[string][]api.SandboxNetworkRule{"not a valid domain!": {}}),
			setupFF:     ffEnabled,
			wantCode:    http.StatusBadRequest,
			wantMsg:     "not a valid domain name",
		},
		{
			name:        "valid plain domain is accepted",
			envdVersion: minEnvdVersionForNetworkRules,
			rules:       rulesMap(map[string][]api.SandboxNetworkRule{"api.openai.com": {}}),
			setupFF:     ffEnabled,
		},
		{
			name:        "wildcard domain is rejected",
			envdVersion: minEnvdVersionForNetworkRules,
			rules:       rulesMap(map[string][]api.SandboxNetworkRule{"*.openai.com": {}}),
			setupFF:     ffEnabled,
			wantCode:    http.StatusBadRequest,
			wantMsg:     "not a valid domain name",
		},
		{
			name:        "bare wildcard is rejected",
			envdVersion: minEnvdVersionForNetworkRules,
			rules:       rulesMap(map[string][]api.SandboxNetworkRule{"*.": {}}),
			setupFF:     ffEnabled,
			wantCode:    http.StatusBadRequest,
			wantMsg:     "not a valid domain name",
		},
		// ── transform count ───────────────────────────────────────────────────────
		{
			name:        "one transform per domain is valid",
			envdVersion: minEnvdVersionForNetworkRules,
			rules: rulesMap(map[string][]api.SandboxNetworkRule{
				"api.openai.com": {simpleRule(map[string]string{"Authorization": "Bearer token"})},
			}),
			setupFF: ffEnabled,
		},
		{
			name:        "two transforms for one domain returns 400",
			envdVersion: minEnvdVersionForNetworkRules,
			rules: rulesMap(map[string][]api.SandboxNetworkRule{
				"api.openai.com": {
					simpleRule(map[string]string{"Authorization": "Bearer token"}),
					simpleRule(map[string]string{"X-Custom": "value"}),
				},
			}),
			setupFF:  ffEnabled,
			wantCode: http.StatusBadRequest,
			wantMsg:  fmt.Sprintf("at most %d transform rule", maxNetworkRuleTransformsPerDomain),
		},
		// ── nil transform (no headers to check) ───────────────────────────────────
		{
			name:        "nil transform in rule is valid",
			envdVersion: minEnvdVersionForNetworkRules,
			rules: rulesMap(map[string][]api.SandboxNetworkRule{
				"api.openai.com": {{Transform: nil}},
			}),
			setupFF: ffEnabled,
		},
		// ── header name ───────────────────────────────────────────────────────────
		{
			name:        "empty header name returns 400",
			envdVersion: minEnvdVersionForNetworkRules,
			rules: rulesMap(map[string][]api.SandboxNetworkRule{
				"api.openai.com": {simpleRule(map[string]string{"": "value"})},
			}),
			setupFF:  ffEnabled,
			wantCode: http.StatusBadRequest,
			wantMsg:  "must not be empty",
		},
		{
			name:        "header name at max length is valid",
			envdVersion: minEnvdVersionForNetworkRules,
			rules: rulesMap(map[string][]api.SandboxNetworkRule{
				"api.openai.com": {simpleRule(map[string]string{
					strings.Repeat("X", maxNetworkRuleHeaderNameLen): "value",
				})},
			}),
			setupFF: ffEnabled,
		},
		{
			name:        "header name exceeding max length returns 400",
			envdVersion: minEnvdVersionForNetworkRules,
			rules: rulesMap(map[string][]api.SandboxNetworkRule{
				"api.openai.com": {simpleRule(map[string]string{
					strings.Repeat("X", maxNetworkRuleHeaderNameLen+1): "value",
				})},
			}),
			setupFF:  ffEnabled,
			wantCode: http.StatusBadRequest,
			wantMsg:  "maximum length",
		},
		// ── header value ──────────────────────────────────────────────────────────
		{
			name:        "header value at max length is valid",
			envdVersion: minEnvdVersionForNetworkRules,
			rules: rulesMap(map[string][]api.SandboxNetworkRule{
				"api.openai.com": {simpleRule(map[string]string{
					"Authorization": strings.Repeat("x", maxNetworkRuleHeaderValueLen),
				})},
			}),
			setupFF: ffEnabled,
		},
		{
			name:        "header value exceeding max length returns 400",
			envdVersion: minEnvdVersionForNetworkRules,
			rules: rulesMap(map[string][]api.SandboxNetworkRule{
				"api.openai.com": {simpleRule(map[string]string{
					"Authorization": strings.Repeat("x", maxNetworkRuleHeaderValueLen+1),
				})},
			}),
			setupFF:  ffEnabled,
			wantCode: http.StatusBadRequest,
			wantMsg:  "maximum length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ff := tt.setupFF(t)
			apiErr := validateNetworkRules(context.Background(), ff, teamID, tt.envdVersion, tt.rules)

			if tt.wantMsg == "" {
				assert.Nil(t, apiErr)

				return
			}

			if assert.NotNil(t, apiErr) {
				assert.Equal(t, tt.wantCode, apiErr.Code)
				assert.Contains(t, apiErr.ClientMsg, tt.wantMsg)
			}
		})
	}
}

func createTestTemplateAliasWithName(ctx context.Context, t *testing.T, db *testutils.Database, templateID, aliasName string, namespace *string) {
	t.Helper()

	err := db.SqlcClient.TestsRawSQL(
		ctx,
		"INSERT INTO public.env_aliases (alias, env_id, is_renamable, namespace) VALUES ($1, $2, $3, $4)",
		aliasName,
		templateID,
		true,
		namespace,
	)
	require.NoError(t, err)
}
