package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/bsm/redislock"
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

const (
	// Prevent resume storms. This is a prototype: keep it long enough that a resume attempt
	// is effectively de-duplicated for a short window.
	proxyResumeLockTTL     = 5 * time.Minute
	proxyResumeWaitTimeout = 30 * time.Second
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

	// Prototype: always resume for a short fixed TTL to validate the request flow.
	// We'll revisit per-team limits/policies later.
	timeout := 5 * time.Minute

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

	lock, lockAcquired, lockErr := s.tryAcquireResumeLock(ctx, sandboxID)
	if lockErr != nil {
		logger.L().Warn(ctx, "Failed to acquire proxy resume lock, proceeding without lock", zap.Error(lockErr), logger.WithSandboxID(sandboxID))
	} else if !lockAcquired {
		if waitErr := s.waitForResumeLock(ctx, sandboxID); waitErr != nil {
			return nil, status.Error(codes.Internal, "error waiting for proxy resume lock")
		}

		// Another request likely resumed it. Try to return routing info.
		running, runErr := s.api.orchestrator.GetSandbox(ctx, teamID, sandboxID)
		if runErr != nil {
			return nil, status.Error(codes.Unavailable, "sandbox resume in progress")
		}
		node := s.api.orchestrator.GetNode(running.ClusterID, running.NodeID)
		if node == nil || node.IPAddress == "" {
			return nil, status.Error(codes.Unavailable, "sandbox resume in progress")
		}

		return &proxygrpc.SandboxResumeResponse{OrchestratorIp: node.IPAddress}, nil
	}
	if lock != nil {
		defer func() {
			if releaseErr := lock.Release(context.WithoutCancel(ctx)); releaseErr != nil {
				logger.L().Warn(ctx, "Failed to release proxy resume lock", zap.Error(releaseErr), logger.WithSandboxID(sandboxID))
			}
		}()
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
	_, apiErr := s.api.startSandbox(
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
		envdAccessToken,
		snap.Snapshot.AllowInternetAccess,
		network,
		nil, // mcp
	)
	if apiErr != nil {
		return nil, status.Errorf(grpcCodeFromHTTPStatus(apiErr.Code), "resume failed: %s", apiErr.ClientMsg)
	}

	// Return routing info so the proxy can send the request immediately (no catalog polling required).
	running, runErr = s.api.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if runErr != nil {
		return nil, status.Error(codes.Internal, "sandbox resumed but routing info is not available yet")
	}
	node := s.api.orchestrator.GetNode(running.ClusterID, running.NodeID)
	if node == nil || node.IPAddress == "" {
		return nil, status.Error(codes.Internal, "sandbox resumed but node is not available")
	}

	return &proxygrpc.SandboxResumeResponse{OrchestratorIp: node.IPAddress}, nil
}

func (s *SandboxService) tryAcquireResumeLock(ctx context.Context, sandboxID string) (*redislock.Lock, bool, error) {
	if s.api.redisClient == nil {
		return nil, true, nil
	}

	lockService := redislock.New(s.api.redisClient)
	lock, err := lockService.Obtain(ctx, "proxy-resume:"+sandboxID, proxyResumeLockTTL, &redislock.Options{
		RetryStrategy: redislock.NoRetry(),
	})
	if err == nil {
		return lock, true, nil
	}
	if errors.Is(err, redislock.ErrNotObtained) {
		return nil, false, nil
	}

	return nil, false, err
}

func (s *SandboxService) waitForResumeLock(ctx context.Context, sandboxID string) error {
	if s.api.redisClient == nil {
		return nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, proxyResumeWaitTimeout)
	defer cancel()

	lockService := redislock.New(s.api.redisClient)
	lock, err := lockService.Obtain(waitCtx, "proxy-resume:"+sandboxID, proxyResumeLockTTL, &redislock.Options{
		RetryStrategy: redislock.ExponentialBackoff(100*time.Millisecond, 2*time.Second),
	})
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := lock.Release(context.WithoutCancel(ctx)); releaseErr != nil {
			logger.L().Warn(ctx, "Failed to release proxy resume lock after wait", zap.Error(releaseErr), logger.WithSandboxID(sandboxID))
		}
	}()

	return nil
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
