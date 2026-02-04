package api

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestResumeSandboxWithAPIKey(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	c := setup.GetAPIClient()
	grpcClient := setup.GetProxyGrpcClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoResume(api.NewSandboxAutoResume("authed")))
	ensureSandboxPaused(t, c, sbx.SandboxID)

	authCtx := metadata.NewOutgoingContext(ctx, metadata.New(map[string]string{
		"x-api-key": setup.APIKey,
	}))
	_, err := grpcClient.ResumeSandbox(authCtx, &proxygrpc.SandboxResumeRequest{SandboxId: sbx.SandboxID})
	require.NoError(t, err)
	waitForSandboxState(t, c, sbx.SandboxID, api.Running)
}

func TestResumeSandboxWithAccessToken(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	c := setup.GetAPIClient()
	grpcClient := setup.GetProxyGrpcClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoResume(api.NewSandboxAutoResume("authed")))
	ensureSandboxPaused(t, c, sbx.SandboxID)

	authCtx := metadata.NewOutgoingContext(ctx, metadata.New(map[string]string{
		"authorization": "Bearer " + setup.AccessToken,
	}))
	_, err := grpcClient.ResumeSandbox(authCtx, &proxygrpc.SandboxResumeRequest{SandboxId: sbx.SandboxID})
	require.NoError(t, err)
	waitForSandboxState(t, c, sbx.SandboxID, api.Running)
}

func TestResumeSandboxPolicyAuthedWithoutAuth(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	c := setup.GetAPIClient()
	grpcClient := setup.GetProxyGrpcClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoResume(api.NewSandboxAutoResume("authed")))
	ensureSandboxPaused(t, c, sbx.SandboxID)

	_, err := grpcClient.ResumeSandbox(ctx, &proxygrpc.SandboxResumeRequest{SandboxId: sbx.SandboxID})
	require.Error(t, err)
	statusErr, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.PermissionDenied, statusErr.Code())
}

func TestResumeSandboxPolicyNull(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	c := setup.GetAPIClient()
	grpcClient := setup.GetProxyGrpcClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, c)
	ensureSandboxPaused(t, c, sbx.SandboxID)

	_, err := grpcClient.ResumeSandbox(ctx, &proxygrpc.SandboxResumeRequest{SandboxId: sbx.SandboxID})
	require.Error(t, err)
	statusErr, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, statusErr.Code())
}

func TestResumeSandboxPolicyAnyWithoutAuth(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	c := setup.GetAPIClient()
	grpcClient := setup.GetProxyGrpcClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoResume(api.NewSandboxAutoResume("any")))
	ensureSandboxPaused(t, c, sbx.SandboxID)

	_, err := grpcClient.ResumeSandbox(ctx, &proxygrpc.SandboxResumeRequest{SandboxId: sbx.SandboxID})
	require.NoError(t, err)
	waitForSandboxState(t, c, sbx.SandboxID, api.Running)
}
