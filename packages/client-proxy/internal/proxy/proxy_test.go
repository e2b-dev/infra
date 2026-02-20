package proxy

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

type stubResumer struct {
	nodeIP string
	err    error
}

func (s stubResumer) Resume(_ context.Context, _ string, _ uint64, _ string, _ string) (string, error) {
	return s.nodeIP, s.err
}

type recordingResumer struct {
	sandboxID          string
	sandboxPort        uint64
	trafficAccessToken string
	envdAccessToken    string
}

func (r *recordingResumer) Resume(_ context.Context, sandboxID string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) (string, error) {
	r.sandboxID = sandboxID
	r.sandboxPort = sandboxPort
	r.trafficAccessToken = trafficAccessToken
	r.envdAccessToken = envdAccessToken

	return "10.0.0.1", nil
}

func newFF(t *testing.T, autoResumeEnabled bool) *featureflags.Client {
	t.Helper()

	source := ldtestdata.DataSource()
	source.Update(source.Flag(featureflags.SandboxAutoResumeFlag.Key()).VariationForAll(autoResumeEnabled))

	ff, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(context.Background()) })

	return ff
}

func TestCatalogResolution_CatalogHit(t *testing.T) {
	t.Parallel()

	c := catalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	err := c.StoreSandbox(context.Background(), "sbx", &catalog.SandboxInfo{
		OrchestratorIP: "10.0.0.1",
		ExecutionID:    "exec",
		StartedAt:      time.Now(),
	}, time.Minute)
	require.NoError(t, err)

	nodeIP, err := catalogResolution(context.Background(), "sbx", 8000, "", "", c, nil, ff)
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", nodeIP)
}

func TestCatalogResolution_CatalogHit_EmptyIPReturnsEmpty(t *testing.T) {
	t.Parallel()

	c := catalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	err := c.StoreSandbox(context.Background(), "sbx", &catalog.SandboxInfo{
		OrchestratorIP: "",
		ExecutionID:    "exec",
		StartedAt:      time.Now(),
	}, time.Minute)
	require.NoError(t, err)

	nodeIP, err := catalogResolution(context.Background(), "sbx", 8000, "", "", c, nil, ff)
	require.NoError(t, err)
	require.Empty(t, nodeIP)
}

func TestCatalogResolution_CatalogMiss(t *testing.T) {
	t.Parallel()

	c := catalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	_, err := catalogResolution(context.Background(), "sbx", 8000, "", "", c, nil, ff)
	require.ErrorIs(t, err, ErrNodeNotFound)
}

func TestHandlePausedSandbox_NoResumer(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(context.Background(), "sbx", 8000, "token", "", nil, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeNotAllowed, res)
}

func TestHandlePausedSandbox_FlagDisabled(t *testing.T) {
	t.Parallel()

	ff := newFF(t, false)

	_, res, err := handlePausedSandbox(context.Background(), "sbx", 8000, "token", "", stubResumer{nodeIP: "10.0.0.1"}, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeNotAllowed, res)
}

func TestHandlePausedSandbox_NotFound(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(context.Background(), "sbx", 8000, "token", "", stubResumer{err: status.Error(codes.NotFound, "not allowed")}, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeNotAllowed, res)
}

func TestHandlePausedSandbox_PermissionDenied(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(context.Background(), "sbx", 8000, "token", "", stubResumer{err: status.Error(codes.PermissionDenied, "permission denied")}, ff)
	require.Error(t, err)
	var deniedErr *reverseproxy.SandboxResumePermissionDeniedError
	require.ErrorAs(t, err, &deniedErr)
	require.Equal(t, autoResumePermissionDenied, res)
}

func TestHandlePausedSandbox_SnapshotNotFound(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(context.Background(), "sbx", 8000, "token", "", stubResumer{err: status.Error(codes.NotFound, "snapshot not found")}, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeNotAllowed, res)
}

func TestHandlePausedSandbox_Error(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(context.Background(), "sbx", 8000, "token", "", stubResumer{err: status.Error(codes.Unavailable, "boom")}, ff)
	require.Error(t, err)
	require.Equal(t, autoResumeErrored, res)
}

func TestHandlePausedSandbox_Succeeded(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	nodeIP, res, err := handlePausedSandbox(context.Background(), "sbx", 8000, "token", "", stubResumer{nodeIP: "10.0.0.1"}, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeSucceeded, res)
	require.Equal(t, "10.0.0.1", nodeIP)
}

func TestHandlePausedSandbox_Succeeded_EmptyIP(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	nodeIP, res, err := handlePausedSandbox(context.Background(), "sbx", 8000, "token", "", stubResumer{nodeIP: ""}, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeSucceeded, res)
	require.Empty(t, nodeIP)
}

func TestHandlePausedSandbox_PassesPortAndTokenToResumer(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)
	resumer := &recordingResumer{}

	nodeIP, res, err := handlePausedSandbox(context.Background(), "sbx", 49983, "token", "envd-token", resumer, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeSucceeded, res)
	require.Equal(t, "10.0.0.1", nodeIP)
	require.Equal(t, "sbx", resumer.sandboxID)
	require.EqualValues(t, 49983, resumer.sandboxPort)
	require.Equal(t, "token", resumer.trafficAccessToken)
	require.Equal(t, "envd-token", resumer.envdAccessToken)
}

func TestGetNotResumableCode(t *testing.T) {
	t.Parallel()

	// Direct PermissionDenied/NotFound are already covered by TestHandlePausedSandbox_*.
	// These cover edge cases: wrapped errors, non-gRPC errors, ignored codes.
	tests := []struct {
		name         string
		err          error
		expectedCode codes.Code
		expectedOK   bool
	}{
		{
			name:       "plain error is not extractable",
			err:        errors.New("plain error"),
			expectedOK: false,
		},
		{
			name:       "Unavailable is not extractable",
			err:        status.Error(codes.Unavailable, "service down"),
			expectedOK: false,
		},
		{
			name:         "wrapped PermissionDenied is extractable",
			err:          fmt.Errorf("grpc resume: %w", status.Error(codes.PermissionDenied, "denied")),
			expectedCode: codes.PermissionDenied,
			expectedOK:   true,
		},
		{
			name:         "wrapped NotFound is extractable",
			err:          fmt.Errorf("grpc resume: %w", status.Error(codes.NotFound, "not found")),
			expectedCode: codes.NotFound,
			expectedOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, ok := getNotResumableCode(tt.err)
			assert.Equal(t, tt.expectedOK, ok)
			if ok {
				assert.Equal(t, tt.expectedCode, code)
			}
		})
	}
}
