package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bsm/redislock"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	dbapi "github.com/e2b-dev/infra/packages/api/internal/db"
	teamtypes "github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	proxyResumeLockTTL     = 2 * time.Minute
	proxyResumeWaitTimeout = 30 * time.Second
)

type SandboxService struct {
	proxygrpc.UnimplementedSandboxServiceServer

	api *APIStore
}

func NewSandboxService(api *APIStore) *SandboxService {
	return &SandboxService{api: api}
}

func (s *SandboxService) ResumeSandbox(ctx context.Context, req *proxygrpc.SandboxResumeRequest) (*emptypb.Empty, error) {
	sandboxID := utils.ShortID(req.GetSandboxId())

	snap, err := s.api.sqlcDB.GetLastSnapshot(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "snapshot not found")
		}

		return nil, status.Errorf(codes.Internal, "failed to get snapshot: %v", err)
	}

	resumesOn := sandboxResumesOnFromConfig(snap.Snapshot.Config)
	policy := autoResumePolicyFromSnapshotResumesOn(resumesOn)
	authTeam, authProvided, authErr := s.resolveAuthTeam(ctx, snap.Snapshot.TeamID)

	switch policy {
	case proxygrpc.AutoResumePolicy_AUTO_RESUME_POLICY_AUTHED:
		if !authProvided || authErr != nil {
			return nil, status.Error(codes.PermissionDenied, "authorization required to resume")
		}
	case proxygrpc.AutoResumePolicy_AUTO_RESUME_POLICY_ANY:
		if authErr != nil {
			logger.L().Warn(ctx, "proxy resume auth failed, continuing for policy=any", zap.Error(authErr), logger.WithSandboxID(sandboxID))
		}
	default:
		return nil, status.Error(codes.FailedPrecondition, "auto-resume disabled")
	}

	var team *teamtypes.Team
	if authTeam != nil {
		team = authTeam
	} else {
		team, err = dbapi.GetTeamByID(ctx, s.api.authDB, snap.Snapshot.TeamID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to load team: %v", err)
		}
	}

	var timeoutSecondsPtr *int32
	if req.GetTimeoutSeconds() > 0 {
		timeoutSeconds := req.GetTimeoutSeconds()
		timeoutSecondsPtr = &timeoutSeconds
	} else {
		timeoutSecondsPtr = sandboxTimeoutSecondsFromConfig(snap.Snapshot.Config)
	}

	timeout := sandbox.SandboxTimeoutDefault
	if timeoutSecondsPtr != nil {
		timeout = time.Duration(*timeoutSecondsPtr) * time.Second
	}
	if timeout > time.Duration(team.Limits.MaxLengthHours)*time.Hour {
		return nil, status.Error(codes.InvalidArgument, "timeout exceeds team limit")
	}

	lock, lockAcquired, lockErr := s.tryAcquireResumeLock(ctx, sandboxID)
	if lockErr != nil {
		logger.L().Warn(ctx, "Failed to acquire proxy resume lock, proceeding without lock", zap.Error(lockErr), logger.WithSandboxID(sandboxID))
	} else if !lockAcquired {
		if waitErr := s.waitForResumeLock(ctx, sandboxID); waitErr != nil {
			return nil, status.Error(codes.Internal, "error waiting for proxy resume lock")
		}

		return &emptypb.Empty{}, nil
	}
	if lock != nil {
		defer func() {
			if releaseErr := lock.Release(context.WithoutCancel(ctx)); releaseErr != nil {
				logger.L().Warn(ctx, "Failed to release proxy resume lock", zap.Error(releaseErr), logger.WithSandboxID(sandboxID))
			}
		}()
	}

	running, runErr := s.api.orchestrator.GetSandbox(ctx, team.Team.ID, sandboxID)
	if runErr == nil {
		switch running.State {
		case sandbox.StatePausing:
			logger.L().Debug(ctx, "Waiting for sandbox to pause", logger.WithSandboxID(sandboxID))
			if err := s.api.orchestrator.WaitForStateChange(ctx, team.Team.ID, sandboxID); err != nil {
				return nil, status.Error(codes.Internal, "error waiting for sandbox to pause")
			}
		case sandbox.StateKilling:
			return nil, status.Error(codes.NotFound, "sandbox can't be resumed, no snapshot found")
		case sandbox.StateRunning:
			return nil, status.Error(codes.AlreadyExists, "sandbox already running")
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

	headers := http.Header{}
	_, apiErr := s.api.startSandbox(
		ctx,
		snap.Snapshot.SandboxID,
		timeout,
		timeoutSecondsPtr,
		nil,
		snap.Snapshot.Metadata,
		resumesOn,
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
		nil,
	)
	if apiErr != nil {
		return nil, status.Errorf(grpcCodeFromHTTPStatus(apiErr.Code), "resume failed: %s", apiErr.ClientMsg)
	}

	return &emptypb.Empty{}, nil
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

func (s *SandboxService) resolveAuthTeam(ctx context.Context, snapshotTeamID uuid.UUID) (*teamtypes.Team, bool, error) {
	md, _ := metadata.FromIncomingContext(ctx)

	if apiKey := firstMetadata(md, "x-api-key"); apiKey != "" {
		apiKey = strings.TrimSpace(apiKey)
		if !strings.HasPrefix(apiKey, keys.ApiKeyPrefix) {
			return nil, true, errors.New("invalid api key format")
		}

		team, apiErr := s.api.GetTeamFromAPIKey(ctx, nil, apiKey)
		if apiErr != nil {
			return nil, true, apiErr.Err
		}

		if team.Team.ID != snapshotTeamID {
			return nil, true, errors.New("api key team mismatch")
		}

		return team, true, nil
	}

	if authHeader := firstMetadata(md, "authorization"); authHeader != "" {
		accessToken := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if !strings.HasPrefix(accessToken, keys.AccessTokenPrefix) {
			return nil, true, errors.New("invalid access token format")
		}

		userID, apiErr := s.api.GetUserFromAccessToken(ctx, nil, accessToken)
		if apiErr != nil {
			return nil, true, apiErr.Err
		}

		team, err := dbapi.GetTeamByIDAndUserIDAuth(ctx, s.api.authDB, snapshotTeamID.String(), userID)
		if err != nil {
			return nil, true, err
		}

		return team, true, nil
	}

	return nil, false, nil
}

func autoResumePolicyFromSnapshotResumesOn(resumesOn *string) proxygrpc.AutoResumePolicy {
	if resumesOn == nil {
		return proxygrpc.AutoResumePolicy_AUTO_RESUME_POLICY_NULL
	}

	return proxygrpc.AutoResumePolicyFromString(*resumesOn)
}

func sandboxResumesOnFromConfig(config *dbtypes.PausedSandboxConfig) *string {
	if config == nil {
		return nil
	}

	return config.SandboxResumesOn
}

func sandboxTimeoutSecondsFromConfig(config *dbtypes.PausedSandboxConfig) *int32 {
	if config == nil {
		return nil
	}

	if config.SandboxPausedSeconds != nil {
		return config.SandboxPausedSeconds
	}
	if config.SandboxTimeoutSeconds != nil {
		return config.SandboxTimeoutSeconds
	}

	return config.SandboxTimeoutSecondsSnake
}

func firstMetadata(md metadata.MD, key string) string {
	values := md.Get(key)
	if len(values) == 0 {
		return ""
	}

	return values[0]
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
