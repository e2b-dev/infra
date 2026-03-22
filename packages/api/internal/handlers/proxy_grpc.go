package handlers

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	snapshotcache "github.com/e2b-dev/infra/packages/api/internal/cache/snapshots"
	dbapi "github.com/e2b-dev/infra/packages/api/internal/db"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
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

const maxAutoResumeTransitionRetries = 3

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

func handleExistingSandboxAutoResume(
	ctx context.Context,
	sandboxID string,
	sbx sandbox.Sandbox,
	waitForStateChange func(context.Context) error,
	getSandbox func(context.Context) (sandbox.Sandbox, error),
	getNodeIP func(sandbox.Sandbox) (string, error),
) (string, bool, error) {
	for attempt := range maxAutoResumeTransitionRetries {
		switch sbx.State {
		case sandbox.StatePausing, sandbox.StateSnapshotting:
			if sbx.State == sandbox.StatePausing {
				logger.L().Debug(ctx, "Waiting for sandbox to pause before auto-resume", logger.WithSandboxID(sandboxID), zap.Int("attempt", attempt+1))
			} else {
				logger.L().Debug(ctx, "Waiting for sandbox snapshot to finish before auto-resume", logger.WithSandboxID(sandboxID), zap.Int("attempt", attempt+1))
			}

			err := waitForStateChange(ctx)
			if err != nil {
				if sbx.State == sandbox.StatePausing {
					return "", false, status.Error(codes.Internal, "error waiting for sandbox to pause")
				}

				return "", false, status.Error(codes.Internal, "error waiting for sandbox snapshot to finish")
			}

			updatedSandbox, getSandboxErr := getSandbox(ctx)
			if getSandboxErr == nil {
				sbx = updatedSandbox

				continue
			}
			if errors.Is(getSandboxErr, sandbox.ErrNotFound) {
				// Sandbox is no longer present in orchestrator state, so continue with normal resume.
				return "", false, nil
			}

			return "", false, status.Errorf(codes.Internal, "failed to refresh sandbox state: %v", getSandboxErr)
		case sandbox.StateKilling:
			logger.L().Debug(ctx, "Sandbox is being killed, cannot auto-resume", logger.WithSandboxID(sandboxID))

			return "", false, status.Error(codes.NotFound, "sandbox not found")
		case sandbox.StateRunning:
			nodeIP, err := getNodeIP(sbx)
			if err != nil {
				return "", false, err
			}

			return nodeIP, true, nil
		default:
			logger.L().Error(ctx, "Sandbox is in an unknown state during auto-resume", logger.WithSandboxID(sandboxID), zap.String("state", string(sbx.State)))

			return "", false, status.Error(codes.Internal, "sandbox is in an unknown state")
		}
	}

	logger.L().Warn(
		ctx,
		"Sandbox is still transitioning after auto-resume retries",
		logger.WithSandboxID(sandboxID),
		zap.String("state", string(sbx.State)),
		zap.Int("attempts", maxAutoResumeTransitionRetries),
	)

	return "", false, status.Error(codes.FailedPrecondition, "sandbox is still transitioning")
}

func (s *SandboxService) ResumeSandbox(ctx context.Context, req *proxygrpc.SandboxResumeRequest) (*proxygrpc.SandboxResumeResponse, error) {
	sandboxID, err := utils.ShortID(req.GetSandboxId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid sandbox ID")
	}

	var autoResume *dbtypes.SandboxAutoResumeConfig
	snap, _, err := s.getAutoResumeSnapshot(ctx, sandboxID)
	if err != nil {
		return nil, err
	}

	teamID := snap.Snapshot.TeamID

	// Fixed 5 minutes for client-proxy initiated resume.
	// This intentionally does not allow callers to override timeouts via gRPC.
	timeout := 300 * time.Second

	sandboxData, sandboxErr := s.api.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if sandboxErr != nil {
		if !errors.Is(sandboxErr, sandbox.ErrNotFound) {
			return nil, status.Errorf(codes.Internal, "failed to get sandbox state: %v", sandboxErr)
		}

		// Reload snapshot metadata after orchestrator checks so we do not resume from stale
		// pre-pause snapshot data.
		snap, autoResume, err = s.getAutoResumeSnapshot(ctx, sandboxID)
		if err != nil {
			return nil, err
		}

		teamID = snap.Snapshot.TeamID
	} else {
		nodeIP, handled, existingErr := handleExistingSandboxAutoResume(
			ctx,
			sandboxID,
			sandboxData,
			func(ctx context.Context) error {
				return s.api.orchestrator.WaitForStateChange(ctx, teamID, sandboxID)
			},
			func(ctx context.Context) (sandbox.Sandbox, error) {
				return s.api.orchestrator.GetSandbox(ctx, teamID, sandboxID)
			},
			func(sbx sandbox.Sandbox) (string, error) {
				node := s.api.orchestrator.GetNode(sbx.ClusterID, sbx.NodeID)
				if node == nil {
					logger.L().Error(
						ctx,
						"Sandbox is running but routing info is not available during auto-resume",
						logger.WithSandboxID(sandboxID),
						logger.WithTeamID(teamID.String()),
						logger.WithNodeID(sbx.NodeID),
						zap.Stringer("cluster_id", sbx.ClusterID),
					)

					return "", status.Error(codes.Internal, "sandbox is running but routing info is not available yet")
				}

				return node.IPAddress, nil
			},
		)
		if existingErr != nil {
			return nil, existingErr
		}
		if handled {
			return &proxygrpc.SandboxResumeResponse{OrchestratorIp: nodeIP}, nil
		}

		// Reload snapshot metadata after orchestrator checks so we do not resume from stale
		// pre-pause snapshot data.
		snap, autoResume, err = s.getAutoResumeSnapshot(ctx, sandboxID)
		if err != nil {
			return nil, err
		}

		teamID = snap.Snapshot.TeamID
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
