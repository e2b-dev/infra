package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

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

type ProxySandboxService struct {
	proxygrpc.UnimplementedProxySandboxServiceServer
	api *APIStore
}

func NewProxySandboxService(api *APIStore) *ProxySandboxService {
	return &ProxySandboxService{api: api}
}

func (s *ProxySandboxService) GetPausedInfo(ctx context.Context, req *proxygrpc.SandboxPausedInfoRequest) (*proxygrpc.SandboxPausedInfoResponse, error) {
	sandboxID := utils.ShortID(req.GetSandboxId())

	snap, err := s.api.sqlcDB.GetLastSnapshot(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &proxygrpc.SandboxPausedInfoResponse{Paused: false, AutoResumePolicy: "null"}, nil
		}

		return nil, status.Errorf(codes.Internal, "failed to get snapshot: %v", err)
	}

	policy := autoResumePolicyFromMetadata(snap.Snapshot.Metadata)

	return &proxygrpc.SandboxPausedInfoResponse{
		Paused:           true,
		AutoResumePolicy: policy,
	}, nil
}

func (s *ProxySandboxService) ResumeSandbox(ctx context.Context, req *proxygrpc.SandboxResumeRequest) (*emptypb.Empty, error) {
	sandboxID := utils.ShortID(req.GetSandboxId())

	snap, err := s.api.sqlcDB.GetLastSnapshot(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "snapshot not found")
		}

		return nil, status.Errorf(codes.Internal, "failed to get snapshot: %v", err)
	}

	policy := autoResumePolicyFromMetadata(snap.Snapshot.Metadata)
	authTeam, authProvided, authErr := s.resolveAuthTeam(ctx, snap.Snapshot.TeamID)

	switch policy {
	case "authed":
		if !authProvided || authErr != nil {
			return nil, status.Error(codes.PermissionDenied, "authorization required to resume")
		}
	case "any":
		if authErr != nil {
			logger.L().Warn(ctx, "proxy resume auth failed, continuing for policy=any", zap.Error(authErr), logger.WithSandboxID(sandboxID))
		}
	default:
		return nil, status.Error(codes.FailedPrecondition, "auto-resume disabled")
	}

	var team *teamtypes.Team = nil
	if authTeam != nil {
		team = authTeam
	} else {
		team, err = dbapi.GetTeamByID(ctx, s.api.authDB, snap.Snapshot.TeamID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to load team: %v", err)
		}
	}

	timeout := sandbox.SandboxTimeoutDefault
	if req.GetTimeoutSeconds() > 0 {
		timeout = time.Duration(req.GetTimeoutSeconds()) * time.Second
	}
	if timeout > time.Duration(team.Limits.MaxLengthHours)*time.Hour {
		return nil, status.Error(codes.InvalidArgument, "timeout exceeds team limit")
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
		nil,
		snap.Snapshot.Metadata,
		alias,
		team,
		snap.EnvBuild,
		&headers,
		true,
		nodeID,
		snap.Snapshot.BaseEnvID,
		autoPause,
		envdAccessToken,
		snap.Snapshot.AllowInternetAccess,
		network,
		nil,
	)
	if apiErr != nil {
		return nil, status.Errorf(codes.Internal, "resume failed: %s", apiErr.ClientMsg)
	}

	return &emptypb.Empty{}, nil
}

func (s *ProxySandboxService) resolveAuthTeam(ctx context.Context, snapshotTeamID uuid.UUID) (*teamtypes.Team, bool, error) {
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

func autoResumePolicyFromMetadata(metadata map[string]string) string {
	if metadata == nil {
		return "null"
	}

	value := strings.TrimSpace(strings.ToLower(metadata["auto_resume"]))
	if value == "" {
		return "null"
	}

	return value
}

func firstMetadata(md metadata.MD, key string) string {
	values := md.Get(key)
	if len(values) == 0 {
		return ""
	}

	return values[0]
}
