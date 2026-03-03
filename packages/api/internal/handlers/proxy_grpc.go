package handlers

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	dbapi "github.com/e2b-dev/infra/packages/api/internal/db"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type SandboxService struct {
	proxygrpc.UnimplementedSandboxServiceServer

	api *APIStore
}

func NewSandboxService(api *APIStore) *SandboxService {
	return &SandboxService{api: api}
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
			"invalid sandbox request port metadata for resume",
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

func (s *SandboxService) ResumeSandbox(ctx context.Context, req *proxygrpc.SandboxResumeRequest) (*proxygrpc.SandboxResumeResponse, error) {
	sandboxID, err := utils.ShortID(req.GetSandboxId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid sandbox ID")
	}

	snap, err := s.api.sqlcDB.GetLastSnapshot(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "snapshot not found")
		}

		return nil, status.Errorf(codes.Internal, "failed to get snapshot: %v", err)
	}

	teamID := snap.Snapshot.TeamID

	// Fixed 5 minutes for client-proxy initiated resume.
	// This intentionally does not allow callers to override timeouts via gRPC.
	timeout := 300 * time.Second

	var autoResume *dbtypes.SandboxAutoResumeConfig
	if snap.Snapshot.Config != nil {
		autoResume = snap.Snapshot.Config.AutoResume
	}
	if autoResume == nil || autoResume.Policy != dbtypes.SandboxAutoResumeAny {
		return nil, status.Error(codes.NotFound, "sandbox auto-resume disabled")
	}

	team, err := dbapi.GetTeamByID(ctx, s.api.authDB, teamID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get team: %v", err)
	}

	autoPause := snap.Snapshot.AutoPause
	nodeID := &snap.Snapshot.OriginNodeID

	alias := ""
	if len(snap.Aliases) > 0 {
		alias = snap.Aliases[0]
	}

	var envdAccessToken *string = nil
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

	incomingMetadata := metadataFromIncomingContext(ctx)
	isNonEnvdTraffic := isNonEnvdTrafficRequest(ctx, incomingMetadata, sandboxID)

	// Validate traffic access token for sandboxes with private ingress.
	if isPrivateIngressTraffic(network) && isNonEnvdTraffic {
		expectedToken, tokenErr := s.api.accessTokenGenerator.GenerateTrafficAccessToken(sandboxID)
		if tokenErr != nil {
			logger.L().Error(ctx, "failed to generate expected traffic access token", zap.Error(tokenErr), logger.WithSandboxID(sandboxID))

			return nil, status.Error(codes.Internal, "failed to validate traffic access token")
		}

		providedToken, _ := metadataFirstValue(incomingMetadata, proxygrpc.MetadataTrafficAccessToken)

		if !tokensMatch(providedToken, expectedToken) {
			return nil, denyResumePermission()
		}
	}

	// Validate envd access token for secure sandboxes on envd traffic
	if !isNonEnvdTraffic && snap.Snapshot.EnvSecure && envdAccessToken != nil {
		providedEnvdToken, _ := metadataFirstValue(incomingMetadata, proxygrpc.MetadataEnvdAccessToken)

		if !tokensMatch(providedEnvdToken, *envdAccessToken) {
			return nil, denyResumePermission()
		}
	}

	headers := http.Header{}
	sbx, apiErr := s.api.startSandboxInternal(
		ctx,
		snap.Snapshot.SandboxID,
		timeout,
		nil,
		snap.Snapshot.Metadata,
		alias,
		team,
		snap.EnvBuild,
		&headers,
		true,
		nodeID,
		snap.Snapshot.EnvID,
		snap.Snapshot.BaseEnvID,
		autoPause,
		autoResume,
		envdAccessToken,
		snap.Snapshot.AllowInternetAccess,
		network,
		nil, // mcp
		nil, // volumeMounts
	)
	if apiErr != nil {
		return nil, status.Error(sharedutils.GRPCCodeFromHTTPStatus(apiErr.Code), apiErr.ClientMsg)
	}

	node := s.api.orchestrator.GetNode(sbx.ClusterID, sbx.NodeID)
	if node == nil {
		return nil, status.Error(codes.Internal, "sandbox resumed but routing info is not available yet")
	}

	return &proxygrpc.SandboxResumeResponse{OrchestratorIp: node.IPAddress}, nil
}
