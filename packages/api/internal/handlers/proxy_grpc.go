package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dbapi "github.com/e2b-dev/infra/packages/api/internal/db"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
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

func (s *SandboxService) ResumeSandbox(ctx context.Context, req *proxygrpc.SandboxResumeRequest) (*proxygrpc.SandboxResumeResponse, error) {
	sandboxID := utils.ShortID(req.GetSandboxId())

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
	if autoResume == nil || autoResume.Policy == nil || *autoResume.Policy != dbtypes.SandboxAutoResumeAny {
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
	)
	if apiErr != nil {
		return nil, status.Errorf(sharedutils.GRPCCodeFromHTTPStatus(apiErr.Code), "resume failed: %s", apiErr.ClientMsg)
	}

	node := s.api.orchestrator.GetNode(sbx.ClusterID, sbx.NodeID)
	if node == nil || node.IPAddress == "" {
		return nil, status.Error(codes.Internal, "sandbox resumed but routing info is not available yet")
	}

	return &proxygrpc.SandboxResumeResponse{OrchestratorIp: node.IPAddress}, nil
}
