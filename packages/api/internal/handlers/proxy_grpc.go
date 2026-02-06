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
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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

	// We should better define what we mean by timeout; https://linear.app/e2b/issue/ENG-3473/codify-what-timeout-should-be
	timeoutSeconds := req.GetTimeoutSeconds()
	if timeoutSeconds <= 0 {
		timeoutSeconds = 300 // default to 5 minutes if not set or invalid
	}
	timeout := time.Duration(timeoutSeconds) * time.Second

	// Fast path: if already running, return routing info immediately.
	if running, runErr := s.api.orchestrator.GetSandbox(ctx, teamID, sandboxID); runErr == nil {
		if running.State == sandbox.StateRunning {
			node := s.api.orchestrator.GetNode(running.ClusterID, running.NodeID)
			if node == nil || node.IPAddress == "" {
				return nil, status.Error(codes.Internal, "node not found for running sandbox")
			}

			return &proxygrpc.SandboxResumeResponse{OrchestratorIp: node.IPAddress}, nil
		}
	}

	var autoResume *dbtypes.SandboxAutoResumeConfig
	if snap.Snapshot.Config != nil {
		autoResume = snap.Snapshot.Config.AutoResume
	}
	if autoResume == nil || autoResume.Policy == nil || *autoResume.Policy != dbtypes.SandboxAutoResumeAny {
		return nil, status.Error(codes.NotFound, "sandbox auto-resume disabled")
	}

	running, runErr := s.api.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if runErr == nil {
		switch running.State {
		case sandbox.StatePausing:
			logger.L().Debug(ctx, "Waiting for sandbox to pause", logger.WithSandboxID(sandboxID))
			if err := s.api.orchestrator.WaitForStateChange(ctx, teamID, sandboxID); err != nil {
				return nil, status.Error(codes.Internal, "error waiting for sandbox to pause")
			}
		case sandbox.StateKilling:
			return nil, status.Error(codes.NotFound, "sandbox can't be resumed, no snapshot found")
		case sandbox.StateRunning:
			// Keep going and return routing info below.
		default:
			logger.L().Error(ctx, "Sandbox is in an unknown state", logger.WithSandboxID(sandboxID), zap.String("state", string(running.State)))

			return nil, status.Error(codes.Internal, "sandbox is in an unknown state")
		}
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

	team, err := dbapi.GetTeamByID(ctx, s.api.authDB, teamID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get team: %v", err)
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
		return nil, status.Errorf(grpcCodeFromHTTPStatus(apiErr.Code), "resume failed: %s", apiErr.ClientMsg)
	}

	node := s.api.orchestrator.GetNode(sbx.ClusterID, sbx.NodeID)
	if node == nil || node.IPAddress == "" {
		return nil, status.Error(codes.Internal, "sandbox resumed but routing info is not available yet")
	}

	return &proxygrpc.SandboxResumeResponse{OrchestratorIp: node.IPAddress}, nil
}

func grpcCodeFromHTTPStatus(statusCode int) codes.Code {
	switch statusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return codes.InvalidArgument
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusNotFound:
		return codes.NotFound
	case http.StatusConflict:
		return codes.AlreadyExists
	case http.StatusTooManyRequests:
		return codes.ResourceExhausted
	case http.StatusPreconditionFailed:
		return codes.FailedPrecondition
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return codes.DeadlineExceeded
	case http.StatusNotImplemented:
		return codes.Unimplemented
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return codes.Unavailable
	default:
		if statusCode >= http.StatusInternalServerError {
			return codes.Internal
		}
		if statusCode >= http.StatusBadRequest {
			return codes.InvalidArgument
		}

		return codes.Internal
	}
}
