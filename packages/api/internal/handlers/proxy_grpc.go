package handlers

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	snapshotcache "github.com/e2b-dev/infra/packages/api/internal/cache/snapshots"
	"github.com/e2b-dev/infra/packages/api/internal/oauth"
	apiorchestrator "github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	typesteam "github.com/e2b-dev/infra/packages/auth/pkg/types"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type SandboxService struct {
	proxygrpc.UnimplementedSandboxServiceServer

	api                        *APIStore
	requireEdgeClientProxyAuth bool
	clientProxyOAuth           oauth.Verifier
}

func NewSandboxService(api *APIStore, requireEdgeClientProxyAuth bool, clientProxyOAuth oauth.Verifier) *SandboxService {
	return &SandboxService{
		api:                        api,
		requireEdgeClientProxyAuth: requireEdgeClientProxyAuth,
		clientProxyOAuth:           clientProxyOAuth,
	}
}

func metadataFromIncomingContext(ctx context.Context) metadata.MD {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		return md
	}

	return metadata.MD{}
}

func metadataFirstValue(md metadata.MD, key string) (string, bool) {
	vals := md.Get(key)
	if len(vals) == 0 {
		return "", false
	}

	return vals[0], true
}

func isNonEnvdTrafficRequest(ctx context.Context, incomingMetadata metadata.MD, sandboxID string) bool {
	requestPortRaw, found := metadataFirstValue(incomingMetadata, proxygrpc.MetadataSandboxRequestPort)
	if !found {
		return true
	}

	requestPort, parseErr := strconv.ParseUint(requestPortRaw, 10, 64)
	if parseErr != nil {
		logger.L().Warn(
			ctx,
			"invalid sandbox request port metadata for proxy traffic",
			zap.Error(parseErr),
			zap.String("request_port", requestPortRaw),
			logger.WithSandboxID(sandboxID),
		)

		return true
	}

	return requestPort != uint64(consts.DefaultEnvdServerPort)
}

func isPrivateIngressTraffic(network *dbtypes.SandboxNetworkConfig) bool {
	return network != nil && network.Ingress != nil && network.Ingress.AllowPublicAccess != nil && !*network.Ingress.AllowPublicAccess
}

func tokensMatch(providedToken string, expectedToken string) bool {
	return subtle.ConstantTimeCompare([]byte(providedToken), []byte(expectedToken)) == 1
}

func denyResumePermission() error {
	return status.Error(codes.PermissionDenied, "permission denied")
}

const autoResumeTransitionWaitBudget = time.Minute

func (s *SandboxService) getAutoResumeSnapshot(ctx context.Context, sandboxID string) (*snapshotcache.SnapshotInfo, *dbtypes.SandboxAutoResumeConfig, error) {
	snap, err := s.api.snapshotCache.Get(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, snapshotcache.ErrSnapshotNotFound) {
			return nil, nil, status.Error(codes.NotFound, "snapshot not found")
		}

		return nil, nil, status.Errorf(codes.Internal, "failed to get snapshot: %v", err)
	}

	var autoResume *dbtypes.SandboxAutoResumeConfig
	if snap.Snapshot.Config != nil {
		autoResume = snap.Snapshot.Config.AutoResume
	}
	if autoResume == nil || autoResume.Policy != dbtypes.SandboxAutoResumeAny {
		return nil, nil, status.Error(codes.NotFound, "sandbox auto-resume disabled")
	}

	return snap, autoResume, nil
}

func (s *SandboxService) validateSandboxTraffic(ctx context.Context, sandboxID string, network *dbtypes.SandboxNetworkConfig, envdAccessToken *string) error {
	incomingMetadata := metadataFromIncomingContext(ctx)
	isNonEnvdTraffic := isNonEnvdTrafficRequest(ctx, incomingMetadata, sandboxID)

	// Validate traffic access token for sandboxes with private ingress.
	if isPrivateIngressTraffic(network) && isNonEnvdTraffic {
		expectedToken, tokenErr := s.api.accessTokenGenerator.GenerateTrafficAccessToken(sandboxID)
		if tokenErr != nil {
			logger.L().Error(ctx, "failed to generate expected traffic access token", zap.Error(tokenErr), logger.WithSandboxID(sandboxID))

			return status.Error(codes.Internal, "failed to validate traffic access token")
		}

		providedToken, _ := metadataFirstValue(incomingMetadata, proxygrpc.MetadataTrafficAccessToken)

		if !tokensMatch(providedToken, expectedToken) {
			return denyResumePermission()
		}
	}

	// Validate envd access token for secure sandboxes on envd traffic.
	if !isNonEnvdTraffic && envdAccessToken != nil {
		providedEnvdToken, _ := metadataFirstValue(incomingMetadata, proxygrpc.MetadataEnvdAccessToken)

		if !tokensMatch(providedEnvdToken, *envdAccessToken) {
			return denyResumePermission()
		}
	}

	return nil
}

func (s *SandboxService) validateSandboxProxyTraffic(ctx context.Context, incomingMetadata metadata.MD, sandboxID string, network *dbtypes.SandboxNetworkConfig) error {
	if !isPrivateIngressTraffic(network) {
		return nil
	}

	expectedToken, tokenErr := s.api.accessTokenGenerator.GenerateTrafficAccessToken(sandboxID)
	if tokenErr != nil {
		logger.L().Error(ctx, "failed to generate expected traffic access token", zap.Error(tokenErr), logger.WithSandboxID(sandboxID))

		return status.Error(codes.Internal, "failed to validate traffic access token")
	}

	providedToken, _ := metadataFirstValue(incomingMetadata, proxygrpc.MetadataTrafficAccessToken)
	if !tokensMatch(providedToken, expectedToken) {
		return denyResumePermission()
	}

	return nil
}

func (s *SandboxService) requireClientProxyAuth(ctx context.Context, incomingMetadata metadata.MD, team *typesteam.Team) error {
	if !s.requireEdgeClientProxyAuth {
		return nil
	}

	clientProxyClaims, err := oauth.RequireClaims(ctx, incomingMetadata, s.clientProxyOAuth)
	if err != nil {
		return err
	}
	if err := oauth.RequireScopeClaims(clientProxyClaims, oauth.RequiredScope); err != nil {
		return err
	}

	var authOrgID string
	if team.ClusterID != nil {
		cluster, found := s.api.clusters.GetClusterById(*team.ClusterID)
		if !found {
			return status.Errorf(codes.Internal, "cluster with ID '%s' not found", *team.ClusterID)
		}

		authOrgID = cluster.AuthOrgID
	}

	return oauth.RequireOrgClaims(clientProxyClaims, authOrgID)
}

func (s *SandboxService) ResumeSandbox(ctx context.Context, req *proxygrpc.SandboxResumeRequest) (*proxygrpc.SandboxResumeResponse, error) {
	incomingMetadata := metadataFromIncomingContext(ctx)

	sandboxID, err := utils.ShortID(req.GetSandboxId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid sandbox ID")
	}

	snap, autoResume, err := s.getAutoResumeSnapshot(ctx, sandboxID)
	if err != nil {
		return nil, err
	}

	teamID := snap.Snapshot.TeamID

	team, err := s.api.authService.GetTeamByID(ctx, teamID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get team: %v", err)
	}

	if err := s.requireClientProxyAuth(ctx, incomingMetadata, team); err != nil {
		return nil, err
	}

	minAutoResumeTimeout := time.Duration(s.api.featureFlags.IntFlag(ctx, featureflags.MinAutoResumeTimeoutSeconds)) * time.Second

	timeout := calculateAutoResumeTimeout(autoResume, minAutoResumeTimeout, team)

	var envdAccessToken *string
	if snap.Snapshot.EnvSecure {
		accessToken, tokenErr := s.api.getEnvdAccessToken(snap.EnvBuild.EnvdVersion, sandboxID)
		if tokenErr != nil {
			logger.L().Error(ctx, "Secure envd access token error", zap.Error(tokenErr.Err), logger.WithSandboxID(sandboxID))

			return nil, status.Error(codes.Internal, "failed to create envd access token")
		}

		envdAccessToken = &accessToken
	}

	var network *dbtypes.SandboxNetworkConfig
	if snap.Snapshot.Config != nil {
		network = snap.Snapshot.Config.Network
	}

	if trafficErr := s.validateSandboxTraffic(ctx, sandboxID, network, envdAccessToken); trafficErr != nil {
		return nil, trafficErr
	}

	sandboxData, sandboxErr := s.api.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if sandboxErr != nil {
		if !errors.Is(sandboxErr, sandbox.ErrNotFound) {
			return nil, status.Errorf(codes.Internal, "failed to get sandbox state: %v", sandboxErr)
		}
	} else {
		nodeIP, handled, existingErr := s.api.orchestrator.HandleExistingSandboxAutoResume(
			ctx,
			teamID,
			sandboxID,
			sandboxData,
			autoResumeTransitionWaitBudget,
		)
		if existingErr != nil {
			if errors.Is(existingErr, apiorchestrator.ErrSandboxStillTransitioning) {
				return nil, status.Error(codes.FailedPrecondition, proxygrpc.SandboxStillTransitioningMessage)
			}
			if errors.Is(existingErr, sandbox.ErrNotFound) {
				return nil, status.Error(codes.NotFound, "sandbox not found")
			}
			if errors.Is(existingErr, context.Canceled) || errors.Is(existingErr, context.DeadlineExceeded) {
				return nil, status.FromContextError(existingErr).Err()
			}

			return nil, status.Error(codes.Internal, existingErr.Error())
		}
		if handled {
			return &proxygrpc.SandboxResumeResponse{OrchestratorIp: nodeIP}, nil
		}
	}

	headers := http.Header{}
	sbx, apiErr := s.api.startSandboxInternal(
		ctx,
		sandboxID,
		timeout,
		team,
		s.api.buildResumeSandboxData(sandboxID, nil),
		&headers,
		true,
		nil, // mcp
	)
	if apiErr != nil {
		return nil, status.Error(sharedutils.GRPCCodeFromHTTPStatus(apiErr.Code), apiErr.ClientMsg)
	}

	nodeIP := s.api.orchestrator.GetNodeRouteIPAddress(sbx.ClusterID, sbx.NodeID)
	if nodeIP == "" {
		return nil, status.Error(codes.Internal, "sandbox resumed but orchestrator IP is not available yet")
	}

	return &proxygrpc.SandboxResumeResponse{OrchestratorIp: nodeIP}, nil
}

func (s *SandboxService) KeepAliveSandbox(ctx context.Context, req *proxygrpc.SandboxKeepAliveRequest) (*proxygrpc.SandboxKeepAliveResponse, error) {
	incomingMetadata := metadataFromIncomingContext(ctx)

	sandboxID, err := utils.ShortID(req.GetSandboxId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid sandbox ID")
	}

	teamID, err := uuid.Parse(req.GetTeamId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid team ID")
	}

	team, err := s.api.authService.GetTeamByID(ctx, teamID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get team: %v", err)
	}

	if err := s.requireClientProxyAuth(ctx, incomingMetadata, team); err != nil {
		return nil, err
	}

	sandboxData, err := s.api.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if err != nil {
		if errors.Is(err, sandbox.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "sandbox not found")
		}

		return nil, status.Errorf(codes.Internal, "failed to get sandbox state: %v", err)
	}

	trafficKeepalive := sandboxData.TrafficKeepalive()
	if trafficKeepalive == nil {
		return nil, status.Error(codes.FailedPrecondition, "sandbox traffic keepalive disabled")
	}

	if trafficErr := s.validateSandboxProxyTraffic(ctx, incomingMetadata, sandboxID, sandboxData.Network); trafficErr != nil {
		return nil, trafficErr
	}

	timeout := time.Duration(trafficKeepalive.Timeout) * time.Second

	if _, apiErr := s.api.orchestrator.KeepAliveFor(ctx, teamID, sandboxID, timeout, false); apiErr != nil {
		return nil, status.Error(sharedutils.GRPCCodeFromHTTPStatus(apiErr.Code), apiErr.ClientMsg)
	}

	return &proxygrpc.SandboxKeepAliveResponse{}, nil
}
